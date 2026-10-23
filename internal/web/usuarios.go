package web

// usuarios.go is Part B's "user management" module. This file starts with the
// meeting point (B2): one button installs the console's own key into the VM's
// /root/.ssh/authorized_keys through the channel the console already has (on a
// prepared VM that is Guest Additions; the simulated channel plays along),
// checks that the VM opens an SSH session with that key, and — on success —
// switches the target's channel to SSH, so every following operation goes over
// the SSH. B3 completes the root-key story — the same button story (one POST)
// generates a dedicated pair for root on the VM, leaves the sshd drop-in alive
// behind an "sshd -t" gate and a reload, and recovers the private key into the
// console for exactly one download, without ever logging its bytes. B4 adds
// account creation (internal/usuarios): validating the form locally, checking
// the name is free with getent, creating the account with the password going
// through chpasswd's Stdin only, the optional sudo membership, and the optional
// ed25519 key pair the user downloads once — with the same sshd gate and
// reload the root flow uses, guarded by sshd -t before it reloads. B5 adds the
// deletion side: the page lists the VM's own accounts (getent passwd filtered in
// Go), the first screen only asks for confirmation, and the delete runs userdel
// (with or without the files), removes the per-user drop-in and reloads sshd
// behind the same gate — never for root or an account the console needs.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/llaves"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/usuarios"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/validate"
)

func init() {
	registerModule("usuarios", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /usuarios", s.usuariosPagina)
		r.HandleFunc("POST /usuarios/conectar-ssh", s.conectarSSH)
		r.HandleFunc("POST /usuarios/crear", s.usuarioCrear)
		r.HandleFunc("POST /usuarios/eliminar", s.usuarioEliminar)
		r.HandleFunc("POST /usuarios/llaves-root", s.llavesRoot)
		r.HandleFunc("GET /usuarios/llaves/listo/{token}", s.llaveLista)
		r.HandleFunc("GET /usuarios/llaves/{token}", s.descargarLlave)
	})
}

const (
	// authorizedKeys is the file the console installs itself into.
	authorizedKeys = "/root/.ssh/authorized_keys"

	// sinLlavesArchivo marks "there was no authorized_keys file" in the read
	// result: the merge then starts from the console's key alone, and the
	// write may create the file. It stands out of any real key material,
	// which never starts with two underscores.
	sinLlavesArchivo = "__CONSOLA_SIN_LLAVES__"
)

// leerLlavesScript prints the authorized_keys named as "$1" (stdout, exact
// bytes) or the marker when there is no file. A missing file is the normal
// first-contact case here, unlike the fixed files of Part A, so the read may
// not fail over its absence.
const leerLlavesScript = `if [ -e "$1" ]; then
  cat "$1"
else
  printf '` + sinLlavesArchivo + `\n'
fi`

// instalarLlavesScript writes content "$2" as the authorized_keys "$1", the way
// writeFileScript works, with two differences a key file needs: it can create
// the file (and its .ssh folder, mode 700), and the recovered original does not
// exist for a file the console created. With "$3" = SHA-256 of "$1" as the
// console read it, a change in between (or a file that appeared where none was
// read) exits 12 and nothing is written. The file stays at mode 600.
const instalarLlavesScript = `set -e
if [ -n "$3" ]; then
  c=$(sha256sum < "$1") || exit 12
  [ "${c%% *}" = "$3" ] || exit 12
elif [ -e "$1" ]; then
  exit 12
fi
if [ -e "$1" ] && [ ! -e "$1.consola-original" ]; then
  cp -p "$1" "$1.consola-original"
fi
mkdir -p "$(dirname "$1")"
chmod 700 "$(dirname "$1")"
t=$(mktemp "$1.consola-XXXXXX")
trap 'rm -f "$t"' EXIT
printf '%s' "$2" > "$t"
chmod 600 "$t"
mv "$t" "$1"`

// usuariosDatos is what the module's page template receives (as .Data).
type usuariosDatos struct {
	Canal             string   // the channel the current target uses right now
	Formulario        string   // which form on this page owns the shown error report ("" = none)
	Usuarios          []string // accounts B5 may delete (DB14); empty when the list could not be read
	AvisoLista        string   // why the deletion form has no list (shown in its place)
	Confirmar         string   // account waiting for the second screen ("" = show the deletion form)
	ConfirmarArchivos bool     // "Eliminar todos los archivos" chosen on the first screen
}

// usuariosPagina shows the module. The deletion form lists the VM's own
// accounts, so the page reads getent passwd through the active channel (a
// GET that only reads; it is not an operation in the log) and filters the
// result in Go with DB14: UID 1000-59999, never nobody, root, the target's
// ssh user or its guest user. The first screen never deletes anything: it
// links to ?confirmar=<u> (and ?archivos=on) so the template can render the
// second screen with "Eliminar" and "Cancelar".
func (s *server) usuariosPagina(w http.ResponseWriter, r *http.Request) {
	cur := s.state.Current()
	datos := usuariosDatos{Canal: s.state.Channel(cur.Name)}
	if ex, hayCanal := s.state.Executor(cur.Name); hayCanal {
		datos.Usuarios, datos.AvisoLista = cuentasEliminables(r.Context(), ex, cur)
	} else {
		datos.AvisoLista = "La consola todavía no tiene canal con la VM: usa «Conectar por SSH» para poder listar y eliminar cuentas."
	}
	consulta := r.URL.Query()
	if confirmar := strings.TrimSpace(consulta.Get("confirmar")); confirmar != "" && contieneCuenta(datos.Usuarios, confirmar) {
		datos.Confirmar = confirmar
		datos.ConfirmarArchivos = consulta.Get("archivos") != ""
	}
	s.renderModule(w, r, "usuarios", http.StatusOK, Form{}, datos)
}

// cuentasEliminables reads getent passwd and returns the account names DB14
// lets the console delete, sorted for a stable dropdown. The second value is
// the explanation the page shows instead of the list when there is none.
func cuentasEliminables(ctx context.Context, ex executor.Executor, target app.Target) ([]string, string) {
	res, err := ex.Run(ctx, executor.Command{Name: "getent", Args: []string{"passwd"}})
	if err != nil || res.ExitCode != 0 {
		return nil, "No se pudo leer la lista de cuentas de la VM: revisa el canal y vuelve a intentarlo."
	}
	protegidos := protegidosDe(target)
	var nombres []string
	for _, linea := range strings.Split(res.Stdout, "\n") {
		linea = strings.TrimSuffix(linea, "\r")
		cuenta, ok := usuarios.ParseUsuarioEntry(linea)
		if !ok || !cuenta.EsUsuarioEliminable(protegidos) {
			continue
		}
		nombres = append(nombres, cuenta.Name)
	}
	sort.Strings(nombres)
	if len(nombres) == 0 {
		return nil, "No hay cuentas eliminables en la VM: solo existen root, las cuentas del sistema y las que la consola necesita."
	}
	return nombres, ""
}

// protegidosDe is DB14's exclusion set: root, nobody, the target's ssh user
// (the account the console connects with) and its Guest Additions user. The
// console must never remove the account that gives it access to the VM.
func protegidosDe(target app.Target) map[string]bool {
	protegidos := map[string]bool{"root": true, "nobody": true}
	if target.SSHUser != "" {
		protegidos[target.SSHUser] = true
	}
	if target.GuestUser != "" {
		protegidos[target.GuestUser] = true
	}
	return protegidos
}

func contieneCuenta(cuentas []string, cuenta string) bool {
	for _, nombre := range cuentas {
		if nombre == cuenta {
			return true
		}
	}
	return false
}

// conectarSSH is the B2 action: install the console key in the VM (whatever
// channel it has now, Guest Additions on a prepared VM), then verify the SSH
// answer and switch the channel so the following operations go over SSH.
func (s *server) conectarSSH(w http.ResponseWriter, r *http.Request) {
	cur := s.state.Current()
	pub, err := llaves.PublicKey(llaves.Dir)
	if err != nil {
		datos := usuariosDatos{Canal: s.state.Channel(cur.Name), Formulario: "conectar-ssh"}
		s.renderModule(w, r, "usuarios", http.StatusInternalServerError, Form{Errors: map[string]string{"": err.Error()}}, datos)
		return
	}
	s.runAndShow(w, r, planConectarSSH(cur.Name, pub))
	// The redirect from runAndShow waits in the ResponseWriter's buffer until
	// this handler returns (net/http), so the probe and the switch below are
	// settled before the browser loads the result page: its top bar already
	// shows SSH. When the probe refuses, the channel stays as it was — the
	// user can try again without any half state.
	ex := llaves.SSHForTarget(cur, llaves.Dir)
	if llaves.ProveSSH(ex) == nil {
		_ = s.state.SetExecutor(cur.Name, ex)
	}
}

// planConectarSSH reads the VM's authorized_keys (a marker when there is no
// file, which is the normal first contact), merges the console's public key
// into it without dropping what was there (llaves.MergeAuthorizedKeys), and
// installs the result with the same guarded, atomic write every console file
// change uses. Rerunning after any failure is safe: the read decides again.
// It shares that whole body with B3's "Instalar la llave de root"
// (planInstalarLlaves); only wording differs.
func planConectarSSH(target, pub string) operacion.Plan {
	return planInstalarLlaves("Conectar por SSH", target, authorizedKeys, pub,
		"La llave de la consola quedó instalada en "+authorizedKeys+" de la VM, junto a las llaves que ya tenía, y quedó una copia del archivo original (la copia no se crea para un archivo que la propia consola tuvo que crear). Cuando la prueba de conexión por SSH también funciona, la barra superior muestra SSH y las siguientes operaciones van por ese canal; si no, revisa la salida de abajo.",
		"", // root's own files already belong to root: no extra chown
		func(leer executor.Command, instalar *executor.Command) func(st oplog.Step) string {
			return func(st oplog.Step) string {
				switch {
				case st.Cmd == leer.String():
					return "No se pudo leer " + authorizedKeys + " en la VM (¿es una carpeta?), así que no se cambió nada. Revisa la salida de abajo."
				case st.Cmd == instalar.String() && st.ExitCode == fileChangedCode:
					return authorizedKeys + " cambió entre la lectura y la escritura (otra consola u otra operación lo tocó), así que no se escribió nada. Vuelve a intentarlo."
				case st.Cmd == instalar.String():
					return "No se pudo instalar la llave en " + authorizedKeys + ", así que no se cambió nada. Revisa la salida de abajo."
				}
				return ""
			}
		})
}

// planInstalarLlaves reads the VM's authorized_keys file named by rutaLlaves
// (a marker when there is no file, which is the normal first contact), merges
// pub into it without dropping what was there (llaves.MergeAuthorizedKeys),
// and installs the result with the same guarded, atomic write every console
// file change uses. Rerunning after any failure is safe: the read decides
// again. Action is the name the operation log shows; success is the outcome
// text on a green run. Dueño, when not empty, appends a chown so the key
// (and its .ssh folder) belongs to that user:group — root's files already
// belong to root, so root callers pass "" and their command list does not
// change. The explain function receives the read command and a POINTER to the
// install command this plan is about to build in Then (its args depend on the
// read), so failures can name the exact command the VM refused.
func planInstalarLlaves(action, target, rutaLlaves, pub, success string, dueño string, explicar func(leer executor.Command, instalar *executor.Command) func(st oplog.Step) string) operacion.Plan {
	leer := executor.Command{Name: "sh", Args: []string{"-c", leerLlavesScript, "sh", rutaLlaves}}
	var instalar executor.Command
	return operacion.Plan{
		Action:   action,
		Target:   target,
		Commands: []executor.Command{leer},
		Then: func(res []executor.Result) ([]executor.Command, error) {
			out := res[0].Stdout
			sinArchivo := out == sinLlavesArchivo+"\n"
			existentes := ""
			sha := ""
			if !sinArchivo {
				existentes = out
				sha = sha256Hex(out)
			}
			instalar = executor.Command{
				Name: "sh",
				Args: []string{"-c", instalarLlavesScript, "sh", rutaLlaves,
					llaves.MergeAuthorizedKeys(existentes, pub), sha,
				},
			}
			siguiendo := []executor.Command{instalar}
			if dueño != "" {
				// The created account gets the key in ITS folder, so both the
				// .ssh folder (mode 700, made by the script) and the file (600,
				// also made by the script) end owned by the user, not by root.
				// VM paths use forward slashes; extract dir manually (not filepath.Dir).
				dir := rutaLlaves
				if i := strings.LastIndex(dir, "/"); i >= 0 {
					dir = dir[:i]
				}
				siguiendo = append(siguiendo, executor.Command{
					Name: "chown",
					Args: []string{"-f", dueño, "--", dir, rutaLlaves},
				})
			}
			return siguiendo, nil
		},
		Success: func(res []executor.Result) string {
			return success
		},
		Explain: explicar(leer, &instalar),
	}
}

// ---- llaves de root (B3) ----

const (
	// dropInTemporal is where the sshd drop-in is staged inside the VM first:
	// the real install to /etc/ssh/sshd_config.d/ happens after "sshd -t" gets
	// to say no, so a rejected configuration never reaches the live sshd.
	dropInTemporal = "/tmp/" + llaves.NombreDropInRoot

	// dropInDestino is the drop-in file sshd reads when it starts (or reloads).
	dropInDestino = "/etc/ssh/sshd_config.d/" + llaves.NombreDropInRoot
)

// sshdPruebaScript runs "sshd -t" and, when it refuses the configuration,
// deletes the staged drop-in named "$1" and exits 11: the sshd is never
// reloaded with a config it will not accept, and the console treats 11 as
// "the VM said no, stop the flow here".
const sshdPruebaScript = "set -e\nif ! sshd -t; then\n  rm -f -- \"$1\"\n  exit 11\nfi"

// dropInVerificadoScript checks the effect of the drop-in with "sshd -T": after
// the reload, root may log in only with a key. "prohibit-password" and its old
// alias "without-password" both mean exactly that; anything else (also an
// unreadable "sshd -T") exits 12.
const dropInVerificadoScript = "set -e\nv=$(sshd -T -C user=root,host=consola,addr=127.0.0.1 | grep -i '^permitrootlogin ') || true\ncase \"$v\" in\n  *prohibit-password*|*without-password*) exit 0 ;;\nesac\nexit 12"

// llaveListaDatos is what web/templates/llave-lista.html receives (as .Data).
type llaveListaDatos struct {
	Token   string // one-time download token (inside the button's href, never shown as data)
	Detalle string // id of the sshd change, shown as the technical detail link when valid; "" otherwise
}

// llavesRoot is B3's one-button flow: generate the root key pair on the VM
// (hostname + mktemp -d + ssh-keygen, no passphrase), stretch the public half
// into the VM's authorized_keys, stage and leave the sshd drop-in live behind
// an "sshd -t" gate and a reload, recover the private half into keys/descargas
// and clean every temporary on the VM. Several plans chain here; the browser
// is redirected EXACTLY once — to the result page of the first failing step,
// or to the "llave lista" page with the one-download token on success.
func (s *server) llavesRoot(w http.ResponseWriter, r *http.Request) {
	cur := s.state.Current()
	ex, ok := s.state.Executor(cur.Name)
	if !ok || ex.Name() != "SSH" {
		s.renderPreparacion(w, r, http.StatusUnprocessableEntity,
			Form{Errors: map[string]string{"": "Para generar las llaves de root, la VM ya debe estar conectada por SSH: primero usa «Conectar por SSH» en Gestión de usuarios."}},
			preparacionData{Formulario: "llaves-root"})
		return
	}

	token, errToken := llaves.NewDownloadToken()
	if errToken != nil {
		s.renderPreparacion(w, r, http.StatusInternalServerError,
			Form{Errors: map[string]string{"": errToken.Error()}},
			preparacionData{Formulario: "llaves-root"})
		return
	}
	rutaPrivada, errPreparada := llaves.PrepareDownload(token)
	if errPreparada != nil {
		s.renderPreparacion(w, r, http.StatusInternalServerError,
			Form{Errors: map[string]string{"": errPreparada.Error()}},
			preparacionData{Formulario: "llaves-root"})
		return
	}

	var dirTemporal string
	eA, pudoA, escrito := s.runPaso(w, r, planGenerarLlaves("Generar llaves de root", cur.Name, "root", &dirTemporal))
	if escrito {
		return
	}
	if !pudoA {
		s.porFallo(w, r, cur.Name, dirTemporal, eA, true, rutaPrivada, limpiezaLlavesRoot(cur.Name, dirTemporal))
		return
	}

	rutaPublica, errPub := os.CreateTemp("", "consola-root-pub-*")
	if errPub != nil {
		e, guardado := s.copiaFallida("Traer la llave pública", cur.Name, ex.Name(), fmt.Errorf("No se pudo preparar el archivo local de la llave pública: %w.", errPub))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesRoot(cur.Name, dirTemporal))
		return
	}
	defer os.Remove(rutaPublica.Name())
	rutaPublica.Close()
	if err := ex.CopyFrom(r.Context(), dirTemporal+"/id_ed25519.pub", rutaPublica.Name()); err != nil {
		e, guardado := s.copiaFallida("Traer la llave pública", cur.Name, ex.Name(), fmt.Errorf("La llave pública (id_ed25519.pub) no se copió de la VM a la consola: %w. El temporal de la VM se borra igual; vuelve a intentarlo.", err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesRoot(cur.Name, dirTemporal))
		return
	}
	pubRoot, errLeer := os.ReadFile(rutaPublica.Name())
	if errLeer != nil {
		e, guardado := s.copiaFallida("Traer la llave pública", cur.Name, ex.Name(), fmt.Errorf("La llave pública llegó pero no se pudo leer en la consola: %w. El temporal de la VM se borra igual; vuelve a intentarlo.", errLeer))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesRoot(cur.Name, dirTemporal))
		return
	}

	eB, pudoB, escritoB := s.runPaso(w, r, planInstalarLlaves("Instalar la llave de root", cur.Name, authorizedKeys, strings.TrimSpace(string(pubRoot)),
		"La llave pública de root quedó instalada en "+authorizedKeys+" de la VM, junto a las llaves que ya tenía. Ahora la consola deja la configuración del sshd lista para que root entre solo con llaves, recarga el servicio y borra los temporales.",
		"", // root's own files already belong to root: no extra chown
		func(leer executor.Command, instalar *executor.Command) func(st oplog.Step) string {
			return func(st oplog.Step) string {
				switch {
				case st.Cmd == leer.String():
					return "No se pudo leer " + authorizedKeys + " en la VM, así que no se cambió nada. Revisa la salida de abajo."
				case st.Cmd == instalar.String() && st.ExitCode == fileChangedCode:
					return authorizedKeys + " cambió mientras se preparaba la llave de root; no se escribió nada. Vuelve a intentarlo."
				case st.Cmd == instalar.String():
					return "No se pudo instalar la llave de root en " + authorizedKeys + ", así que no se cambió nada. Revisa la salida de abajo."
				}
				return ""
			}
		}))
	if escritoB {
		return
	}
	if !pudoB {
		s.porFallo(w, r, cur.Name, dirTemporal, eB, true, rutaPrivada, limpiezaLlavesRoot(cur.Name, dirTemporal))
		return
	}

	rutaDropIn, errDrop := os.CreateTemp("", "consola-dropin-*")
	if errDrop != nil {
		e, guardado := s.copiaFallida("Subir el drop-in del sshd", cur.Name, ex.Name(), fmt.Errorf("No se pudo preparar el archivo local del drop-in %s: %w.", llaves.NombreDropInRoot, errDrop))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesRoot(cur.Name, dirTemporal))
		return
	}
	defer os.Remove(rutaDropIn.Name())
	if err := os.WriteFile(rutaDropIn.Name(), []byte(llaves.ContenidoDropInRoot()), 0o644); err != nil {
		e, guardado := s.copiaFallida("Subir el drop-in del sshd", cur.Name, ex.Name(), fmt.Errorf("No se pudo escribir el drop-in %s en la consola: %w.", llaves.NombreDropInRoot, err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesRoot(cur.Name, dirTemporal))
		return
	}
	rutaDropIn.Close()
	if err := ex.CopyTo(r.Context(), rutaDropIn.Name(), dropInTemporal); err != nil {
		e, guardado := s.copiaFallida("Subir el drop-in del sshd", cur.Name, ex.Name(), fmt.Errorf("El drop-in %s no se pudo subir a la VM: %w. No se cambió nada ahí; vuelve a intentarlo.", llaves.NombreDropInRoot, err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesRoot(cur.Name, dirTemporal))
		return
	}

	eC, pudoC, escritoC := s.runPaso(w, r, planConfigurarSSHRoot(cur.Name))
	if escritoC {
		return
	}
	if !pudoC {
		s.porFallo(w, r, cur.Name, dirTemporal, eC, true, rutaPrivada, limpiezaLlavesRoot(cur.Name, dirTemporal))
		return
	}

	if err := ex.CopyFrom(r.Context(), dirTemporal+"/id_ed25519", rutaPrivada); err != nil {
		e, guardado := s.copiaFallida("Traer la llave privada", cur.Name, ex.Name(), fmt.Errorf("Las llaves de root quedaron instaladas en la VM, pero la privada (id_ed25519) no se pudo traer a la consola: %w. El temporal de la VM se borra igual; vuelve a intentarlo, la operación es repetible.", err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesRoot(cur.Name, dirTemporal))
		return
	}
	if err := os.Chmod(rutaPrivada, 0o600); err != nil {
		e, guardado := s.copiaFallida("Traer la llave privada", cur.Name, ex.Name(), fmt.Errorf("La llave privada llegó a la consola pero no se pudo fijar el permiso 600: %w. Se descarta la copia; vuelve a intentarlo.", err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesRoot(cur.Name, dirTemporal))
		return
	}

	// el flujo completo termina limpio: la VM no guarda ni la carpeta de las
	// llaves ni el drop-in pendiente de /tmp (la operación queda en el registro)
	if _, err := s.runner.Run(r.Context(), planBorrarTemporalesLlaves("Borrar los temporales de las llaves de root", cur.Name, dirTemporal, dropInTemporal)); err != nil {
		log.Printf("Borrar los temporales de las llaves de root: %v", err)
	}

	http.Redirect(w, r, "/usuarios/llaves/listo/"+token+"?id="+eC.ID, http.StatusSeeOther)
}

// runPaso runs one chained plan without redirecting: the handler decides where
// the single redirect goes once every step's outcome is known. The booleans
// separate console-side problems (already written to the browser) from an
// operation that ran and failed (needs its own redirect by the caller).
func (s *server) runPaso(w http.ResponseWriter, r *http.Request, plan operacion.Plan) (oplog.Entry, bool, bool) {
	e, err := s.runner.Run(r.Context(), plan)
	if err != nil {
		log.Printf("%s: %v", plan.Action, err)
		switch {
		case errors.Is(err, operacion.ErrUnknownTarget):
			http.Error(w, "El destino elegido no existe en config.json. Elige otro en la barra superior y vuelve a intentarlo.", http.StatusBadRequest)
		case e.ID == "": // nothing ran: the plan itself is wrong
			http.Error(w, "No se pudo ejecutar la operación. Revisa el registro de la consola en la terminal.", http.StatusInternalServerError)
		}
		return e, false, true
	}
	return e, e.OK, false
}

// copiaFallida writes a manual log entry for a copy that did not happen: copies
// do not log themselves (that is exactly how the private key stays out of the
// operation log file), so their failures deserve their own explanations.
func (s *server) copiaFallida(action, target, channel string, err error) (oplog.Entry, bool) {
	e, addErr := s.log.Add(oplog.Entry{
		Action: action, Target: target, Channel: channel, OK: false,
		Message: err.Error(),
	})
	if addErr != nil {
		log.Printf("%s: sin entrada en el registro: %v", action, addErr)
		return e, false
	}
	return e, true
}

// porFallo ends the flow after any failure: the half-prepared private file is
// discarded (an incomplete copy must never be downloadable), the VM temporals
// are cleaned through the limpieza plan the caller built (an empty plan —
// one with no Target — runs nothing: nothing to clean yet), and the browser
// is sent to the entry that explains what went wrong. When that explanation
// itself could not be stored, the error is reported by the server instead.
func (s *server) porFallo(w http.ResponseWriter, r *http.Request, target string, dirTemporal string, aviso oplog.Entry, guardado bool, rutaPrivada string, limpieza operacion.Plan) {
	_ = os.Remove(rutaPrivada)
	if limpieza.Target != "" {
		if _, err := s.runner.Run(r.Context(), limpieza); err != nil {
			log.Printf("%s: %v", limpieza.Action, err)
		}
	}
	if guardado && aviso.ID != "" {
		http.Redirect(w, r, "/resultado/"+aviso.ID, http.StatusSeeOther)
		return
	}
	http.Error(w, "No se pudo explicar en el registro lo que falló en la operación. Revisa el registro de la consola en la terminal.", http.StatusInternalServerError)
}

// planGenerarLlaves asks the VM for its name and a fresh /tmp directory, then
// generates the ed25519 pair there without a passphrase, labeled with
// usuario@<hostname> (root's flow passes "root" here). dirVM lets the handler
// capture the remote directory the copies (and the cleanup) will use — the
// same pattern planInstalarLlaves uses with its then-built install command.
func planGenerarLlaves(action, target, usuario string, dirVM *string) operacion.Plan {
	return operacion.Plan{
		Action: action,
		Target: target,
		Commands: []executor.Command{
			{Name: "hostname"},
			{Name: "mktemp", Args: []string{"-d"}},
		},
		Then: func(res []executor.Result) ([]executor.Command, error) {
			host := strings.TrimSpace(res[0].Stdout)
			dir := strings.TrimSpace(res[1].Stdout)
			if dir == "" {
				return nil, fmt.Errorf("la VM no entregó una carpeta temporal para las llaves (mktemp -d no imprimió una ruta)")
			}
			*dirVM = dir
			generar := executor.Command{
				Name: "ssh-keygen",
				Args: []string{"-t", "ed25519", "-N", "", "-C", usuario + "@" + host, "-f", dir + "/id_ed25519"},
			}
			return []executor.Command{generar}, nil
		},
		Success: func(res []executor.Result) string {
			return "El par de llaves se generó en la VM, sin frase de seguridad: la privada se guarda aparte para una sola descarga y la pública empieza su instalación en la VM."
		},
	}
}

// planConfigurarSSHRoot installs the staged drop-in into /etc/ssh/sshd_config.d/,
// gates the reload on "sshd -t" (a rejected config deletes the drop-in and
// exits 11, so the unit never reloads a broken sshd), reloads ssh, and
// verifies with "sshd -T" that root now logs in with a key and nothing else.
func planConfigurarSSHRoot(target string) operacion.Plan {
	instalar := executor.Command{Name: "install", Args: []string{"-m", "644", "-o", "root", "-g", "root", "--", dropInTemporal, dropInDestino}}
	prueba := executor.Command{Name: "sh", Args: []string{"-c", sshdPruebaScript, "sh", dropInDestino}}
	recargar := executor.Command{Name: "systemctl", Args: []string{"reload", "ssh"}}
	verificar := executor.Command{Name: "sh", Args: []string{"-c", dropInVerificadoScript}}
	return operacion.Plan{
		Action:   "Configurar el acceso de root por SSH",
		Target:   target,
		Commands: []executor.Command{instalar, prueba, recargar, verificar},
		Success: func(res []executor.Result) string {
			return "El sshd de la VM ahora deja entrar a root solo con llaves: el drop-in " + dropInDestino + " pasó el sshd -t, el servicio se recargó y el sshd -T confirma PermitRootLogin prohibit-password."
		},
		Explain: func(st oplog.Step) string {
			switch {
			case st.Cmd == prueba.String():
				return "El sshd -t de la VM rechazó el drop-in (que se borró), así que NO se recargó el servicio: lo que ya funcionaba sigue funcionando. Revisa la salida de abajo y vuelve a intentarlo."
			case st.Cmd == instalar.String():
				return "No se pudo instalar el drop-in en " + dropInDestino + "; no se cambió nada más. Revisa la salida de abajo."
			case st.Cmd == recargar.String():
				return "No se pudo recargar el sshd de la VM. El drop-in ya está instalado, así que la próxima recarga lo aplicará; revisa la salida de abajo."
			case st.Cmd == verificar.String():
				return "Después de la recarga, el sshd -T no confirma que root entre solo con llaves (PermitRootLogin). Revisa la salida de abajo antes de confiar en las llaves de root."
			}
			return ""
		},
	}
}

// planBorrarTemporalesLlaves makes the VM forget the key directory and the
// staged drop-in named temporal, on success or after any failure, so no
// private key or half-applied configuration stays there. Root's flow and the
// users' flows both call it; only wording and the two paths differ.
func planBorrarTemporalesLlaves(action, target, dirVM, temporal string) operacion.Plan {
	borrarDir := executor.Command{Name: "rm", Args: []string{"-rf", "--", dirVM}}
	borrarTemporal := executor.Command{Name: "rm", Args: []string{"-f", "--", temporal}}
	return operacion.Plan{
		Action:   action,
		Target:   target,
		Commands: []executor.Command{borrarDir, borrarTemporal},
		Success: func(res []executor.Result) string {
			return "La VM se queda sin la carpeta temporal de las llaves ni el drop-in pendiente de /tmp: ninguna llave privada ni configuración a medias."
		},
	}
}

// limpiezaLlavesRoot builds the cleanup root's flow runs when it stops early;
// with no directory yet there is nothing to clean (an empty plan, whose empty
// Target porFallo understands as "run nothing").
func limpiezaLlavesRoot(target, dirVM string) operacion.Plan {
	if dirVM == "" {
		return operacion.Plan{}
	}
	return planBorrarTemporalesLlaves("Borrar los temporales de las llaves de root", target, dirVM, dropInTemporal)
}

// ---- crear usuario (B4) ----

// rutaLlavesDe names the authorized_keys file of one created user: the merge
// and the guarded install target it, and the chown that follows gives both the
// .ssh folder and the key to the account (mode 700 / 600 come from the script).
// VM paths always use forward slashes.
func rutaLlavesDe(usuario string) string {
	return "/home/" + usuario + "/.ssh/authorized_keys"
}

// dropInTemporalUsuario is where ONE user's sshd drop-in is staged inside the
// VM first, the way root's flow stages its own; the real install only happens
// past the sshd -t gate.
func dropInTemporalUsuario(usuario string) string {
	return "/tmp/" + llaves.NombreDropInUsuario(usuario)
}

// limpiezaLlavesUsuario is the cleanup the user-key flow runs when it stops
// early; with no directory yet there is nothing to clean (an empty plan, whose
// empty Target porFallo understands as "run nothing").
func limpiezaLlavesUsuario(target, dirVM, usuario string) operacion.Plan {
	if dirVM == "" {
		return operacion.Plan{}
	}
	return planBorrarTemporalesLlaves("Borrar los temporales de las llaves del usuario", target, dirVM, dropInTemporalUsuario(usuario))
}

// dropInVerificadoScriptFmt is the user-flow variant of dropInVerificadoScript:
// after the reload, the EFFECTIVE sshd for exactly this user must say that
// password logins are off. Anything else (also an unreadable "sshd -T") exits
// 12, and the console treats it as "the change did not take, stop here".
const dropInVerificadoScriptFmt = "set -e\nv=$(sshd -T -C user=%s,host=consola,addr=127.0.0.1 | grep -i '^passwordauthentication ') || true\ncase \"$v\" in\n  *\" no\"*) exit 0 ;;\nesac\nexit 12"

// planConfigurarSSHUsuario installs the staged per-user drop-in into
// /etc/ssh/sshd_config.d/, gates the reload on sshd -t (a rejected config
// deletes the staged drop-in and exits 11, so the unit never reloads a broken
// sshd), reloads ssh, and verifies with sshd -T that THIS user now needs a key
// (passwordauthentication no) while nobody else's configuration moves.
func planConfigurarSSHUsuario(target, usuario string) operacion.Plan {
	temporal := dropInTemporalUsuario(usuario)
	destino := "/etc/ssh/sshd_config.d/" + llaves.NombreDropInUsuario(usuario)
	instalar := executor.Command{Name: "install", Args: []string{"-m", "644", "-o", "root", "-g", "root", "--", temporal, destino}}
	prueba := executor.Command{Name: "sh", Args: []string{"-c", sshdPruebaScript, "sh", destino}}
	recargar := executor.Command{Name: "systemctl", Args: []string{"reload", "ssh"}}
	verificar := executor.Command{Name: "sh", Args: []string{"-c", fmt.Sprintf(dropInVerificadoScriptFmt, usuario)}}
	return operacion.Plan{
		Action:   "Configurar el acceso del usuario por SSH",
		Target:   target,
		Commands: []executor.Command{instalar, prueba, recargar, verificar},
		Success: func(res []executor.Result) string {
			return "El sshd de la VM ahora deja entrar al usuario solo con llaves: el drop-in " + destino + " pasó el sshd -t, el servicio se recargó y el sshd -T confirma passwordauthentication no para " + usuario + "."
		},
		Explain: func(st oplog.Step) string {
			switch {
			case st.Cmd == prueba.String():
				return "El sshd -t de la VM rechazó el drop-in del usuario (que se borró), así que NO se recargó el servicio: lo que ya funcionaba sigue funcionando. Revisa la salida de abajo y vuelve a intentarlo."
			case st.Cmd == instalar.String():
				return "No se pudo instalar el drop-in del usuario en " + destino + "; no se cambió nada más. Revisa la salida de abajo."
			case st.Cmd == recargar.String():
				return "No se pudo recargar el sshd de la VM. El drop-in del usuario ya está instalado, así que la próxima recarga lo aplicará; revisa la salida de abajo."
			case st.Cmd == verificar.String():
				return "Después de la recarga, el sshd -T no confirma que el usuario entre solo con llaves (passwordauthentication). Revisa la salida de abajo antes de confiar en las llaves del usuario."
			}
			return ""
		},
	}
}

// valoresDeFormulario rebuilds what the user typed for a rejected form: the
// password is deliberately absent (the console never echoes one back, DB13's
// spirit), so the browser keeps it out of the page and out of caches.
func valoresDeFormulario(usuario string, sudo, llave bool) map[string]string {
	valores := map[string]string{"usuario": usuario}
	if sudo {
		valores["sudo"] = "on"
	}
	if llave {
		valores["llave"] = "on"
	}
	return valores
}

// usuarioCrear is B4's one-button flow: validate the form locally (validate.Usuario
// + validate.Contrasena), check the name is free with getent (exit 2 means
// "free" and is logged as a success), create the account with the password
// travelling only through chpasswd's Stdin, add the sudo membership when
// asked, and — when the llave checkbox was on — run the same guarded key story
// B3 runs for root, only with the account's own folder, owner and drop-in,
// ending in the same one-time download page. The browser is redirected
// EXACTLY once, to the result page of the first failing step or to the "llave
// lista" page on success; a rejected form re-renders with errors next to the
// fields and no password in it.
func (s *server) usuarioCrear(w http.ResponseWriter, r *http.Request) {
	cur := s.state.Current()
	ex, hayCanal := s.state.Executor(cur.Name)
	datos := usuariosDatos{Canal: s.state.Channel(cur.Name)}
	mostrar := func(estado int, form Form) {
		s.renderModule(w, r, "usuarios", estado, form, datos)
	}
	if !hayCanal || ex.Name() != "SSH" {
		mostrar(http.StatusUnprocessableEntity, Form{Errors: map[string]string{"": "Para crear un usuario, la VM ya debe estar conectada por SSH: primero usa «Conectar por SSH»."}})
		return
	}
	if err := r.ParseForm(); err != nil {
		mostrar(http.StatusUnprocessableEntity, Form{Errors: map[string]string{"": "No se pudo leer el formulario. Vuelve a intentarlo."}})
		return
	}
	usuario := strings.TrimSpace(r.PostForm.Get("usuario"))
	contrasena := r.PostForm.Get("contrasena")
	sudo := r.PostForm.Get("sudo") != ""
	llave := r.PostForm.Get("llave") != ""

	var errores []error
	if err := validate.Usuario(usuario); err != nil {
		errores = append(errores, err)
	}
	if err := validate.Contrasena(contrasena); err != nil {
		errores = append(errores, err)
	}
	if len(errores) > 0 {
		mostrar(http.StatusUnprocessableEntity, Form{Values: valoresDeFormulario(usuario, sudo, llave), Errors: FieldErrors(errores...)})
		return
	}

	eA, pudoA, escrito := s.runPaso(w, r, usuarios.PlanRevisar(cur.Name, usuario))
	if escrito {
		return
	}
	if !pudoA {
		s.porFallo(w, r, cur.Name, "", eA, true, "", operacion.Plan{})
		return
	}
	if eA.Steps[0].ExitCode == 0 {
		mostrar(http.StatusUnprocessableEntity, Form{
			Values: valoresDeFormulario(usuario, sudo, llave),
			Errors: map[string]string{"usuario": "Ya existe un usuario con ese nombre en la VM: elige otro nombre y vuelve a intentarlo."},
		})
		return
	}

	eB, pudoB, escritoB := s.runPaso(w, r, usuarios.PlanCrear(cur.Name, usuario, contrasena, sudo))
	if escritoB {
		return
	}
	if !pudoB {
		s.porFallo(w, r, cur.Name, "", eB, true, "", operacion.Plan{})
		return
	}
	if !llave {
		http.Redirect(w, r, "/resultado/"+eB.ID, http.StatusSeeOther)
		return
	}

	token, errToken := llaves.NewDownloadToken()
	if errToken != nil {
		mostrar(http.StatusInternalServerError, Form{Errors: map[string]string{"": errToken.Error()}})
		return
	}
	rutaPrivada, errPreparada := llaves.PrepareDownload(token)
	if errPreparada != nil {
		mostrar(http.StatusInternalServerError, Form{Errors: map[string]string{"": errPreparada.Error()}})
		return
	}

	dirTemporal := ""
	eC, pudoC, escritoC := s.runPaso(w, r, planGenerarLlaves("Generar las llaves del usuario", cur.Name, usuario, &dirTemporal))
	if escritoC {
		return
	}
	if !pudoC {
		s.porFallo(w, r, cur.Name, dirTemporal, eC, true, rutaPrivada, limpiezaLlavesUsuario(cur.Name, dirTemporal, usuario))
		return
	}

	rutaPublica, errPub := os.CreateTemp("", "consola-usuario-pub-*")
	if errPub != nil {
		e, guardado := s.copiaFallida("Traer la llave pública del usuario", cur.Name, ex.Name(), fmt.Errorf("No se pudo preparar el archivo local de la llave pública: %w.", errPub))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesUsuario(cur.Name, dirTemporal, usuario))
		return
	}
	defer os.Remove(rutaPublica.Name())
	rutaPublica.Close()
	if err := ex.CopyFrom(r.Context(), dirTemporal+"/id_ed25519.pub", rutaPublica.Name()); err != nil {
		e, guardado := s.copiaFallida("Traer la llave pública del usuario", cur.Name, ex.Name(), fmt.Errorf("La llave pública (id_ed25519.pub) no se copió de la VM a la consola: %w. El temporal de la VM se borra igual; vuelve a intentarlo.", err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesUsuario(cur.Name, dirTemporal, usuario))
		return
	}
	pubUsuario, errLeer := os.ReadFile(rutaPublica.Name())
	if errLeer != nil {
		e, guardado := s.copiaFallida("Traer la llave pública del usuario", cur.Name, ex.Name(), fmt.Errorf("La llave pública llegó pero no se pudo leer en la consola: %w. El temporal de la VM se borra igual; vuelve a intentarlo.", errLeer))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesUsuario(cur.Name, dirTemporal, usuario))
		return
	}

	eD, pudoD, escritoD := s.runPaso(w, r, planInstalarLlaves("Instalar la llave del usuario", cur.Name, rutaLlavesDe(usuario), strings.TrimSpace(string(pubUsuario)),
		"La llave pública del usuario quedó instalada en "+rutaLlavesDe(usuario)+" de la VM, con dueño "+usuario+":"+usuario+". Ahora la consola deja la configuración del sshd lista para que este usuario entre solo con llaves, recarga el servicio y borra los temporales.",
		usuario+":"+usuario,
		func(leer executor.Command, instalar *executor.Command) func(st oplog.Step) string {
			return func(st oplog.Step) string {
				switch {
				case st.Cmd == leer.String():
					return "No se pudo leer " + rutaLlavesDe(usuario) + " en la VM, así que no se cambió nada. Revisa la salida de abajo."
				case st.Cmd == instalar.String() && st.ExitCode == fileChangedCode:
					return rutaLlavesDe(usuario) + " cambió mientras se preparaba la llave; no se escribió nada. Vuelve a intentarlo."
				case st.Cmd == instalar.String():
					return "No se pudo instalar la llave del usuario en " + rutaLlavesDe(usuario) + ", así que no se cambió nada. Revisa la salida de abajo."
				case strings.HasPrefix(st.Cmd, "chown "):
					return "La llave quedó instalada, pero la consola no pudo darle el dueño " + usuario + ":" + usuario + " (ni a su carpeta .ssh). Vuelve a intentarlo: la operación es repetible y terminará de ponerlo bien."
				}
				return ""
			}
		}))
	if escritoD {
		return
	}
	if !pudoD {
		s.porFallo(w, r, cur.Name, dirTemporal, eD, true, rutaPrivada, limpiezaLlavesUsuario(cur.Name, dirTemporal, usuario))
		return
	}

	rutaDropIn, errDrop := os.CreateTemp("", "consola-dropin-usuario-*")
	if errDrop != nil {
		e, guardado := s.copiaFallida("Subir el drop-in del usuario", cur.Name, ex.Name(), fmt.Errorf("No se pudo preparar el archivo local del drop-in %s: %w.", llaves.NombreDropInUsuario(usuario), errDrop))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesUsuario(cur.Name, dirTemporal, usuario))
		return
	}
	defer os.Remove(rutaDropIn.Name())
	if err := os.WriteFile(rutaDropIn.Name(), []byte(llaves.ContenidoDropInUsuario(usuario)), 0o644); err != nil {
		e, guardado := s.copiaFallida("Subir el drop-in del usuario", cur.Name, ex.Name(), fmt.Errorf("No se pudo escribir el drop-in %s en la consola: %w.", llaves.NombreDropInUsuario(usuario), err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesUsuario(cur.Name, dirTemporal, usuario))
		return
	}
	rutaDropIn.Close()
	if err := ex.CopyTo(r.Context(), rutaDropIn.Name(), dropInTemporalUsuario(usuario)); err != nil {
		e, guardado := s.copiaFallida("Subir el drop-in del usuario", cur.Name, ex.Name(), fmt.Errorf("El drop-in %s no se pudo subir a la VM: %w. No se cambió nada ahí; vuelve a intentarlo.", llaves.NombreDropInUsuario(usuario), err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesUsuario(cur.Name, dirTemporal, usuario))
		return
	}

	eE, pudoE, escritoE := s.runPaso(w, r, planConfigurarSSHUsuario(cur.Name, usuario))
	if escritoE {
		return
	}
	if !pudoE {
		s.porFallo(w, r, cur.Name, dirTemporal, eE, true, rutaPrivada, limpiezaLlavesUsuario(cur.Name, dirTemporal, usuario))
		return
	}

	if err := ex.CopyFrom(r.Context(), dirTemporal+"/id_ed25519", rutaPrivada); err != nil {
		e, guardado := s.copiaFallida("Traer la llave privada", cur.Name, ex.Name(), fmt.Errorf("Las llaves del usuario quedaron instaladas en la VM, pero la privada (id_ed25519) no se pudo traer a la consola: %w. El temporal de la VM se borra igual; vuelve a intentarlo, la operación es repetible.", err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesUsuario(cur.Name, dirTemporal, usuario))
		return
	}
	if err := os.Chmod(rutaPrivada, 0o600); err != nil {
		e, guardado := s.copiaFallida("Traer la llave privada", cur.Name, ex.Name(), fmt.Errorf("La llave privada llegó a la consola pero no se pudo fijar el permiso 600: %w. Se descarta la copia; vuelve a intentarlo.", err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada, limpiezaLlavesUsuario(cur.Name, dirTemporal, usuario))
		return
	}

	// el flujo completo termina limpio: la VM no guarda ni la carpeta de las
	// llaves ni el drop-in pendiente de /tmp (la operación queda en el registro)
	if _, err := s.runner.Run(r.Context(), limpiezaLlavesUsuario(cur.Name, dirTemporal, usuario)); err != nil && limpiezaLlavesUsuario(cur.Name, dirTemporal, usuario).Target != "" {
		log.Printf("%s: %v", "Borrar los temporales de las llaves del usuario", err)
	}

	http.Redirect(w, r, "/usuarios/llaves/listo/"+token+"?id="+eE.ID, http.StatusSeeOther)
}

// usuarioEliminar is B5's action. The browser reaches it only from the second
// screen the page renders after "Continuar" (?confirmar=<u>[&archivos=on]), so
// a single click never deletes anything. The handler refuses DB14's protected
// accounts and any invalid name BEFORE running a command, and only then sends
// PlanEliminar: userdel (with -r when the checkbox asked), the per-user drop-in
// removal, the sshd -t gate and the reload. The result lands on the ordinary
// operation page, where the "user has active processes" stderr gets its own
// explanation.
func (s *server) usuarioEliminar(w http.ResponseWriter, r *http.Request) {
	cur := s.state.Current()
	datos := usuariosDatos{Canal: s.state.Channel(cur.Name), Formulario: "eliminar"}
	mostrar := func(estado int, form Form) {
		// A rejected form sends NO command (DB14's test asks for exactly that),
		// so the list is not re-read here: the page tells the user to reload it.
		datos.AvisoLista = "Vuelve a abrir esta página para ver la lista de cuentas."
		s.renderModule(w, r, "usuarios", estado, form, datos)
	}
	ex, hayCanal := s.state.Executor(cur.Name)
	if !hayCanal || ex.Name() != "SSH" {
		mostrar(http.StatusUnprocessableEntity, Form{Errors: map[string]string{"": "Para eliminar un usuario, la VM ya debe estar conectada por SSH: primero usa «Conectar por SSH»."}})
		return
	}
	if err := r.ParseForm(); err != nil {
		mostrar(http.StatusUnprocessableEntity, Form{Errors: map[string]string{"": "No se pudo leer el formulario. Vuelve a intentarlo."}})
		return
	}
	usuario := strings.TrimSpace(r.PostForm.Get("usuario"))
	archivos := r.PostForm.Get("archivos") != ""
	if err := validate.Usuario(usuario); err != nil {
		mostrar(http.StatusUnprocessableEntity, Form{Values: map[string]string{"usuario": usuario}, Errors: FieldErrors(err)})
		return
	}
	if protegidosDe(cur)[usuario] {
		mostrar(http.StatusUnprocessableEntity, Form{Values: map[string]string{"usuario": usuario}, Errors: map[string]string{"usuario": "No se puede eliminar la cuenta «" + usuario + "»: la consola la necesita para seguir administrando la VM."}})
		return
	}

	e, pudo, escrito := s.runPaso(w, r, usuarios.PlanEliminar(cur.Name, usuario, archivos))
	if escrito {
		return
	}
	if !pudo {
		s.porFallo(w, r, cur.Name, "", e, true, "", operacion.Plan{})
		return
	}
	http.Redirect(w, r, "/resultado/"+e.ID, http.StatusSeeOther)
}

// llaveLista is the page the browser lands on after the flow: it offers the
// one-time download and, when the sshd change's id is in the log (?id=), the
// technical detail link. Anything outside a real token is a 404.
func (s *server) llaveLista(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if _, err := llaves.DownloadPath(token); err != nil {
		s.noEncontrada(w, r)
		return
	}
	v := s.newView(r, "Tu llave está lista", "usuarios")
	datos := llaveListaDatos{Token: token}
	if id := r.URL.Query().Get("id"); reOperationID.MatchString(id) {
		if e, ok := s.log.Get(id); ok {
			datos.Detalle = e.ID
		}
	}
	v.Data = datos
	s.render(w, http.StatusOK, "llave-lista.html", v)
}

// descargarLlave serves the private key the token names and deletes it in the
// same read (DB11: one download per token). Any miss — bad token, already
// taken, expired — is a 404; the bytes are never cached anywhere.
func (s *server) descargarLlave(w http.ResponseWriter, r *http.Request) {
	data, err := llaves.ReadDeleteDownload(r.PathValue("token"))
	if err != nil {
		log.Printf("descarga de la llave privada: %v", err)
		s.noEncontrada(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", `attachment; filename="id_ed25519"`)
	h.Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

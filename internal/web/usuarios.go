package web

// usuarios.go is Part B's "user management" module. This file starts with the
// meeting point (B2): one button installs the console's own key into the VM's
// /root/.ssh/authorized_keys through the channel the console already has (on a
// prepared VM that is Guest Additions; the simulated channel plays along),
// checks that the VM opens an SSH session with that key, and — on success —
// switches the target's channel to SSH, so every following operation goes over
// the SSH. The user accounts themselves arrive in the following phases of
// Part B: B3 completes the root-key story — the same button story (one POST)
// generates a dedicated pair for root on the VM, leaves the sshd drop-in alive
// behind an "sshd -t" gate and a reload, and recovers the private key into the
// console for exactly one download, without ever logging its bytes.

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/llaves"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
)

func init() {
	registerModule("usuarios", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /usuarios", s.usuariosPagina)
		r.HandleFunc("POST /usuarios/conectar-ssh", s.conectarSSH)
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
	Canal string // the channel the current target uses right now
}

func (s *server) usuariosPagina(w http.ResponseWriter, r *http.Request) {
	cur := s.state.Current()
	s.renderModule(w, r, "usuarios", http.StatusOK, Form{}, usuariosDatos{Canal: s.state.Channel(cur.Name)})
}

// conectarSSH is the B2 action: install the console key in the VM (whatever
// channel it has now, Guest Additions on a prepared VM), then verify the SSH
// answer and switch the channel so the following operations go over SSH.
func (s *server) conectarSSH(w http.ResponseWriter, r *http.Request) {
	cur := s.state.Current()
	pub, err := llaves.PublicKey(llaves.Dir)
	if err != nil {
		datos := usuariosDatos{Canal: s.state.Channel(cur.Name)}
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
	return planInstalarLlaves("Conectar por SSH", target, pub,
		"La llave de la consola quedó instalada en "+authorizedKeys+" de la VM, junto a las llaves que ya tenía, y quedó una copia del archivo original (la copia no se crea para un archivo que la propia consola tuvo que crear). Cuando la prueba de conexión por SSH también funciona, la barra superior muestra SSH y las siguientes operaciones van por ese canal; si no, revisa la salida de abajo.",
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

// planInstalarLlaves reads the VM's authorized_keys (a marker when there is no
// file, which is the normal first contact), merges pub into it without
// dropping what was there (llaves.MergeAuthorizedKeys), and installs the
// result with the same guarded, atomic write every console file change uses.
// Rerunning after any failure is safe: the read decides again. Action is the
// name the operation log shows; success is the outcome text on a green run;
// the explain function receives the read command and a POINTER to the install
// command this plan is about to build in Then (its args depend on the read),
// so failures can name the exact command the VM refused.
func planInstalarLlaves(action, target, pub, success string, explicar func(leer executor.Command, instalar *executor.Command) func(st oplog.Step) string) operacion.Plan {
	leer := executor.Command{Name: "sh", Args: []string{"-c", leerLlavesScript, "sh", authorizedKeys}}
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
				Args: []string{"-c", instalarLlavesScript, "sh", authorizedKeys,
					llaves.MergeAuthorizedKeys(existentes, pub), sha,
				},
			}
			return []executor.Command{instalar}, nil
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
	eA, pudoA, escrito := s.runPaso(w, r, planGenerarLlavesRoot(cur.Name, &dirTemporal))
	if escrito {
		return
	}
	if !pudoA {
		s.porFallo(w, r, cur.Name, dirTemporal, eA, true, rutaPrivada)
		return
	}

	rutaPublica, errPub := os.CreateTemp("", "consola-root-pub-*")
	if errPub != nil {
		e, guardado := s.copiaFallida("Traer la llave pública", cur.Name, ex.Name(), fmt.Errorf("No se pudo preparar el archivo local de la llave pública: %w.", errPub))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada)
		return
	}
	defer os.Remove(rutaPublica.Name())
	rutaPublica.Close()
	if err := ex.CopyFrom(r.Context(), dirTemporal+"/id_ed25519.pub", rutaPublica.Name()); err != nil {
		e, guardado := s.copiaFallida("Traer la llave pública", cur.Name, ex.Name(), fmt.Errorf("La llave pública (id_ed25519.pub) no se copió de la VM a la consola: %w. El temporal de la VM se borra igual; vuelve a intentarlo.", err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada)
		return
	}
	pubRoot, errLeer := os.ReadFile(rutaPublica.Name())
	if errLeer != nil {
		e, guardado := s.copiaFallida("Traer la llave pública", cur.Name, ex.Name(), fmt.Errorf("La llave pública llegó pero no se pudo leer en la consola: %w. El temporal de la VM se borra igual; vuelve a intentarlo.", errLeer))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada)
		return
	}

	eB, pudoB, escritoB := s.runPaso(w, r, planInstalarLlaves("Instalar la llave de root", cur.Name, strings.TrimSpace(string(pubRoot)),
		"La llave pública de root quedó instalada en "+authorizedKeys+" de la VM, junto a las llaves que ya tenía. Ahora la consola deja la configuración del sshd lista para que root entre solo con llaves, recarga el servicio y borra los temporales.",
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
		s.porFallo(w, r, cur.Name, dirTemporal, eB, true, rutaPrivada)
		return
	}

	rutaDropIn, errDrop := os.CreateTemp("", "consola-dropin-*")
	if errDrop != nil {
		e, guardado := s.copiaFallida("Subir el drop-in del sshd", cur.Name, ex.Name(), fmt.Errorf("No se pudo preparar el archivo local del drop-in %s: %w.", llaves.NombreDropInRoot, errDrop))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada)
		return
	}
	defer os.Remove(rutaDropIn.Name())
	if err := os.WriteFile(rutaDropIn.Name(), []byte(llaves.ContenidoDropInRoot()), 0o644); err != nil {
		e, guardado := s.copiaFallida("Subir el drop-in del sshd", cur.Name, ex.Name(), fmt.Errorf("No se pudo escribir el drop-in %s en la consola: %w.", llaves.NombreDropInRoot, err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada)
		return
	}
	rutaDropIn.Close()
	if err := ex.CopyTo(r.Context(), rutaDropIn.Name(), dropInTemporal); err != nil {
		e, guardado := s.copiaFallida("Subir el drop-in del sshd", cur.Name, ex.Name(), fmt.Errorf("El drop-in %s no se pudo subir a la VM: %w. No se cambió nada ahí; vuelve a intentarlo.", llaves.NombreDropInRoot, err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada)
		return
	}

	eC, pudoC, escritoC := s.runPaso(w, r, planConfigurarSSHRoot(cur.Name))
	if escritoC {
		return
	}
	if !pudoC {
		s.porFallo(w, r, cur.Name, dirTemporal, eC, true, rutaPrivada)
		return
	}

	if err := ex.CopyFrom(r.Context(), dirTemporal+"/id_ed25519", rutaPrivada); err != nil {
		e, guardado := s.copiaFallida("Traer la llave privada", cur.Name, ex.Name(), fmt.Errorf("Las llaves de root quedaron instaladas en la VM, pero la privada (id_ed25519) no se pudo traer a la consola: %w. El temporal de la VM se borra igual; vuelve a intentarlo, la operación es repetible.", err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada)
		return
	}
	if err := os.Chmod(rutaPrivada, 0o600); err != nil {
		e, guardado := s.copiaFallida("Traer la llave privada", cur.Name, ex.Name(), fmt.Errorf("La llave privada llegó a la consola pero no se pudo fijar el permiso 600: %w. Se descarta la copia; vuelve a intentarlo.", err))
		s.porFallo(w, r, cur.Name, dirTemporal, e, guardado, rutaPrivada)
		return
	}

	// el flujo completo termina limpio: la VM no guarda ni la carpeta de las
	// llaves ni el drop-in pendiente de /tmp (la operación queda en el registro)
	if _, err := s.runner.Run(r.Context(), planBorrarTemporalesLlavesRoot(cur.Name, dirTemporal)); err != nil {
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
// are cleaned through their own logged operation, and the browser is sent to
// the entry that explains what went wrong. When that explanation itself could
// not be stored, the error is reported by the server instead.
func (s *server) porFallo(w http.ResponseWriter, r *http.Request, target string, dirTemporal string, aviso oplog.Entry, guardado bool, rutaPrivada string) {
	_ = os.Remove(rutaPrivada)
	if dirTemporal != "" {
		if _, err := s.runner.Run(r.Context(), planBorrarTemporalesLlavesRoot(target, dirTemporal)); err != nil {
			log.Printf("Borrar los temporales de las llaves de root: %v", err)
		}
	}
	if guardado && aviso.ID != "" {
		http.Redirect(w, r, "/resultado/"+aviso.ID, http.StatusSeeOther)
		return
	}
	http.Error(w, "No se pudo explicar en el registro lo que falló en la operación. Revisa el registro de la consola en la terminal.", http.StatusInternalServerError)
}

// planGenerarLlavesRoot asks the VM for its name and a fresh /tmp directory,
// then generates the ed25519 pair there without a passphrase, labeled with the
// VM's hostname. dirVM lets the handler capture the remote directory the
// copies (and the cleanup) will use — the same pattern planInstalarLlaves uses
// with its then-built install command.
func planGenerarLlavesRoot(target string, dirVM *string) operacion.Plan {
	return operacion.Plan{
		Action: "Generar llaves de root",
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
				Args: []string{"-t", "ed25519", "-N", "", "-C", "root@" + host, "-f", dir + "/id_ed25519"},
			}
			return []executor.Command{generar}, nil
		},
		Success: func(res []executor.Result) string {
			return "El par de llaves de root se generó en la VM, sin frase de seguridad: la privada se guarda aparte para una sola descarga y la pública empieza su instalación en la VM."
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
		Action: "Configurar el acceso de root por SSH",
		Target: target,
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

// planBorrarTemporalesLlavesRoot makes the VM forget the key directory and the
// staged drop-in, on success or after any failure, so no private key or
// half-applied configuration stays there.
func planBorrarTemporalesLlavesRoot(target, dirVM string) operacion.Plan {
	borrarDir := executor.Command{Name: "rm", Args: []string{"-rf", "--", dirVM}}
	borrarTemporal := executor.Command{Name: "rm", Args: []string{"-f", "--", dropInTemporal}}
	return operacion.Plan{
		Action:   "Borrar los temporales de las llaves de root",
		Target:   target,
		Commands: []executor.Command{borrarDir, borrarTemporal},
		Success: func(res []executor.Result) string {
			return "La VM se queda sin la carpeta temporal de las llaves ni el drop-in pendiente de /tmp: ninguna llave privada ni configuración a medias."
		},
	}
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

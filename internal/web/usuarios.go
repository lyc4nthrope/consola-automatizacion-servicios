package web

// usuarios.go is Part B's "user management" module. This file starts with the
// meeting point (B2): one button installs the console's own key into the VM's
// /root/.ssh/authorized_keys through the channel the console already has (on a
// prepared VM that is Guest Additions; the simulated channel plays along),
// checks that the VM opens an SSH session with that key, and — on success —
// switches the target's channel to SSH, so every following operation goes over
// the SSH. The user accounts themselves arrive in the following phases of
// Part B.

import (
	"net/http"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/llaves"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
)

func init() {
	registerModule("usuarios", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /usuarios", s.usuariosPagina)
		r.HandleFunc("POST /usuarios/conectar-ssh", s.conectarSSH)
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
func planConectarSSH(target, pub string) operacion.Plan {
	leer := executor.Command{Name: "sh", Args: []string{"-c", leerLlavesScript, "sh", authorizedKeys}}
	var instalar executor.Command
	return operacion.Plan{
		Action:   "Conectar por SSH",
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
			return "La llave de la consola quedó instalada en " + authorizedKeys + " de la VM, junto a las llaves que ya tenía, y quedó una copia del archivo original (la copia no se crea para un archivo que la propia consola tuvo que crear). Cuando la prueba de conexión por SSH también funciona, la barra superior muestra SSH y las siguientes operaciones van por ese canal; si no, revisa la salida de abajo."
		},
		Explain: func(st oplog.Step) string {
			switch {
			case st.Cmd == leer.String():
				return "No se pudo leer " + authorizedKeys + " en la VM (¿es una carpeta?), así que no se cambió nada. Revisa la salida de abajo."
			case st.Cmd == instalar.String() && st.ExitCode == fileChangedCode:
				return authorizedKeys + " cambió entre la lectura y la escritura (otra consola u otra operación lo tocó), así que no se escribió nada. Vuelve a intentarlo."
			case st.Cmd == instalar.String():
				return "No se pudo instalar la llave en " + authorizedKeys + ", así que no se cambió nada. Revisa la salida de abajo."
			}
			return ""
		},
	}
}

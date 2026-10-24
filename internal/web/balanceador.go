package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/balanceador"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/validate"
)

const haproxyCfg = "/etc/haproxy/haproxy.cfg"

// Exit codes of applyHaproxyScript.
const (
	haproxyChangedCode = 12 // the file changed after it was read
	haproxyInvalidCode = 13 // haproxy -c rejected the new file
	haproxyReloadCode  = 14 // the new file is in place, but the reload failed
)

// applyHaproxyScript writes the new configuration ($1) next to the file ($2), checks it
// with haproxy -c and only then replaces the file and reloads. $3 is the sha256 of the
// file that was read: if it changed meanwhile, nothing is written. The previous file is
// kept as haproxy.cfg.consola-anterior. The content travels as an argument, never as
// shell text.
const applyHaproxyScript = `set -e; umask 022
c=$(sha256sum < "$2") || exit 12
[ "${c%% *}" = "$3" ] || exit 12
t=$(mktemp "$2.consola-XXXXXX")
trap 'rm -f "$t"' EXIT
printf '%s' "$1" > "$t"
chmod 644 "$t"
haproxy -c -q -f "$t" || exit 13
cp -p "$2" "$2.consola-anterior"
mv "$t" "$2"
systemctl reload haproxy || exit 14`

func init() {
	registerModule("balanceador", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /balanceador", s.balanceadorPagina)
		r.HandleFunc("POST /balanceador/agregar", s.agregarServidor)
		r.HandleFunc("POST /balanceador/quitar", s.quitarServidor)
	})
}

// balanceadorDatos tells the page which form the errors belong to.
type balanceadorDatos struct{ Formulario string }

func (s *server) balanceadorPagina(w http.ResponseWriter, r *http.Request) {
	s.renderModule(w, r, "balanceador", http.StatusOK, Form{}, balanceadorDatos{})
}

func (s *server) agregarServidor(w http.ResponseWriter, r *http.Request) {
	backend := strings.TrimSpace(r.FormValue("backend"))
	ip := strings.TrimSpace(r.FormValue("ip"))
	port := strings.TrimSpace(r.FormValue("puerto"))
	if errs := FieldErrors(balanceador.ValidateBackend(backend), validate.IPv4(ip), validate.Puerto(port)); len(errs) > 0 {
		form := Form{Values: map[string]string{"backend": backend, "ip": ip, "puerto": port}, Errors: errs}
		s.renderModule(w, r, "balanceador", http.StatusUnprocessableEntity, form, balanceadorDatos{"agregar"})
		return
	}
	srv := balanceador.Server{Name: balanceador.ServerName(ip, port), IP: ip, Port: port}
	s.runAndShow(w, r, planHaproxy(s.state.Current().Name, "Agregar servidor al balanceador",
		func(cfg string) (string, error) { return balanceador.AddServer(cfg, backend, srv) },
		"Se agregó "+srv.Address()+" al back-end «"+backend+"» como «"+srv.Name+"» y HAProxy recargó la configuración sin cortar las conexiones abiertas."))
}

func (s *server) quitarServidor(w http.ResponseWriter, r *http.Request) {
	backend := strings.TrimSpace(r.FormValue("backend-quitar"))
	ip := strings.TrimSpace(r.FormValue("ip-quitar"))
	if errs := FieldErrors(asField(balanceador.ValidateBackend(backend), "backend-quitar"), asField(validate.IPv4(ip), "ip-quitar")); len(errs) > 0 {
		form := Form{Values: map[string]string{"backend-quitar": backend, "ip-quitar": ip}, Errors: errs}
		s.renderModule(w, r, "balanceador", http.StatusUnprocessableEntity, form, balanceadorDatos{"quitar"})
		return
	}
	s.runAndShow(w, r, planHaproxy(s.state.Current().Name, "Quitar servidor del balanceador",
		func(cfg string) (string, error) { return balanceador.RemoveServer(cfg, backend, ip) },
		"Se quitó "+ip+" del back-end «"+backend+"» y HAProxy recargó la configuración."))
}

// planHaproxy reads haproxy.cfg, changes it with edit (a pure function) and applies it
// with applyHaproxyScript. An error from edit stops the operation before anything is
// written; its text is the message for the user.
func planHaproxy(target, action string, edit func(string) (string, error), success string) operacion.Plan {
	read := executor.Command{Name: "cat", Args: []string{"--", haproxyCfg}}
	var apply executor.Command
	return operacion.Plan{
		Action:   action,
		Target:   target,
		Commands: []executor.Command{read},
		Then: func(res []executor.Result) ([]executor.Command, error) {
			cfg, err := edit(res[0].Stdout)
			if err != nil {
				return nil, err
			}
			apply = executor.Command{Name: "sh", Args: []string{"-c", applyHaproxyScript, "sh", cfg, haproxyCfg, sha256Hex(res[0].Stdout)}}
			return []executor.Command{apply}, nil
		},
		Success: func([]executor.Result) string { return success },
		Explain: func(st oplog.Step) string {
			switch {
			case st.Cmd == read.String():
				return "No se pudo leer " + haproxyCfg + ". ¿El destino elegido es el balanceador y tiene HAProxy instalado?"
			case st.Cmd == apply.String() && st.ExitCode == haproxyInvalidCode:
				return "HAProxy rechazó la configuración resultante (haproxy -c), así que no se aplicó ningún cambio. Revisa la salida de abajo."
			case st.Cmd == apply.String() && st.ExitCode == haproxyChangedCode:
				return haproxyCfg + " cambió mientras la consola lo leía, así que no se cambió nada. Vuelve a intentarlo."
			case st.Cmd == apply.String() && st.ExitCode == haproxyReloadCode:
				return "El archivo nuevo quedó escrito (el anterior está en haproxy.cfg.consola-anterior), pero falló la recarga de HAProxy. Revisa: systemctl status haproxy."
			case st.Cmd == apply.String():
				return "No se pudo escribir " + haproxyCfg + ", así que no se cambió nada. Revisa la salida de abajo."
			}
			return ""
		},
	}
}

// asField moves a validation error to another form field (the same rule used by two
// forms on one page).
func asField(err error, field string) error {
	var v *validate.Error
	if errors.As(err, &v) {
		return &validate.Error{Field: field, Msg: v.Msg}
	}
	return err
}

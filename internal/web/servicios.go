package web

import (
	"net/http"
	"strings"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/servicios"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/validate"
)

const (
	// ensureUserScript creates the system user $1 only if it does not exist, so a second
	// deployment of the same service works.
	ensureUserScript = `id -u -- "$1" >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin -- "$1"`
	// writeUnitScript writes the unit ($1) to $2 atomically. The content travels as an
	// argument, never as shell text.
	writeUnitScript = `set -e; umask 022
t=$(mktemp "$2.consola-XXXXXX")
trap 'rm -f "$t"' EXIT
printf '%s' "$1" > "$t"
chmod 644 "$t"
mv "$t" "$2"`
)

func init() {
	registerModule("servicios", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /servicios", s.serviciosPagina)
		r.HandleFunc("POST /servicios/desplegar", s.desplegarServicio)
		r.HandleFunc("POST /servicios/accion", s.accionServicio)
	})
}

// serviciosDatos tells the page which form the errors belong to.
type serviciosDatos struct{ Formulario string }

func (s *server) serviciosPagina(w http.ResponseWriter, r *http.Request) {
	s.renderModule(w, r, "servicios", http.StatusOK, Form{}, serviciosDatos{})
}

func (s *server) desplegarServicio(w http.ResponseWriter, r *http.Request) {
	cfg := servicios.Config{
		Description: strings.TrimSpace(r.FormValue("aplicacion")),
		Name:        strings.TrimSpace(r.FormValue("servicio")),
		Exec:        strings.TrimSpace(r.FormValue("ejecutable")),
		Port:        strings.TrimSpace(r.FormValue("puerto")),
	}
	var errPuerto error
	if cfg.Port != "" {
		errPuerto = validate.Puerto(cfg.Port)
	}
	if errs := FieldErrors(servicios.ValidateDescription(cfg.Description), servicios.ValidateName(cfg.Name), servicios.ValidateExec(cfg.Exec), errPuerto); len(errs) > 0 {
		form := Form{Values: map[string]string{"aplicacion": cfg.Description, "servicio": cfg.Name, "ejecutable": cfg.Exec, "puerto": cfg.Port}, Errors: errs}
		s.renderModule(w, r, "servicios", http.StatusUnprocessableEntity, form, serviciosDatos{"desplegar"})
		return
	}
	s.runAndShow(w, r, planDesplegar(s.state.Current().Name, cfg))
}

// planDesplegar installs the unit of a program that is already in the VM (DC6–DC8):
// system user, data folder, unit file, daemon-reload, systemd-analyze verify, enable.
// Copying the program from the host (DC4, DC5) is still pending: see docs/parte-c/PLAN.md.
func planDesplegar(target string, cfg servicios.Config) operacion.Plan {
	unitPath := "/etc/systemd/system/" + cfg.Name + ".service"
	exists := executor.Command{Name: "test", Args: []string{"-x", cfg.Exec}}
	user := executor.Command{Name: "sh", Args: []string{"-c", ensureUserScript, "sh", cfg.Name}}
	data := executor.Command{Name: "install", Args: []string{"-d", "-m", "750", "-o", cfg.Name, "-g", cfg.Name, "/var/lib/" + cfg.Name}}
	write := executor.Command{Name: "sh", Args: []string{"-c", writeUnitScript, "sh", servicios.UnitFile(cfg), unitPath}}
	verify := executor.Command{Name: "systemd-analyze", Args: []string{"verify", unitPath}}
	return operacion.Plan{
		Action: "Desplegar aplicación como servicio",
		Target: target,
		Commands: []executor.Command{
			exists, user, data, write,
			{Name: "systemctl", Args: []string{"daemon-reload"}},
			verify,
			{Name: "systemctl", Args: []string{"enable", "--", cfg.Name}},
		},
		Success: func([]executor.Result) string {
			return "El servicio «" + cfg.Name + "» quedó instalado en " + unitPath + " y habilitado al arranque. Para iniciarlo, elige «Iniciar (start)» abajo."
		},
		Explain: func(st oplog.Step) string {
			switch st.Cmd {
			case exists.String():
				return "No existe un programa ejecutable en " + cfg.Exec + " dentro de la VM; no se cambió nada. Revisa la ruta (o los permisos: chmod 755)."
			case verify.String():
				return "systemd encontró errores en la unidad; quedó en " + unitPath + " pero no se habilitó. Revisa la salida de abajo."
			}
			return ""
		},
	}
}

func (s *server) accionServicio(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("servicio-accion"))
	action := r.FormValue("accion")
	errName := validate.NombreServicio(name)
	if errName != nil {
		errName = &validate.Error{Field: "servicio-accion", Msg: errName.Error()}
	}
	var errAction error
	switch {
	case !servicios.Actions[action]:
		errAction = &validate.Error{Field: "accion", Msg: "Elige una de las acciones de la lista."}
	case errName == nil && servicios.IsProtected(name) && (action == "stop" || action == "disable"):
		errName = &validate.Error{Field: "servicio-accion", Msg: "«" + name + "» es un servicio del que depende la VM o la consola (por ejemplo, ssh es el canal de la consola); no se puede detener ni deshabilitar desde aquí."}
	}
	if errs := FieldErrors(errName, errAction); len(errs) > 0 {
		form := Form{Values: map[string]string{"servicio-accion": name, "accion": action}, Errors: errs}
		s.renderModule(w, r, "servicios", http.StatusUnprocessableEntity, form, serviciosDatos{"accion"})
		return
	}
	s.runAndShow(w, r, planAccion(s.state.Current().Name, name, action))
}

// planAccion runs one systemctl action. "status" exits with 3 when the service is
// stopped: that is a state, not an error (DC9).
func planAccion(target, name, action string) operacion.Plan {
	args := []string{action, "--", name}
	var alsoOK []int
	if action == "status" {
		args = []string{"status", "--no-pager", "--lines=20", "--", name}
		alsoOK = []int{3}
	}
	return operacion.Plan{
		Action:   "Servicio: " + action + " " + name,
		Target:   target,
		AlsoOK:   alsoOK,
		Commands: []executor.Command{{Name: "systemctl", Args: args}},
		Success: func(res []executor.Result) string {
			if action == "status" {
				if res[0].ExitCode == 3 {
					return "«" + name + "» está detenido. El detalle está abajo."
				}
				return "«" + name + "» está en ejecución. El detalle está abajo."
			}
			return "systemctl " + action + " " + name + " terminó correctamente."
		},
		Explain: func(st oplog.Step) string {
			if strings.Contains(st.Stderr, "not found") || strings.Contains(st.Stderr, "could not be found") {
				return "La VM no tiene un servicio llamado «" + name + "». Revisa el nombre."
			}
			return ""
		},
	}
}

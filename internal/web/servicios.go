package web

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/servicios"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/validate"
)

const ensureUserScript = `id -u "$1" >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin "$1"
install -d -m 750 -o "$1" -g "$1" "/var/lib/$1"`

// aplicacionesDir is the host folder with one subfolder per application that can be
// deployed. Tests point it to a temporary folder.
var aplicacionesDir = "aplicaciones"

const writeUnitScript = `t=$(mktemp)
trap 'rm -f "$t"' EXIT
printf '%s' "$1" > "$t"
install -m 644 -o root -g root "$t" "$2"
systemctl daemon-reload
systemd-analyze verify "$2"`

func init() {
	registerModule("servicios", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /servicios", s.handleServiciosGet)
		r.HandleFunc("POST /servicios/desplegar", s.handleServiciosDesplegar)
		r.HandleFunc("POST /servicios/accion", s.handleServiciosAccion)
	})
}

func formValues(v url.Values) map[string]string {
	if len(v) == 0 {
		return nil
	}
	m := make(map[string]string, len(v))
	for k, vals := range v {
		if len(vals) > 0 {
			m[k] = vals[0]
		}
	}
	return m
}

func (s *server) handleServiciosGet(w http.ResponseWriter, r *http.Request) {
	apps, _ := servicios.Aplicaciones(aplicacionesDir)
	s.renderModule(w, r, "servicios", http.StatusOK, Form{}, map[string]any{
		"Aplicaciones": apps,
	})
}

func (s *server) handleServiciosDesplegar(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error al procesar el formulario", http.StatusBadRequest)
		return
	}

	appNombre := strings.TrimSpace(r.FormValue("aplicacion"))
	servicioNombre := strings.TrimSpace(r.FormValue("servicio"))
	ejecutableNombre := strings.TrimSpace(r.FormValue("ejecutable"))
	puerto := strings.TrimSpace(r.FormValue("puerto"))
	isOneshot := r.FormValue("oneshot") == "on"

	var errApp, errServ, errExec, errPort error

	apps, _ := servicios.Aplicaciones(aplicacionesDir)
	if appNombre == "" {
		errApp = &validate.Error{Field: "aplicacion", Msg: "Debes seleccionar una aplicación."}
	} else if !slices.Contains(apps, appNombre) {
		errApp = &validate.Error{Field: "aplicacion", Msg: fmt.Sprintf("«%s» no es una carpeta de %s/ en este equipo. Copia allí la aplicación (el ejecutable y sus carpetas) y elígela de la lista.", appNombre, aplicacionesDir)}
	}
	errServ = servicios.ValidateName(servicioNombre)

	ejecutableRuta := fmt.Sprintf("/opt/%s/%s", appNombre, ejecutableNombre)
	if ejecutableNombre == "" {
		errExec = &validate.Error{Field: "ejecutable", Msg: "Debes especificar el nombre del ejecutable."}
	} else {
		errExec = servicios.ValidateExec(ejecutableRuta)
	}

	if puerto != "" {
		errPort = validate.Puerto(puerto)
	}

	errs := FieldErrors(errApp, errServ, errExec, errPort)

	if len(errs) > 0 {
		s.renderModule(w, r, "servicios", http.StatusUnprocessableEntity, Form{
			Values: formValues(r.PostForm),
			Errors: errs,
		}, map[string]any{
			"Formulario":   "desplegar",
			"Aplicaciones": apps,
		})
		return
	}

	var paquete bytes.Buffer
	if err := servicios.Empaquetar(filepath.Join(aplicacionesDir, appNombre), &paquete); err != nil {
		errs = FieldErrors(&validate.Error{Field: "aplicacion", Msg: fmt.Sprintf("No se pudo empaquetar la aplicación: %v", err)})
		s.renderModule(w, r, "servicios", http.StatusUnprocessableEntity, Form{
			Values: formValues(r.PostForm),
			Errors: errs,
		}, map[string]any{
			"Formulario":   "desplegar",
			"Aplicaciones": apps,
		})
		return
	}

	cfg := servicios.Config{
		Name:        servicioNombre,
		Description: appNombre,
		Exec:        ejecutableRuta,
		Port:        puerto,
		Oneshot:     isOneshot,
	}
	s.runAndShow(w, r, planDesplegar(s.state.Current().Name, cfg, appNombre, paquete.Bytes()))
}

// planDesplegar installs the application package (a .tar.gz of aplicaciones/<app>) in
// /opt/<app> and turns it into a systemd unit. Order matters: the package is extracted
// before the program is checked, because on a first deployment /opt/<app> is empty.
// The code stays root-owned and read-only for others; only the program gets the execute
// bit (a package built on Windows carries no modes). A long-running service then gets its
// system user and /var/lib/<name>, its working directory, which belongs to that user:
// the application writes its data there (Bookzilla creates data/ and images/ from the
// seed inside its binary). The unit is enabled, and a service that
// was already running is restarted so it runs the new program; a stopped one stays
// stopped until the Start button.
func planDesplegar(target string, cfg servicios.Config, app string, paquete []byte) operacion.Plan {
	dir := "/opt/" + app
	cmds := []executor.Command{
		{Name: "install", Args: []string{"-d", "-m", "755", "--", dir}},
		{Name: "tar", Args: []string{"-xzf", "-", "-C", dir, "--no-same-owner"}, Stdin: string(paquete)},
		{Name: "chown", Args: []string{"-R", "root:root", "--", dir}},
		{Name: "chmod", Args: []string{"-R", "u=rwX,go=rX", "--", dir}},
		{Name: "chmod", Args: []string{"755", "--", cfg.Exec}},
		{Name: "test", Args: []string{"-f", cfg.Exec, "-a", "-x", cfg.Exec}}, // a folder is "executable" too
	}
	if !cfg.Oneshot {
		cmds = append(cmds, executor.Command{Name: "sh", Args: []string{"-c", ensureUserScript, "sh", cfg.Name}})
	}
	unitPath := fmt.Sprintf("/etc/systemd/system/%s.service", cfg.Name)
	cmds = append(cmds,
		executor.Command{Name: "sh", Args: []string{"-c", writeUnitScript, "sh", servicios.UnitFile(cfg), unitPath}},
		executor.Command{Name: "systemctl", Args: []string{"enable", "--", cfg.Name}},
		executor.Command{Name: "systemctl", Args: []string{"try-restart", "--", cfg.Name}},
	)

	return operacion.Plan{
		Action:   "Desplegar servicio systemd",
		Target:   target,
		Commands: cmds,
		Timeout:  2 * time.Minute,
		Success: func([]executor.Result) string {
			return fmt.Sprintf("«%s» quedó instalado en %s y habilitado como servicio «%s». Usa «Iniciar (start)» en el formulario de acciones para arrancarlo.", app, dir, cfg.Name)
		},
	}
}

func (s *server) handleServiciosAccion(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error al procesar el formulario", http.StatusBadRequest)
		return
	}

	servicioNombre := strings.TrimSpace(r.FormValue("servicio-accion"))
	accion := strings.TrimSpace(r.FormValue("accion"))

	var errServ, errAccion error

	if err := servicios.ValidateName(servicioNombre); err != nil {
		errServ = &validate.Error{Field: "servicio-accion", Msg: err.Error()}
	} else if servicios.IsProtected(servicioNombre) && (accion == "stop" || accion == "disable") {
		errServ = &validate.Error{Field: "servicio-accion", Msg: fmt.Sprintf("«%s» es un servicio protegido del sistema; no se puede detener ni deshabilitar.", servicioNombre)}
	}

	if !servicios.Actions[accion] {
		errAccion = &validate.Error{Field: "accion", Msg: "Acción no permitida."}
	}

	errs := FieldErrors(errServ, errAccion)
	apps, _ := servicios.Aplicaciones(aplicacionesDir)

	if len(errs) > 0 {
		s.renderModule(w, r, "servicios", http.StatusUnprocessableEntity, Form{
			Values: formValues(r.PostForm),
			Errors: errs,
		}, map[string]any{
			"Formulario":   "accion",
			"Aplicaciones": apps,
		})
		return
	}

	cur := s.state.Current()
	plan := planAccion(cur.Name, servicioNombre, accion)

	s.runAndShow(w, r, plan)
}

func planAccion(target string, servicio string, accion string) operacion.Plan {
	var cmd executor.Command
	var alsoOK []int

	if accion == "status" {
		cmd = executor.Command{
			Name: "systemctl",
			Args: []string{"status", "--no-pager", "--lines=20", "--", servicio},
		}
		alsoOK = []int{3}
	} else {
		cmd = executor.Command{
			Name: "systemctl",
			Args: []string{accion, "--", servicio},
		}
	}

	return operacion.Plan{
		Action:   fmt.Sprintf("Acción %s en %s", accion, servicio),
		Target:   target,
		Commands: []executor.Command{cmd},
		AlsoOK:   alsoOK,
		Success: func(res []executor.Result) string {
			if accion == "status" && len(res) > 0 && res[0].ExitCode == 3 {
				return fmt.Sprintf("«%s» está detenido. El detalle está abajo.", servicio)
			}
			return fmt.Sprintf("Acción «%s» completada en el servicio «%s».", accion, servicio)
		},
	}
}

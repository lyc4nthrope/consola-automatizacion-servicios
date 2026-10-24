package web

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/servicios"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/validate"
)

const ensureUserScript = `id -u "$1" >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin "$1"
install -d -m 750 -o "$1" -g "$1" "/var/lib/$1"`

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
	apps, _ := servicios.Aplicaciones("aplicaciones")
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

	if appNombre == "" {
		errApp = &validate.Error{Field: "aplicacion", Msg: "Debes seleccionar una aplicación."}
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
	apps, _ := servicios.Aplicaciones("aplicaciones")

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

	appLocalDir := filepath.Join("aplicaciones", appNombre)
	var tarBuf bytes.Buffer
	if err := servicios.Empaquetar(appLocalDir, &tarBuf); err != nil {
		errs = FieldErrors(&validate.Error{Field: "aplicacion", Msg: fmt.Sprintf("Error empaquetando la aplicación: %v", err)})
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

	cur := s.state.Current()
	plan := planDesplegar(cur.Name, cfg)

	if tarBuf.Len() > 0 {
		tarCmds := []executor.Command{
			{Name: "install", Args: []string{"-d", "-m", "755", fmt.Sprintf("/opt/%s", appNombre)}},
			{Name: "tar", Args: []string{"-xzf", "-", "-C", fmt.Sprintf("/opt/%s", appNombre), "--no-same-owner"}, Stdin: tarBuf.String()},
			{Name: "chown", Args: []string{"-R", "root:root", fmt.Sprintf("/opt/%s", appNombre)}},
			{Name: "chmod", Args: []string{"-R", "755", fmt.Sprintf("/opt/%s", appNombre)}},
		}
		cmds := make([]executor.Command, 0, len(plan.Commands)+len(tarCmds))
		cmds = append(cmds, plan.Commands[0])
		cmds = append(cmds, tarCmds...)
		cmds = append(cmds, plan.Commands[1:]...)
		plan.Commands = cmds
	}

	s.runAndShow(w, r, plan)
}

func planDesplegar(target string, cfg servicios.Config) operacion.Plan {
	unitContent := servicios.UnitFile(cfg)
	unitPath := fmt.Sprintf("/etc/systemd/system/%s.service", cfg.Name)

	cmds := []executor.Command{
		{Name: "test", Args: []string{"-x", cfg.Exec}},
		{Name: "sh", Args: []string{"-c", ensureUserScript, "sh", cfg.Name}},
		{Name: "sh", Args: []string{"-c", writeUnitScript, "sh", unitContent, unitPath}},
		{Name: "systemctl", Args: []string{"enable", "--", cfg.Name}},
	}

	return operacion.Plan{
		Action:   "Desplegar servicio systemd",
		Target:   target,
		Commands: cmds,
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
	apps, _ := servicios.Aplicaciones("aplicaciones")

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
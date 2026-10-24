package web

import (
	"fmt"
	"net/http"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/servicios"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/validate"
)

func init() {
	registerModule("servicios", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /servicios", s.handleServiciosPage)
		r.HandleFunc("POST /servicios/desplegar", s.handleServiciosDesplegar)
		r.HandleFunc("POST /servicios/accion", s.handleServiciosAccion)
	})
}

func (s *server) handleServiciosPage(w http.ResponseWriter, r *http.Request) {
	s.renderModule(w, r, "servicios", http.StatusOK, Form{}, nil)
}

func (s *server) handleServiciosDesplegar(w http.ResponseWriter, r *http.Request) {
	appName := r.FormValue("aplicacion")
	serviceName := r.FormValue("nombre")
	execName := r.FormValue("ejecutable")
	port := r.FormValue("puerto")

	errNombre := validate.NombreServicio(serviceName)
	var errPuerto error
	if port != "" {
		errPuerto = validate.Puerto(port)
	}

	errs := FieldErrors(errNombre, errPuerto)
	if len(errs) > 0 {
		form := Form{
			Values: map[string]string{
				"aplicacion": appName,
				"nombre":     serviceName,
				"ejecutable": execName,
				"puerto":     port,
			},
			Errors: errs,
		}
		s.renderModule(w, r, "servicios", http.StatusBadRequest, form, nil)
		return
	}

	unitContent := servicios.UnitFile(servicios.Config{
		Name:       serviceName,
		Executable: execName,
		Port:       port,
	})

	tmpUnitPath := fmt.Sprintf("/tmp/%s.service", serviceName)
	finalUnitPath := fmt.Sprintf("/etc/systemd/system/%s.service", serviceName)

	target := s.state.Current().Name

	plan := operacion.Plan{
		Action: "Desplegar Aplicación como Servicio",
		Target: target,
		Commands: []executor.Command{
			{Name: "useradd", Args: []string{"--system", "--no-create-home", "--shell", "/usr/sbin/nologin", serviceName}},
			{Name: "mkdir", Args: []string{"-p", fmt.Sprintf("/opt/%s", serviceName), fmt.Sprintf("/var/lib/%s", serviceName)}},
			{Name: "chown", Args: []string{"-R", fmt.Sprintf("%s:%s", serviceName, serviceName), fmt.Sprintf("/var/lib/%s", serviceName)}},
			{Name: "sh", Args: []string{"-c", fmt.Sprintf("echo '%s' > %s", unitContent, tmpUnitPath)}},
			{Name: "install", Args: []string{"-m", "644", "-o", "root", "-g", "root", tmpUnitPath, finalUnitPath}},
			{Name: "systemctl", Args: []string{"daemon-reload"}},
			{Name: "systemctl", Args: []string{"enable", serviceName}},
		},
		Success: func(res []executor.Result) string {
			return fmt.Sprintf("Servicio %s desplegado y habilitado correctamente.", serviceName)
		},
	}

	s.runAndShow(w, r, plan)
}

func (s *server) handleServiciosAccion(w http.ResponseWriter, r *http.Request) {
	serviceName := r.FormValue("nombre")
	action := r.FormValue("accion")

	if servicios.IsProtected(serviceName) && (action == "stop" || action == "disable") {
		form := Form{
			Values: map[string]string{"nombre": serviceName, "accion": action},
			Errors: map[string]string{
				"nombre": fmt.Sprintf("El servicio '%s' está protegido por el sistema y no se puede %s.", serviceName, action),
			},
		}
		s.renderModule(w, r, "servicios", http.StatusBadRequest, form, nil)
		return
	}

	var alsoOK []int
	if action == "status" {
		alsoOK = []int{3} // Código 3 en systemctl status indica servicio detenido
	}

	args := []string{action, serviceName}
	if action == "status" {
		args = []string{"status", "--no-pager", "--lines=20", serviceName}
	}

	target := s.state.Current().Name

	plan := operacion.Plan{
		Action: fmt.Sprintf("Ejecutar %s en %s", action, serviceName),
		Target: target,
		AlsoOK: alsoOK,
		Commands: []executor.Command{
			{Name: "systemctl", Args: args},
		},
	}

	s.runAndShow(w, r, plan)
}
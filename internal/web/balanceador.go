package web

import (
	"fmt"
	"net/http"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/balanceador"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/validate"
)

func init() {
	registerModule("balanceador", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /balanceador", s.handleBalanceadorPage)
		r.HandleFunc("POST /balanceador/agregar", s.handleBalanceadorAgregar)
		r.HandleFunc("POST /balanceador/quitar", s.handleBalanceadorQuitar)
	})
}

func (s *server) handleBalanceadorPage(w http.ResponseWriter, r *http.Request) {
	s.renderModule(w, r, "balanceador", http.StatusOK, Form{}, nil)
}

func (s *server) handleBalanceadorAgregar(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("nombre")
	ip := r.FormValue("ip")
	port := r.FormValue("puerto")

	errNombre := validate.Hostname(name)
	errIP := validate.IPv4(ip)
	errPuerto := validate.Puerto(port)

	errs := FieldErrors(errNombre, errIP, errPuerto)
	if len(errs) > 0 {
		form := Form{
			Values: map[string]string{"nombre": name, "ip": ip, "puerto": port},
			Errors: errs,
		}
		s.renderModule(w, r, "balanceador", http.StatusBadRequest, form, nil)
		return
	}

	srv := balanceador.Server{Name: name, IP: ip, Port: port}
	target := s.state.Current().Name

	plan := operacion.Plan{
		Action: fmt.Sprintf("Agregar servidor %s (%s:%s) al balanceador", name, ip, port),
		Target: target,
		Commands: []executor.Command{
			{Name: "cp", Args: []string{"/etc/haproxy/haproxy.cfg", "/tmp/haproxy.cfg.bak"}},
		},
		Then: func(results []executor.Result) ([]executor.Command, error) {
			currentCfg := ""
			newCfg, err := balanceador.AddServer(currentCfg, srv)
			if err != nil {
				return nil, err
			}
			tmpFile := "/tmp/consola-haproxy.cfg"
			return []executor.Command{
				{Name: "sh", Args: []string{"-c", fmt.Sprintf("echo '%s' > %s", newCfg, tmpFile)}},
				{Name: "haproxy", Args: []string{"-c", "-f", tmpFile}},
				{Name: "install", Args: []string{"-b", "-m", "644", tmpFile, "/etc/haproxy/haproxy.cfg"}},
				{Name: "systemctl", Args: []string{"reload-or-restart", "haproxy"}},
			}, nil
		},
		Explain: func(step oplog.Step) string {
			if step.Cmd == "haproxy -c -f /tmp/consola-haproxy.cfg" {
				return "La configuración resultante no es válida; no se aplicó ningún cambio."
			}
			return ""
		},
	}

	s.runAndShow(w, r, plan)
}

func (s *server) handleBalanceadorQuitar(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("nombre")

	errNombre := validate.Hostname(name)
	errs := FieldErrors(errNombre)
	if len(errs) > 0 {
		form := Form{
			Values: map[string]string{"nombre": name},
			Errors: errs,
		}
		s.renderModule(w, r, "balanceador", http.StatusBadRequest, form, nil)
		return
	}

	target := s.state.Current().Name

	plan := operacion.Plan{
		Action: fmt.Sprintf("Quitar servidor %s del balanceador", name),
		Target: target,
		Commands: []executor.Command{
			{Name: "cp", Args: []string{"/etc/haproxy/haproxy.cfg", "/tmp/haproxy.cfg.bak"}},
		},
		Then: func(results []executor.Result) ([]executor.Command, error) {
			currentCfg := ""
			newCfg, err := balanceador.RemoveServer(currentCfg, name)
			if err != nil {
				return nil, err
			}
			tmpFile := "/tmp/consola-haproxy.cfg"
			return []executor.Command{
				{Name: "sh", Args: []string{"-c", fmt.Sprintf("echo '%s' > %s", newCfg, tmpFile)}},
				{Name: "haproxy", Args: []string{"-c", "-f", tmpFile}},
				{Name: "install", Args: []string{"-b", "-m", "644", tmpFile, "/etc/haproxy/haproxy.cfg"}},
				{Name: "systemctl", Args: []string{"reload-or-restart", "haproxy"}},
			}, nil
		},
		Explain: func(step oplog.Step) string {
			if step.Cmd == "haproxy -c -f /tmp/consola-haproxy.cfg" {
				return "La configuración resultante no es válida; no se aplicó ningún cambio."
			}
			return ""
		},
	}

	s.runAndShow(w, r, plan)
}
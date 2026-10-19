package web

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/validate"
)

// Part A: system preparation (section 3.1 of the assignment). The root keys form on the same
// page belongs to Part B (web/templates/_llaves-root.html); the page shows it once it exists.

func init() {
	registerModule("preparacion", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /preparacion", s.preparacionPagina)
		r.HandleFunc("POST /preparacion/instalar", s.instalarPaquete)
		r.HandleFunc("POST /preparacion/eliminar", s.eliminarPaquete)
		r.HandleFunc("POST /preparacion/issue", s.instalarIssue)
		r.HandleFunc("POST /preparacion/issue/vista-previa", s.vistaPreviaIssue)
		r.HandleFunc("POST /preparacion/issue/restaurar", s.restaurarIssue)
	})
}

// channelPackages are the packages the console itself uses to reach the VM (the SSH
// channel; modern scp copies files through sftp). Removing them would cut the console off.
var channelPackages = []string{"openssh-server", "openssh-sftp-server"}

// preparacionData is the page data. The page has several forms and one Form value, so
// Formulario says which form the values and errors belong to.
type preparacionData struct {
	Formulario string        // "paquete" or "issue"
	Issue      *issuePreview // set after "Vista previa"
	Datos      []issueDato
}

func (s *server) preparacionPagina(w http.ResponseWriter, r *http.Request) {
	s.renderPreparacion(w, r, http.StatusOK, Form{}, preparacionData{})
}

func (s *server) renderPreparacion(w http.ResponseWriter, r *http.Request, status int, form Form, data preparacionData) {
	data.Datos = issueDatos
	s.renderModule(w, r, "preparacion", status, form, data)
}

func (s *server) instalarPaquete(w http.ResponseWriter, r *http.Request) {
	pkg, err := paqueteDelFormulario(r)
	if err != nil {
		s.rechazarPaquete(w, r, pkg, err)
		return
	}
	s.runAndShow(w, r, planInstalar(s.state.Current().Name, pkg))
}

func (s *server) eliminarPaquete(w http.ResponseWriter, r *http.Request) {
	pkg, err := paqueteDelFormulario(r)
	if err == nil && slices.Contains(channelPackages, pkg) {
		err = &validate.Error{Field: "paquete", Msg: "La consola usa «" + pkg + "» para comunicarse con la VM por SSH; eliminarlo la dejaría sin conexión, así que no se elimina desde aquí."}
	}
	if err != nil {
		s.rechazarPaquete(w, r, pkg, err)
		return
	}
	s.runAndShow(w, r, planEliminar(s.state.Current().Name, pkg))
}

func paqueteDelFormulario(r *http.Request) (string, error) {
	pkg := strings.TrimSpace(r.PostFormValue("paquete"))
	return pkg, validate.Paquete(pkg)
}

// rechazarPaquete shows the page again with what the user typed and the error next to the field.
func (s *server) rechazarPaquete(w http.ResponseWriter, r *http.Request, pkg string, err error) {
	s.renderPreparacion(w, r, http.StatusUnprocessableEntity,
		Form{Values: map[string]string{"paquete": pkg}, Errors: FieldErrors(err)}, preparacionData{Formulario: "paquete"})
}

// Fixed scripts for the checks that need more than an exit code. The package name is always
// a positional parameter ("$1"), never part of the script.
//
// dpkg's status abbreviation has the wanted state first and the current state second
// ("ii" installed, "hi" installed and held, "rc" removed with its configuration left), so
// "installed" is the second letter. The format ends in a line break because dpkg prints
// one entry per architecture of a multi-arch package.
const (
	// stateScript prints "<status>|<version>" lines and exits "$3" when the package is
	// installed, or "$2" when it is not.
	stateScript = `s=$(dpkg-query -W -f '${db:Status-Abbrev}|${Version}\n' -- "$1" 2>/dev/null); printf '%s\n' "$s"; case $s in ?i*) exit "$3";; esac; exit "$2"`

	// removeCheckScript refuses a removal that would break the system or take other
	// packages with it: 10 = not installed, 11 = essential, protected or required package,
	// 12 = apt would also remove the packages it prints, 13 = the dry run failed or said
	// nothing, 15 = the package is held (apt-mark hold).
	removeCheckScript = `s=$(dpkg-query -W -f '${db:Status-Abbrev}|${Essential}|${Protected}|${Priority}\n' -- "$1" 2>/dev/null)
case $s in ?i*) ;; *) exit 10;; esac
case $s in h*) exit 15;; esac
case $s in *'|yes|'*|*'|required'*) exit 11;; esac
o=$(apt-get -s remove -- "$1") || { printf '%s\n' "$o"; exit 13; }
r=$(printf '%s\n' "$o" | sed -n 's/^Remv \([^ :]*\).*/\1/p' | sort -u)
[ -n "$r" ] || { printf '%s\n' "$o"; exit 13; }
if [ "$r" != "$1" ]; then printf '%s\n' "$r"; exit 12; fi`
)

// Exit codes of the scripts above.
const (
	notInstalledCode   = 10
	protectedCode      = 11
	takesOthersCode    = 12
	dryRunFailedCode   = 13
	stillInstalledCode = 14
	heldCode           = 15
)

// stateCmd reads the state of pkg; it exits ifNotInstalled or ifInstalled, so a plan can
// stop when the package is (or is not) there.
func stateCmd(pkg string, ifNotInstalled, ifInstalled int) executor.Command {
	return executor.Command{Name: "sh", Args: []string{"-c", stateScript, "sh", pkg, strconv.Itoa(ifNotInstalled), strconv.Itoa(ifInstalled)}}
}

// aptCmd runs apt-get without questions and waits for a lock held by another apt (an
// automatic update, for example) instead of failing at once.
func aptCmd(args ...string) executor.Command {
	return executor.Command{Name: "env", Args: append([]string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "-o", "DPkg::Lock::Timeout=120"}, args...)}
}

// pkgState is the parsed output of stateScript.
type pkgState struct {
	Installed bool
	Status    string // dpkg status, e.g. "ii", "hi", "rc"; empty when dpkg does not know the package
	Version   string
}

// parseState reads the first entry (the native architecture comes first).
func parseState(out string) pkgState {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	status, version, _ := strings.Cut(line, "|")
	status = strings.TrimSpace(status)
	return pkgState{Installed: len(status) == 2 && status[1] == 'i', Status: status, Version: version}
}

// planInstalar installs pkg: it refreshes the package lists, checks that the package exists
// before changing anything, installs it and reads the installed version.
func planInstalar(target, pkg string) operacion.Plan {
	update := aptCmd("update")
	show := executor.Command{Name: "apt-cache", Args: []string{"show", "--no-all-versions", "--", pkg}}
	install := aptCmd("install", "-y", "--", pkg)
	after := stateCmd(pkg, notInstalledCode, 0)
	return operacion.Plan{
		Action:   "Instalar aplicación",
		Target:   target,
		Timeout:  10 * time.Minute,
		Commands: []executor.Command{update, show, stateCmd(pkg, 0, 0), install, after},
		Success: func(res []executor.Result) string {
			before, now := parseState(res[2].Stdout), parseState(res[4].Stdout)
			switch {
			case before.Installed && before.Version == now.Version:
				return "«" + pkg + "» ya estaba instalado (versión " + now.Version + "); no se cambió nada."
			case before.Installed:
				return "«" + pkg + "» se actualizó de la versión " + before.Version + " a la " + now.Version + "."
			}
			return "«" + pkg + "» quedó instalado (versión " + now.Version + ")."
		},
		Explain: func(st oplog.Step) string {
			switch st.Cmd {
			case update.String():
				return "No se pudo actualizar la lista de paquetes de la VM. Revisa que tenga salida a internet y vuelve a intentarlo; no se instaló nada."
			case show.String():
				if st.ExitCode == 100 {
					return "«" + pkg + "» no existe en los repositorios de la VM. Revisa el nombre (por ejemplo: tree); no se instaló nada."
				}
			case after.String():
				if st.ExitCode == notInstalledCode {
					return "apt-get terminó, pero «" + pkg + "» no quedó instalado. Revisa la salida de abajo."
				}
			}
			return ""
		},
	}
}

// planEliminar removes pkg after checking that removing it is safe, and reports whether its
// configuration files stay on the VM.
func planEliminar(target, pkg string) operacion.Plan {
	check := executor.Command{Name: "sh", Args: []string{"-c", removeCheckScript, "sh", pkg}}
	after := stateCmd(pkg, 0, stillInstalledCode)
	return operacion.Plan{
		Action:   "Eliminar aplicación",
		Target:   target,
		Timeout:  5 * time.Minute,
		Commands: []executor.Command{check, aptCmd("remove", "-y", "--", pkg), after},
		Success: func(res []executor.Result) string {
			if parseState(res[2].Stdout).Status == "rc" {
				return "Se eliminó «" + pkg + "». Sus archivos de configuración siguen en la VM, por si lo vuelves a instalar."
			}
			return "Se eliminó «" + pkg + "»."
		},
		Explain: func(st oplog.Step) string {
			switch {
			case st.Cmd == after.String() && st.ExitCode == stillInstalledCode:
				return "apt-get terminó, pero «" + pkg + "» sigue instalado. Revisa la salida de abajo."
			case st.Cmd != check.String():
				return ""
			}
			switch st.ExitCode {
			case notInstalledCode:
				return "«" + pkg + "» no está instalado en la VM; no hay nada que eliminar."
			case protectedCode:
				return "«" + pkg + "» es un paquete esencial o protegido del sistema; la consola no lo elimina porque la VM dejaría de funcionar."
			case takesOthersCode:
				var others []string
				for _, name := range strings.Fields(st.Stdout) {
					if name != pkg {
						others = append(others, name)
					}
				}
				return "Eliminar «" + pkg + "» también eliminaría otros paquetes (" + strings.Join(others, ", ") + "); la consola no lo hace para no romper otras aplicaciones. No se eliminó nada."
			case dryRunFailedCode:
				return "apt no pudo calcular una eliminación segura de «" + pkg + "», así que no se eliminó nada. La salida de abajo explica por qué."
			case heldCode:
				return "«" + pkg + "» está retenido en la VM (apt-mark hold), así que apt no lo cambia. Si quieres eliminarlo, quita la retención con: apt-mark unhold " + pkg
			}
			return ""
		},
	}
}

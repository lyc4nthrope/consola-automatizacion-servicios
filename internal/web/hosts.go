package web

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/validate"
)

// Part A, section 3.1.4: the VM's host name and its line in /etc/hosts.

const (
	hostsFile = "/etc/hosts"

	// hostsLoopback is the address Debian gives to the machine's own name in /etc/hosts.
	hostsLoopback = "127.0.1.1"

	// maxHostsSize keeps the file well under the 128 KiB Linux allows for one argument.
	maxHostsSize = 100 << 10

	// verifyHostnameScript prints the VM's name and checks it is "$1" (10 otherwise) and
	// that /etc/hosts itself, not DNS, resolves it (11 otherwise).
	verifyHostnameScript = `h=$(hostname); printf 'hostname: %s\n' "$h"; [ "$h" = "$1" ] || exit 10; getent -s files hosts "$1" || exit 11`

	hostnameMismatchCode = 10
	nameNotResolvedCode  = 11
	fileChangedCode      = 12 // writeFileScript: the file is not what the console read
)

// replaceHostsLine returns hosts with the machine named name instead of old. On every
// 127.0.1.1 line, the names old and old.<domain> become name and name.<domain>; the other
// names and comments on those lines, and every other line, stay as they were. The first
// 127.0.1.1 line always ends up with name itself (first if it did not have old). Without a 127.0.1.1 line, one is
// added after the last 127.0.0.1 line (or first). The result always ends with a line
// break, so a line added later never joins the last one.
func replaceHostsLine(hosts, old, name string) string {
	lines := strings.SplitAfter(hosts, "\n") // a final "" piece never matches nor moves
	insertAt, found := 0, false
	for i, line := range lines {
		switch firstField(line) {
		case hostsLoopback:
			lines[i] = renameOnLine(line, old, name, !found)
			found = true
		case "127.0.0.1":
			insertAt = i + 1
		}
	}
	if !found {
		if insertAt > 0 && !strings.HasSuffix(lines[insertAt-1], "\n") {
			lines[insertAt-1] += "\n"
		}
		own := hostsLoopback + "\t" + name + "\n"
		lines = append(lines[:insertAt], append([]string{own}, lines[insertAt:]...)...)
	}
	out := strings.Join(lines, "")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

// renameOnLine renames old to name in one 127.0.1.1 line; with first, name ends up on
// the line even if old was not there. A line where nothing changes is kept as it was.
func renameOnLine(line, old, name string, first bool) string {
	text := strings.TrimRight(line, "\r\n")
	ending := line[len(text):]
	body, comment, hasComment := strings.Cut(text, "#")
	fields := strings.Fields(body)
	names, renamed := fields[1:], false
	if old != "" && old != "localhost" { // never rename the loopback name itself
		for i, n := range names {
			switch {
			case n == old:
				names[i], renamed = name, true
			case strings.HasPrefix(n, old+"."):
				names[i], renamed = name+n[len(old):], true
			}
		}
	}
	switch {
	case !renamed && (!first || slices.Contains(names, name)):
		return line
	case !renamed:
		names = append([]string{name}, names...) // the new name becomes the machine's name
	case first && !slices.Contains(names, name):
		// Only old.<domain> (or "old.") was there: keep it first, as hostname -f reads it,
		// and add the plain name so it resolves.
		names = append(names, name)
	}
	var unique []string
	for _, n := range names {
		if !slices.Contains(unique, n) {
			unique = append(unique, n)
		}
	}
	out := fields[0] + "\t" + strings.Join(unique, " ")
	if hasComment {
		out += " #" + comment
	}
	return out + ending
}

// firstField is the first word of an /etc/hosts line: its address, or "#..." for a
// comment (never an address), or "" for a blank line.
func firstField(line string) string {
	f := strings.Fields(line)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

func (s *server) cambiarHostname(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSpace(r.PostFormValue("hostname"))) // host names ignore case
	err := validate.Hostname(name)
	switch {
	case err != nil:
	case name == "localhost":
		err = &validate.Error{Field: "hostname", Msg: "«localhost» es el nombre que todo equipo usa para referirse a sí mismo; elige otro, por ejemplo servidor1."}
	case strings.Trim(name, "0123456789") == "":
		err = &validate.Error{Field: "hostname", Msg: "Un nombre de host solo con números se puede confundir con una dirección; agrega al menos una letra, por ejemplo servidor1."}
	}
	if err != nil {
		s.renderPreparacion(w, r, http.StatusUnprocessableEntity,
			Form{Values: map[string]string{"hostname": name}, Errors: FieldErrors(err)}, preparacionData{Formulario: "hostname"})
		return
	}
	s.runAndShow(w, r, planHostname(s.state.Current().Name, name))
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// planHostname reads the current name and /etc/hosts, writes /etc/hosts with the new name
// (replaceHostsLine; only if the file still is what was read), sets the name with
// hostnamectl, redraws the login prompt (it shows the name) and checks the result. Every
// step can be repeated, so after a failure the user just tries again.
func planHostname(target, name string) operacion.Plan {
	current := executor.Command{Name: "hostname"}
	read := executor.Command{Name: "cat", Args: []string{"--", hostsFile}}
	set := executor.Command{Name: "hostnamectl", Args: []string{"set-hostname", name}}
	verify := executor.Command{Name: "sh", Args: []string{"-c", verifyHostnameScript, "sh", name}}
	var write executor.Command
	return operacion.Plan{
		Action:   "Cambiar nombre de host",
		Target:   target,
		Commands: []executor.Command{current, read},
		Then: func(res []executor.Result) ([]executor.Command, error) {
			if len(res[1].Stdout) > maxHostsSize {
				return nil, errors.New("El archivo /etc/hosts de la VM pasa de 100 KB; la consola no lo modifica. Revísalo a mano.")
			}
			hosts := replaceHostsLine(res[1].Stdout, strings.TrimSpace(res[0].Stdout), name)
			write = executor.Command{Name: "sh", Args: []string{"-c", writeFileScript, "sh", hosts, hostsFile, sha256Hex(res[1].Stdout)}}
			return []executor.Command{write, set, reloadAgetty, verify}, nil
		},
		Success: func(res []executor.Result) string {
			before := strings.TrimSpace(res[0].Stdout)
			if before == name {
				return "La VM ya se llamaba «" + name + "»; su línea en /etc/hosts quedó confirmada."
			}
			return "La VM ahora se llama «" + name + "» (antes «" + before + "») y /etc/hosts apunta " + hostsLoopback + " al nombre nuevo. El archivo de antes del primer cambio está en /etc/hosts.consola-original. Las sesiones abiertas muestran el nombre nuevo al volver a entrar."
		},
		Explain: func(st oplog.Step) string {
			switch {
			case st.Cmd == read.String():
				return "No se pudo leer /etc/hosts en la VM, así que no se cambió nada. Revisa la salida de abajo."
			case st.Cmd == write.String() && st.ExitCode == fileChangedCode:
				return "/etc/hosts cambió mientras la consola lo leía (otra operación, o la lectura llegó incompleta), así que no se cambió nada. Vuelve a intentarlo."
			case st.Cmd == write.String():
				return "No se pudo escribir /etc/hosts, así que no se cambió nada. Revisa la salida de abajo."
			case st.Cmd == set.String():
				return "/etc/hosts ya tiene el nombre «" + name + "», pero hostnamectl no pudo cambiar el nombre de la VM. Vuelve a intentarlo: repetir la operación es seguro."
			case st.Cmd == reloadAgetty.String():
				return "La VM ya se llama «" + name + "»; solo falló volver a mostrar la pantalla de inicio de sesión, que lo mostrará al reiniciar."
			case st.Cmd == verify.String() && st.ExitCode == hostnameMismatchCode:
				return "hostnamectl terminó, pero la VM no quedó con el nombre «" + name + "». Revisa la salida de abajo."
			case st.Cmd == verify.String() && st.ExitCode == nameNotResolvedCode:
				return "La VM ya se llama «" + name + "», pero /etc/hosts no resuelve ese nombre. Revisa la salida de abajo."
			}
			return ""
		},
	}
}

// Package balanceador edits haproxy.cfg as plain text: pure functions that receive the
// file read from the VM and return the new file. Nothing here talks to the VM.
package balanceador

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/validate"
)

// Server is one "server" line of a backend section.
type Server struct {
	Name string
	IP   string
	Port string
}

// Address is how HAProxy writes the server: ip:port.
func (s Server) Address() string { return s.IP + ":" + s.Port }

// sectionKeywords start a new section in haproxy.cfg; every other line belongs to the
// section above it.
var sectionKeywords = map[string]bool{
	"global": true, "defaults": true, "frontend": true, "backend": true, "listen": true,
	"resolvers": true, "peers": true, "userlist": true, "program": true, "mailers": true,
	"http-errors": true, "cache": true, "ring": true,
}

var reBackend = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)

// ValidateBackend checks the backend name from the form. HAProxy names allow letters,
// digits, '-', '_', '.' and ':'.
func ValidateBackend(name string) error {
	if !reBackend.MatchString(name) {
		return &validate.Error{Field: "backend", Msg: "El nombre del back-end solo admite letras, números y los signos - _ . : (hasta 64), y empieza con letra o número. Ejemplo: web_servers"}
	}
	return nil
}

// ServerName is the name the console gives a server it adds: derived from its address,
// so the same address always gets the same name. Example: srv-192-168-40-51-8080.
func ServerName(ip, port string) string {
	return "srv-" + strings.ReplaceAll(ip, ".", "-") + "-" + port
}

// section returns the first and last line index (inclusive) of "backend <name>", or
// -1, -1 when the backend does not exist.
func section(lines []string, kind, name string) (int, int) {
	start := -1
	for i, line := range lines {
		f := strings.Fields(stripComment(line))
		if len(f) == 0 || !sectionKeywords[f[0]] {
			continue
		}
		if start >= 0 {
			return start, i - 1
		}
		if f[0] == kind && len(f) > 1 && f[1] == name {
			start = i
		}
	}
	if start >= 0 {
		return start, len(lines) - 1
	}
	return -1, -1
}

func stripComment(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		return line[:i]
	}
	return line
}

// Backends lists the backend names in the order they appear.
func Backends(cfg string) []string {
	var out []string
	for _, line := range strings.Split(cfg, "\n") {
		f := strings.Fields(stripComment(line))
		if len(f) > 1 && f[0] == "backend" {
			out = append(out, f[1])
		}
	}
	return out
}

// ListServers returns the servers of one backend.
func ListServers(cfg, backend string) []Server {
	lines := strings.Split(cfg, "\n")
	start, end := section(lines, "backend", backend)
	var out []Server
	for i := start + 1; start >= 0 && i <= end; i++ {
		if s, ok := parseServer(lines[i]); ok {
			out = append(out, s)
		}
	}
	return out
}

func parseServer(line string) (Server, bool) {
	f := strings.Fields(stripComment(line))
	if len(f) < 3 || f[0] != "server" {
		return Server{}, false
	}
	ip, port, _ := strings.Cut(f[2], ":")
	return Server{Name: f[1], IP: ip, Port: port}, true
}

func missingBackend(cfg, backend string) error {
	if all := Backends(cfg); len(all) > 0 {
		return fmt.Errorf("El back-end «%s» no existe en haproxy.cfg. Los que hay son: %s.", backend, strings.Join(all, ", "))
	}
	return fmt.Errorf("haproxy.cfg no tiene ningún back-end; crea uno (por ejemplo «backend web_servers») antes de agregar servidores.")
}

// AddServer adds "server <name> <ip>:<port> check" at the end of the backend, with the
// indentation of the section. The backend must exist, and an address already in it is
// rejected (adding it twice would send it double traffic).
func AddServer(cfg, backend string, srv Server) (string, error) {
	lines := strings.Split(cfg, "\n")
	start, end := section(lines, "backend", backend)
	if start < 0 {
		return "", missingBackend(cfg, backend)
	}
	indent := "    "
	last := start
	for i := start + 1; i <= end; i++ {
		if s, ok := parseServer(lines[i]); ok {
			if s.Address() == srv.Address() {
				return "", fmt.Errorf("%s ya está en el back-end «%s» (como «%s»); no se agregó de nuevo.", srv.Address(), backend, s.Name)
			}
			if s.Name == srv.Name {
				return "", fmt.Errorf("Ya hay un servidor llamado «%s» en el back-end «%s».", srv.Name, backend)
			}
		}
		if strings.TrimSpace(lines[i]) != "" {
			last = i
			if i > start {
				indent = lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
			}
		}
	}
	if indent == "" {
		indent = "    "
	}
	line := indent + "server " + srv.Name + " " + srv.Address() + " check"
	out := append([]string{}, lines[:last+1]...)
	out = append(out, line)
	out = append(out, lines[last+1:]...)
	return strings.Join(out, "\n"), nil
}

// RemoveServer removes every server of the backend that points to ip. It refuses to
// remove the last server, because the backend would answer every request with 503.
func RemoveServer(cfg, backend, ip string) (string, error) {
	lines := strings.Split(cfg, "\n")
	start, end := section(lines, "backend", backend)
	if start < 0 {
		return "", missingBackend(cfg, backend)
	}
	var out []string
	removed, total := 0, 0
	for i, line := range lines {
		if s, ok := parseServer(line); ok && i > start && i <= end {
			total++
			if s.IP == ip {
				removed++
				continue
			}
		}
		out = append(out, line)
	}
	switch {
	case removed == 0:
		return "", fmt.Errorf("El back-end «%s» no tiene ningún servidor con la IP %s; no se cambió nada.", backend, ip)
	case removed == total:
		return "", errors.New("Ese es el único servidor del back-end «" + backend + "»: sin servidores, HAProxy respondería 503 a todas las peticiones. Agrega otro antes de quitarlo.")
	}
	return strings.Join(out, "\n"), nil
}

// Package servicios builds systemd units and checks the data they are made of. Pure
// functions: nothing here talks to the VM.
package servicios

import (
	"regexp"
	"strings"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/validate"
)

// Config is what a unit is generated from. Every field must pass Validate first: the
// values end up in a file systemd parses line by line.
type Config struct {
	Name        string // unit and system user, e.g. "bookzilla"
	Description string // free text for Description=, e.g. "Bookzilla"
	Exec        string // absolute path of the program in the VM
	Port        string // optional, exported as PORT
	Oneshot     bool   // Type=oneshot (the date script), runs as root and exits
}

// Actions is the closed list of systemctl actions the console runs (DC9).
var Actions = map[string]bool{
	"start": true, "stop": true, "restart": true, "status": true, "enable": true, "disable": true,
}

// protectedUnits are units the console never stops or disables (DC10): stopping ssh
// would cut the console's own access to the VM.
var protectedUnits = map[string]bool{
	"ssh": true, "sshd": true, "dbus": true, "networking": true, "NetworkManager": true, "haproxy": true,
}

// IsProtected reports whether name is a unit the console must not stop or disable.
func IsProtected(name string) bool {
	clean := strings.TrimSuffix(name, ".service")
	return protectedUnits[clean] || strings.HasPrefix(clean, "systemd-") || strings.HasPrefix(clean, "getty@")
}

var (
	// A unit that is also a system user: lowercase start, then lowercase, digits, - and _.
	reUnitUser = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	// An absolute path without spaces, quotes, $, % or other characters systemd expands.
	reExec        = regexp.MustCompile(`^/[A-Za-z0-9._+/-]{1,254}$`)
	reDescription = regexp.MustCompile(`^[\p{L}\p{N} ._()-]{1,64}$`)
)

// ValidateName checks the unit name of a deployment. It is also the name of the system
// user the service runs as, so it follows the user rules.
func ValidateName(name string) error {
	if !reUnitUser.MatchString(name) {
		return &validate.Error{Field: "servicio", Msg: "El nombre del servicio también es el de su usuario de sistema: minúsculas, números, - y _, empieza con letra y tiene hasta 32 caracteres. Ejemplo: bookzilla"}
	}
	if IsProtected(name) {
		return &validate.Error{Field: "servicio", Msg: "«" + name + "» es un servicio del sistema; elige otro nombre para tu aplicación."}
	}
	return nil
}

// ValidateExec checks the program path written into ExecStart.
func ValidateExec(path string) error {
	if !reExec.MatchString(path) || strings.Contains(path, "/../") || strings.HasSuffix(path, "/..") || strings.HasSuffix(path, "/") {
		return &validate.Error{Field: "ejecutable", Msg: "Escribe la ruta absoluta del programa en la VM, sin espacios ni comillas. Ejemplo: /opt/bookzilla/bookzilla"}
	}
	return nil
}

// ValidateDescription checks the application name shown in Description=.
func ValidateDescription(s string) error {
	if !reDescription.MatchString(s) {
		return &validate.Error{Field: "aplicacion", Msg: "El nombre de la aplicación admite letras, números, espacios y . _ ( ) - (hasta 64). Ejemplo: Bookzilla"}
	}
	return nil
}

// UnitFile generates the unit (DC7). A long-running service runs as its own system user
// with its data in /var/lib/<name>; a oneshot runs as root and exits.
func UnitFile(cfg Config) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=" + cfg.Description + " (desplegado por la consola)\n")
	b.WriteString("After=network-online.target\nWants=network-online.target\n\n")
	b.WriteString("[Service]\n")
	if cfg.Oneshot {
		b.WriteString("Type=oneshot\n")
		b.WriteString("ExecStart=" + cfg.Exec + "\n")
	} else {
		b.WriteString("Type=simple\n")
		b.WriteString("User=" + cfg.Name + "\nGroup=" + cfg.Name + "\n")
		b.WriteString("WorkingDirectory=/var/lib/" + cfg.Name + "\n")
		b.WriteString("ExecStart=" + cfg.Exec + "\n")
		if cfg.Port != "" {
			b.WriteString("Environment=PORT=" + cfg.Port + "\n")
		}
		b.WriteString("Restart=on-failure\nNoNewPrivileges=yes\nProtectSystem=full\nPrivateTmp=yes\n")
	}
	b.WriteString("\n[Install]\nWantedBy=multi-user.target\n")
	return b.String()
}

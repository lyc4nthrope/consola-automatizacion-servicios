package servicios

import (
	"fmt"
	"strings"
)

// Config define los parámetros para la creación de la unidad systemd.
type Config struct {
	Name       string // Nombre de la aplicación (ej. "bookzilla")
	Executable string // Nombre del ejecutable dentro de /opt/<Name>/
	Port       string // Puerto opcional (ej. "8080")
	IsOneshot  bool   // true para fecha.service (Type=oneshot)
}

// ProtectedUnits contiene servicios que la consola no debe detener ni deshabilitar (DC10).
var ProtectedUnits = map[string]bool{
	"ssh":            true,
	"sshd":           true,
	"systemd-logind": true,
	"dbus":           true,
	"networking":     true,
	"NetworkManager": true,
	"haproxy":        true,
}

// IsProtected evalúa si un servicio está protegido.
func IsProtected(name string) bool {
	clean := strings.TrimSuffix(name, ".service")
	if ProtectedUnits[clean] {
		return true
	}
	if strings.HasPrefix(clean, "systemd-") || strings.HasPrefix(clean, "getty@") {
		return true
	}
	return false
}

// UnitFile genera el contenido textual de la unidad systemd respetando la regla DC7.
func UnitFile(cfg Config) string {
	var sb strings.Builder

	sb.WriteString("[Unit]\n")
	sb.WriteString(fmt.Sprintf("Description=Servicio %s gestionado por Consola\n", cfg.Name))
	sb.WriteString("After=network-online.target\n")
	sb.WriteString("Wants=network-online.target\n\n")

	sb.WriteString("[Service]\n")
	if cfg.IsOneshot {
		sb.WriteString("Type=oneshot\n")
		sb.WriteString(fmt.Sprintf("ExecStart=/opt/%s/%s\n", cfg.Name, cfg.Executable))
	} else {
		sb.WriteString("Type=simple\n")
		sb.WriteString(fmt.Sprintf("User=%s\n", cfg.Name))
		sb.WriteString(fmt.Sprintf("Group=%s\n", cfg.Name))
		sb.WriteString(fmt.Sprintf("WorkingDirectory=/var/lib/%s\n", cfg.Name))
		sb.WriteString(fmt.Sprintf("ExecStart=/opt/%s/%s\n", cfg.Name, cfg.Executable))
		if cfg.Port != "" {
			sb.WriteString(fmt.Sprintf("Environment=PORT=%s\n", cfg.Port))
		}
		sb.WriteString("Restart=on-failure\n")
		sb.WriteString("NoNewPrivileges=yes\n")
		sb.WriteString("ProtectSystem=full\n")
		sb.WriteString("PrivateTmp=yes\n")
	}

	sb.WriteString("\n[Install]\n")
	sb.WriteString("WantedBy=multi-user.target\n")

	return sb.String()
}
package balanceador

import (
	"fmt"
	"strings"
)

// Server representa una línea de backend en HAProxy.
type Server struct {
	Name string
	IP   string
	Port string
}

const (
	defaultFrontend = "frontend consola_front\n    bind *:80\n    default_backend consola_back\n\n"
	defaultBackend  = "backend consola_back\n    balance roundrobin\n"
)

// ListServers extrae los servidores configurados en consola_back.
func ListServers(cfg string) []Server {
	var servers []Server
	inBackend := false

	lines := strings.Split(cfg, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "backend consola_back") {
			inBackend = true
			continue
		}
		if inBackend && (strings.HasPrefix(trimmed, "backend ") || strings.HasPrefix(trimmed, "frontend ")) {
			break
		}
		if inBackend && strings.HasPrefix(trimmed, "server ") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 3 {
				name := parts[1]
				ipPort := strings.Split(parts[2], ":")
				ip := ipPort[0]
				port := "8080"
				if len(ipPort) > 1 {
					port = ipPort[1]
				}
				servers = append(servers, Server{Name: name, IP: ip, Port: port})
			}
		}
	}
	return servers
}

// AddServer agrega un servidor a consola_back. Si no existen los bloques, los crea (DC12).
func AddServer(cfg string, srv Server) (string, error) {
	servers := ListServers(cfg)
	for _, s := range servers {
		if s.Name == srv.Name {
			return cfg, fmt.Errorf("ya existe un servidor con el nombre %s", srv.Name)
		}
		if s.IP == srv.IP && s.Port == srv.Port {
			return cfg, fmt.Errorf("ya existe un servidor apuntando a %s:%s", srv.IP, srv.Port)
		}
	}

	serverLine := fmt.Sprintf("    server %s %s:%s check\n", srv.Name, srv.IP, srv.Port)

	if !strings.Contains(cfg, "backend consola_back") {
		var sb strings.Builder
		sb.WriteString(cfg)
		if !strings.HasSuffix(cfg, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString(defaultFrontend)
		sb.WriteString(defaultBackend)
		sb.WriteString(serverLine)
		return sb.String(), nil
	}

	lines := strings.Split(cfg, "\n")
	var result []string
	inBackend := false
	added := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "backend consola_back") {
			inBackend = true
			result = append(result, line)
			continue
		}

		if inBackend && !added && (strings.HasPrefix(trimmed, "backend ") || strings.HasPrefix(trimmed, "frontend ")) {
			result = append(result, strings.TrimRight(serverLine, "\n"))
			added = true
			inBackend = false
		}
		result = append(result, line)
	}

	if inBackend && !added {
		result = append(result, strings.TrimRight(serverLine, "\n"))
	}

	return strings.Join(result, "\n"), nil
}

// RemoveServer elimina un servidor por nombre del bloque consola_back.
func RemoveServer(cfg string, name string) (string, error) {
	lines := strings.Split(cfg, "\n")
	var result []string
	inBackend := false
	found := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "backend consola_back") {
			inBackend = true
			result = append(result, line)
			continue
		}

		if inBackend && (strings.HasPrefix(trimmed, "backend ") || strings.HasPrefix(trimmed, "frontend ")) {
			inBackend = false
		}

		if inBackend && strings.HasPrefix(trimmed, "server "+name+" ") {
			found = true
			continue
		}

		result = append(result, line)
	}

	if !found {
		return cfg, fmt.Errorf("el servidor %s no existe en la configuración", name)
	}

	return strings.Join(result, "\n"), nil
}
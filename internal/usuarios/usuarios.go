// Package usuarios is Part B's pure logic for account operations (B4 and B5):
// it builds the plans and commands for creating and removing users on the VM,
// and parses what getent prints. It touches no channel, no state and no web.
package usuarios

import (
	"strconv"
	"strings"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
)

// UsuarioEntry represents a parsed line from getent passwd.
type UsuarioEntry struct {
	Name  string
	UID   int
	GID   int
	Gecos string
	Home  string
	Shell string
}

// ParseUsuarioEntry parses one line of getent passwd output.
// Format: name:x:uid:gid:gecos:home:shell
func ParseUsuarioEntry(line string) (UsuarioEntry, bool) {
	parts := strings.SplitN(line, ":", 7)
	if len(parts) != 7 {
		return UsuarioEntry{}, false
	}
	uid, _ := strconv.Atoi(parts[2])
	gid, _ := strconv.Atoi(parts[3])
	return UsuarioEntry{
		Name:  parts[0],
		UID:   uid,
		GID:   gid,
		Gecos: parts[4],
		Home:  parts[5],
		Shell: parts[6],
	}, true
}

// EsUsuarioEliminable applies the DB14 filter: UID 1000-59999, not "nobody",
// and not a protected name (root, the console's SSH user, the connecting user).
// The caller provides the set of protected names.
func (u UsuarioEntry) EsUsuarioEliminable(protegidos map[string]bool) bool {
	if u.UID < 1000 || u.UID > 59999 {
		return false
	}
	if u.Name == "nobody" {
		return false
	}
	if protegidos[u.Name] {
		return false
	}
	return true
}

// PlanRevisar asks the VM whether the name is taken, with getent passwd (its
// exit code 0 = user exists, 2 = key not found). AlsoOK accepts 2 so the
// "free" answer is a normal success; the plan's Success message branches on
// the code so the operation log keeps a readable account of why the flow
// stopped. The handler reads Entry.Steps' exit code to decide what to show.
func PlanRevisar(target, usuario string) operacion.Plan {
	revisar := executor.Command{Name: "getent", Args: []string{"passwd", usuario}}
	return operacion.Plan{
		Action:   "Revisar la cuenta nueva",
		Target:   target,
		AlsoOK:   []int{2},
		Commands: []executor.Command{revisar},
		Success: func(res []executor.Result) string {
			if res[0].ExitCode == 2 {
				return "El nombre de usuario está libre en la VM: la consola puede crear la cuenta."
			}
			return "Un usuario con ese nombre ya existe en la VM: la consola muestra el aviso junto al campo y no toca esa cuenta."
		},
	}
}

// PlanCrear builds the account operations: the user with its /home folder and
// bash, the password through chpasswd's Stdin — the only place where a
// password may travel, per DB12 — and, when the checkbox asked, the sudo
// group membership. The SSH llave flow is another operation that follows.
func PlanCrear(target, usuario, contrasena string, sudo bool) operacion.Plan {
	crear := executor.Command{
		Name: "useradd",
		Args: []string{"-m", "-s", "/bin/bash", "--", usuario},
	}
	contrasenia := executor.Command{
		Name:  "chpasswd",
		Stdin: usuario + ":" + contrasena,
	}
	comandos := []executor.Command{crear, contrasenia}
	if sudo {
		comandos = append(comandos, executor.Command{
			Name: "usermod",
			Args: []string{"-aG", "sudo", "--", usuario},
		})
	}
	return operacion.Plan{
		Action:   "Crear la cuenta de usuario",
		Target:   target,
		Commands: comandos,
		Success: func(res []executor.Result) string {
			if sudo {
				return "La cuenta quedó creada en la VM con su carpeta /home, sesión bash y privilegio de sudo; la contraseña se envió solo por el canal activo."
			}
			return "La cuenta quedó creada en la VM con su carpeta /home y sesión bash; la contraseña se envió solo por el canal activo."
		},
	}
}

// PlanEliminar builds the user removal operation: userdel (with -r if the
// checkbox asked), removal of the per-user sshd drop-in, sshd -t gate, and
// ssh reload. The Explain function handles the "user has active processes"
// case by checking stderr for that phrase.
func PlanEliminar(target, usuario string, borrarArchivos bool) operacion.Plan {
	args := []string{"--", usuario}
	if borrarArchivos {
		args = append([]string{"-r"}, args...)
	}
	userdel := executor.Command{
		Name: "userdel",
		Args: args,
	}
	dropIn := "/etc/ssh/sshd_config.d/20-consola-" + usuario + ".conf"
	borrarDropIn := executor.Command{
		Name: "rm",
		Args: []string{"-f", "--", dropIn},
	}
	prueba := executor.Command{
		Name: "sh",
		Args: []string{"-c", "set -e\nif ! sshd -t; then\n  exit 11\nfi"},
	}
	recargar := executor.Command{
		Name: "systemctl",
		Args: []string{"reload", "ssh"},
	}
	return operacion.Plan{
		Action:   "Eliminar el usuario",
		Target:   target,
		Commands: []executor.Command{userdel, borrarDropIn, prueba, recargar},
		Success: func(res []executor.Result) string {
			if borrarArchivos {
				return "El usuario y su carpeta /home fueron eliminados; el drop-in " + dropIn + " se borró, sshd -t pasó y el servicio se recargó."
			}
			return "El usuario fue eliminado (su carpeta /home queda intacta); el drop-in " + dropIn + " se borró, sshd -t pasó y el servicio se recargó."
		},
		Explain: func(step oplog.Step) string {
			if step.Cmd == userdel.String() && strings.Contains(step.Stderr, "is currently used by process") {
				return "El usuario tiene procesos activos. Ciérralos en la VM o espera a que terminen y vuelve a intentarlo."
			}
			if step.Cmd == prueba.String() {
				return "El sshd -t rechazó la configuración tras borrar el drop-in; no se recargó el servicio. Revisa la salida de abajo."
			}
			if step.Cmd == recargar.String() {
				return "No se pudo recargar el sshd; el drop-in ya está borrado, la próxima recarga lo aplicará. Revisa la salida de abajo."
			}
			return ""
		},
	}
}

// PlanEditar builds the user edit operations: optional password change via
// chpasswd (the only place a password may travel, per DB12), optional sudo
// membership toggle via usermod/gpasswd. The SSH key flow is a separate
// operation that the handler chains when the checkbox is on.
func PlanEditar(target, usuario, nuevaContrasena string, sudo bool) operacion.Plan {
	var comandos []executor.Command

	if nuevaContrasena != "" {
		comandos = append(comandos, executor.Command{
			Name:  "chpasswd",
			Stdin: usuario + ":" + nuevaContrasena,
		})
	}

	if sudo {
		comandos = append(comandos, executor.Command{
			Name: "usermod",
			Args: []string{"-aG", "sudo", "--", usuario},
		})
	} else {
		comandos = append(comandos, executor.Command{
			Name: "gpasswd",
			Args: []string{"-d", usuario, "sudo"},
		})
	}

	return operacion.Plan{
		Action:   "Editar la cuenta de usuario",
		Target:   target,
		Commands: comandos,
		Success: func(res []executor.Result) string {
			var partes []string
			if nuevaContrasena != "" {
				partes = append(partes, "la contraseña se actualizó")
			}
			if sudo {
				partes = append(partes, "se activó sudo")
			} else {
				partes = append(partes, "se desactivó sudo")
			}
			if len(partes) == 0 {
				return "No hubo cambios que aplicar."
			}
			return "Cuenta actualizada: " + strings.Join(partes, ", ") + "."
		},
		Explain: func(step oplog.Step) string {
			switch {
			case step.Cmd == "chpasswd" && step.ExitCode != 0:
				return "Error al cambiar la contraseña: revisa la salida de abajo."
			case step.Cmd == "usermod" && step.ExitCode != 0:
				return "Error al activar sudo: revisa la salida de abajo."
			case step.Cmd == "gpasswd" && step.ExitCode != 0:
				return "Error al desactivar sudo: revisa la salida de abajo."
			}
			return ""
		},
	}
}

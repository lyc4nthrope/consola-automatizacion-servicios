// Package usuarios is Part B's pure logic for account operations (B4 and B5):
// it builds the plans and commands for creating and removing users on the VM,
// and parses what getent prints. It touches no channel, no state and no web.
package usuarios

import (
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
)

// PlanRevisar asks the VM whether the name is taken, with getent passwd (its
// exit code 0 = user exists, 2 = key not found). AlsoOK accepts 2 so the
// "free" answer is a normal success; the plan's Success message branches on
// the code so the operation log keeps a readable account of why the flow
// stopped. The handler reads Entry.Steps' exit code to decide what to show.
func PlanRevisar(target, usuario string) operacion.Plan {
	revisar := executor.Command{Name: "getent", Args: []string{"passwd", usuario}}
	return operacion.Plan{
		Action:  "Revisar la cuenta nueva",
		Target:  target,
		AlsoOK:  []int{2},
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

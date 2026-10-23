package usuarios

import (
	"strings"
	"testing"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/operacion"
)

// PlanRevisar asks the VM whether the name is free with getent passwd (DB14's
// predecessor for B4): exit 2 (= not found) counts as success and stops, so
// the handler can read the code from the log entry afterwards.
func TestPlanRevisar(t *testing.T) {
	p := PlanRevisar("pruebas", "daniel")
	if p.Action != "Revisar la cuenta nueva" || p.Target != "pruebas" {
		t.Fatalf("plan = %+v", p)
	}
	want := []executor.Command{{Name: "getent", Args: []string{"passwd", "daniel"}}}
	if !sameCommands(p.Commands, want) {
		t.Errorf("comandos = %+v", p.Commands)
	}
	if !equalInts(p.AlsoOK, []int{2}) {
		t.Errorf("AlsoOK = %v, want [2]", p.AlsoOK)
	}
	if p.Success == nil {
		t.Fatal("el plan revisar necesita Success para explicar el código")
	}
	msg2 := p.Success([]executor.Result{{ExitCode: 2}})
	if !strings.Contains(msg2, "libre") {
		t.Errorf("con código 2 el mensaje no dice que el nombre está libre: %q", msg2)
	}
	msg0 := p.Success([]executor.Result{{ExitCode: 0}})
	if strings.Contains(msg0, "libre") || !strings.Contains(msg0, "ya existe") {
		t.Errorf("con código 0 (existe) el mensaje debe decir que ya existe: %q", msg0)
	}
}

// PlanCrear builds useradd + chpasswd (the password only through Stdin, DB12)
// and, when asked, the sudo usermod. The four combinations are the exact
// commands the B4 phase spec demands.
func TestPlanCrear4Combinaciones(t *testing.T) {
	const (
		contrasena   = "ClaveMuy-2026"
		usuario      = "daniel"
		target       = "pruebas"
	)
	pruebas := []struct {
		nombre  string
		sudo    bool
		completo bool
	}{
		{"sin sudo ni llave", false, false},
		{"solo sudo", true, false},
		{"solo llave", false, true},
		{"todo", true, true},
	}
	// llave no cambia PlanCrear: la llave es una operación aparte que sigue.
	for _, caso := range pruebas {
		p := PlanCrear(target, usuario, contrasena, caso.sudo)
		if p.Action != "Crear la cuenta de usuario" || p.Target != target {
			t.Fatalf("%s: título/destino = %+v", caso.nombre, p)
		}
		want := []executor.Command{
			{Name: "useradd", Args: []string{"-m", "-s", "/bin/bash", "--", usuario}},
			{Name: "chpasswd", Stdin: usuario + ":" + contrasena},
		}
		if caso.sudo {
			want = append(want, executor.Command{Name: "usermod", Args: []string{"-aG", "sudo", "--", usuario}})
		}
		if !sameCommands(p.Commands, want) {
			t.Errorf("%s: comandos = %+v", caso.nombre, p.Commands)
		}
	}
}

// La contraseña viaja SOLO en el Stdin de chpasswd: no puede salir en la
// representación de ningún comando (ni en ps ni en el registro).
func TestPlanCrearLaContraseñaSoloVaEnStdin(t *testing.T) {
	const contrasena = "ClaveMuy-2026-x"
	p := PlanCrear("pruebas", "daniel", contrasena, true)
	for _, c := range p.Commands {
		if strings.Contains(c.String(), contrasena) {
			t.Errorf("el comando %s filtra la contraseña en su línea", c.String())
		}
		if c.Name == "chpasswd" && c.Stdin != "daniel:"+contrasena {
			t.Errorf("chpasswd.Stdin = %q", c.Stdin)
		}
	}
}

func sameCommands(tuvimos, want []executor.Command) bool {
	if len(tuvimos) != len(want) {
		return false
	}
	for i := range want {
		if tuvimos[i].Name != want[i].Name || strings.Join(tuvimos[i].Args, "|") != strings.Join(want[i].Args, "|") || tuvimos[i].Stdin != want[i].Stdin {
			return false
		}
	}
	return true
}

func equalInts(ganadas, want []int) bool {
	if len(ganadas) != len(want) {
		return false
	}
	for i := range want {
		if ganadas[i] != want[i] {
			return false
		}
	}
	return true
}

// plan interface check…: PlanRevisar y PlanCrear devuelven operacion.Plan.
var _ operacion.Plan
var _ operacion.Plan = PlanRevisar("t", "u")
var _ operacion.Plan = PlanCrear("t", "u", "p", false)

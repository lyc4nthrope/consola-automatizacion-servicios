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
		contrasena = "ClaveMuy-2026"
		usuario    = "daniel"
		target     = "pruebas"
	)
	pruebas := []struct {
		nombre   string
		sudo     bool
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
var _ operacion.Plan = PlanEliminar("t", "u", false)

// ParseUsuarioEntry parses a getent passwd line correctly.
func TestParseUsuarioEntry(t *testing.T) {
	line := "daniel:x:1000:1000:Daniel,,:/home/daniel:/bin/bash"
	entry, ok := ParseUsuarioEntry(line)
	if !ok {
		t.Fatal("ParseUsuarioEntry returned false on valid line")
	}
	if entry.Name != "daniel" || entry.UID != 1000 || entry.GID != 1000 || entry.Home != "/home/daniel" || entry.Shell != "/bin/bash" {
		t.Errorf("entry = %+v", entry)
	}
	// Invalid line
	if _, ok := ParseUsuarioEntry("no:colon:here"); ok {
		t.Error("ParseUsuarioEntry should reject invalid format")
	}
}

// EsUsuarioEliminable filters per DB14: UID 1000-59999, not nobody, not protected.
func TestEsUsuarioEliminable(t *testing.T) {
	protegidos := map[string]bool{"root": true, "consola-ssh": true}
	tests := []struct {
		nombre string
		entry  UsuarioEntry
		ok     bool
	}{
		{"usuario normal UID 1000", UsuarioEntry{Name: "daniel", UID: 1000}, true},
		{"usuario normal UID 59999", UsuarioEntry{Name: "usuario", UID: 59999}, true},
		{"UID muy bajo 999", UsuarioEntry{Name: "sys", UID: 999}, false},
		{"UID muy alto 60000", UsuarioEntry{Name: "alto", UID: 60000}, false},
		{"nobody", UsuarioEntry{Name: "nobody", UID: 65534}, false},
		{"protegido root", UsuarioEntry{Name: "root", UID: 0}, false},
		{"protegido ssh_user", UsuarioEntry{Name: "consola-ssh", UID: 1001}, false},
	}
	for _, tc := range tests {
		if got := tc.entry.EsUsuarioEliminable(protegidos); got != tc.ok {
			t.Errorf("%s: EsUsuarioEliminable = %v, want %v", tc.nombre, got, tc.ok)
		}
	}
}

// PlanEliminar builds userdel (with -r when asked), drop-in removal, sshd -t gate, reload.
func TestPlanEliminar(t *testing.T) {
	casos := []struct {
		nombre         string
		borrarArchivos bool
		wantArgs       []string
	}{
		{"sin -r", false, []string{"--", "daniel"}},
		{"con -r", true, []string{"-r", "--", "daniel"}},
	}
	for _, tc := range casos {
		p := PlanEliminar("pruebas", "daniel", tc.borrarArchivos)
		if p.Action != "Eliminar el usuario" || p.Target != "pruebas" {
			t.Fatalf("%s: action/target = %+v", tc.nombre, p)
		}
		// userdel command
		if p.Commands[0].Name != "userdel" || strings.Join(p.Commands[0].Args, "|") != strings.Join(tc.wantArgs, "|") {
			t.Errorf("%s: userdel args = %v, want %v", tc.nombre, p.Commands[0].Args, tc.wantArgs)
		}
		// drop-in removal
		if p.Commands[1].Name != "rm" || p.Commands[1].Args[2] != "/etc/ssh/sshd_config.d/20-consola-daniel.conf" {
			t.Errorf("%s: drop-in rm = %v", tc.nombre, p.Commands[1].Args)
		}
		// sshd -t gate
		if p.Commands[2].Name != "sh" || !strings.Contains(p.Commands[2].Args[1], "sshd -t") {
			t.Errorf("%s: sshd -t gate missing", tc.nombre)
		}
		// reload
		if p.Commands[3].Name != "systemctl" || strings.Join(p.Commands[3].Args, "|") != "reload|ssh" {
t.Errorf("%s: systemctl reload missing: %v", tc.nombre, p.Commands[3].Args)
		}
		if len(p.Commands) != 4 {
			t.Errorf("%s: want 4 commands, got %d", tc.nombre, len(p.Commands))
		}
	}
}

// PlanEditar builds chpasswd (if new password), usermod/gpasswd for sudo toggle.
func TestPlanEditar(t *testing.T) {
	casos := []struct {
		nombre          string
		nuevaContrasena string
		sudo            bool
		wantCmds        []executor.Command
	}{
		{
			nombre:          "solo contraseña (sudo=false -> quita sudo)",
			nuevaContrasena: "NuevaClave-2026",
			sudo:            false,
			wantCmds: []executor.Command{
				{Name: "chpasswd", Stdin: "daniel:NuevaClave-2026"},
				{Name: "gpasswd", Args: []string{"-d", "daniel", "sudo"}},
			},
		},
		{
			nombre:          "activar sudo",
			nuevaContrasena: "",
			sudo:            true,
			wantCmds: []executor.Command{
				{Name: "usermod", Args: []string{"-aG", "sudo", "--", "daniel"}},
			},
		},
		{
			nombre:          "desactivar sudo",
			nuevaContrasena: "",
			sudo:            false,
			wantCmds: []executor.Command{
				{Name: "gpasswd", Args: []string{"-d", "daniel", "sudo"}},
			},
		},
		{
			nombre:          "contraseña + activar sudo",
			nuevaContrasena: "OtraClave-2026",
			sudo:            true,
			wantCmds: []executor.Command{
				{Name: "chpasswd", Stdin: "daniel:OtraClave-2026"},
				{Name: "usermod", Args: []string{"-aG", "sudo", "--", "daniel"}},
			},
		},
		{
			nombre:          "contraseña + desactivar sudo",
			nuevaContrasena: "OtraClave-2026",
			sudo:            false,
			wantCmds: []executor.Command{
				{Name: "chpasswd", Stdin: "daniel:OtraClave-2026"},
				{Name: "gpasswd", Args: []string{"-d", "daniel", "sudo"}},
			},
		},
	}
	for _, tc := range casos {
		p := PlanEditar("pruebas", "daniel", tc.nuevaContrasena, tc.sudo)
		if p.Action != "Editar la cuenta de usuario" || p.Target != "pruebas" {
			t.Fatalf("%s: action/target = %+v", tc.nombre, p)
		}
		if !sameCommands(p.Commands, tc.wantCmds) {
			t.Errorf("%s: comandos = %+v, want %+v", tc.nombre, p.Commands, tc.wantCmds)
		}
		if p.Success == nil {
			t.Errorf("%s: falta Success", tc.nombre)
		}
		if p.Explain == nil {
			t.Errorf("%s: falta Explain", tc.nombre)
		}
	}
}

// La contraseña viaja SOLO en el Stdin de chpasswd.
func TestPlanEditarLaContraseñaSoloVaEnStdin(t *testing.T) {
	const contrasena = "ClaveSecreta-2026"
	p := PlanEditar("pruebas", "daniel", contrasena, true)
	for _, c := range p.Commands {
		if strings.Contains(c.String(), contrasena) {
			t.Errorf("el comando %s filtra la contraseña en su línea", c.String())
		}
		if c.Name == "chpasswd" && c.Stdin != "daniel:"+contrasena {
			t.Errorf("chpasswd.Stdin = %q", c.Stdin)
		}
	}
}

// interface check para PlanEditar
var _ operacion.Plan = PlanEditar("t", "u", "p", false)

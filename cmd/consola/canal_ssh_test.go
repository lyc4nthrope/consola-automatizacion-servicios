package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
)

// sshDisponible evita depender del cliente de SSH en un equipo que no lo tenga.
func sshDisponible(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen no está disponible en este equipo (no está en el PATH)")
	}
}

// ttlSinServerSSH sostiene... skip si un servidor responde en 127.0.0.1:22.
func skipSiHayServerSSHLocal(t *testing.T) {
	t.Helper()
	c, err := net.DialTimeout("tcp", "127.0.0.1:22", 500*time.Millisecond)
	if err == nil {
		c.Close()
		t.Skip("hay un servidor SSH en 127.0.0.1:22: la prueba esperaba el puerto cerrado")
	}
}

func TestCanalSSHRegistrado(t *testing.T) {
	if sshChannel == nil {
		t.Fatal("el init de canal_ssh.go no asignó la función al sshChannel de main.go")
	}
}

func TestCanalSSHRechazaPrivadaSinPublica(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "consola_ed25519"), []byte("privada huérfana"), 0o600); err != nil {
		t.Fatal(err)
	}
	ex, detalle, ok := sshChannel(app.Target{Name: "pruebas", VM: "consola-pruebas", IP: "127.0.0.1", SSHUser: "root"}, dir)
	if ok || ex != nil || detalle != "" {
		t.Errorf("con una llave privada sin su pública: ok=%v detalle=%q ex=%v", ok, detalle, ex)
	}
}

// Sin VM: la llave se prepara (ssh-keygen real local si hace falta), la prueba
// falla con el puerto de 127.0.0.1 cerrado y el canal queda rechazado.
func TestCanalSSHConPuertoCerrado(t *testing.T) {
	sshDisponible(t)
	skipSiHayServerSSHLocal(t)
	dir := t.TempDir()
	ex, detalle, ok := sshChannel(app.Target{Name: "pruebas", VM: "consola-pruebas", IP: "127.0.0.1", SSHUser: "root"}, dir)
	if ok || ex != nil || detalle != "" {
		t.Errorf("con 127.0.0.1 cerrado: ok=%v detalle=%q ex=%v", ok, detalle, ex)
	}
	if _, err := os.Stat(filepath.Join(dir, "consola_ed25519.pub")); err != nil {
		t.Errorf("la llave debería haberse generado aunque la VM no respondiera: %v", err)
	}
}

// Con una VM preparada (la llave de la consola ya conocida): normalmente no
// corre; se activa señalando el host con CONSOLA_SSH_HOST.
func TestCanalSSHConVMReal(t *testing.T) {
	host := os.Getenv("CONSOLA_SSH_HOST")
	if host == "" {
		t.Skip("CONSOLA_SSH_HOST no está fijado: se necesita una VM preparada para SSH")
	}
	dir := t.TempDir()
	ex, detalle, ok := sshChannel(app.Target{Name: "pruebas", VM: "consola-pruebas", IP: host, SSHUser: "root"}, dir)
	if !ok {
		t.Fatalf("la VM %s está lista para SSH y el canal lo negó", host)
	}
	if ex.Name() != "SSH" {
		t.Errorf("el canal devuelto se anuncia como %q", ex.Name())
	}
	if !strings.Contains(detalle, "consola_ed25519") {
		t.Errorf("el detalle del registro debería nombrar la llave: %q", detalle)
	}
}

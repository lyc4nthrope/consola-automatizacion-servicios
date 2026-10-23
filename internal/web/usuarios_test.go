package web

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
)

// sshDisponibleWeb evita depender del cliente de SSH en un equipo que no lo tenga.
func sshDisponibleWeb(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("el cliente ssh no está disponible en este equipo")
	}
}

// sinServerSSHLocal para cuando un sshd responde en 127.0.0.1:22: la prueba del
// rechazo espera ese puerto cerrado (500 ms y se da por perdido).
func sinServerSSHLocal(t *testing.T) {
	t.Helper()
	c, err := net.DialTimeout("tcp", "127.0.0.1:22", 500*time.Millisecond)
	if err == nil {
		c.Close()
		t.Skip("hay un servidor SSH en 127.0.0.1:22: la prueba esperaba el puerto cerrado")
	}
}

func TestUsuariosPaginaExisteYSeVe(t *testing.T) {
	estado := newTestState()
	_ = estado.SetExecutor("pruebas", &executor.Fake{NameValue: "Guest Additions"})
	rec := serve(t, newTestHandler(t, estado), httptest.NewRequest("GET", "/usuarios", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /usuarios = %d", rec.Code)
	}
	cuerpo := rec.Body.String()
	for _, esperada := range []string{"Gestión de usuarios", "Conectar por SSH", "Guest Additions"} {
		if !strings.Contains(cuerpo, esperada) {
			t.Errorf("la página no muestra %q", esperada)
		}
	}
}

// El POST instala la llave: el canal actual de la VM recibe la lectura y luego la
// escritura con la fusión (las llaves existentes primero), y la prueba SSH —
// contra el 127.0.0.1 cerrado de este equipo — no cambia el canal.
func TestConectarSSHInstalaLaLlaveSinCambiarElCanal(t *testing.T) {
	sshDisponibleWeb(t)
	sinServerSSHLocal(t)
	t.Chdir(t.TempDir()) // la llave de la consola se genera en keys/ del directorio de trabajo

	estado := newTestState()
	contacto := &executor.Fake{NameValue: "Guest Additions"}
	_ = estado.SetExecutor("pruebas", contacto)
	h, _ := newTestHandlerWithLog(t, estado)

	rec := serve(t, h, httptest.NewRequest("POST", "/usuarios/conectar-ssh", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /usuarios/conectar-ssh = %d, want 303 (redirect al /resultado/…)", rec.Code)
	}
	destino := rec.Header().Get("Location")
	if !strings.HasPrefix(destino, "/resultado/") {
		t.Errorf("Location %q, want /resultado/…", destino)
	}

	pub, err := os.ReadFile(filepath.Join("keys", "consola_ed25519.pub"))
	if err != nil {
		t.Fatalf("la llave de la consola no quedó en keys/: %v", err)
	}

	registradas := contacto.Calls()
	if len(registradas) != 2 {
		t.Fatalf("el canal recibió %d comandos, want 2 (leer + instalar)", len(registradas))
	}
	leer, escribir := registradas[0], registradas[1]
	// La ruta viaja como "$1"... aquí: Args = ["-c", <script>, "sh", <ruta>, <mezcla>, <sha>]
	if leer.Args[3] != authorizedKeys {
		t.Errorf("la lectura no apunta a authorized_keys: %q", leer.Args[3])
	}
	if escribir.Args[3] != authorizedKeys {
		t.Errorf("la escritura no apunta a authorized_keys: %q", escribir.Args[3])
	}
	// La mezcla contiene la llave pública de la consola (y nada más, ante una
	// lectura vacía del Fake).
	if mezcla := strings.TrimSpace(escribir.Args[4]); mezcla != strings.TrimSpace(string(pub)) {
		t.Errorf("la escritura no contiene la llave pública de la consola: %q (quedó %q)", mezcla, string(pub))
	}
	// el Fake leyó un archivo presente y vacío, así que la escritura exige su
	// sha vacío conocido (64 hexadecimales)
	if sha := escribir.Args[5]; len(sha) != 64 {
		t.Errorf("el sha de la lectura vacía no llegó a la escritura: %q", sha)
	}

	if got := estado.Channel("pruebas"); got != "Guest Additions" {
		t.Errorf("la prueba SSH falló y el canal cambió a %q", got)
	}

	// La página de resultado se ve con el nuevo canal (aquí sigue sin cambiar)
	// y no pierde la explicación del paso.
	vistaRT := serve(t, h, httptest.NewRequest("GET", destino, nil))
	if vistaRT.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", destino, vistaRT.Code)
	}
	if !strings.Contains(vistaRT.Body.String(), "authorized_keys") {
		t.Errorf("el registro de la operación no nombra authorized_keys")
	}
}

// Cuando cambió el archivo entre la lectura y la escritura (código 12), la
// operación no escribe nada, el registro lo explica y el canal queda igual.
func TestConectarSSHRechazaArchivoCambiado(t *testing.T) {
	sshDisponibleWeb(t)
	sinServerSSHLocal(t)
	t.Chdir(t.TempDir())

	estado := newTestState()
	paso := 0
	contacto := &executor.Fake{NameValue: "Guest Additions", RunFunc: func(c executor.Command) (executor.Result, error) {
		paso++
		if paso == 1 { // la lectura llegó antes del cambio ajeno
			return executor.Result{Stdout: "ssh-ed25519 AAAAANTES root@vm\n"}, nil
		}
		// El archivo (ficticio aquí) cambió: el script real habría salido 12.
		return executor.Result{ExitCode: fileChangedCode}, nil
	}}
	_ = estado.SetExecutor("pruebas", contacto)
	h, _ := newTestHandlerWithLog(t, estado)

	rec := serve(t, h, httptest.NewRequest("POST", "/usuarios/conectar-ssh", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST = %d, want 303", rec.Code)
	}
	vista := serve(t, h, httptest.NewRequest("GET", rec.Header().Get("Location"), nil))
	if vista.Code != http.StatusOK {
		t.Fatalf("GET /resultado/… = %d", vista.Code)
	}
	if !strings.Contains(vista.Body.String(), "cambió entre la lectura y la escritura") {
		t.Errorf("la explicación del rechazo no aparece en el registro")
	}
	if got := estado.Channel("pruebas"); got != "Guest Additions" {
		t.Errorf("el canal cambió a %q", got)
	}
}

// Con una llave de la consola imposible de preparar, la respuesta es un error
// del propio módulo (no un redirect), sin tocar ninguna VM.
func TestConectarSSHRespondeErrorSinLlave(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, "keys"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keys", "consola_ed25519"), []byte("privada huérfana"), 0o600); err != nil {
		t.Fatal(err)
	}

	estado := newTestState()
	contacto := &executor.Fake{NameValue: "Guest Additions"}
	_ = estado.SetExecutor("pruebas", contacto)

	rec := serve(t, newTestHandler(t, estado), httptest.NewRequest("POST", "/usuarios/conectar-ssh", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("POST con llave huérfana = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "parte pública") {
		t.Errorf("el mensaje no explica lo que pasa: %s", rec.Body.String())
	}
	if got := estado.Channel("pruebas"); got != "Guest Additions" {
		t.Errorf("el canal cambió a %q", got)
	}
}

// El Preparado conexo: los destinos sigue en su lugar... un destino listo para SSH
// se dispara con CONSOLA_SSH_HOST (no es la ruta normal de CI).
func TestConectarSSHConVM(t *testing.T) {
	host := os.Getenv("CONSOLA_SSH_HOST")
	if host == "" {
		t.Skip("CONSOLA_SSH_HOST no está fijado: se necesita una VM preparada para SSH")
	}
	t.Chdir(t.TempDir())
	appCfg := app.Config{Listen: app.DefaultListen, Targets: []app.Target{
		{Name: "pruebas", VM: "consola-pruebas", IP: host, SSHUser: "root"},
	}}
	estado := app.NewState(appCfg)
	_ = estado.SetExecutor("pruebas", &executor.Fake{NameValue: "Guest Additions"})

	rec := serve(t, newTestHandler(t, estado), httptest.NewRequest("POST", "/usuarios/conectar-ssh", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST = %d", rec.Code)
	}
	if got := estado.Channel("pruebas"); got != "SSH" {
		t.Errorf("con una VM lista para SSH el canal quedó %q, want SSH", got)
	}
}

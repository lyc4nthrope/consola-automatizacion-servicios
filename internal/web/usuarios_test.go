package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/llaves"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/usuarios"
	assets "github.com/lyc4nthrope/consola-automatizacion-servicios/web"
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

// ---- llaves de root (B3): un canal de prueba que hace las copias ----

const (
	pubDeRootPrueba     = "ssh-ed25519 AAAAROOTAQUI root@repo-vm\n"
	privadaDeRootPrueba = "-----BEGIN OPENSSH PRIVATE KEY-----\nROSALIA-B3-NO-ES-UNA-LLAVE\n-----END OPENSSH PRIVATE KEY-----\n"
)

// canalLlavesFalso juega el canal SSH con copias reales: cada Run se compara
// contra el repertorio exacto del flujo (lo inesperado queda en "nuevos", que
// no debe llenarse) y las copias funcionan: la .pub y la privada "salen" de la
// VM con el contenido de prueba y el drop-in subido queda apuntado en subidas.
type canalLlavesFalso struct {
	executor.Fake
	subidas      map[string]string // ruta remota -> contenido que recibió (CopyTo)
	nuevos       []string          // comandos fuera del repertorio (debe quedar vacío)
	codigoGetent int               // exit code de getent en el flujo crear (2 = libre)
}

func canalLlavesDePrueba(t *testing.T) *canalLlavesFalso {
	t.Helper()
	canal := &canalLlavesFalso{subidas: map[string]string{}}
	canal.NameValue = "SSH"
	canal.RunFunc = func(c executor.Command) (executor.Result, error) {
		switch {
		case c.String() == "hostname":
			return executor.Result{Stdout: "servidor1\n"}, nil
		case c.String() == "mktemp -d":
			return executor.Result{Stdout: "/tmp/consola.llaves-XXXXXX\n"}, nil
		case c.Name == "ssh-keygen", c.Name == "systemctl", c.Name == "install", c.Name == "rm":
			return executor.Result{}, nil
		case c.Name == "sh" && len(c.Args) > 1:
			switch c.Args[1] {
			case leerLlavesScript:
				return executor.Result{Stdout: "ssh-ed25519 AAAAEXISTENTE root@repo-vm\n"}, nil
			case instalarLlavesScript, sshdPruebaScript, dropInVerificadoScript:
				return executor.Result{}, nil
			}
		}
		canal.nuevos = append(canal.nuevos, c.String())
		return executor.Result{}, nil
	}
	return canal
}

func (c *canalLlavesFalso) CopyFrom(ctx context.Context, remotePath, localPath string) error {
	if err := c.Fake.CopyFrom(ctx, remotePath, localPath); err != nil {
		return err
	}
	switch {
	case strings.HasSuffix(remotePath, "id_ed25519.pub"):
		return os.WriteFile(localPath, []byte(pubDeRootPrueba), 0o600)
	case strings.HasSuffix(remotePath, "id_ed25519"):
		return os.WriteFile(localPath, []byte(privadaDeRootPrueba), 0o600)
	}
	return fmt.Errorf("CopyFrom sobre %q (la prueba no lo espera)", remotePath)
}

func (c *canalLlavesFalso) CopyTo(ctx context.Context, localPath, remotePath string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	c.subidas[remotePath] = string(data)
	return c.Fake.CopyTo(ctx, localPath, remotePath)
}

// canalCrearDePrueba is the SSH test channel for B4's crear flow: the same
// copy mechanics (the .pub and the private key "arrive" from the VM) with the
// account commands in its repertoire. codigoGetent decides what the precheck
// reports (2 = the name is free, 0 = it exists).
func canalCrearDePrueba(t *testing.T, codigoGetent int) *canalLlavesFalso {
	t.Helper()
	canal := canalLlavesDePrueba(t)
	canal.codigoGetent = codigoGetent
	canal.RunFunc = func(c executor.Command) (executor.Result, error) {
		switch {
		case c.String() == "hostname":
			return executor.Result{Stdout: "servidor1\n"}, nil
		case c.String() == "mktemp -d":
			return executor.Result{Stdout: "/tmp/consola.llaves-XXXXXX\n"}, nil
		case c.Name == "getent":
			return executor.Result{ExitCode: canal.codigoGetent}, nil
		case c.Name == "useradd", c.Name == "chpasswd", c.Name == "usermod":
			return executor.Result{}, nil
		case c.Name == "ssh-keygen", c.Name == "systemctl", c.Name == "install", c.Name == "rm", c.Name == "chown":
			return executor.Result{}, nil
		case c.Name == "sh" && len(c.Args) > 1:
			switch c.Args[1] {
			case leerLlavesScript:
				return executor.Result{Stdout: "ssh-ed25519 AAAAEXISTENTE root@repo-vm\n"}, nil
			case instalarLlavesScript, sshdPruebaScript, dropInVerificadoScript:
				return executor.Result{}, nil
			}
		}
		if len(c.Args) > 1 && (strings.HasPrefix(c.Args[1], "v=$(sshd -T -C user=") || strings.Contains(c.Args[1], "passwordauthentication")) {
			return executor.Result{}, nil
		}
		canal.nuevos = append(canal.nuevos, c.String())
		return executor.Result{}, nil
	}
	return canal
}

// El flujo completo: genera el par en la VM con ssh-keygen, instala la pública
// de root junto a las llaves existentes, deja el drop-in con el contenido
// exacto, recarga sshd, borra los temporales de la VM y redirige a la página
// "llave lista". El repertorio de comandos es exacto y en orden.
func TestGenerarLlavesRootFlujoCompleto(t *testing.T) {
	t.Chdir(t.TempDir())
	estado := newTestState()
	canal := canalLlavesDePrueba(t)
	_ = estado.SetExecutor("pruebas", canal)
	h, _ := newTestHandlerWithLog(t, estado)

	rec := serve(t, h, httptest.NewRequest("POST", "/usuarios/llaves-root", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /usuarios/llaves-root = %d, want 303", rec.Code)
	}
	destino := rec.Header().Get("Location")
	u, err := url.Parse(destino)
	if err != nil || !strings.HasPrefix(destino, "/usuarios/llaves/listo/") {
		t.Fatalf("Location %q, want /usuarios/llaves/listo/<token>?id=…", destino)
	}
	token := strings.TrimPrefix(u.Path, "/usuarios/llaves/listo/")
	if len(token) != 32 {
		t.Fatalf("el token %q no mide 32 caracteres", token)
	}
	idDetalle := u.Query().Get("id")
	if idDetalle == "" {
		t.Fatalf("el redirect no trae ?id= (el detalle técnico): %q", destino)
	}

	dir := "/tmp/consola.llaves-XXXXXX"
	merge := llaves.MergeAuthorizedKeys("ssh-ed25519 AAAAEXISTENTE root@repo-vm\n", strings.TrimSpace(pubDeRootPrueba))
	want := []executor.Command{
		{Name: "hostname"},
		{Name: "mktemp", Args: []string{"-d"}},
		{Name: "ssh-keygen", Args: []string{"-t", "ed25519", "-N", "", "-C", "root@servidor1", "-f", dir + "/id_ed25519"}},
		{Name: "sh", Args: []string{"-c", leerLlavesScript, "sh", authorizedKeys}},
		{Name: "sh", Args: []string{"-c", instalarLlavesScript, "sh", authorizedKeys, merge, sha256Hex("ssh-ed25519 AAAAEXISTENTE root@repo-vm\n")}},
		{Name: "install", Args: []string{"-m", "644", "-o", "root", "-g", "root", "--", "/tmp/10-consola-root.conf", "/etc/ssh/sshd_config.d/10-consola-root.conf"}},
		{Name: "sh", Args: []string{"-c", sshdPruebaScript, "sh", "/etc/ssh/sshd_config.d/10-consola-root.conf"}},
		{Name: "systemctl", Args: []string{"reload", "ssh"}},
		{Name: "sh", Args: []string{"-c", dropInVerificadoScript}},
		{Name: "rm", Args: []string{"-rf", "--", dir}},
		{Name: "rm", Args: []string{"-f", "--", "/tmp/10-consola-root.conf"}},
	}
	llamadas := canal.Calls()
	if len(llamadas) != len(want) {
		t.Fatalf("el flujo envió %d comandos, want %d; inesperados: %v", len(llamadas), len(want), canal.nuevos)
	}
	for i, real := range llamadas {
		if real.String() != want[i].String() {
			t.Errorf("el comando %d fue %q, want %q", i, real.String(), want[i].String())
		}
	}
	if len(canal.nuevos) != 0 {
		t.Errorf("comandos fuera del repertorio: %v", canal.nuevos)
	}

	copias := canal.Copies()
	if len(copias) != 3 {
		t.Fatalf("el canal recibió %d copias, want 3 (pub, drop-in, privada): %v", len(copias), copias)
	}
	if copias[0].ToVM || copias[0].Remote != dir+"/id_ed25519.pub" {
		t.Errorf("la primera copia no trajo la .pub de la VM: %v", copias[0])
	}
	if !copias[1].ToVM || copias[1].Remote != "/tmp/10-consola-root.conf" {
		t.Errorf("la segunda copia no subió el drop-in: %v", copias[1])
	}
	if got := canal.subidas["/tmp/10-consola-root.conf"]; got != "PermitRootLogin prohibit-password\n" {
		t.Errorf("el drop-in subido fue %q, want PermitRootLogin prohibit-password", got)
	}
	if copias[2].ToVM || copias[2].Remote != dir+"/id_ed25519" {
		t.Errorf("la tercera copia no trajo la llave privada: %v", copias[2])
	}

	privada, err := os.ReadFile(filepath.Join("keys", "descargas", token))
	if err != nil || string(privada) != privadaDeRootPrueba {
		t.Fatalf("la privada no quedó en keys/descargas/%s: datos=%q err=%v", token, privada, err)
	}

	lista := serve(t, h, httptest.NewRequest("GET", destino, nil))
	if lista.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", destino, lista.Code)
	}
	cuerpo := body(lista)
	for _, esperado := range []string{"Descargar llave privada", "funciona una sola vez", "/resultado/" + idDetalle, "/usuarios/llaves/" + token} {
		if !strings.Contains(cuerpo, esperado) {
			t.Errorf("la página de lista no muestra %q", esperado)
		}
	}
}

// Con sshd -t en rechazo (código 11) no se recarga sshd ni se verifica el
// drop-in; los temporales de la VM (directorio y copia del drop-in en /tmp) se
// borran igual y la llave privada nunca sale de la VM. El redirect va al
// /resultado del paso fallido y la explicación nombra sshd -t.
func TestGenerarLlavesRootRechazaSshdT(t *testing.T) {
	t.Chdir(t.TempDir())
	estado := newTestState()
	canal := canalLlavesDePrueba(t)
	todoLoDemas := canal.RunFunc
	canal.RunFunc = func(c executor.Command) (executor.Result, error) {
		if c.Name == "sh" && len(c.Args) > 1 && c.Args[1] == sshdPruebaScript {
			return executor.Result{ExitCode: 11}, nil
		}
		return todoLoDemas(c)
	}
	_ = estado.SetExecutor("pruebas", canal)
	h, _ := newTestHandlerWithLog(t, estado)

	rec := serve(t, h, httptest.NewRequest("POST", "/usuarios/llaves-root", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST = %d, want 303", rec.Code)
	}
	destino := rec.Header().Get("Location")
	if !strings.HasPrefix(destino, "/resultado/") {
		t.Fatalf("el rechazo debe ir al /resultado del paso fallido, no a %q", destino)
	}
	vista := serve(t, h, httptest.NewRequest("GET", destino, nil))
	if vista.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", destino, vista.Code)
	}
	if !strings.Contains(body(vista), "sshd -t") {
		t.Errorf("la explicación del rechazo no nombra sshd -t: %s", body(vista))
	}

	llamadas := canal.Calls()
	nombres := make(map[string]bool)
	for _, c := range llamadas {
		nombres[c.Name] = true
	}
	if nombres["systemctl"] {
		t.Errorf("con sshd -t en rechazo no se recarga sshd")
	}
	verifico := false
	for _, c := range llamadas {
		if c.Name == "sh" && len(c.Args) > 1 && c.Args[1] == dropInVerificadoScript {
			verifico = true
		}
	}
	if verifico {
		t.Errorf("con sshd -t en rechazo no se verifica el drop-in")
	}
	dirVM := "/tmp/consola.llaves-XXXXXX"
	if ll := len(llamadas); ll < 2 || llamadas[ll-2].String() != (executor.Command{Name: "rm", Args: []string{"-rf", "--", dirVM}}).String() || llamadas[ll-1].String() != (executor.Command{Name: "rm", Args: []string{"-f", "--", "/tmp/10-consola-root.conf"}}).String() {
		t.Errorf("los temporales de la VM deben borrarse al final; llegaron: %v", llamadas)
	}
	if len(canal.nuevos) != 0 {
		t.Errorf("comandos fuera del repertorio: %v", canal.nuevos)
	}
	if len(canal.Copies()) != 2 {
		t.Errorf("con el rechazo no se copia la llave privada a la consola; copias=%v", canal.Copies())
	}
	if resto, _ := os.ReadDir(filepath.Join("keys", "descargas")); len(resto) != 0 {
		t.Errorf("keys/descargas debe quedar vacía tras el rechazo; hay %d archivos", len(resto))
	}
}

// Sin canal SSH (Guest Additions) la respuesta es un error del módulo (422)
// con el aviso de ir primero a Conectar por SSH; no se toca ninguna VM.
func TestGenerarLlavesRootSinCanalSSH(t *testing.T) {
	estado := newTestState()
	_ = estado.SetExecutor("pruebas", &executor.Fake{NameValue: "Guest Additions"})
	rec := serve(t, newTestHandler(t, estado), httptest.NewRequest("POST", "/usuarios/llaves-root", nil))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST sin SSH = %d, want 422", rec.Code)
	}
	if !strings.Contains(body(rec), "Conectar por SSH") {
		t.Errorf("el aviso debe mandar a Conectar por SSH primero: %s", body(rec))
	}
}

// La llave privada de root no llega nunca al archivo del registro: el flujo
// corre entero (con sus copias) y ningún archivo del oplog contiene los bytes
// ni la marca de la privada. Las cuatro operaciones del flujo sí quedan.
func TestElRegistroNoGuardaLaLlavePrivada(t *testing.T) {
	t.Chdir(t.TempDir())
	estado := newTestState()
	canal := canalLlavesDePrueba(t)
	_ = estado.SetExecutor("pruebas", canal)

	dirLog := t.TempDir()
	log, err := oplog.Open(dirLog)
	if err != nil {
		t.Fatalf("oplog.Open: %v", err)
	}
	defer log.Close()
	h, err := NewHandler(Deps{Static: assets.Static, Templates: assets.Templates, State: estado, Log: log, Listen: testListen})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rec := serve(t, h, httptest.NewRequest("POST", "/usuarios/llaves-root", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /usuarios/llaves-root = %d, want 303", rec.Code)
	}

	var archivos []string
	if err := filepath.WalkDir(dirLog, func(punto string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			archivos = append(archivos, punto)
		}
		return nil
	}); err != nil {
		t.Fatalf("recorriendo el registro: %v", err)
	}
	if len(archivos) == 0 {
		t.Fatalf("el registro no dejó archivos en %s", dirLog)
	}
	for _, punto := range archivos {
		data, err := os.ReadFile(punto)
		if err != nil {
			t.Fatalf("leyendo %s: %v", punto, err)
		}
		if strings.Contains(string(data), privadaDeRootPrueba) || strings.Contains(string(data), "ROSALIA-B3-NO-ES-UNA-LLAVE") {
			t.Errorf("los bytes de la llave privada quedaron en %s", punto)
		}
	}
	nombres := map[string]bool{}
	for _, e := range log.Recent(100) {
		nombres[e.Action] = true
	}
	for _, querido := range []string{
		"Generar llaves de root",
		"Instalar la llave de root",
		"Configurar el acceso de root por SSH",
		"Borrar los temporales de las llaves de root",
	} {
		if !nombres[querido] {
			t.Errorf("el registro no guarda la operación %q", querido)
		}
	}
}

// La descarga es de una sola vez: la primera GET entrega los bytes con las
// cabeceras de archivo; la segunda falla; un token inválido falla; y la que
// superó los 15 minutos ya no se da.
func TestDescargaLlaveUnaVezYCaduca(t *testing.T) {
	t.Chdir(t.TempDir())
	h := newTestHandler(t, newTestState())

	token, err := llaves.NewDownloadToken()
	if err != nil {
		t.Fatalf("NewDownloadToken: %v", err)
	}
	ruta, err := llaves.PrepareDownload(token)
	if err != nil {
		t.Fatalf("PrepareDownload: %v", err)
	}
	if err := os.WriteFile(ruta, []byte(privadaDeRootPrueba), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rec := serve(t, h, httptest.NewRequest("GET", "/usuarios/llaves/"+token, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET de la descarga = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", got)
	}
	if disp := rec.Header().Get("Content-Disposition"); !strings.Contains(disp, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", disp)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := body(rec); got != privadaDeRootPrueba {
		t.Errorf("la descarga entregó %q", got)
	}

	if otra := serve(t, h, httptest.NewRequest("GET", "/usuarios/llaves/"+token, nil)); otra.Code != http.StatusNotFound {
		t.Errorf("la segunda descarga = %d, want 404", otra.Code)
	}
	if falso := serve(t, h, httptest.NewRequest("GET", "/usuarios/llaves/token-falso", nil)); falso.Code != http.StatusNotFound {
		t.Errorf("un token falso = %d, want 404", falso.Code)
	}
	if listoFalso := serve(t, h, httptest.NewRequest("GET", "/usuarios/llaves/listo/token-falso", nil)); listoFalso.Code != http.StatusNotFound {
		t.Errorf("la página listo con token falso = %d, want 404", listoFalso.Code)
	}

	tokenViejo, err := llaves.NewDownloadToken()
	if err != nil {
		t.Fatalf("NewDownloadToken viejo: %v", err)
	}
	rutaVieja, err := llaves.PrepareDownload(tokenViejo)
	if err != nil {
		t.Fatalf("PrepareDownload viejo: %v", err)
	}
	if err := os.WriteFile(rutaVieja, []byte("llave vieja"), 0o600); err != nil {
		t.Fatalf("WriteFile viejo: %v", err)
	}
	pasado := time.Now().Add(-16 * time.Minute)
	if err := os.Chtimes(rutaVieja, pasado, pasado); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if expirada := serve(t, h, httptest.NewRequest("GET", "/usuarios/llaves/"+tokenViejo, nil)); expirada.Code != http.StatusNotFound {
		t.Errorf("una descarga expirada = %d, want 404", expirada.Code)
	}
}

// La página "llave lista" enlaza el detalle técnico solo con un ?id= que de
// verdad esté en el registro; sin id (o con uno que no existe) no enlaza nada
// extra y el botón de descarga sigue apuntando al token.
func TestPaginaListoDetalleSoloConIdReal(t *testing.T) {
	t.Chdir(t.TempDir())
	estado := newTestState()
	h, log := newTestHandlerWithLog(t, estado)
	token, err := llaves.NewDownloadToken()
	if err != nil {
		t.Fatalf("NewDownloadToken: %v", err)
	}
	_ = token
	basica := serve(t, h, httptest.NewRequest("GET", "/usuarios/llaves/listo/"+token, nil))
	if basica.Code != http.StatusOK {
		t.Fatalf("GET listo con token real = %d", basica.Code)
	}
	ba := body(basica)
	if !strings.Contains(ba, "Descargar llave privada") || strings.Contains(ba, "/resultado/") {
		t.Errorf("sin ?id= la página no debe enlazar operaciones: %s", ba)
	}

	guardada, err := log.Add(oplog.Entry{Action: "Configurar el acceso de root por SSH"})
	if err != nil {
		t.Fatalf("log.Add: %v", err)
	}
	conId := serve(t, h, httptest.NewRequest("GET", "/usuarios/llaves/listo/"+token+"?id="+guardada.ID, nil))
	if conId.Code != http.StatusOK {
		t.Fatalf("GET listo ?id= = %d", conId.Code)
	}
	ci := body(conId)
	if !strings.Contains(ci, "/resultado/"+guardada.ID) {
		t.Errorf("con un id real la página enlaza el detalle: %s", ci)
	}
	if entendida := serve(t, h, httptest.NewRequest("GET", "/usuarios/llaves/listo/"+token+"?id=0123456789abcdef", nil)); entendida.Code == http.StatusOK && strings.Contains(body(entendida), "/resultado/0123456789abcdef") {
		t.Errorf("un id que no está en el log no debe enlazarse")
	}
}

// ---- crear usuario (B4) ----

// repertorioLlaveDeUsuario lists the exact commands the user-key story sends:
// its root counterpart with the account's names and paths.
func repertorioLlaveDeUsuario(dirLlaves, merge string) []executor.Command {
	temporal := "/tmp/consola.llaves-XXXXXX"
	dropIn := "/tmp/20-consola-daniel.conf"
	return []executor.Command{
		{Name: "hostname"},
		{Name: "mktemp", Args: []string{"-d"}},
		{Name: "ssh-keygen", Args: []string{"-t", "ed25519", "-N", "", "-C", "daniel@servidor1", "-f", temporal + "/id_ed25519"}},
		{Name: "sh", Args: []string{"-c", leerLlavesScript, "sh", dirLlaves}},
		{Name: "sh", Args: []string{"-c", instalarLlavesScript, "sh", dirLlaves, merge, sha256Hex("ssh-ed25519 AAAAEXISTENTE root@repo-vm\n")}},
		{Name: "chown", Args: []string{"-f", "daniel:daniel", "--", "/home/daniel/.ssh", dirLlaves}},
		{Name: "install", Args: []string{"-m", "644", "-o", "root", "-g", "root", "--", dropIn, "/etc/ssh/sshd_config.d/20-consola-daniel.conf"}},
		{Name: "sh", Args: []string{"-c", sshdPruebaScript, "sh", "/etc/ssh/sshd_config.d/20-consola-daniel.conf"}},
		{Name: "systemctl", Args: []string{"reload", "ssh"}},
		{Name: "sh", Args: []string{"-c", fmt.Sprintf(dropInVerificadoScriptFmt, "daniel")}},
		{Name: "rm", Args: []string{"-rf", "--", temporal}},
		{Name: "rm", Args: []string{"-f", "--", dropIn}},
	}
}

// The four checkbox combinations send exactly their repertoire: creating the
// account always goes (getent + useradd + chpasswd with the password only in
// Stdin), the sudo box adds usermod, and the llave checkbox launches the same
// guarded key story as B3, only with the account's folder, owner and drop-in,
// landing on the same one-time download page.
func TestCrearLas4CombinacionesDeCasillas(t *testing.T) {
	const (
		usuario    = "daniel"
		contrasena = "ClaveSegura-2026"
	)
	dirLlaves := "/home/daniel/.ssh/authorized_keys"
	pubDePrueba := strings.TrimSpace(pubDeRootPrueba)
	merge := llaves.MergeAuthorizedKeys("ssh-ed25519 AAAAEXISTENTE root@repo-vm\n", pubDePrueba)

	base := []executor.Command{
		{Name: "getent", Args: []string{"passwd", usuario}},
		{Name: "useradd", Args: []string{"-m", "-s", "/bin/bash", "--", usuario}},
		{Name: "chpasswd", Stdin: usuario + ":" + contrasena},
	}
	conSudo := append(append([]executor.Command{}, base...),
		executor.Command{Name: "usermod", Args: []string{"-aG", "sudo", "--", usuario}})
	casos := []struct {
		nombre string
		sudo   bool
		llave  bool
		want   []executor.Command
	}{
		{"sin casillas", false, false, base},
		{"solo sudo", true, false, conSudo},
		{"solo llave", false, true, append(append([]executor.Command{}, base...), repertorioLlaveDeUsuario(dirLlaves, merge)...)},
		{"todo", true, true, append(conSudo, repertorioLlaveDeUsuario(dirLlaves, merge)...)},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Chdir(t.TempDir())
			estado := newTestState()
			canal := canalCrearDePrueba(t, 2)
			_ = estado.SetExecutor("pruebas", canal)
			h, _ := newTestHandlerWithLog(t, estado)

			forma := url.Values{"usuario": {usuario}, "contrasena": {contrasena}}
			if caso.sudo {
				forma.Set("sudo", "on")
			}
			if caso.llave {
				forma.Set("llave", "on")
			}
			rec := serve(t, h, postForm("/usuarios/crear", forma))
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("POST /usuarios/crear = %d, want 303 (%s)", rec.Code, rec.Body.String())
			}
			destino := rec.Header().Get("Location")

			llamadas := canal.Calls()
			if len(llamadas) != len(caso.want) {
				t.Fatalf("el flujo envió %d comandos, want %d; inesperados: %v", len(llamadas), len(caso.want), canal.nuevos)
			}
			for i, real := range llamadas {
				if real.String() != caso.want[i].String() {
					t.Errorf("el comando %d fue %q, want %q", i, real.String(), caso.want[i].String())
				}
				if strings.Contains(real.String(), contrasena) {
					t.Errorf("la contraseña aparece en la línea del comando %d: %s", i, real.String())
				}
			}
			if len(canal.nuevos) != 0 {
				t.Errorf("comandos fuera del repertorio: %v", canal.nuevos)
			}

			if caso.llave {
				u, err := url.Parse(destino)
				if err != nil || !strings.HasPrefix(destino, "/usuarios/llaves/listo/") {
					t.Fatalf("Location %q, want /usuarios/llaves/listo/<token>?id=…", destino)
				}
				token := strings.TrimPrefix(u.Path, "/usuarios/llaves/listo/")
				if len(token) != 32 {
					t.Errorf("el token %q no mide 32 caracteres", token)
				}
				if _, ok := u.Query()["id"]; !ok {
					t.Errorf("el redirect no trae ?id= (el detalle técnico): %q", destino)
				}
				copias := canal.Copies()
				if len(copias) != 3 {
					t.Fatalf("%s: el canal recibió %d copias, want 3 (pub, drop-in, privada): %v", caso.nombre, len(copias), copias)
				}
				if copias[0].ToVM || copias[0].Remote != "/tmp/consola.llaves-XXXXXX/id_ed25519.pub" {
					t.Errorf("%s: la primera copia no trajo la .pub: %v", caso.nombre, copias[0])
				}
				if !copias[1].ToVM || copias[1].Remote != "/tmp/20-consola-daniel.conf" {
					t.Errorf("%s: la segunda copia no subió el drop-in del usuario: %v", caso.nombre, copias[1])
				}
				if got := canal.subidas["/tmp/20-consola-daniel.conf"]; got != llaves.ContenidoDropInUsuario(usuario) {
					t.Errorf("%s: el drop-in subido fue %q, want %q", caso.nombre, got, llaves.ContenidoDropInUsuario(usuario))
				}
				if copias[2].ToVM || copias[2].Remote != "/tmp/consola.llaves-XXXXXX/id_ed25519" {
					t.Errorf("%s: la tercera copia no trajo la llave privada: %v", caso.nombre, copias[2])
				}
				pagina := serve(t, h, httptest.NewRequest("GET", destino, nil))
				if !strings.Contains(pagina.Body.String(), "/usuarios/llaves/"+token) {
					t.Errorf("%s: la página de la llave no ofrece la descarga del token", caso.nombre)
				}
			} else if !strings.HasPrefix(destino, "/resultado/") {
				t.Errorf("%s: Location %q, want /resultado/…", caso.nombre, destino)
			}
		})
	}
}

// A getent that answers 0 ("the name is taken") stops before touching the
// account: the form comes back 422 with the message next to the field and
// useradd never runs.
func TestCrearRechazaUsuarioExistente(t *testing.T) {
	t.Chdir(t.TempDir())
	estado := newTestState()
	canal := canalCrearDePrueba(t, 0)
	_ = estado.SetExecutor("pruebas", canal)
	h, _ := newTestHandlerWithLog(t, estado)

	rec := serve(t, h, postForm("/usuarios/crear", url.Values{
		"usuario": {"daniel"}, "contrasena": {"ClaveSegura-2026"},
	}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /usuarios/crear = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Ya existe un usuario con ese nombre") {
		t.Errorf("la página no explica por el campo: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `for="usuario"`) {
		t.Errorf("el mensaje no está ligado al campo usuario")
	}
	llamadas := canal.Calls()
	if len(llamadas) != 1 || llamadas[0].Name != "getent" {
		t.Fatalf("el flujo llamó %v, want solo getent", llamadas)
	}
}

// The local validation answers before touching the VM: invalid user name and
// too-short password each render 422 with their field message and send zero
// commands.
func TestCrearValidaLocalmente(t *testing.T) {
	t.Chdir(t.TempDir())
	estado := newTestState()
	canal := canalCrearDePrueba(t, 2)
	_ = estado.SetExecutor("pruebas", canal)
	h, _ := newTestHandlerWithLog(t, estado)

	rec := serve(t, h, postForm("/usuarios/crear", url.Values{
		"usuario": {"Root-!"}, "contrasena": {"ClaveSegura-2026"},
	}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST con nombre invalido = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "El nombre de usuario empieza con una minúscula") {
		t.Errorf("la página no muestra el mensaje del nombre: %s", rec.Body.String())
	}
	if lleno := canal.Calls(); len(lleno) != 0 {
		t.Errorf("la validación local no debió tocar la VM: %v", lleno)
	}
	rec = serve(t, h, postForm("/usuarios/crear", url.Values{
		"usuario": {"daniel"}, "contrasena": {"corta"},
	}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST con contraseña corta = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "entre 8 y 128 caracteres") {
		t.Errorf("la página no muestra el mensaje de la contraseña: %s", rec.Body.String())
	}
}

// The password travels only in chpasswd's Stdin: it does not appear in any
// command line and not in one byte of the operation log directory (DB12).
func TestCrearLaContraseñaViajaSoloEnStdin(t *testing.T) {
	t.Chdir(t.TempDir())
	estado := newTestState()
	canal := canalCrearDePrueba(t, 2)
	_ = estado.SetExecutor("pruebas", canal)
	const contrasena = "ClaveSegura-2026"

	dirLog := t.TempDir()
	log, err := oplog.Open(dirLog)
	if err != nil {
		t.Fatalf("oplog.Open: %v", err)
	}
	defer log.Close()
	h, err := NewHandler(Deps{Static: assets.Static, Templates: assets.Templates, State: estado, Log: log, Listen: testListen})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rec := serve(t, h, postForm("/usuarios/crear", url.Values{
		"usuario": {"daniel"}, "contrasena": {contrasena}, "sudo": {"on"},
	}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /usuarios/crear = %d, want 303", rec.Code)
	}
	for i, c := range canal.Calls() {
		if strings.Contains(c.String(), contrasena) {
			t.Errorf("la contraseña aparece en la línea del comando %d: %s", i, c.String())
		}
		if c.Name == "chpasswd" && c.Stdin != "daniel:"+contrasena {
			t.Errorf("chpasswd.Stdin = %q", c.Stdin)
		}
	}

	var archivos []string
	if err := filepath.WalkDir(dirLog, func(punto string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			archivos = append(archivos, punto)
		}
		return nil
	}); err != nil {
		t.Fatalf("recorriendo el registro: %v", err)
	}
	if len(archivos) == 0 {
		t.Fatalf("el registro no dejó archivos en %s", dirLog)
	}
	for _, punto := range archivos {
		data, err := os.ReadFile(punto)
		if err != nil {
			t.Fatalf("leyendo %s: %v", punto, err)
		}
		if strings.Contains(string(data), contrasena) {
			t.Errorf("la contraseña quedó en %s", punto)
		}
	}
}

// passwdDePrueba is what getent passwd prints on a VM with system accounts, two
// normal accounts and a malformed line. It exercises DB14's filter.
const passwdDePrueba = `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
nobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin
daniel:x:1000:1000:Daniel:/home/daniel:/bin/bash
viejo:x:1001:1001::/home/viejo:/bin/bash
raro:x:65000:65000::/home/raro:/bin/bash
sistema:x:999:999::/home/sistema:/bin/sh
basura sin formato
`

// canalEliminarDePrueba answers the page's read-only getent passwd with the
// fixture and accepts the deletion plan's commands, recording every call.
func canalEliminarDePrueba(listado string) *executor.Fake {
	canal := &executor.Fake{NameValue: "SSH"}
	canal.RunFunc = func(c executor.Command) (executor.Result, error) {
		if c.Name == "getent" && len(c.Args) == 1 && c.Args[0] == "passwd" {
			return executor.Result{Stdout: listado}, nil
		}
		return executor.Result{}, nil
	}
	return canal
}

// La página lista solo las cuentas que DB14 permite borrar (UID 1000 a 59999,
// sin nobody ni root) y lee la lista con un único getent passwd: el GET no
// ejecuta ninguna operación destructiva.
func TestPaginaEliminarListaFiltrada(t *testing.T) {
	estado := newTestState()
	canal := canalEliminarDePrueba(passwdDePrueba)
	_ = estado.SetExecutor("pruebas", canal)
	rec := serve(t, newTestHandler(t, estado), httptest.NewRequest("GET", "/usuarios", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /usuarios = %d", rec.Code)
	}
	cuerpo := rec.Body.String()
	for _, quiero := range []string{`<option value="daniel">`, `<option value="viejo">`, "Eliminar todos los archivos", `name="confirmar"`} {
		if !strings.Contains(cuerpo, quiero) {
			t.Errorf("la página no muestra %q", quiero)
		}
	}
	for _, noQuiero := range []string{`value="root"`, `value="daemon"`, `value="nobody"`, `value="raro"`, `value="sistema"`} {
		if strings.Contains(cuerpo, noQuiero) {
			t.Errorf("la página ofrece una cuenta protegida o fuera de rango: %q", noQuiero)
		}
	}
	llamadas := canal.Calls()
	if len(llamadas) != 1 || llamadas[0].String() != "getent passwd" {
		t.Errorf("el GET envió %v; solo debía leer getent passwd", llamadas)
	}
}

// El usuario de ssh del destino es la vía por la que la consola administra la
// VM: nunca puede aparecer en la lista de cuentas a eliminar.
func TestPaginaEliminarProtegeLaCuentaDeLaConsola(t *testing.T) {
	estado := app.NewState(app.Config{Listen: app.DefaultListen, Targets: []app.Target{
		{Name: "pruebas", VM: "consola-pruebas", IP: "192.168.40.60", SSHUser: "daniel"},
	}})
	canal := canalEliminarDePrueba(passwdDePrueba)
	_ = estado.SetExecutor("pruebas", canal)
	rec := serve(t, newTestHandler(t, estado), httptest.NewRequest("GET", "/usuarios", nil))
	cuerpo := rec.Body.String()
	if strings.Contains(cuerpo, `<option value="daniel">`) {
		t.Error("la lista ofrece la cuenta ssh de la consola (daniel)")
	}
	if !strings.Contains(cuerpo, `<option value="viejo">`) {
		t.Error("la lista no ofrece la cuenta normal viejo")
	}
}

// Sin canal no hay lista que mostrar: la página lo explica en lugar del campo.
func TestPaginaEliminarSinCanalAvisa(t *testing.T) {
	rec := serve(t, newTestHandler(t, newTestState()), httptest.NewRequest("GET", "/usuarios", nil))
	cuerpo := rec.Body.String()
	if !strings.Contains(cuerpo, "todavía no tiene canal con la VM") {
		t.Error("la página no explica que falta el canal")
	}
	if strings.Contains(cuerpo, `name="confirmar"`) {
		t.Error("la página ofrece el desplegable sin canal")
	}
}

// El primer envío NO elimina: pide confirmación en una segunda pantalla con
// el nombre elegido y el estado de la casilla, y no manda ningún comando más
// allá de la lectura de la lista.
func TestConfirmacionPideSegundaPantallaSinEliminar(t *testing.T) {
	estado := newTestState()
	canal := canalEliminarDePrueba(passwdDePrueba)
	_ = estado.SetExecutor("pruebas", canal)
	rec := serve(t, newTestHandler(t, estado), httptest.NewRequest("GET", "/usuarios?confirmar=viejo&archivos=on", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET de confirmación = %d", rec.Code)
	}
	cuerpo := rec.Body.String()
	for _, quiero := range []string{"¿Eliminar a", "viejo", "Esta acción no se puede deshacer", "/home/viejo", `name="usuario" value="viejo"`, `name="archivos" value="on"`, "Cancelar"} {
		if !strings.Contains(cuerpo, quiero) {
			t.Errorf("la confirmación no muestra %q", quiero)
		}
	}
	for _, c := range canal.Calls() {
		if c.Name == "userdel" || c.Name == "rm" || c.Name == "systemctl" {
			t.Errorf("la confirmación ya ejecutó un comando destructivo: %s", c.String())
		}
	}
}

// Una petición manipulada con un nombre protegido no llega a la pantalla de
// confirmación: la página vuelve al formulario.
func TestConfirmacionIgnoraCuentasProtegidas(t *testing.T) {
	estado := newTestState()
	_ = estado.SetExecutor("pruebas", canalEliminarDePrueba(passwdDePrueba))
	rec := serve(t, newTestHandler(t, estado), httptest.NewRequest("GET", "/usuarios?confirmar=root", nil))
	cuerpo := rec.Body.String()
	if strings.Contains(cuerpo, "¿Eliminar a") {
		t.Error("la confirmación aceptó root")
	}
	if !strings.Contains(cuerpo, `name="confirmar"`) {
		t.Error("la página no volvió al formulario")
	}
}

// El POST ejecuta el plan exacto de B5: userdel con o sin -r, el borrado del
// drop-in del usuario, la compuerta sshd -t y la recarga del servicio.
func TestEliminarConYSinArchivos(t *testing.T) {
	casos := []struct {
		nombre   string
		archivos bool
	}{
		{"sin archivos", false},
		{"con archivos", true},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			estado := newTestState()
			canal := canalEliminarDePrueba(passwdDePrueba)
			_ = estado.SetExecutor("pruebas", canal)
			h, _ := newTestHandlerWithLog(t, estado)

			forma := url.Values{"usuario": {"viejo"}}
			if caso.archivos {
				forma.Set("archivos", "on")
			}
			rec := serve(t, h, postForm("/usuarios/eliminar", forma))
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("POST /usuarios/eliminar = %d, want 303 (%s)", rec.Code, rec.Body.String())
			}
			if !strings.HasPrefix(rec.Header().Get("Location"), "/resultado/") {
				t.Errorf("Location = %q, want /resultado/…", rec.Header().Get("Location"))
			}

			want := usuarios.PlanEliminar("pruebas", "viejo", caso.archivos).Commands
			llamadas := canal.Calls()
			if len(llamadas) != len(want) {
				t.Fatalf("el flujo envió %d comandos, want %d: %v", len(llamadas), len(want), llamadas)
			}
			for i, real := range llamadas {
				if real.String() != want[i].String() {
					t.Errorf("el comando %d fue %q, want %q", i, real.String(), want[i].String())
				}
			}
		})
	}
}

// root, nobody y la cuenta ssh de la consola se rechazan junto al campo y sin
// enviar NINGÚN comando a la VM (DB14).
func TestEliminarRechazaCuentasProtegidas(t *testing.T) {
	for _, protegida := range []string{"root", "nobody"} {
		t.Run(protegida, func(t *testing.T) {
			estado := newTestState()
			canal := canalEliminarDePrueba(passwdDePrueba)
			_ = estado.SetExecutor("pruebas", canal)
			rec := serve(t, newTestHandler(t, estado), postForm("/usuarios/eliminar", url.Values{"usuario": {protegida}}))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("POST = %d, want 422", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "No se puede eliminar la cuenta") {
				t.Error("el aviso no explica el rechazo")
			}
			if llamadas := canal.Calls(); len(llamadas) != 0 {
				t.Errorf("se enviaron comandos: %v", llamadas)
			}
		})
	}
}

// También se rechaza el usuario ssh del destino, aunque su UID sea normal.
func TestEliminarRechazaElUsuarioSSH(t *testing.T) {
	estado := app.NewState(app.Config{Listen: app.DefaultListen, Targets: []app.Target{
		{Name: "pruebas", VM: "consola-pruebas", IP: "192.168.40.60", SSHUser: "daniel"},
	}})
	canal := canalEliminarDePrueba(passwdDePrueba)
	_ = estado.SetExecutor("pruebas", canal)
	rec := serve(t, newTestHandler(t, estado), postForm("/usuarios/eliminar", url.Values{"usuario": {"daniel"}}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST = %d, want 422", rec.Code)
	}
	if llamadas := canal.Calls(); len(llamadas) != 0 {
		t.Errorf("se enviaron comandos: %v", llamadas)
	}
}

// Sin SSH no se elimina nada: el canal de Guest Additions no basta.
func TestEliminarRechazaSinSSH(t *testing.T) {
	estado := newTestState()
	canal := &executor.Fake{NameValue: "Guest Additions"}
	_ = estado.SetExecutor("pruebas", canal)
	rec := serve(t, newTestHandler(t, estado), postForm("/usuarios/eliminar", url.Values{"usuario": {"viejo"}}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "primero usa «Conectar por SSH»") {
		t.Error("el aviso no pide conectar por SSH")
	}
	if llamadas := canal.Calls(); len(llamadas) != 0 {
		t.Errorf("se enviaron comandos: %v", llamadas)
	}
}

// Un nombre inválido se rechaza en Go, antes de tocar la VM.
func TestEliminarRechazaNombreInvalido(t *testing.T) {
	estado := newTestState()
	canal := canalEliminarDePrueba(passwdDePrueba)
	_ = estado.SetExecutor("pruebas", canal)
	rec := serve(t, newTestHandler(t, estado), postForm("/usuarios/eliminar", url.Values{"usuario": {"Root!"}}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST = %d, want 422", rec.Code)
	}
	if llamadas := canal.Calls(); len(llamadas) != 0 {
		t.Errorf("se enviaron comandos: %v", llamadas)
	}
}

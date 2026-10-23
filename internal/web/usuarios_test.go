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
	subidas map[string]string // ruta remota -> contenido que recibió (CopyTo)
	nuevos  []string          // comandos fuera del repertorio (debe quedar vacío)
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

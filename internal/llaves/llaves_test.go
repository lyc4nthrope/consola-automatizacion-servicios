package llaves

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
)

const unaLlave = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIConsolaConsolaConsolaConsolaConsolaCons la-consola@host"

func TestMergeAuthorizedKeysConservaYAnnade(t *testing.T) {
	cases := []struct {
		name       string
		existentes string
		nueva      string
		resultado  string
	}{
		{"archivo vacío con la nueva", "", unaLlave, unaLlave + "\n"},
		{"archivo sin salto final se completa", "ssh-ed25519 AAAOld otro-root@vm", unaLlave,
			"ssh-ed25519 AAAOld otro-root@vm\n" + unaLlave + "\n"},
		{"comentarios y líneas en blanco se conservan", "# mis llaves\n\nssh-ed25519 AAAOld root@vm\n", unaLlave,
			"# mis llaves\n\nssh-ed25519 AAAOld root@vm\n" + unaLlave + "\n"},
		{"la misma llave no se repite", unaLlave + "\n", unaLlave, unaLlave + "\n"},
		{"el mismo material con otro comentario no se repite",
			unaLlave + "\n", "ssh-ed25519 " + strings.Fields(unaLlave)[1] + " otro-comentario", unaLlave + "\n"},
		{"sin nada nuevo quedó tal cual", unaLlave + "\n \n", unaLlave, unaLlave + "\n \n"},
	}
	for _, c := range cases {
		if out := MergeAuthorizedKeys(c.existentes, c.nueva); out != c.resultado {
			t.Errorf("%s: = %q, want %q", c.name, out, c.resultado)
		}
	}
}

func TestMergeAuthorizedKeysNoPierdeAutoridad(t *testing.T) {
	const autoridad = "AAAAAUTORIDAD"
	existentes := "ssh-ed25519 " + autoridad + " administrador"
	out := MergeAuthorizedKeys(existentes, unaLlave)
	if !strings.Contains(out, autoridad) {
		t.Errorf("la llave ya presente se perdió: %q", out)
	}
	if got, want := strings.Count(out, unaLlave), 1; got != want {
		t.Errorf("la llave nueva queda %d veces, want %d", got, want)
	}
}

// reemplazaKeygen sustituye al generador real por un doble de prueba; devuelve
// el contador de llamadas y restaura el original al terminar el caso.
func reemplazaKeygen(t *testing.T, dbl func(context.Context, []string) error) *atomic.Int32 {
	t.Helper()
	var llamadas atomic.Int32
	antes := keygen
	keygen = func(ctx context.Context, args []string) error {
		llamadas.Add(1)
		return dbl(ctx, args)
	}
	t.Cleanup(func() { keygen = antes })
	return &llamadas
}

// keygenQueCrea genera (en el doble) la parte pública que dejaría el ssh-keygen real.
func keygenQueCrea(pub string) func(context.Context, []string) error {
	return func(_ context.Context, args []string) error {
		for i, a := range args { // los argumentos están en pares y terminan en "-f", <ruta>
			if a == "-f" {
				return os.WriteFile(args[i+1]+".pub", []byte(pub), 0o644)
			}
		}
		return nil
	}
}

func TestEnsureConsolaKeyReutilizaLaExistente(t *testing.T) {
	dir := t.TempDir()
	ruta := filepath.Join(dir, "consola_ed25519")
	if err := os.WriteFile(ruta+".pub", []byte(unaLlave+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	llamadas := reemplazaKeygen(t, func(context.Context, []string) error {
		return fmt.Errorf("no se debe llamar a ssh-keygen con una llave ya creada")
	})
	priv, err := EnsureConsolaKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if priv != ruta {
		t.Errorf("EnsureConsolaKey devolvió %q, want %q", priv, ruta)
	}
	if llamadas.Load() != 0 {
		t.Errorf("una llave existente se volvió a generar (%d veces)", llamadas.Load())
	}
}

func TestEnsureConsolaKeyLaGeneraLaPrimeraVez(t *testing.T) {
	dir := t.TempDir()
	llamadas := reemplazaKeygen(t, keygenQueCrea(unaLlave+"\n"))
	priv, err := EnsureConsolaKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if priv != filepath.Join(dir, "consola_ed25519") {
		t.Errorf("ruta devuelta %q", priv)
	}
	if llamadas.Load() != 1 {
		t.Errorf("ssh-keygen se llamó %d veces en el primer uso", llamadas.Load())
	}
}

func TestEnsureConsolaKeyCreaLaCarpeta(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	llamadas := reemplazaKeygen(t, keygenQueCrea(unaLlave+"\n"))
	if priv, err := EnsureConsolaKey(dir); err != nil {
		t.Fatalf("una carpeta que no existe no debería impedir generar: %v", err)
	} else if priv != filepath.Join(dir, "consola_ed25519") {
		t.Errorf("ruta devuelta %q", priv)
	}
	if llamadas.Load() != 1 {
		t.Errorf("ssh-keygen se llamó %d veces con una carpeta inexistente", llamadas.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, "consola_ed25519.pub")); err != nil {
		t.Errorf("la parte pública no quedó donde el generador la deja: %v", err)
	}
}

func TestEnsureConsolaKeyFallaSiElGeneradorNoCrea(t *testing.T) {
	dir := t.TempDir()
	llamadas := reemplazaKeygen(t, func(context.Context, []string) error { return nil })
	if _, err := EnsureConsolaKey(dir); err == nil {
		t.Fatal("ssh-keygen terminó sin crear la llave y EnsureConsolaKey no se enteró")
	}
	if llamadas.Load() != 1 {
		t.Errorf("ssh-keygen se llamó %d veces", llamadas.Load())
	}
}

func TestEnsureConsolaKeyRechazaPrivadaSinPublica(t *testing.T) {
	dir := t.TempDir()
	ruta := filepath.Join(dir, "consola_ed25519")
	if err := os.WriteFile(ruta, []byte("llave privada huérfana"), 0o600); err != nil {
		t.Fatal(err)
	}
	llamadas := reemplazaKeygen(t, keygenQueCrea(unaLlave+"\n"))
	if _, err := EnsureConsolaKey(dir); err == nil {
		t.Fatal("una llave privada sin su pública pasó como si nada")
	}
	if llamadas.Load() != 0 {
		t.Errorf("se intentó regenerar sobre la existente %d veces", llamadas.Load())
	}
}

func TestEnsureConsolaKeyConcurrenteGeneraUnaVez(t *testing.T) {
	dir := t.TempDir()
	llamadas := reemplazaKeygen(t, keygenQueCrea(unaLlave+"\n"))
	var esperan sync.WaitGroup
	rutas := make([]string, 6)
	errores := make([]error, 6)
	for i := range rutas {
		esperan.Add(1)
		go func(i int) {
			defer esperan.Done()
			rutas[i], errores[i] = EnsureConsolaKey(dir)
		}(i)
	}
	esperan.Wait()
	for i := range rutas {
		if errores[i] != nil {
			t.Fatalf("llamada %d: %v", i, errores[i])
		}
		if rutas[i] != rutas[0] {
			t.Errorf("llamada %d devolvió otra ruta", i)
		}
	}
	if llamadas.Load() != 1 {
		t.Errorf("las pruebas simultáneas generaron la llave %d veces", llamadas.Load())
	}
}

func TestPublicKeyDevuelveLaPublica(t *testing.T) {
	dir := t.TempDir()
	reemplazaKeygen(t, keygenQueCrea(unaLlave+"\n"))
	pub, err := PublicKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pub != unaLlave {
		t.Errorf("PublicKey = %q, want %q", pub, unaLlave)
	}
}

func TestSSHForTargetFijaElCanal(t *testing.T) {
	dir := "keys-de-prueba"
	ex := SSHForTarget(app.Target{Name: "pruebas", VM: "consola-pruebas", IP: "192.168.40.60", SSHUser: "root"}, dir)
	if ex.Name() != "SSH" {
		t.Errorf("SSHForTarget devolvió un canal %q", ex.Name())
	}
	if ex.User != "root" || ex.Host != "192.168.40.60" {
		t.Errorf("usuario/host: %q@%q", ex.User, ex.Host)
	}
	if ex.KeyPath != filepath.Join(dir, "consola_ed25519") {
		t.Errorf("llave %q", ex.KeyPath)
	}
	if ex.KnownHosts != filepath.Join(dir, "known_hosts") {
		t.Errorf("known_hosts %q", ex.KnownHosts)
	}
	if ex.Port != 22 {
		t.Errorf("puerto %d, want 22 (la configuración no tiene campo de puerto)", ex.Port)
	}
}

func TestProveSSHUsaHostnameYExigeSalida(t *testing.T) {
	exito := &executor.Fake{RunFunc: func(executor.Command) (executor.Result, error) {
		return executor.Result{Stdout: "servidor1\n"}, nil
	}}
	if err := ProveSSH(exito); err != nil {
		t.Errorf("una VM que responde no debería fallar: %v", err)
	}
	if got := len(exito.Calls()); got != 1 {
		t.Fatalf("ProveSSH envió %d comandos", got)
	}
	if exito.Calls()[0].String() != "hostname" {
		t.Errorf("la prueba corrió %q, want hostname sin argumentos", exito.Calls()[0].String())
	}

	fallo := &executor.Fake{RunFunc: func(executor.Command) (executor.Result, error) {
		return executor.Result{ExitCode: 7}, nil
	}}
	if err := ProveSSH(fallo); err == nil || !strings.Contains(err.Error(), "7") {
		t.Errorf("hostname con código 7: err=%v (debe nombrar el código)", err)
	}

	rechaza := &executor.Fake{RunFunc: func(executor.Command) (executor.Result, error) {
		return executor.Result{}, fmt.Errorf("conexión rechazada")
	}}
	if err := ProveSSH(rechaza); err == nil || !strings.Contains(err.Error(), "conexión rechazada") {
		t.Errorf("un rechazo de red debe llegar completo: err=%v", err)
	}
}

func TestContenidoDropInRootExacto(t *testing.T) {
	if got := NombreDropInRoot; got != "10-consola-root.conf" {
		t.Errorf("NombreDropInRoot = %q, want 10-consola-root.conf", got)
	}
	want := "PermitRootLogin prohibit-password\n"
	if got := ContenidoDropInRoot(); got != want {
		t.Errorf("ContenidoDropInRoot() = %q, want %q", got, want)
	}
}

func TestNuevoTokenDescargaEsHexDe128Bits(t *testing.T) {
	primero, err := NewDownloadToken()
	if err != nil {
		t.Fatalf("NewDownloadToken: %v", err)
	}
	if len(primero) != 32 {
		t.Errorf("el token %q mide %d, want 32 carácteres hex", primero, len(primero))
	}
	for _, letra := range primero {
		if !strings.ContainsRune("0123456789abcdef", letra) {
			t.Fatalf("el token %q tiene caracteres fuera de 0-9a-f", primero)
		}
	}
	segundo, err := NewDownloadToken()
	if err != nil {
		t.Fatalf("NewDownloadToken dos veces: %v", err)
	}
	if segundo == primero {
		t.Errorf("dos tokens deben ser distintos: %q == %q", primero, segundo)
	}
}

func TestDownloadDirYPath(t *testing.T) {
	if got := DownloadDir(); got != filepath.Join("keys", "descargas") {
		t.Errorf("DownloadDir() = %q, want %q", got, filepath.Join("keys", "descargas"))
	}
	bueno := strings.Repeat("a", 32)
	want := filepath.Join("keys", "descargas", bueno)
	got, err := DownloadPath(bueno)
	if err != nil || got != want {
		t.Errorf("DownloadPath(%q) = %q, %v; want %q sin error", bueno, got, err, want)
	}
	for _, mal := range []string{
		"", "abc", strings.Repeat("A", 32), strings.Repeat("0", 31), strings.Repeat("0", 33),
		strings.Repeat("0", 32) + "\n", "../../llave", "keys/descargas",
	} {
		if got, err := DownloadPath(mal); err == nil || got != "" {
			t.Errorf("DownloadPath(%q)=%q (err=%v); debe ser rechazado (path vacío y error)", mal, got, err)
		}
	}
}

func TestPrepareDownloadPreparaYReadDeleteDownloadEntregaUnaVez(t *testing.T) {
	t.Chdir(t.TempDir())
	token, err := NewDownloadToken()
	if err != nil {
		t.Fatalf("NewDownloadToken: %v", err)
	}
	ruta, err := PrepareDownload(token)
	if err != nil {
		t.Fatalf("PrepareDownload: %v", err)
	}
	if ruta != filepath.Join("keys", "descargas", token) {
		t.Errorf("la descarga vive en %q, want keys/descargas/%s", ruta, token)
	}
	privada := "-----BEGIN OPENSSH PRIVATE KEY-----\nllave nueva\n-----END OPENSSH PRIVATE KEY-----\n"
	if err := os.WriteFile(ruta, []byte(privada), 0o600); err != nil {
		t.Fatalf("WriteFile de prueba: %v", err)
	}
	datos, err := ReadDeleteDownload(token)
	if err != nil || string(datos) != privada {
		t.Fatalf("la primera lectura del token=%q: datos=%q, err=%v", token, datos, err)
	}
	if _, err := ReadDeleteDownload(token); err == nil || !strings.Contains(err.Error(), "no está disponible") {
		t.Errorf("la segunda descarga debe fallar con 'no está disponible' (err=%v)", err)
	}
	if _, err := os.Stat(ruta); !os.IsNotExist(err) {
		t.Errorf("después de descargar, la llave debe estar borrada; stat err=%v", err)
	}
}

func TestReadDeleteDownloadRechazaTokenFalso(t *testing.T) {
	if _, err := ReadDeleteDownload("token-falso"); err == nil {
		t.Errorf("un token falso debe rechazarse")
	}
}

func TestReadDeleteDownloadCaduca(t *testing.T) {
	t.Chdir(t.TempDir())
	token, err := NewDownloadToken()
	if err != nil {
		t.Fatalf("NewDownloadToken: %v", err)
	}
	ruta, err := PrepareDownload(token)
	if err != nil {
		t.Fatalf("PrepareDownload: %v", err)
	}
	if err := os.WriteFile(ruta, []byte("llave"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	viejo := time.Now().Add(-16 * time.Minute)
	if err := os.Chtimes(ruta, viejo, viejo); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if _, err := ReadDeleteDownload(token); err == nil || !strings.Contains(err.Error(), "caducó") {
		t.Errorf("una descarga vieja debe caducar con el mensaje 'caducó' (err=%v)", err)
	}
	if _, err := os.Stat(ruta); !os.IsNotExist(err) {
		t.Errorf("la descarga caducada se debe borrar; stat err=%v", err)
	}
}

// El drop-in de un usuario apaga el acceso por contraseña SOLO para ese
// usuario (DB6): Match User + las dos directivas en "no", una por línea.
func TestNombreYContenidoDropInUsuario(t *testing.T) {
	nombre := NombreDropInUsuario("daniel")
	if nombre != "20-consola-daniel.conf" {
		t.Errorf("NombreDropInUsuario(daniel) = %q, want 20-consola-daniel.conf", nombre)
	}
	otro := NombreDropInUsuario("otro.sistema") // el validador no admite "." en usuarios, pero Q igual no debe romper
	if otro != "20-consola-otro.sistema.conf" {
		t.Errorf("NombreDropInUsuario con punto: %q", otro)
	}
	contenido := ContenidoDropInUsuario("daniel")
	want := "Match User daniel\nPasswordAuthentication no\nKbdInteractiveAuthentication no\n"
	if contenido != want {
		t.Errorf("ContenidoDropInUsuario(daniel) = %q, want %q", contenido, want)
	}
}

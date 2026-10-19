package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
)

func TestNewServerLimitsSlowConnections(t *testing.T) {
	srv := newServer("127.0.0.1:8090", http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("timeouts must be set: header=%v read=%v idle=%v", srv.ReadHeaderTimeout, srv.ReadTimeout, srv.IdleTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %v: long operations would be cut off", srv.WriteTimeout)
	}
	if srv.Addr != "127.0.0.1:8090" {
		t.Fatalf("Addr = %q", srv.Addr)
	}
}

func TestNewStateSimulatedGivesEveryTargetAChannel(t *testing.T) {
	cfg := app.Config{Listen: app.DefaultListen, Targets: []app.Target{{Name: "a", VM: "a", IP: "10.0.0.1"}, {Name: "b", VM: "b", IP: "10.0.0.2"}}}
	for _, name := range []string{"a", "b"} {
		if got := newState(cfg, true).Channel(name); got != "Simulado" {
			t.Errorf("simulated: Channel(%s) = %q, want Simulado", name, got)
		}
		if got := newState(cfg, false).Channel(name); got != "Sin conexión" {
			t.Errorf("normal: Channel(%s) = %q, want Sin conexión", name, got)
		}
	}
}

func TestGuestChannelUsesThePrivatePasswordFile(t *testing.T) {
	t.Chdir(t.TempDir())
	keys := "keys" // relative, as in production: VBoxManage must still get an absolute path
	if err := os.Mkdir(keys, 0o700); err != nil {
		t.Fatal(err)
	}
	tg := app.Target{Name: "pruebas", VM: "consola-pruebas", IP: "10.0.0.1"}
	if _, _, ok := guestChannel(tg, keys); ok {
		t.Fatal("without a password file there is no Guest Additions channel")
	}
	general := filepath.Join(keys, "guest-pass")
	if err := os.WriteFile(general, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ex, file, ok := guestChannel(tg, keys)
	gc, isGC := ex.(*executor.GuestControl)
	general, _ = filepath.Rel(".", general)
	if !ok || !isGC || file != general || gc.VM != "consola-pruebas" || gc.User != "root" || !filepath.IsAbs(gc.PasswordFile) {
		t.Fatalf("guestChannel = %#v, %q, %v", ex, file, ok)
	}
	perTarget := filepath.Join(keys, "guest-pass-pruebas")
	if err := os.WriteFile(perTarget, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, file, _ := guestChannel(tg, keys); file != perTarget {
		t.Fatalf("a per-target file must win over the general one, got %q", file)
	}
	tg.GuestUser = "admin"
	if ex, _, _ := guestChannel(tg, keys); ex.(*executor.GuestControl).User != "admin" {
		t.Fatal("guest_user from config.json must be used")
	}
}

func TestNewStateActivatesGuestAdditionsOnlyWhereConfigured(t *testing.T) {
	keys := t.TempDir()
	_ = os.WriteFile(filepath.Join(keys, "guest-pass-a"), []byte("x"), 0o600)
	cfg := app.Config{Listen: app.DefaultListen, Targets: []app.Target{{Name: "a", VM: "a", IP: "10.0.0.1"}, {Name: "b", VM: "b", IP: "10.0.0.2"}}}
	s := newStateWithKeys(cfg, false, keys)
	if got := s.Channel("a"); got != "Guest Additions" {
		t.Errorf("Channel(a) = %q, want Guest Additions", got)
	}
	if got := s.Channel("b"); got != "Sin conexión" {
		t.Errorf("Channel(b) = %q, want Sin conexión", got)
	}
	if got := newStateWithKeys(cfg, true, keys).Channel("a"); got != "Simulado" {
		t.Errorf("-simular must win: Channel(a) = %q", got)
	}
}

// withSSHChannel installs a fake Part B SSH channel for one test.
func withSSHChannel(t *testing.T, ch func(app.Target, string) (executor.Executor, string, bool)) {
	t.Helper()
	saved := sshChannel
	sshChannel = ch
	t.Cleanup(func() { sshChannel = saved })
}

func TestNewStatePrefersSSHWhereItIsReady(t *testing.T) {
	keys := t.TempDir()
	for _, name := range []string{"guest-pass-a", "guest-pass-b"} {
		_ = os.WriteFile(filepath.Join(keys, name), []byte("x"), 0o600)
	}
	var (
		mu    sync.Mutex // the probes run at the same time
		asked []string
	)
	withSSHChannel(t, func(tg app.Target, dir string) (executor.Executor, string, bool) {
		if dir != keys {
			t.Errorf("sshChannel got keys dir %q, want %q", dir, keys)
		}
		mu.Lock()
		asked = append(asked, tg.Name)
		mu.Unlock()
		if tg.Name == "a" {
			return &executor.Fake{NameValue: "SSH"}, "llave keys/root-a", true
		}
		return nil, "", false
	})
	cfg := app.Config{Listen: app.DefaultListen, Targets: []app.Target{
		{Name: "a", VM: "a", IP: "10.0.0.1"}, {Name: "b", VM: "b", IP: "10.0.0.2"}, {Name: "c", VM: "c", IP: "10.0.0.3"},
	}}
	s := newStateWithKeys(cfg, false, keys)
	for target, want := range map[string]string{"a": "SSH", "b": "Guest Additions", "c": "Sin conexión"} {
		if got := s.Channel(target); got != want {
			t.Errorf("Channel(%s) = %q, want %q", target, got, want)
		}
	}
	if len(asked) != 3 {
		t.Errorf("sshChannel asked for %v, want every target", asked)
	}
	if got := newStateWithKeys(cfg, true, keys).Channel("a"); got != "Simulado" {
		t.Errorf("-simular must win over SSH: Channel(a) = %q", got)
	}
}

func TestNewStateFallsBackWhenSSHGivesNoChannel(t *testing.T) {
	keys := t.TempDir()
	_ = os.WriteFile(filepath.Join(keys, "guest-pass"), []byte("x"), 0o600)
	withSSHChannel(t, func(app.Target, string) (executor.Executor, string, bool) { return nil, "x", true })
	cfg := app.Config{Listen: app.DefaultListen, Targets: []app.Target{{Name: "a", VM: "a", IP: "10.0.0.1"}}}
	if got := newStateWithKeys(cfg, false, keys).Channel("a"); got != "Guest Additions" {
		t.Fatalf("ok without a channel must fall back to Guest Additions, got %q", got)
	}
}

func TestSSHProbesRunAtTheSameTime(t *testing.T) {
	withSSHChannel(t, func(app.Target, string) (executor.Executor, string, bool) {
		time.Sleep(300 * time.Millisecond) // a VM that is off: the probe waits its timeout
		return nil, "", false
	})
	cfg := app.Config{Listen: app.DefaultListen, Targets: []app.Target{
		{Name: "a", VM: "a", IP: "10.0.0.1"}, {Name: "b", VM: "b", IP: "10.0.0.2"}, {Name: "c", VM: "c", IP: "10.0.0.3"}, {Name: "d", VM: "d", IP: "10.0.0.4"},
	}}
	start := time.Now()
	newStateWithKeys(cfg, false, t.TempDir())
	if d := time.Since(start); d > 900*time.Millisecond {
		t.Fatalf("4 probes of 300 ms took %v: they must run in parallel", d)
	}
}

func TestSimulatedModeNeverProbesSSH(t *testing.T) {
	withSSHChannel(t, func(app.Target, string) (executor.Executor, string, bool) {
		t.Error("with -simular the console must not reach any VM")
		return nil, "", false
	})
	cfg := app.Config{Listen: app.DefaultListen, Targets: []app.Target{{Name: "a", VM: "a", IP: "10.0.0.1"}}}
	if got := newStateWithKeys(cfg, true, t.TempDir()).Channel("a"); got != "Simulado" {
		t.Fatalf("Channel(a) = %q", got)
	}
}

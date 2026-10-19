package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validTarget() Target {
	return Target{Name: "pruebas", VM: "consola-pruebas", IP: "192.168.40.60", SSHUser: "root"}
}

func TestValidateListenAcceptsOnlyLoopback(t *testing.T) {
	cases := []struct {
		listen string
		ok     bool
	}{
		{"127.0.0.1:8090", true},
		{"[::1]:8090", true},
		{"127.0.0.2:9000", true},
		{"0.0.0.0:8090", false},       // every interface
		{":8090", false},              // empty host also means every interface
		{"[::]:8090", false},          // every IPv6 interface
		{"192.168.40.22:8090", false}, // reachable from the LAN
		{"localhost:8090", false},     // a name, not a verified loopback IP
		{"127.0.0.1", false},          // missing port
		{"127.0.0.1:0", false},        // random port is useless for the user
		{"127.0.0.1:70000", false},    // out of range
		{"127.0.0.1:http", false},     // named port
		{"127.0.0.1:+8090", false},    // non-canonical: binds 8090 but logs an unusable URL
		{"127.0.0.1:08090", false},    // non-canonical leading zero
	}
	for _, c := range cases {
		cfg := Config{Listen: c.listen, Targets: []Target{validTarget()}}
		err := cfg.Validate()
		if c.ok && err != nil {
			t.Errorf("listen %q: unexpected error: %v", c.listen, err)
		}
		if !c.ok && err == nil {
			t.Errorf("listen %q: expected an error, got nil", c.listen)
		}
	}
}

func TestValidateAppliesDefaultListen(t *testing.T) {
	cfg := Config{Targets: []Target{validTarget()}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listen != DefaultListen {
		t.Fatalf("Listen = %q, want %q", cfg.Listen, DefaultListen)
	}
	if DefaultListen != "127.0.0.1:8090" {
		t.Fatalf("DefaultListen = %q, want 127.0.0.1:8090", DefaultListen)
	}
}

func TestValidateTargets(t *testing.T) {
	dup := validTarget()
	cases := []struct {
		name    string
		targets []Target
		wantErr string
	}{
		{"no targets", nil, "at least one target"},
		{"empty name", []Target{{VM: "x", IP: "10.0.0.1"}}, "name"},
		{"empty vm", []Target{{Name: "a", IP: "10.0.0.1"}}, "vm"},
		{"invalid ip", []Target{{Name: "a", VM: "x", IP: "10.0.0"}}, "ip"},
		{"duplicate name", []Target{validTarget(), dup}, "duplicate"},
		{"path in name", []Target{{Name: "../otra", VM: "x", IP: "10.0.0.1"}}, "lowercase letters"},
		{"spaces in name", []Target{{Name: "mi vm", VM: "x", IP: "10.0.0.1"}}, "lowercase letters"},
	}
	for _, c := range cases {
		cfg := Config{Listen: DefaultListen, Targets: c.targets}
		err := cfg.Validate()
		if err == nil {
			t.Errorf("%s: expected an error, got nil", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.wantErr)
		}
	}
}

func TestLoadExampleConfig(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.example.json"))
	if err != nil {
		t.Fatalf("config.example.json must load: %v", err)
	}
	if len(cfg.Targets) < 2 {
		t.Fatalf("example must describe the test VM and the load balancer, got %d targets", len(cfg.Targets))
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"listen":"127.0.0.1:8090","targets":[{"name":"a","vm":"x","ip":"10.0.0.1","pasword":"typo"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for an unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error %q should come from the unknown field", err)
	}
}

func TestLoadRejectsTrailingContent(t *testing.T) {
	valid := `{"listen":"127.0.0.1:8090","targets":[{"name":"a","vm":"x","ip":"10.0.0.1"}]}`
	for name, body := range map[string]string{
		"second object": valid + `{"listen":"0.0.0.0:80"}`,
		"garbage":       valid + ` garbage`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("%s: expected an error, got nil", name)
		}
	}
}

func TestLoadAcceptsTrailingWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"listen":"127.0.0.1:8090","targets":[{"name":"a","vm":"x","ip":"10.0.0.1"}]}` + "\n\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("trailing newlines must be accepted: %v", err)
	}
}

func TestLoadRejectsNonLoopbackListen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"listen":"0.0.0.0:8090","targets":[{"name":"a","vm":"x","ip":"10.0.0.1"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load must validate: expected an error for 0.0.0.0, got nil")
	}
}

func TestLoadMissingFileExplainsWhatToDo(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "config.json"))
	if err == nil {
		t.Fatal("expected an error for a missing file, got nil")
	}
	if !strings.Contains(err.Error(), "config.example.json") {
		t.Fatalf("error %q should tell the user to copy config.example.json", err)
	}
}

func TestTargetAcceptsGuestUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"targets":[{"name":"a","vm":"x","ip":"10.0.0.1","guest_user":"admin"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil || cfg.Targets[0].GuestUser != "admin" {
		t.Fatalf("Load = %+v, %v", cfg, err)
	}
}

package balanceador

import (
	"strings"
	"testing"
)

// A haproxy.cfg like the one of the load balancing lab: tabs in global/defaults, spaces
// in the sections, comments and a listen section after the backend.
const labCfg = `global
	log /dev/log	local0
	daemon

defaults
	mode	http
	timeout connect 5000

frontend web_front
    bind *:80
    default_backend web_servers

backend web_servers   # the lab's servers
    balance roundrobin
    server servidor1 192.168.40.51:8080 check
    server servidor2 192.168.40.52:8080 check

listen stats
    bind *:8404
    stats enable
`

func TestBackends(t *testing.T) {
	if got := Backends(labCfg); len(got) != 1 || got[0] != "web_servers" {
		t.Fatalf("Backends = %v", got)
	}
}

func TestAddServerGoesAtTheEndOfItsBackend(t *testing.T) {
	srv := Server{Name: ServerName("192.168.40.53", "8080"), IP: "192.168.40.53", Port: "8080"}
	got, err := AddServer(labCfg, "web_servers", srv)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(labCfg, "servidor2 192.168.40.52:8080 check\n",
		"servidor2 192.168.40.52:8080 check\n    server srv-192-168-40-53-8080 192.168.40.53:8080 check\n", 1)
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if s := ListServers(got, "web_servers"); len(s) != 3 || s[2].Address() != "192.168.40.53:8080" {
		t.Fatalf("servers = %v", s)
	}
}

func TestAddServerToTheLastSectionOfTheFile(t *testing.T) {
	cfg := "backend b\n\tserver a 10.0.0.1:80 check"
	got, err := AddServer(cfg, "b", Server{Name: "n", IP: "10.0.0.2", Port: "80"})
	if err != nil || got != cfg+"\n\tserver n 10.0.0.2:80 check" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestAddServerRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		backend string
		srv     Server
		want    string
	}{
		"missing backend":   {"nope", Server{"n", "10.0.0.9", "80"}, "Los que hay son: web_servers"},
		"repeated address":  {"web_servers", Server{"otro", "192.168.40.51", "8080"}, "ya está en el back-end"},
		"repeated name":     {"web_servers", Server{"servidor1", "10.0.0.9", "80"}, "Ya hay un servidor llamado"},
		"frontend, no back": {"web_front", Server{"n", "10.0.0.9", "80"}, "no existe"},
	} {
		if _, err := AddServer(labCfg, tc.backend, tc.srv); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	if _, err := AddServer("global\n\tdaemon\n", "b", Server{"n", "10.0.0.9", "80"}); err == nil || !strings.Contains(err.Error(), "ningún back-end") {
		t.Errorf("no backends: err = %v", err)
	}
}

func TestRemoveServer(t *testing.T) {
	got, err := RemoveServer(labCfg, "web_servers", "192.168.40.51")
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.Replace(labCfg, "    server servidor1 192.168.40.51:8080 check\n", "", 1) {
		t.Fatalf("got:\n%s", got)
	}
	if _, err := RemoveServer(labCfg, "web_servers", "10.9.9.9"); err == nil || !strings.Contains(err.Error(), "no tiene ningún servidor") {
		t.Errorf("unknown ip: %v", err)
	}
	if _, err := RemoveServer(got, "web_servers", "192.168.40.52"); err == nil || !strings.Contains(err.Error(), "único servidor") {
		t.Errorf("last server: %v", err)
	}
	// A server with the same IP in another section is not touched.
	if _, err := RemoveServer(labCfg+"backend otro\n    server x 192.168.40.51:9000 check\n    server y 10.0.0.1:80\n", "otro", "192.168.40.51"); err != nil {
		t.Errorf("other backend: %v", err)
	}
}

func TestValidateBackend(t *testing.T) {
	for _, ok := range []string{"web_servers", "consola_back", "b1", "app.v2:x"} {
		if ValidateBackend(ok) != nil {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "-x", "a b", "x;reboot", "x\nbackend y", strings.Repeat("a", 65)} {
		if ValidateBackend(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

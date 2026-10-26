package web

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/servicios"
)

const haproxyLab = "global\n\tdaemon\n\nbackend web_servers\n    balance roundrobin\n    server servidor1 192.168.40.51:8080 check\n"

func TestAddServerReadsTheRealFileAndAppliesIt(t *testing.T) {
	read := executor.Command{Name: "cat", Args: []string{"--", haproxyCfg}}
	fake, h, log := fakeVM(t, map[string]executor.Result{read.String(): {Stdout: haproxyLab}})
	rec := serve(t, h, postForm("/balanceador/agregar", url.Values{"backend": {"web_servers"}, "ip": {"192.168.40.52"}, "puerto": {"8080"}}))
	e := resultEntry(t, rec, log)
	if !e.OK || !strings.Contains(e.Message, "srv-192-168-40-52-8080") {
		t.Fatalf("entry = %+v", e)
	}
	calls := fake.Calls()
	if len(calls) != 2 || calls[1].Name != "sh" {
		t.Fatalf("ran %v", calls)
	}
	newCfg := calls[1].Args[3]
	if !strings.HasPrefix(newCfg, "global\n\tdaemon\n") || !strings.Contains(newCfg, "server srv-192-168-40-52-8080 192.168.40.52:8080 check") {
		t.Fatalf("the rest of the file must be kept:\n%s", newCfg)
	}
	if calls[1].Args[5] != sha256Hex(haproxyLab) {
		t.Error("the write must be tied to the file that was read")
	}
}

func TestRemoveLastServerWritesNothing(t *testing.T) {
	read := executor.Command{Name: "cat", Args: []string{"--", haproxyCfg}}
	fake, h, log := fakeVM(t, map[string]executor.Result{read.String(): {Stdout: haproxyLab}})
	rec := serve(t, h, postForm("/balanceador/quitar", url.Values{"backend-quitar": {"web_servers"}, "ip-quitar": {"192.168.40.51"}}))
	if e := resultEntry(t, rec, log); e.OK || !strings.Contains(e.Message, "único servidor") {
		t.Fatalf("entry = %+v", e)
	}
	if n := len(fake.Calls()); n != 1 {
		t.Fatalf("ran %d commands, want only the read", n)
	}
}

func TestInvalidPartCFormsRunNothing(t *testing.T) {
	for _, tc := range []struct {
		path  string
		form  url.Values
		field string
	}{
		{"/balanceador/agregar", url.Values{"backend": {"x;reboot"}, "ip": {"192.168.40.52"}, "puerto": {"8080"}}, "backend"},
		{"/balanceador/agregar", url.Values{"backend": {"web"}, "ip": {"999.1.1.1"}, "puerto": {"8080"}}, "ip"},
		{"/balanceador/quitar", url.Values{"backend-quitar": {"web"}, "ip-quitar": {"x"}}, "ip-quitar"},
		{"/servicios/desplegar", url.Values{"aplicacion": {"App"}, "servicio": {"app"}, "ejecutable": {"/opt/x'; rm -rf / #"}}, "ejecutable"},
		{"/servicios/desplegar", url.Values{"aplicacion": {"App"}, "servicio": {"ssh"}, "ejecutable": {"/opt/x"}}, "servicio"},
		{"/servicios/accion", url.Values{"servicio-accion": {"ssh"}, "accion": {"stop"}}, "servicio-accion"},
		{"/servicios/accion", url.Values{"servicio-accion": {"cron"}, "accion": {"mask"}}, "accion"},
		{"/servicios/accion", url.Values{"servicio-accion": {"--host=x"}, "accion": {"start"}}, "servicio-accion"},
	} {
		fake, h, _ := fakeVM(t, nil)
		rec := serve(t, h, postForm(tc.path, tc.form))
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body(rec), `id="`+tc.field+`-error"`) {
			t.Errorf("%s %v: status %d, want 422 with the error on %s", tc.path, tc.form, rec.Code, tc.field)
		}
		if n := len(fake.Calls()); n != 0 {
			t.Errorf("%s %v: ran %d commands", tc.path, tc.form, n)
		}
	}
}

func TestDeployPlanNeverBuildsShellText(t *testing.T) {
	p := planDesplegar("pruebas", servicioEjemplo, "bookzilla", nil)
	for _, c := range p.Commands {
		if c.Name == "sh" && c.Args[1] != ensureUserScript && c.Args[1] != writeUnitScript {
			t.Errorf("unexpected shell script %q", c.Args[1])
		}
	}
	if p.Commands[0].String() != "install -d -m 755 -- /opt/bookzilla" || p.Commands[len(p.Commands)-1].String() != "systemctl try-restart -- bookzilla" {
		t.Fatalf("commands = %v", p.Commands)
	}
}

func TestStatusOfAStoppedServiceIsNotAnError(t *testing.T) {
	p := planAccion("pruebas", "bookzilla", "status")
	if len(p.AlsoOK) != 1 || p.AlsoOK[0] != 3 || p.Success([]executor.Result{{ExitCode: 3}}) != "«bookzilla» está detenido. El detalle está abajo." {
		t.Fatalf("plan = %+v", p)
	}
	if got := planAccion("pruebas", "cron", "stop").Commands[0].String(); got != "systemctl stop -- cron" {
		t.Errorf("command = %q", got)
	}
}

// The real script on a copy of the file, with stand-ins for haproxy and systemctl: a
// valid file replaces the old one and keeps a copy; an invalid one or a file that changed
// after it was read leaves everything as it was.
func TestApplyHaproxyScriptInARealShell(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	_ = os.Mkdir(bin, 0o755)
	_ = os.WriteFile(filepath.Join(bin, "haproxy"), []byte("#!/bin/sh\ngrep -q INVALID \"$4\" && exit 1; exit 0\n"), 0o755)
	_ = os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\necho \"$@\" > \"$(dirname \"$0\")/reloaded\"\n"), 0o755)
	cfg := filepath.Join(dir, "haproxy.cfg")
	run := func(content, sha string) int {
		cmd := exec.Command("/bin/sh", "-c", applyHaproxyScript, "sh", content, cfg, sha)
		cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin"}
		if err := cmd.Run(); err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return ee.ExitCode()
			}
			t.Fatal(err)
		}
		return 0
	}
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }
	_ = os.WriteFile(cfg, []byte(haproxyLab), 0o644)

	if code := run("INVALID\n", sha256Hex(haproxyLab)); code != haproxyInvalidCode || read(cfg) != haproxyLab {
		t.Fatalf("invalid config: exit %d, file %q", code, read(cfg))
	}
	if code := run("new\n", sha256Hex("something else")); code != haproxyChangedCode || read(cfg) != haproxyLab {
		t.Fatalf("changed file: exit %d", code)
	}
	if code := run("new\n", sha256Hex(haproxyLab)); code != 0 || read(cfg) != "new\n" || read(cfg+".consola-anterior") != haproxyLab {
		t.Fatalf("valid config: exit %d, file %q", code, read(cfg))
	}
	if got := read(filepath.Join(bin, "reloaded")); got != "reload haproxy\n" {
		t.Errorf("systemctl got %q", got)
	}
	if left, _ := filepath.Glob(cfg + ".consola-??????"); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

var servicioEjemplo = servicios.Config{Name: "bookzilla", Description: "Bookzilla", Exec: "/opt/bookzilla/bookzilla", Port: "8080"}

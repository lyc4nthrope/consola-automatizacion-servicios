package web

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/servicios"
)

// index returns the position of the first command whose text starts with prefix, or -1.
func index(cmds []executor.Command, prefix string) int {
	return slices.IndexFunc(cmds, func(c executor.Command) bool { return strings.HasPrefix(c.String(), prefix) })
}

// The program must be in the VM before the console checks it: on a first deployment
// /opt/<app> is empty until the package is extracted.
func TestDeployExtractsThePackageBeforeCheckingTheProgram(t *testing.T) {
	p := planDesplegar("pruebas", servicioEjemplo, "bookzilla", []byte("paquete"))
	extract, check := index(p.Commands, "tar -xzf - -C /opt/bookzilla"), index(p.Commands, "test -f /opt/bookzilla/bookzilla -a -x /opt/bookzilla/bookzilla")
	if extract < 0 || check < 0 || extract > check {
		t.Fatalf("extract at %d, check at %d: %v", extract, check, p.Commands)
	}
	if p.Commands[extract].Stdin != "paquete" {
		t.Error("the package must travel as tar's standard input")
	}
	if index(p.Commands, "install -d -m 755 -- /opt/bookzilla") > extract {
		t.Error("the folder must exist before tar extracts into it")
	}
	if c := index(p.Commands, "chmod 755 -- /opt/bookzilla/bookzilla"); c < extract || c > check {
		t.Error("the program gets its execute bit after extraction and before the check")
	}
}

// A long-running service gets its system user and its working directory before the unit
// is written, and a service that was already running is restarted with the new program.
func TestDeployCreatesTheServiceUserBeforeTheUnit(t *testing.T) {
	p := planDesplegar("pruebas", servicioEjemplo, "bookzilla", []byte("x"))
	user := slices.IndexFunc(p.Commands, func(c executor.Command) bool { return len(c.Args) > 1 && c.Args[1] == ensureUserScript })
	unit := slices.IndexFunc(p.Commands, func(c executor.Command) bool { return len(c.Args) > 1 && c.Args[1] == writeUnitScript })
	if user < 0 || unit < 0 || user > unit || user < index(p.Commands, "test -f") {
		t.Fatalf("user %d, unit %d: %v", user, unit, p.Commands)
	}
	if p.Commands[user].Args[3] != "bookzilla" {
		t.Errorf("user script args = %v", p.Commands[user].Args)
	}
	restart := index(p.Commands, "systemctl try-restart -- bookzilla")
	if restart < 0 || index(p.Commands, "systemctl enable -- bookzilla") < unit || restart < unit {
		t.Errorf("the unit must be enabled, then a running service restarted: %v", p.Commands)
	}
}

// A oneshot runs as root and keeps no data: no system user and no data folder.
func TestDeployOneshotHasNoServiceUser(t *testing.T) {
	cfg := servicios.Config{Name: "fecha", Description: "fecha", Exec: "/opt/fecha/fecha.sh", Oneshot: true}
	p := planDesplegar("pruebas", cfg, "fecha", []byte("x"))
	for _, c := range p.Commands {
		if len(c.Args) > 1 && c.Args[1] == ensureUserScript {
			t.Errorf("oneshot ran %v", c)
		}
	}
	if index(p.Commands, "test -f /opt/fecha/fecha.sh -a -x /opt/fecha/fecha.sh") < index(p.Commands, "tar -xzf - -C /opt/fecha") {
		t.Error("the script must be extracted before it is checked")
	}
}

// From the form: the folder of aplicaciones/ is packed and sent; a name that is not one
// of those folders runs nothing.
func TestDeployFromTheForm(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "bookzilla")
	_ = os.MkdirAll(filepath.Join(app, "data"), 0o755)
	_ = os.WriteFile(filepath.Join(app, "bookzilla"), []byte("bin"), 0o644)
	_ = os.WriteFile(filepath.Join(app, "data", "books.json"), []byte("[]"), 0o644)
	old := aplicacionesDir
	aplicacionesDir = dir
	t.Cleanup(func() { aplicacionesDir = old })

	fake, h, log := fakeVM(t, nil)
	rec := serve(t, h, postForm("/servicios/desplegar", url.Values{"aplicacion": {"bookzilla"}, "servicio": {"bookzilla"}, "ejecutable": {"bookzilla"}, "puerto": {"8080"}}))
	if e := resultEntry(t, rec, log); !e.OK {
		t.Fatalf("entry = %+v", e)
	}
	calls := fake.Calls()
	i := index(calls, "tar -xzf -")
	if i < 0 {
		t.Fatalf("no extraction in %v", calls)
	}
	names := tarNames(t, calls[i].Stdin)
	if !names["bookzilla"] || !names["data/books.json"] {
		t.Errorf("package holds %v", names)
	}

	for _, bad := range []string{"otra", ".", "../bookzilla"} {
		fake, h, _ := fakeVM(t, nil)
		rec := serve(t, h, postForm("/servicios/desplegar", url.Values{"aplicacion": {bad}, "servicio": {"bookzilla"}, "ejecutable": {"bookzilla"}}))
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body(rec), `id="aplicacion-error"`) {
			t.Errorf("aplicacion %q: status %d, want 422 on aplicacion", bad, rec.Code)
		}
		if n := len(fake.Calls()); n != 0 {
			t.Errorf("aplicacion %q ran %d commands", bad, n)
		}
	}
}

func tarNames(t *testing.T, data string) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names[h.Name] = true
	}
}

// The six actions of the assignment, and only those.
func TestServicesPageOffersOnlyTheSixActions(t *testing.T) {
	_, h, _ := fakeVM(t, nil)
	page := body(serve(t, h, httptest.NewRequest(http.MethodGet, "/servicios", nil)))
	start := strings.Index(page, `id="accion"`)
	if start < 0 {
		t.Fatal("no action select")
	}
	page = page[start : start+strings.Index(page[start:], "</select>")]
	for a := range servicios.Actions {
		if !strings.Contains(page, `<option value="`+a+`"`) {
			t.Errorf("missing action %s", a)
		}
	}
	if n := strings.Count(page, `<option value="`); n != len(servicios.Actions) {
		t.Errorf("%d actions offered, want %d", n, len(servicios.Actions))
	}
	if bytes.Contains([]byte(page), []byte(`value="reload"`)) {
		t.Error("reload is not one of the six actions")
	}
}

// The deploy message says what really happened: a service that was already running was
// restarted with the new program; a stopped one waits for the Start button.
func TestDeployMessageFollowsTheServiceState(t *testing.T) {
	p := planDesplegar("pruebas", servicioEjemplo, "bookzilla", []byte("x"))
	last := p.Commands[len(p.Commands)-1]
	if last.Name != "sh" || last.Args[1] != isActiveScript || last.Args[3] != "bookzilla" {
		t.Fatalf("last command = %v", last)
	}
	if got := p.Success([]executor.Result{{Stdout: "active\n"}}); !strings.Contains(got, "se reinició con el programa nuevo") {
		t.Errorf("running: %q", got)
	}
	if got := p.Success([]executor.Result{{Stdout: "inactive\n"}}); !strings.Contains(got, "Iniciar (start)") {
		t.Errorf("stopped: %q", got)
	}
}

// The real script with a stand-in for systemctl: a stopped unit (exit 3) still reports its
// state and does not fail the deployment.
func TestIsActiveScriptInARealShell(t *testing.T) {
	bin := t.TempDir()
	_ = os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\necho inactive\nexit 3\n"), 0o755)
	cmd := exec.Command("/bin/sh", "-c", isActiveScript, "sh", "bookzilla")
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "inactive" {
		t.Fatalf("out %q, err %v", out, err)
	}
}

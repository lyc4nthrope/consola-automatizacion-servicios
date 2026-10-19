package web

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
)

func TestPlanInstalarCommands(t *testing.T) {
	p := planInstalar("pruebas", "tree")
	if p.Action != "Instalar aplicación" || p.Target != "pruebas" || p.Timeout.Minutes() != 10 {
		t.Fatalf("plan = %+v", p)
	}
	want := []executor.Command{
		{Name: "env", Args: []string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "-o", "DPkg::Lock::Timeout=120", "update"}},
		{Name: "apt-cache", Args: []string{"show", "--no-all-versions", "--", "tree"}},
		{Name: "sh", Args: []string{"-c", stateScript, "sh", "tree", "0", "0"}},
		{Name: "env", Args: []string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "-o", "DPkg::Lock::Timeout=120", "install", "-y", "--", "tree"}},
		{Name: "sh", Args: []string{"-c", stateScript, "sh", "tree", "10", "0"}},
	}
	if !slices.EqualFunc(p.Commands, want, sameCommand) {
		t.Fatalf("commands =\n%v\nwant\n%v", p.Commands, want)
	}
}

func TestPlanEliminarCommands(t *testing.T) {
	p := planEliminar("pruebas", "tree")
	if p.Action != "Eliminar aplicación" || p.Target != "pruebas" || p.Timeout.Minutes() != 5 {
		t.Fatalf("plan = %+v", p)
	}
	want := []executor.Command{
		{Name: "sh", Args: []string{"-c", removeCheckScript, "sh", "tree"}},
		{Name: "env", Args: []string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "-o", "DPkg::Lock::Timeout=120", "remove", "-y", "--", "tree"}},
		{Name: "sh", Args: []string{"-c", stateScript, "sh", "tree", "0", "14"}},
	}
	if !slices.EqualFunc(p.Commands, want, sameCommand) {
		t.Fatalf("commands =\n%v\nwant\n%v", p.Commands, want)
	}
}

func sameCommand(a, b executor.Command) bool {
	return a.Name == b.Name && slices.Equal(a.Args, b.Args) && a.Stdin == b.Stdin
}

func TestParseState(t *testing.T) {
	cases := []struct {
		out  string
		want pkgState
	}{
		{"ii |2.1.1-2\n", pkgState{true, "ii", "2.1.1-2"}},
		{"hi |2.1.1-2\n", pkgState{true, "hi", "2.1.1-2"}}, // held: still installed
		{"rc |4.9.1-3\n", pkgState{false, "rc", "4.9.1-3"}},
		{"ii |2.41-12\nii |2.41-12\n", pkgState{true, "ii", "2.41-12"}}, // one entry per architecture
		{"\n", pkgState{}},
		{"i\n", pkgState{false, "i", ""}},
	}
	for _, c := range cases {
		if got := parseState(c.out); got != c.want {
			t.Errorf("parseState(%q) = %+v, want %+v", c.out, got, c.want)
		}
	}
}

func TestInstallSuccessMessages(t *testing.T) {
	cases := []struct{ before, after, want string }{
		{"", "ii |2.1.1-2", "«tree» quedó instalado (versión 2.1.1-2)."},
		{"rc |2.1.1-2", "ii |2.1.1-2", "«tree» quedó instalado (versión 2.1.1-2)."}, // config left from an old removal
		{"ii |2.1.1-2", "ii |2.1.1-2", "«tree» ya estaba instalado (versión 2.1.1-2); no se cambió nada."},
		{"hi |2.1.1-2", "hi |2.1.1-2", "«tree» ya estaba instalado (versión 2.1.1-2); no se cambió nada."},
		{"ii |2.1.0-1", "ii |2.1.1-2", "«tree» se actualizó de la versión 2.1.0-1 a la 2.1.1-2."},
	}
	p := planInstalar("pruebas", "tree")
	for _, c := range cases {
		res := []executor.Result{{}, {}, {Stdout: c.before + "\n"}, {}, {Stdout: c.after + "\n"}}
		if got := p.Success(res); got != c.want {
			t.Errorf("before %q after %q: %q, want %q", c.before, c.after, got, c.want)
		}
	}
}

func TestInstallFailuresAreExplained(t *testing.T) {
	p := planInstalar("pruebas", "tree")
	step := func(i, code int) oplog.Step { return oplog.Step{Cmd: p.Commands[i].String(), ExitCode: code} }
	cases := []struct {
		step oplog.Step
		want string // "" = default message
	}{
		{step(0, 100), "No se pudo actualizar la lista de paquetes"},
		{step(1, 100), "no existe en los repositorios"},
		{step(1, 1), ""},
		{step(3, 100), ""}, // apt-get install's own output says why
		{step(4, notInstalledCode), "no quedó instalado"},
		{step(4, 1), ""},
	}
	for _, c := range cases {
		got := p.Explain(c.step)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s (code %d): %q, want %q", c.step.Cmd, c.step.ExitCode, got, c.want)
		}
	}
}

func TestRemoveMessages(t *testing.T) {
	p := planEliminar("pruebas", "screen")
	if got := p.Success([]executor.Result{{}, {}, {Stdout: "rc |4.9.1-3\n"}}); !strings.Contains(got, "archivos de configuración siguen") {
		t.Errorf("leftover config: %q", got)
	}
	if got := p.Success([]executor.Result{{}, {}, {Stdout: "\n"}}); got != "Se eliminó «screen»." {
		t.Errorf("purged: %q", got)
	}

	check, after := p.Commands[0].String(), p.Commands[2].String()
	cases := []struct {
		step oplog.Step
		want string
	}{
		{oplog.Step{Cmd: check, ExitCode: notInstalledCode}, "no está instalado"},
		{oplog.Step{Cmd: check, ExitCode: protectedCode}, "esencial o protegido"},
		{oplog.Step{Cmd: check, ExitCode: takesOthersCode, Stdout: "byobu\nscreen\n"}, "eliminaría otros paquetes (byobu)"},
		{oplog.Step{Cmd: check, ExitCode: dryRunFailedCode}, "no pudo calcular una eliminación segura"},
		{oplog.Step{Cmd: check, ExitCode: heldCode}, "apt-mark unhold screen"},
		{oplog.Step{Cmd: check, ExitCode: 1}, ""},
		{oplog.Step{Cmd: after, ExitCode: stillInstalledCode}, "sigue instalado"},
		{oplog.Step{Cmd: after, ExitCode: 1}, ""},
		{oplog.Step{Cmd: p.Commands[1].String(), ExitCode: notInstalledCode}, ""}, // only the check step has these codes
	}
	for _, c := range cases {
		got := p.Explain(c.step)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s code %d: %q, want %q", c.step.Cmd[:12], c.step.ExitCode, got, c.want)
		}
	}
}

// stubTools puts fake dpkg-query and apt-get first in PATH and returns their directory.
//
// dpkg-query fills its -f format like the real one, from the lines of <dir>/pkg
// ("status|version|essential|protected|priority", one per architecture); without that file
// it acts as if the package were unknown. apt-get prints <dir>/apt-get.out and exits with
// <dir>/apt-get.code. Both record their arguments in <dir>/<tool>.args.
func stubTools(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stubs := map[string]string{
		"dpkg-query": `#!/bin/sh
d=$(dirname "$0")
printf '%s\n' "$@" > "$d/dpkg-query.args"
[ -f "$d/pkg" ] || exit 1
while IFS='|' read -r st ver ess prot prio; do
  printf '%s' "$3" | sed -e "s/\${db:Status-Abbrev}/$st/g" -e "s/\${Version}/$ver/g" -e "s/\${Essential}/$ess/g" \
    -e "s/\${Protected}/$prot/g" -e "s/\${Priority}/$prio/g" -e 's/\\n/\n/g'
done < "$d/pkg"
`,
		"apt-get": `#!/bin/sh
d=$(dirname "$0")
printf '%s\n' "$@" > "$d/apt-get.args"
cat "$d/apt-get.out" 2>/dev/null
exit $(cat "$d/apt-get.code" 2>/dev/null || echo 0)
`,
	}
	for name, script := range stubs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// setPkg sets what dpkg knows about the package; no lines = unknown package.
func setPkg(t *testing.T, dir string, lines ...string) {
	t.Helper()
	_ = os.Remove(filepath.Join(dir, "pkg"))
	if len(lines) > 0 {
		_ = os.WriteFile(filepath.Join(dir, "pkg"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}
}

func setApt(t *testing.T, dir, out string, code int) {
	t.Helper()
	_ = os.WriteFile(filepath.Join(dir, "apt-get.out"), []byte(out), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "apt-get.code"), []byte(strconv.Itoa(code)), 0o644)
	_ = os.Remove(filepath.Join(dir, "apt-get.args"))
}

func runScript(t *testing.T, dir string, c executor.Command) (string, int) {
	t.Helper()
	cmd := exec.Command("/bin/"+c.Name, c.Args...)
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin"}
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

func TestStateScriptInARealShell(t *testing.T) {
	dir := stubTools(t)
	cases := []struct {
		pkg       []string
		ifNot     int
		ifIn      int
		wantOut   string
		wantCode  int
		wantParse pkgState
	}{
		{[]string{"ii |2.1.1-2|no|no|optional"}, 0, 0, "ii |2.1.1-2\n", 0, pkgState{true, "ii", "2.1.1-2"}},
		{[]string{"ii |2.1.1-2|no|no|optional"}, 10, 0, "ii |2.1.1-2\n", 0, pkgState{true, "ii", "2.1.1-2"}},
		{[]string{"ii |2.1.1-2|no|no|optional"}, 0, 14, "ii |2.1.1-2\n", 14, pkgState{true, "ii", "2.1.1-2"}},
		{[]string{"hi |2.1.1-2|no|no|optional"}, 10, 0, "hi |2.1.1-2\n", 0, pkgState{true, "hi", "2.1.1-2"}},
		{[]string{"rc |4.9.1-3|no|no|optional"}, 0, 0, "rc |4.9.1-3\n", 0, pkgState{false, "rc", "4.9.1-3"}},
		{[]string{"rc |4.9.1-3|no|no|optional"}, 10, 0, "rc |4.9.1-3\n", 10, pkgState{false, "rc", "4.9.1-3"}},
		{[]string{"rc |4.9.1-3|no|no|optional"}, 0, 14, "rc |4.9.1-3\n", 0, pkgState{false, "rc", "4.9.1-3"}},
		{nil, 10, 0, "\n", 10, pkgState{}}, // dpkg does not know the package
		{nil, 0, 14, "\n", 0, pkgState{}},
		{[]string{"ii |2.41-12|no|no|required", "ii |2.41-12|no|no|required"}, 10, 0, "ii |2.41-12\nii |2.41-12\n", 0, pkgState{true, "ii", "2.41-12"}},
	}
	for _, c := range cases {
		setPkg(t, dir, c.pkg...)
		out, code := runScript(t, dir, stateCmd("tree", c.ifNot, c.ifIn))
		if out != c.wantOut || code != c.wantCode || parseState(out) != c.wantParse {
			t.Errorf("dpkg %q (%d,%d): out %q code %d, want %q code %d", c.pkg, c.ifNot, c.ifIn, out, code, c.wantOut, c.wantCode)
		}
	}
	args, _ := os.ReadFile(filepath.Join(dir, "dpkg-query.args"))
	if want := "-W\n-f\n${db:Status-Abbrev}|${Version}\\n\n--\ntree\n"; string(args) != want {
		t.Fatalf("dpkg-query args %q, want %q (the format must reach dpkg-query unexpanded)", args, want)
	}
}

func TestRemoveCheckScriptInARealShell(t *testing.T) {
	dir := stubTools(t)
	const leaf = "ii |2.1.1-2|no|no|optional"
	cases := []struct {
		name     string
		pkg      []string
		aptOut   string
		aptCode  int
		wantOut  string
		wantCode int
		dryRun   bool // apt-get -s must have run
	}{
		{"safe leaf package", []string{leaf}, "NOTE: simulation\nRemv tree [2.1.1-2]\n", 0, "", 0, true},
		{"held package", []string{"hi |2.1.1-2|no|no|optional"}, "Remv tree [2.1.1-2]\n", 0, "", heldCode, false},
		{"held and essential", []string{"hi |5.2|yes|no|required"}, "", 0, "", heldCode, false},
		{"arch suffix", []string{leaf}, "Remv tree:amd64 [2.1.1-2]\n", 0, "", 0, true},
		{"two architectures", []string{leaf, leaf}, "Remv tree:amd64 [2.1.1-2]\nRemv tree:i386 [2.1.1-2]\n", 0, "", 0, true},
		{"not installed", nil, "", 0, "", notInstalledCode, false},
		{"only config left", []string{"rc |2.1.1-2|no|no|optional"}, "", 0, "", notInstalledCode, false},
		{"essential", []string{"ii |5.2|yes|no|required"}, "Remv tree [5.2]\n", 0, "", protectedCode, false},
		{"essential, other priority", []string{"ii |1|yes|no|important"}, "Remv tree [1]\n", 0, "", protectedCode, false},
		{"protected", []string{"ii |257|no|yes|important"}, "Remv tree [257]\n", 0, "", protectedCode, false},
		{"required", []string{"ii |1|no|no|required"}, "Remv tree [1]\n", 0, "", protectedCode, false},
		{"takes others", []string{leaf}, "Remv tree [2.1.1-2]\nRemv byobu [6]\nRemv apt-listchanges [4]\n", 0, "apt-listchanges\nbyobu\ntree\n", takesOthersCode, true},
		{"dry run fails", []string{leaf}, "E: algo falló", 100, "E: algo falló\n", dryRunFailedCode, true},
		{"dry run fails after listing", []string{leaf}, "Remv tree [2.1.1-2]\nE: algo falló", 100, "Remv tree [2.1.1-2]\nE: algo falló\n", dryRunFailedCode, true},
		{"dry run removes nothing", []string{leaf}, "0 para eliminar", 0, "0 para eliminar\n", dryRunFailedCode, true},
	}
	for _, c := range cases {
		setPkg(t, dir, c.pkg...)
		setApt(t, dir, c.aptOut, c.aptCode)
		out, code := runScript(t, dir, planEliminar("pruebas", "tree").Commands[0])
		if out != c.wantOut || code != c.wantCode {
			t.Errorf("%s: out %q code %d, want %q code %d", c.name, out, code, c.wantOut, c.wantCode)
		}
		args, err := os.ReadFile(filepath.Join(dir, "apt-get.args"))
		switch {
		case c.dryRun && string(args) != "-s\nremove\n--\ntree\n":
			t.Errorf("%s: apt-get args %q, want the dry run -s remove -- tree", c.name, args)
		case !c.dryRun && err == nil:
			t.Errorf("%s: apt-get ran, but the check must stop before the dry run", c.name)
		}
	}
	args, _ := os.ReadFile(filepath.Join(dir, "dpkg-query.args"))
	if want := "-W\n-f\n${db:Status-Abbrev}|${Essential}|${Protected}|${Priority}\\n\n--\ntree\n"; string(args) != want {
		t.Fatalf("dpkg-query args %q, want %q", args, want)
	}
}

// fakeVM answers the install and remove plans with the given results, keyed by command
// line; other commands succeed with no output.
func fakeVM(t *testing.T, answers map[string]executor.Result) (*executor.Fake, http.Handler, *oplog.Log) {
	t.Helper()
	fake := &executor.Fake{RunFunc: func(c executor.Command) (executor.Result, error) { return answers[c.String()], nil }}
	state := newTestState()
	_ = state.SetExecutor("pruebas", fake)
	h, log := newTestHandlerWithLog(t, state)
	return fake, h, log
}

func resultEntry(t *testing.T, rec interface{ Header() http.Header }, log *oplog.Log) oplog.Entry {
	t.Helper()
	id := strings.TrimPrefix(rec.Header().Get("Location"), "/resultado/")
	e, ok := log.Get(id)
	if !ok {
		t.Fatalf("no log entry for Location %q", rec.Header().Get("Location"))
	}
	return e
}

func TestInstallFromTheForm(t *testing.T) {
	inst := planInstalar("pruebas", "tree")
	fake, h, log := fakeVM(t, map[string]executor.Result{
		inst.Commands[2].String(): {Stdout: "\n"},
		inst.Commands[4].String(): {Stdout: "ii |2.1.1-2\n"},
	})
	rec := serve(t, h, postForm("/preparacion/instalar", url.Values{"paquete": {" tree "}}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", rec.Code)
	}
	e := resultEntry(t, rec, log)
	if !e.OK || e.Action != "Instalar aplicación" || e.Target != "pruebas" || e.Message != "«tree» quedó instalado (versión 2.1.1-2)." {
		t.Fatalf("entry = %+v", e)
	}
	if got := fake.Calls(); !slices.EqualFunc(got, inst.Commands, sameCommand) {
		t.Fatalf("ran %v", got)
	}
}

func TestRemoveFromTheForm(t *testing.T) {
	rem := planEliminar("pruebas", "screen")
	fake, h, log := fakeVM(t, map[string]executor.Result{rem.Commands[2].String(): {Stdout: "rc |4.9.1-3\n"}})
	rec := serve(t, h, postForm("/preparacion/eliminar", url.Values{"paquete": {"screen"}}))
	e := resultEntry(t, rec, log)
	if !e.OK || e.Action != "Eliminar aplicación" || !strings.Contains(e.Message, "Se eliminó «screen»") {
		t.Fatalf("entry = %+v", e)
	}
	if got := fake.Calls(); !slices.EqualFunc(got, rem.Commands, sameCommand) {
		t.Fatalf("ran %v", got)
	}
}

func TestRemoveFromTheFormStopsWhenNotInstalled(t *testing.T) {
	rem := planEliminar("pruebas", "tree")
	fake, h, log := fakeVM(t, map[string]executor.Result{rem.Commands[0].String(): {ExitCode: notInstalledCode}})
	rec := serve(t, h, postForm("/preparacion/eliminar", url.Values{"paquete": {"tree"}}))
	e := resultEntry(t, rec, log)
	if e.OK || !strings.Contains(e.Message, "no está instalado") {
		t.Fatalf("entry = %+v", e)
	}
	if n := len(fake.Calls()); n != 1 {
		t.Fatalf("%d commands ran; after a failed check apt-get must not run", n)
	}
}

func TestInvalidPackageRunsNothing(t *testing.T) {
	for _, path := range []string{"/preparacion/instalar", "/preparacion/eliminar"} {
		for _, name := range []string{"", "tree; reboot", "-y", "$(reboot)", "Tree", "a"} {
			fake, h, _ := fakeVM(t, nil)
			rec := serve(t, h, postForm(path, url.Values{"paquete": {name}}))
			b := body(rec)
			if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(b, `id="paquete-error"`) || !strings.Contains(b, `aria-invalid="true" aria-describedby="paquete-error paquete-ayuda"`) {
				t.Errorf("%s %q: status %d, want 422 with the error next to the field", path, name, rec.Code)
			}
			if len(fake.Calls()) != 0 {
				t.Errorf("%s %q: commands ran for an invalid name", path, name)
			}
			if name == "tree; reboot" && !strings.Contains(b, `value="tree; reboot"`) {
				t.Errorf("the form must come back with what the user typed")
			}
		}
	}
}

func TestChannelPackagesAreNeverRemoved(t *testing.T) {
	for _, name := range channelPackages {
		fake, h, _ := fakeVM(t, nil)
		rec := serve(t, h, postForm("/preparacion/eliminar", url.Values{"paquete": {name}}))
		if b := body(rec); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(b, "sin conexión") {
			t.Errorf("%s: status %d, want 422 explaining the console would lose its connection", name, rec.Code)
		}
		if len(fake.Calls()) != 0 {
			t.Errorf("%s: commands ran", name)
		}
	}
	// Installing them is fine.
	fake, h, _ := fakeVM(t, nil)
	if rec := serve(t, h, postForm("/preparacion/instalar", url.Values{"paquete": {"openssh-server"}})); rec.Code != http.StatusSeeOther || len(fake.Calls()) == 0 {
		t.Errorf("installing openssh-server: status %d, %d commands", rec.Code, len(fake.Calls()))
	}
}

func TestPreparacionPageOffersBothActions(t *testing.T) {
	b := body(do(t, http.MethodGet, "/preparacion"))
	for _, want := range []string{
		`action="/preparacion/instalar" data-operacion="Instalar aplicación"`,
		`formaction="/preparacion/eliminar" data-operacion="Eliminar aplicación" aria-describedby="eliminar-ayuda"`,
		`id="eliminar-ayuda"`,
		`<label class="form-label" for="paquete">`,
		`aria-describedby="paquete-ayuda">`, // valid field: only the hint
		`id="paquete-ayuda"`,
	} {
		if !strings.Contains(b, want) {
			t.Errorf("page lacks %s", want)
		}
	}
}

func TestRootKeysPartialAppearsOnlyWhenPartBAddsIt(t *testing.T) {
	const placeholder = "Generar y autorizar las llaves de root"
	b := body(serve(t, mustHandlerWith(t, templatesWith(t, nil)), httptestGet("/preparacion")))
	if !strings.Contains(b, placeholder) {
		t.Fatal("without Part B's partial the page must show the placeholder")
	}

	partial := `{{define "llaves-root"}}<form id="llaves-root" data-destino="{{.Current.Name}}">{{"<b>"}} Llaves de Root</form>{{end}}`
	b = body(serve(t, mustHandlerWith(t, templatesWith(t, map[string]string{"_llaves-root.html": partial})), httptestGet("/preparacion")))
	if !strings.Contains(b, `<form id="llaves-root" data-destino="pruebas">&lt;b&gt; Llaves de Root</form>`) {
		t.Fatalf("Part B's partial must be rendered, escaped and with the page data; body lacks it")
	}
	if strings.Contains(b, placeholder) {
		t.Fatal("the placeholder must disappear once Part B's partial exists")
	}
}

func TestBrokenOptionalPartialFailsThePageNotTheConsole(t *testing.T) {
	h := mustHandlerWith(t, templatesWith(t, map[string]string{"_llaves-root.html": `{{define "llaves-root"}}{{.NoExiste}}{{end}}`}))
	if rec := serve(t, h, httptestGet("/preparacion")); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
	if rec := serve(t, h, httptestGet("/")); rec.Code != http.StatusOK {
		t.Fatalf("other pages must keep working, got %d", rec.Code)
	}
}

func TestPartialsCannotUseOpcional(t *testing.T) {
	for name, src := range map[string]string{
		"direct":    `{{define "x"}}{{opcional "x" .}}{{end}}`,
		"nested":    `{{define "x"}}{{if true}}{{range .Targets}}{{with 1}}{{else}}{{opcional "x" .}}{{end}}{{end}}{{end}}{{end}}`,
		"in a pipe": `{{define "x"}}{{template "error-campo" (opcional "x" .)}}{{end}}`,
	} {
		_, err := newHandlerWith(t, templatesWith(t, map[string]string{"_x.html": src}))
		if err == nil || !strings.Contains(err.Error(), "must not use opcional") {
			t.Errorf("%s: err = %v, want the partial rejected at startup", name, err)
		}
	}
	// The word in text, or in a string, is not a call.
	ok := `{{define "x"}}<p>Campo opcional</p>{{"opcional"}}{{end}}`
	if _, err := newHandlerWith(t, templatesWith(t, map[string]string{"_x.html": ok})); err != nil {
		t.Fatalf("a partial that only mentions the word must load: %v", err)
	}
}

func httptestGet(path string) *http.Request {
	req, _ := http.NewRequest(http.MethodGet, path, nil)
	req.Host = testListen
	return req
}

package web

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
)

func datos(fields ...string) []issueDato {
	var out []issueDato
	for _, d := range issueDatos {
		if slices.Contains(fields, d.Field) {
			out = append(out, d)
		}
	}
	return out
}

func TestIssueContent(t *testing.T) {
	cases := []struct {
		name   string
		msg    string
		chosen []issueDato
		want   string
	}{
		{"message only", "Bienvenido", nil, "Bienvenido\n\n"},
		{"data only", "", datos("dato-equipo", "dato-ip"), "Nombre del equipo:  \\n\nDirección IP:       \\4\n\n"},
		{"both", "Hola\nsegunda línea", datos("dato-fecha", "dato-hora"), "Hola\nsegunda línea\n\nFecha:              \\d\nHora:               \\t\n\n"},
		{"backslashes are doubled", `C:\temp \n \\`, nil, `C:\\temp \\n \\\\` + "\n\n"},
		{"browser line breaks", "a\r\nb\r\n", nil, "a\nb\n\n"},
		{"trailing line breaks", "a\n\n\n", nil, "a\n\n"},
		{"only spaces, with data", "   \n  ", datos("dato-ip"), "Dirección IP:       \\4\n\n"},
		{"leading blank line kept", "\nHola", nil, "\nHola\n\n"},
		{"every data item", "", issueDatos, "Nombre del equipo:  \\n\nSistema operativo:  \\S\nDirección IP:       \\4\nFecha:              \\d\nHora:               \\t\nTerminal:           \\l\n\n"},
	}
	for _, c := range cases {
		if got := issueContent(c.msg, c.chosen); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRenderIssue(t *testing.T) {
	values := map[byte]string{'n': "consola-pruebas", '4': "192.168.40.57"}
	cases := map[string]string{
		`\n en \4`:         "consola-pruebas en 192.168.40.57",
		`barra \\ y \\n`:   `barra \ y \n`,
		`desconocido \x`:   `desconocido \x`,
		`al final \`:       `al final \`,
		"sin escapes\nfin": "sin escapes\nfin",
	}
	for in, want := range cases {
		if got := renderIssue(in, values); got != want {
			t.Errorf("renderIssue(%q) = %q, want %q", in, got, want)
		}
	}
}

// What the user writes must come out exactly as written, whatever backslashes it has.
// renderIssue is the console's model of agetty; that agetty itself prints \\ as one
// backslash was checked on the VM (Debian 13, util-linux 2.41).
func FuzzIssueKeepsTheUsersText(f *testing.F) {
	for _, s := range []string{`\n`, `\\`, `\`, `a\4b`, `C:\temp\`, "línea\ncon ñ"} {
		f.Add(s)
	}
	values := exampleValues(app.Target{VM: "vm", IP: "1.2.3.4"}, time.Now())
	f.Fuzz(func(t *testing.T, msg string) {
		msg = strings.TrimRight(strings.ReplaceAll(msg, "\r\n", "\n"), "\n")
		if strings.ContainsRune(msg, '\r') {
			t.Skip()
		}
		want := msg + "\n\n"
		if strings.TrimSpace(msg) == "" {
			want = "\n"
		}
		if got := renderIssue(issueContent(msg, nil), values); got != want {
			t.Fatalf("message %q shows as %q", msg, got)
		}
	})
}

func TestExampleValues(t *testing.T) {
	now := time.Date(2026, 10, 9, 8, 5, 3, 0, time.UTC)
	v := exampleValues(app.Target{VM: "consola-pruebas", IP: "192.168.40.57"}, now)
	want := map[byte]string{'n': "consola-pruebas", 'S': "Debian GNU/Linux 13 (trixie)", '4': "192.168.40.57", 'd': "Fri Oct  9  2026", 't': "08:05:03", 'l': "tty1"}
	for k, w := range want {
		if v[k] != w {
			t.Errorf("\\%c = %q, want %q (as agetty writes it)", k, v[k], w)
		}
	}
	if len(v) != len(issueDatos) {
		t.Errorf("%d example values for %d data items", len(v), len(issueDatos))
	}
	for _, d := range issueDatos {
		if _, ok := v[d.Escape[1]]; !ok {
			t.Errorf("no example value for %s", d.Escape)
		}
	}
}

func TestPlanIssueCommands(t *testing.T) {
	p := planIssue("pruebas", "Hola\n\n")
	want := []executor.Command{
		{Name: "sh", Args: []string{"-c", writeFileScript, "sh", "Hola\n\n", "/etc/issue"}},
		{Name: "agetty", Args: []string{"--reload"}},
		{Name: "sh", Args: []string{"-c", showIssueScript}},
	}
	if p.Action != "Cambiar mensaje de entrada" || p.Target != "pruebas" || !slices.EqualFunc(p.Commands, want, sameCommand) {
		t.Fatalf("plan = %+v", p)
	}
	if !strings.Contains(p.Success(nil), "/etc/issue.consola-original") {
		t.Errorf("the success message must say where the original is")
	}

	r := planRestaurarIssue("pruebas")
	if r.Action != "Restaurar mensaje de entrada" || !slices.EqualFunc(r.Commands[1:], want[1:], sameCommand) ||
		!sameCommand(r.Commands[0], executor.Command{Name: "sh", Args: []string{"-c", restoreFileScript, "sh", "/etc/issue"}}) {
		t.Fatalf("restore plan = %+v", r)
	}
	if got := r.Explain(oplog.Step{Cmd: r.Commands[0].String(), ExitCode: noOriginalCode}); !strings.Contains(got, "No hay un mensaje original guardado") {
		t.Errorf("no original: %q", got)
	}
	for _, st := range []oplog.Step{{Cmd: r.Commands[0].String(), ExitCode: 1}, {Cmd: r.Commands[1].String(), ExitCode: noOriginalCode}} {
		if got := r.Explain(st); got != "" {
			t.Errorf("%q code %d: %q, want the default message", st.Cmd, st.ExitCode, got)
		}
	}
}

func runSh(t *testing.T, script string, args ...string) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", append([]string{"-c", script, "sh"}, args...)...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if out, err := cmd.CombinedOutput(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		t.Fatalf("%v: %s", err, out)
	}
	return 0
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestIssueScriptsInARealShell(t *testing.T) {
	dir := t.TempDir()
	issue := filepath.Join(dir, "issue")
	orig := issue + ".consola-original"
	if err := os.WriteFile(issue, []byte("Debian \\n \\l\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := syscall.Umask(0o077) // the script must not depend on the caller's umask
	defer syscall.Umask(old)

	first := "Primera versión con ñ\n\\\\ y 'comillas' $(id)\n\n"
	if code := runSh(t, writeFileScript, first, issue); code != 0 {
		t.Fatalf("write: exit %d", code)
	}
	if got := readFile(t, issue); got != first {
		t.Fatalf("issue = %q, want %q", got, first)
	}
	if got := readFile(t, orig); got != "Debian \\n \\l\n\n" {
		t.Fatalf("original = %q: the VM's own message must be kept", got)
	}
	if st, _ := os.Stat(issue); st.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v, want 0644", st.Mode().Perm())
	}
	if leftovers(issue) {
		t.Fatal("the temporary file must not stay behind")
	}

	if code := runSh(t, writeFileScript, "Segunda\n\n", issue); code != 0 || readFile(t, issue) != "Segunda\n\n" {
		t.Fatalf("second write: exit %d, issue %q", code, readFile(t, issue))
	}
	if got := readFile(t, orig); got != "Debian \\n \\l\n\n" {
		t.Fatalf("original = %q: a second change must not replace the saved original", got)
	}

	_ = os.Chmod(issue, 0o600) // the original's mode comes back with it
	if code := runSh(t, restoreFileScript, issue); code != 0 || readFile(t, issue) != "Debian \\n \\l\n\n" {
		t.Fatalf("restore: exit %d, issue %q", code, readFile(t, issue))
	}
	if st, _ := os.Stat(issue); st.Mode().Perm() != 0o644 {
		t.Fatalf("restored mode %v, want the original 0644", st.Mode().Perm())
	}
	if _, err := os.Stat(orig); !os.IsNotExist(err) {
		t.Fatal("after restoring, the saved original must be forgotten")
	}
	if leftovers(issue) {
		t.Fatal("restore must not leave its temporary file")
	}

	// The admin edits the file by hand after a restore: the next change keeps that edit.
	_ = os.WriteFile(issue, []byte("editado a mano\n"), 0o644)
	if code := runSh(t, writeFileScript, "Tercera\n\n", issue); code != 0 || readFile(t, orig) != "editado a mano\n" {
		t.Fatalf("after a restore, the next original must be the current file; got %q", readFile(t, orig))
	}

	// A VM with no /etc/issue: its original is "no message", so restoring leaves it empty.
	bare := filepath.Join(t.TempDir(), "issue")
	if code := runSh(t, writeFileScript, "Nuevo\n\n", bare); code != 0 || readFile(t, bare) != "Nuevo\n\n" {
		t.Fatalf("write without an issue file: exit %d", code)
	}
	if got := readFile(t, bare+".consola-original"); got != "" {
		t.Fatalf("original = %q, want an empty one", got)
	}
	if code := runSh(t, restoreFileScript, bare); code != 0 || readFile(t, bare) != "" {
		t.Fatalf("restore without an original file: exit %d, issue %q", code, readFile(t, bare))
	}

	// A write that fails halfway (like a full disk: here a file size limit of 0) fails,
	// leaves the message as it was and no temporary file behind.
	full := filepath.Join(t.TempDir(), "issue")
	_ = os.WriteFile(full, []byte("actual\n"), 0o644)
	_ = os.WriteFile(full+".consola-original", []byte("original\n"), 0o644)
	const diskFull = "trap '' XFSZ; ulimit -f 0; "
	if code := runSh(t, diskFull+writeFileScript, "nuevo\n", full); code == 0 || readFile(t, full) != "actual\n" {
		t.Fatalf("write on a full disk: exit %d, issue %q", code, readFile(t, full))
	}
	if leftovers(full) {
		t.Fatal("a failed write must not leave its temporary file")
	}
	if code := runSh(t, diskFull+restoreFileScript, full); code == 0 || readFile(t, full) != "actual\n" {
		t.Fatalf("restore on a full disk: exit %d, issue %q", code, readFile(t, full))
	}
	if leftovers(full) {
		t.Fatal("a failed restore must not leave its temporary file")
	}
	if got := readFile(t, full+".consola-original"); got != "original\n" {
		t.Fatalf("a failed restore must keep the saved original, got %q", got)
	}

	// Restoring when the console never changed the message.
	never := filepath.Join(t.TempDir(), "issue")
	_ = os.WriteFile(never, []byte("propio\n"), 0o644)
	if code := runSh(t, restoreFileScript, never); code != noOriginalCode || readFile(t, never) != "propio\n" {
		t.Fatalf("restore without original: exit %d, issue %q", code, readFile(t, never))
	}
}

func issueForm(msg string, fields ...string) url.Values {
	v := url.Values{"mensaje": {msg}}
	for _, f := range fields {
		v.Set(f, "on")
	}
	return v
}

func TestIssuePreviewRunsNothingAndKeepsTheForm(t *testing.T) {
	fake, h, _ := fakeVM(t, nil)
	rec := serve(t, h, postForm("/preparacion/issue/vista-previa", issueForm("<b>Hola</b> \\", "dato-equipo", "dato-ip")))
	b := body(rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{
		`id="vista-previa"`,
		"&lt;b&gt;Hola&lt;/b&gt; \\\n\nNombre del equipo:  consola-pruebas\nDirección IP:       192.168.40.60\n", // the example, escaped
		"&lt;b&gt;Hola&lt;/b&gt; \\\\\n", // the file keeps the doubled backslash
		`id="dato-equipo" name="dato-equipo" value="on" checked`,
		`id="dato-ip" name="dato-ip" value="on" checked`,
		`&lt;b&gt;Hola&lt;/b&gt; \</textarea>`,
	} {
		if !strings.Contains(b, want) {
			t.Errorf("preview page lacks %q", want)
		}
	}
	if strings.Contains(b, `id="dato-fecha" name="dato-fecha" value="on" checked`) {
		t.Error("an unchosen item must stay unchecked")
	}
	if len(fake.Calls()) != 0 {
		t.Fatal("the preview must not touch the VM")
	}
}

func TestIssueInstallRunsThePlan(t *testing.T) {
	fake, h, log := fakeVM(t, nil)
	rec := serve(t, h, postForm("/preparacion/issue", issueForm("Hola\r\n", "dato-terminal", "dato-equipo")))
	e := resultEntry(t, rec, log)
	if !e.OK || e.Action != "Cambiar mensaje de entrada" {
		t.Fatalf("entry = %+v", e)
	}
	want := planIssue("pruebas", "Hola\n\nNombre del equipo:  \\n\nTerminal:           \\l\n\n").Commands
	if got := fake.Calls(); !slices.EqualFunc(got, want, sameCommand) {
		t.Fatalf("ran %v\nwant %v", got, want)
	}
}

func TestIssueRestoreRunsThePlan(t *testing.T) {
	fake, h, log := fakeVM(t, nil)
	rec := serve(t, h, postForm("/preparacion/issue/restaurar", url.Values{}))
	if e := resultEntry(t, rec, log); e.Action != "Restaurar mensaje de entrada" {
		t.Fatalf("entry = %+v", e)
	}
	if got := fake.Calls(); !slices.EqualFunc(got, planRestaurarIssue("pruebas").Commands, sameCommand) {
		t.Fatalf("ran %v", got)
	}
}

func TestInvalidIssueRunsNothing(t *testing.T) {
	for _, path := range []string{"/preparacion/issue", "/preparacion/issue/vista-previa"} {
		for name, form := range map[string]url.Values{
			"nothing chosen":    issueForm("  \n "),
			"control character": issueForm("hola\x1b[2J", "dato-ip"),
			"too long":          issueForm(strings.Repeat("a", 501)),
		} {
			fake, h, _ := fakeVM(t, nil)
			rec := serve(t, h, postForm(path, form))
			b := body(rec)
			if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(b, `id="mensaje-error"`) || !strings.Contains(b, `aria-invalid="true" aria-describedby="mensaje-error mensaje-ayuda"`) {
				t.Errorf("%s %s: status %d, want 422 with the error next to the message", path, name, rec.Code)
			}
			if n := strings.Count(b, "Revisa los datos antes de continuar"); n != 1 {
				t.Errorf("%s %s: %d error summaries, want 1 (only in the form that was sent)", path, name, n)
			}
			if len(fake.Calls()) != 0 || strings.Contains(b, `id="vista-previa"`) {
				t.Errorf("%s %s: ran commands or showed a preview", path, name)
			}
		}
	}
	// A rejected package shows its summary only once too.
	_, h, _ := fakeVM(t, nil)
	if n := strings.Count(body(serve(t, h, postForm("/preparacion/instalar", url.Values{"paquete": {"x"}}))), "Revisa los datos antes de continuar"); n != 1 {
		t.Errorf("package form: %d error summaries, want 1", n)
	}
}

func TestIssueFormOnThePage(t *testing.T) {
	b := body(do(t, http.MethodGet, "/preparacion"))
	for _, want := range []string{
		`action="/preparacion/issue" data-operacion="Cambiar mensaje de entrada"`,
		`formaction="/preparacion/issue/vista-previa#vista-previa" data-local`,
		`action="/preparacion/issue/restaurar" data-operacion="Restaurar mensaje de entrada"`,
		`<label class="form-label" for="mensaje">`,
		`aria-describedby="mensaje-ayuda">`,
	} {
		if !strings.Contains(b, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	for _, d := range issueDatos {
		if !strings.Contains(b, `<input class="form-check-input" type="checkbox" id="`+d.Field+`" name="`+d.Field+`" value="on">`) ||
			!strings.Contains(b, `<label class="form-check-label" for="`+d.Field+`">`+d.Label) {
			t.Errorf("page lacks the checkbox for %s", d.Field)
		}
	}
	if strings.Contains(b, `id="vista-previa"`) {
		t.Error("no preview before the user asks for one")
	}
}

func TestShowIssueScriptInARealShell(t *testing.T) {
	// script(1) gives agetty a terminal, which ends every line in \r\n; the console must
	// show plain lines.
	dir := t.TempDir()
	stub := "#!/bin/sh\nprintf '%s\\n' \"$LC_ALL\" \"$@\" > \"$(dirname \"$0\")/script.args\"\nprintf 'Hola consola-pruebas\\r\\n\\r\\n'\nexit $(cat \"$(dirname \"$0\")/script.code\" 2>/dev/null || echo 0)\n"
	if err := os.WriteFile(filepath.Join(dir, "script"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", showIssueScript)
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil || string(out) != "Hola consola-pruebas\n\n" {
		t.Fatalf("out %q err %v, want the lines without \\r", out, err)
	}
	if args := readFile(t, filepath.Join(dir, "script.args")); args != "C.UTF-8\n-eqc\nagetty --show-issue\n/dev/null\n" { // locale first
		t.Fatalf("script args %q", args)
	}
}

func TestShowIssueScriptKeepsTheFailure(t *testing.T) {
	dir := t.TempDir()
	stub := "#!/bin/sh\necho 'agetty: error' >&2\nexit 3\n"
	_ = os.WriteFile(filepath.Join(dir, "script"), []byte(stub), 0o755)
	cmd := exec.Command("/bin/sh", "-c", showIssueScript)
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin"}
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 3 {
		t.Fatalf("err = %v, want exit 3: a failed check must not look like a success", err)
	}
}

func TestIssueFormKeepsALeadingBlankLine(t *testing.T) {
	_, h, _ := fakeVM(t, nil)
	b := body(serve(t, h, postForm("/preparacion/issue/vista-previa", issueForm("\nHola"))))
	// HTML drops one line break right after <textarea> and <pre>; the template adds it.
	for _, want := range []string{"\">\n\nHola</textarea>", "translate=\"no\">\n\nHola\n\n<span class=\"issue-login\">", "translate=\"no\">\n\nHola\n\n</pre>"} {
		if !strings.Contains(b, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// leftovers reports a temporary file left next to p (anything but the saved original).
func leftovers(p string) bool {
	entries, _ := filepath.Glob(p + ".consola-*")
	for _, e := range entries {
		if e != p+".consola-original" {
			return true
		}
	}
	return false
}

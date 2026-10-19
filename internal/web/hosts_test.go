package web

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
)

// debianHosts is /etc/hosts as Debian 13 writes it (copied from the test VM).
const debianHosts = "127.0.0.1\tlocalhost\n127.0.1.1\tconsola-pruebas\n\n# The following lines are desirable for IPv6 capable hosts\n::1     localhost ip6-localhost ip6-loopback\nff02::1 ip6-allnodes\nff02::2 ip6-allrouters\n"

func TestReplaceHostsLine(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"domain and aliases are kept", "127.0.0.1 localhost\n  127.0.1.1   viejo.lab viejo otro # mio\n", "127.0.0.1 localhost\n127.0.1.1\tservidor1.lab servidor1 otro # mio\n"},
		{"old name on every 127.0.1.1 line", "127.0.1.1 viejo\n127.0.1.1 viejo.lab b\n", "127.0.1.1\tservidor1\n127.0.1.1\tservidor1.lab b\n"},
		{"line without the old name gets the new one first", "127.0.1.1 otro\n", "127.0.1.1\tservidor1 otro\n"},
		{"second line without the old name is untouched", "127.0.1.1 viejo\n127.0.1.1  b\n", "127.0.1.1\tservidor1\n127.0.1.1  b\n"},
		{"same name: nothing changes", "127.0.1.1  servidor1 # x\n", "127.0.1.1  servidor1 # x\n"},
		{"only the domain name: plain name added", "127.0.1.1 viejo.lab\n", "127.0.1.1\tservidor1.lab servidor1\n"},
		{"trailing dot", "127.0.1.1 viejo.\n", "127.0.1.1\tservidor1. servidor1\n"},
		{"names are not repeated", "127.0.1.1 viejo servidor1\n", "127.0.1.1\tservidor1\n"},
		{"a longer name is another name", "127.0.1.1 viejos viejo-2\n", "127.0.1.1\tservidor1 viejos viejo-2\n"},
		{"localhost is never renamed", "127.0.1.1 localhost\n", "127.0.1.1\tservidor1 localhost\n"},
		{"other lines with the old name are untouched", "127.0.0.1 localhost viejo\n127.0.1.1 viejo\n", "127.0.0.1 localhost viejo\n127.0.1.1\tservidor1\n"},
		{"commented line is not the machine's", "# 127.0.1.1 viejo\n127.0.0.1 localhost\n", "# 127.0.1.1 viejo\n127.0.0.1 localhost\n127.0.1.1\tservidor1\n"},
		{"added after 127.0.0.1", "127.0.0.1 localhost\n::1 localhost\n", "127.0.0.1 localhost\n127.0.1.1\tservidor1\n::1 localhost\n"},
		{"after the last 127.0.0.1", "127.0.0.1 localhost\n127.0.0.1 otro\n::1 x\n", "127.0.0.1 localhost\n127.0.0.1 otro\n127.0.1.1\tservidor1\n::1 x\n"},
		{"added first without 127.0.0.1", "::1 localhost\n", "127.0.1.1\tservidor1\n::1 localhost\n"},
		{"empty file", "", "127.0.1.1\tservidor1\n"},
		{"no final line break", "127.0.0.1 localhost", "127.0.0.1 localhost\n127.0.1.1\tservidor1\n"},
		{"last line renamed without line break", "127.0.0.1 localhost\n127.0.1.1 viejo", "127.0.0.1 localhost\n127.0.1.1\tservidor1\n"},
		{"final line break added", "127.0.1.1 viejo\n::1 x", "127.0.1.1\tservidor1\n::1 x\n"},
		{"similar address is another line", "127.0.1.10 otro\n", "127.0.1.1\tservidor1\n127.0.1.10 otro\n"},
		{"CRLF lines keep their ending", "127.0.0.1 localhost\r\n127.0.1.1 viejo\r\n::1 x\r\n", "127.0.0.1 localhost\r\n127.0.1.1\tservidor1\r\n::1 x\r\n"},
	}
	for _, c := range cases {
		if got := replaceHostsLine(c.in, "viejo", "servidor1"); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	if got := replaceHostsLine(debianHosts, "consola-pruebas", "servidor1"); got != strings.Replace(debianHosts, "127.0.1.1\tconsola-pruebas\n", "127.0.1.1\tservidor1\n", 1) {
		t.Errorf("debian file: %q", got)
	}
	if got := replaceHostsLine("127.0.1.1 localhost x\n", "localhost", "servidor1"); got != "127.0.1.1\tservidor1 localhost x\n" {
		t.Errorf("a VM named localhost: %q", got)
	}
	if got := replaceHostsLine("127.0.1.1 x .raro\n", "", "servidor1"); got != "127.0.1.1\tservidor1 x .raro\n" {
		t.Errorf("unknown old name: nothing is renamed, got %q", got)
	}
}

// Only 127.0.1.1 lines change, and the new name is on the first one; every other line
// stays as it was, in order.
func FuzzReplaceHostsKeepsOtherLines(f *testing.F) {
	for _, s := range []string{debianHosts, "", "127.0.1.1 a\n127.0.1.1 b", "# x\n\n127.0.0.1 l\n", "\n\n", "127.0.1.1", "127.0.1.1 viejo.x viejo\r\n"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := replaceHostsLine(in, "viejo", "servidor1")
		if !strings.HasSuffix(out, "\n") {
			t.Fatalf("out %q must end with a line break", out)
		}
		others := func(s string) (keep []string, own []string) {
			for _, line := range strings.SplitAfter(s, "\n") {
				line = strings.TrimSuffix(line, "\n")
				if firstField(line) == hostsLoopback {
					own = append(own, line)
				} else if line != "" {
					keep = append(keep, line)
				}
			}
			return keep, own
		}
		inKeep, _ := others(in)
		outKeep, outOwn := others(out)
		if !slices.Equal(inKeep, outKeep) {
			t.Fatalf("in %q\nout %q: other lines changed", in, out)
		}
		if len(outOwn) == 0 || !slices.Contains(strings.Fields(strings.SplitN(outOwn[0], "#", 2)[0]), "servidor1") {
			t.Fatalf("in %q\nout %q: the first 127.0.1.1 line must name servidor1", in, out)
		}
		for _, line := range outOwn {
			if slices.Contains(strings.Fields(strings.SplitN(line, "#", 2)[0]), "viejo") {
				t.Fatalf("in %q\nout %q: the old name is still on a 127.0.1.1 line", in, out)
			}
		}
	})
}

func TestPlanHostname(t *testing.T) {
	p := planHostname("pruebas", "servidor1")
	want := []executor.Command{{Name: "hostname"}, {Name: "cat", Args: []string{"--", "/etc/hosts"}}}
	if p.Action != "Cambiar nombre de host" || p.Target != "pruebas" || !slices.EqualFunc(p.Commands, want, sameCommand) {
		t.Fatalf("plan = %+v", p)
	}
	next, err := p.Then([]executor.Result{{Stdout: "consola-pruebas\n"}, {Stdout: debianHosts}})
	wantNext := []executor.Command{
		{Name: "sh", Args: []string{"-c", writeFileScript, "sh", replaceHostsLine(debianHosts, "consola-pruebas", "servidor1"), "/etc/hosts",
			"487e31b541d575329a015844caf535f0375a02a6aaf92e5c9d6aed92ad2304f9"}}, // sha256sum of the VM's /etc/hosts
		{Name: "hostnamectl", Args: []string{"set-hostname", "servidor1"}},
		{Name: "agetty", Args: []string{"--reload"}},
		{Name: "sh", Args: []string{"-c", verifyHostnameScript, "sh", "servidor1"}},
	}
	if err != nil || !slices.EqualFunc(next, wantNext, sameCommand) {
		t.Fatalf("Then = %v, %v\nwant %v", next, err, wantNext)
	}

	res := []executor.Result{{Stdout: "consola-pruebas\n"}, {}, {}, {}, {}, {}}
	if got := p.Success(res); !strings.Contains(got, "ahora se llama «servidor1» (antes «consola-pruebas»)") || !strings.Contains(got, "antes del primer cambio está en /etc/hosts.consola-original") {
		t.Errorf("success: %q", got)
	}
	res[0].Stdout = "servidor1\n"
	if got := p.Success(res); !strings.Contains(got, "ya se llamaba «servidor1»") {
		t.Errorf("same name: %q", got)
	}

	if _, err := p.Then([]executor.Result{{Stdout: "x\n"}, {Stdout: strings.Repeat("#\n", maxHostsSize)}}); err == nil || !strings.Contains(err.Error(), "100 KB") {
		t.Errorf("a huge /etc/hosts: err = %v", err)
	}

	write, verify := wantNext[0].String(), wantNext[3].String()
	for _, c := range []struct {
		step oplog.Step
		want string
	}{
		{oplog.Step{Cmd: want[1].String(), ExitCode: 1}, "No se pudo leer /etc/hosts"},
		{oplog.Step{Cmd: write, ExitCode: fileChangedCode}, "cambió mientras la consola lo leía"},
		{oplog.Step{Cmd: write, ExitCode: 1}, "No se pudo escribir /etc/hosts"},
		{oplog.Step{Cmd: wantNext[1].String(), ExitCode: 1}, "repetir la operación es seguro"},
		{oplog.Step{Cmd: wantNext[2].String(), ExitCode: 1}, "solo falló volver a mostrar"},
		{oplog.Step{Cmd: want[0].String(), ExitCode: fileChangedCode}, ""},
		{oplog.Step{Cmd: verify, ExitCode: hostnameMismatchCode}, "no quedó con el nombre «servidor1»"},
		{oplog.Step{Cmd: verify, ExitCode: nameNotResolvedCode}, "/etc/hosts no resuelve ese nombre"},
		{oplog.Step{Cmd: verify, ExitCode: 1}, ""},
		{oplog.Step{Cmd: want[0].String(), ExitCode: hostnameMismatchCode}, ""},
		{oplog.Step{Cmd: want[0].String(), ExitCode: nameNotResolvedCode}, ""},
	} {
		got := p.Explain(c.step)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s code %d: %q, want %q", c.step.Cmd, c.step.ExitCode, got, c.want)
		}
	}
}

func TestVerifyHostnameScriptInARealShell(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		_ = os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755)
	}
	cases := []struct {
		host, getentCode string
		wantCode         int
	}{
		{"servidor1", "0", 0},
		{"otro", "0", hostnameMismatchCode},
		{"servidor1", "2", nameNotResolvedCode},
	}
	for _, c := range cases {
		write("hostname", "echo "+c.host+"\n")
		write("getent", "[ \"$*\" = \"-s files hosts servidor1\" ] || exit 9\necho '127.0.1.1 servidor1'\nexit "+c.getentCode+"\n")
		cmd := exec.Command("/bin/sh", "-c", verifyHostnameScript, "sh", "servidor1")
		cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin"}
		out, err := cmd.Output()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		if code != c.wantCode || !strings.HasPrefix(string(out), "hostname: "+c.host+"\n") {
			t.Errorf("host %s getent %s: code %d out %q, want code %d", c.host, c.getentCode, code, out, c.wantCode)
		}
	}
}

func TestHostnameFromTheForm(t *testing.T) {
	p := planHostname("pruebas", "servidor1")
	fake, h, log := fakeVM(t, map[string]executor.Result{
		p.Commands[0].String(): {Stdout: "consola-pruebas\n"},
		p.Commands[1].String(): {Stdout: debianHosts},
	})
	rec := serve(t, h, postForm("/preparacion/hostname", url.Values{"hostname": {" Servidor1 "}})) // host names ignore case
	e := resultEntry(t, rec, log)
	if !e.OK || e.Action != "Cambiar nombre de host" || !strings.Contains(e.Message, "ahora se llama «servidor1»") {
		t.Fatalf("entry = %+v", e)
	}
	calls := fake.Calls()
	if len(calls) != 6 || calls[2].Args[3] != replaceHostsLine(debianHosts, "consola-pruebas", "servidor1") || calls[3].Name != "hostnamectl" {
		t.Fatalf("ran %v", calls)
	}
}

func TestInvalidHostnameRunsNothing(t *testing.T) {
	for _, name := range []string{"", "123", "0", "servidor_1", "-servidor", "servidor-", "a.b", "localhost", "x; reboot", strings.Repeat("a", 64)} {
		fake, h, _ := fakeVM(t, nil)
		rec := serve(t, h, postForm("/preparacion/hostname", url.Values{"hostname": {name}}))
		b := body(rec)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(b, `aria-invalid="true" aria-describedby="hostname-error hostname-ayuda"`) {
			t.Errorf("%q: status %d, want 422 with the error next to the field", name, rec.Code)
		}
		if n := strings.Count(b, "Revisa los datos antes de continuar"); n != 1 {
			t.Errorf("%q: %d error summaries, want 1", name, n)
		}
		if len(fake.Calls()) != 0 {
			t.Errorf("%q: commands ran", name)
		}
	}
}

func TestHostnameFormOnThePage(t *testing.T) {
	b := body(do(t, http.MethodGet, "/preparacion"))
	for _, want := range []string{
		`action="/preparacion/hostname" data-operacion="Cambiar nombre de host"`,
		`<label class="form-label" for="hostname">`,
		`aria-describedby="hostname-ayuda">`,
	} {
		if !strings.Contains(b, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(b, "En construcción") && !strings.Contains(b, "Generar y autorizar las llaves de root") {
		t.Error("only Part B's placeholder may remain under construction")
	}
}

// The whole path on a copy of the VM's file: what Then computes, written by the real
// script, gives exactly the expected bytes; and a file that changed after it was read
// (or a read that came back incomplete) is never overwritten.
func TestHostnameWriteRoundTripInARealShell(t *testing.T) {
	dir := t.TempDir()
	hosts := filepath.Join(dir, "hosts")
	_ = os.WriteFile(hosts, []byte(debianHosts), 0o644)
	p := planHostname("pruebas", "servidor1")
	next, err := p.Then([]executor.Result{{Stdout: "consola-pruebas\n"}, {Stdout: debianHosts}})
	if err != nil {
		t.Fatal(err)
	}
	args := next[0].Args
	args[len(args)-2] = hosts // the same script, on the copy
	if code := runSh(t, args[1], args[3:]...); code != 0 {
		t.Fatalf("write: exit %d", code)
	}
	want := strings.Replace(debianHosts, "consola-pruebas", "servidor1", 1)
	if got := readFile(t, hosts); got != want {
		t.Fatalf("hosts = %q, want %q", got, want)
	}
	if got := readFile(t, hosts+".consola-original"); got != debianHosts {
		t.Fatalf("original = %q", got)
	}

	// Read when the file was different (or the read was cut short): nothing is written.
	for name, read := range map[string]string{"incomplete read": "", "file changed meanwhile": debianHosts} {
		next, _ := p.Then([]executor.Result{{Stdout: "consola-pruebas\n"}, {Stdout: read}})
		args := next[0].Args
		args[len(args)-2] = hosts
		before := readFile(t, hosts)
		if code := runSh(t, args[1], args[3:]...); code != fileChangedCode || readFile(t, hosts) != before {
			t.Errorf("%s: exit %d, want %d and the file untouched", name, code, fileChangedCode)
		}
	}
	// The file disappeared after it was read: nothing is created.
	gone := filepath.Join(dir, "no-esta")
	if code := runSh(t, args[1], "x\n", gone, sha256Hex(debianHosts)); code != fileChangedCode {
		t.Errorf("file gone after reading: exit %d, want %d", code, fileChangedCode)
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Error("a file that disappeared must not be created again")
	}
	if entries, _ := filepath.Glob(hosts + ".consola-*"); len(entries) != 1 {
		t.Fatalf("leftover files: %v (only the original may stay)", entries)
	}
}

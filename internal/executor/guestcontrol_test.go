package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// recorder replaces VBoxManage: it records the arguments and answers like the real tool.
type recorder struct {
	args           [][]string
	stdout, stderr string
	exitCode       int
	err            error
	onRun          func(args []string) // optional side effect (e.g. create the copied file)
}

func (r *recorder) exec(_ context.Context, args []string) (string, string, int, error) {
	r.args = append(r.args, append([]string(nil), args...))
	if r.onRun != nil {
		r.onRun(args)
	}
	return r.stdout, r.stderr, r.exitCode, r.err
}

func newTestGC(t *testing.T, r *recorder) *GuestControl {
	t.Helper()
	pf := filepath.Join(t.TempDir(), "guest-pass")
	if err := os.WriteFile(pf, []byte("s3creta"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := &GuestControl{VM: "consola-pruebas", User: "root", PasswordFile: pf}
	g.exec = r.exec
	return g
}

func TestGuestControlName(t *testing.T) {
	if got := (&GuestControl{}).Name(); got != "Guest Additions" {
		t.Fatalf("Name() = %q", got)
	}
}

func TestGuestControlRunArguments(t *testing.T) {
	r := &recorder{stderr: "\n" + exitMarker + "0\n"}
	g := newTestGC(t, r)
	if _, err := g.Run(context.Background(), Command{Name: "apt-get", Args: []string{"install", "-y", "tree; reboot"}}); err != nil {
		t.Fatal(err)
	}
	want := []string{"guestcontrol", "consola-pruebas", "run",
		"--username", "root", "--passwordfile", g.PasswordFile, "--quiet",
		"--exe", "/bin/sh", "--wait-stdout", "--wait-stderr",
		"--", "-c", runWrapper, "apt-get", "xinstall", "x-y", "xtree; reboot"}
	if got := r.args[0]; !slices.Equal(got, want) {
		t.Fatalf("args =\n%q\nwant\n%q", got, want)
	}
}

func TestGuestControlNeverPutsThePasswordInArguments(t *testing.T) {
	r := &recorder{stderr: exitMarker + "0\n"}
	g := newTestGC(t, r)
	_, _ = g.Run(context.Background(), Command{Name: "hostname"})
	_ = g.CopyTo(context.Background(), writeTemp(t, "x"), "/tmp/consola-x")
	for _, args := range r.args {
		for _, a := range args {
			if strings.Contains(a, "s3creta") || a == "--password" {
				t.Fatalf("the password reached the command line: %q", args)
			}
		}
	}
}

func TestGuestControlRefusesAnUnsafePasswordFile(t *testing.T) {
	r := &recorder{stderr: exitMarker + "0\n"}
	g := newTestGC(t, r)
	if err := os.Chmod(g.PasswordFile, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := g.Run(context.Background(), Command{Name: "hostname"})
	var ce *ChannelError
	if !errors.As(err, &ce) || !strings.Contains(ce.Msg, "chmod 600") {
		t.Fatalf("err = %v, want a ChannelError telling to chmod 600", err)
	}
	if len(r.args) != 0 {
		t.Fatal("VBoxManage must not run with a password file other users can read")
	}
	g.PasswordFile = filepath.Join(t.TempDir(), "no-existe")
	if _, err := g.Run(context.Background(), Command{Name: "hostname"}); !errors.As(err, &ce) {
		t.Fatalf("missing password file: err = %v, want a ChannelError", err)
	}
	g.PasswordFile = t.TempDir()
	_ = os.Chmod(g.PasswordFile, 0o700) // a private directory: only the "regular file" rule can reject it
	if _, err := g.Run(context.Background(), Command{Name: "hostname"}); !errors.As(err, &ce) {
		t.Fatalf("a directory as password file: err = %v, want a ChannelError", err)
	}
	if len(r.args) != 0 {
		t.Fatal("VBoxManage must not run with an unusable password file")
	}
}

func TestGuestControlPassesTheTimeoutToTheGuest(t *testing.T) {
	r := &recorder{stderr: exitMarker + "0\n"}
	g := newTestGC(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, _ = g.Run(ctx, Command{Name: "hostname"})
	i := slices.Index(r.args[0], "--timeout")
	if i < 0 || i+1 >= len(r.args[0]) {
		t.Fatalf("args = %q: want --timeout so the guest process stops too", r.args[0])
	}
	if ms, err := strconv.Atoi(r.args[0][i+1]); err != nil || ms < 89000 || ms > 90000 {
		t.Fatalf("--timeout %q, want between 89000 and 90000 ms", r.args[0][i+1])
	}
}

func TestGuestControlNeverSendsAZeroOrNegativeTimeout(t *testing.T) {
	// VirtualBox reads --timeout 0 as "no limit": an expired deadline must run nothing.
	r := &recorder{stderr: exitMarker + "0\n"}
	g := newTestGC(t, r)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer cancel()
	if _, err := g.Run(ctx, Command{Name: "hostname"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if len(r.args) != 0 {
		t.Fatalf("an expired deadline must not reach VBoxManage: %q", r.args)
	}
	if got := guestTimeoutMS(time.Now().Add(300 * time.Microsecond)); got != "1" {
		t.Fatalf("less than 1 ms left must round up to 1, got %q", got)
	}
}

func TestGuestControlRejectsStdinAndInvalidCommands(t *testing.T) {
	r := &recorder{stderr: exitMarker + "0\n"}
	g := newTestGC(t, r)
	_, err := g.Run(context.Background(), Command{Name: "chpasswd", Stdin: "ana:secreta"})
	var ce *ChannelError
	if !errors.As(err, &ce) || !strings.Contains(ce.Msg, "SSH") {
		t.Fatalf("stdin: err = %v, want a ChannelError pointing to SSH", err)
	}
	if _, err := g.Run(context.Background(), Command{Name: "apt\x00get"}); err == nil {
		t.Fatal("a command that fails Check must not run")
	}
	if len(r.args) != 0 {
		t.Fatal("nothing must reach VBoxManage")
	}
}

func TestParseRunResult(t *testing.T) {
	cases := []struct {
		name               string
		stdout, stderr     string
		wantCode           int
		wantStdout, wantSE string
	}{
		{"success", "pruebas\n", "\n" + exitMarker + "0\n", 0, "pruebas\n", ""},
		{"exit code kept", "", "E: Unable to locate package x\n\n" + exitMarker + "100\n", 100, "", "E: Unable to locate package x\n"},
		{"systemctl status stopped", "inactive\n", "\n" + exitMarker + "3\n", 3, "inactive\n", ""},
		{"forged marker in output is ignored", "", exitMarker + "0\nreal error\n\n" + exitMarker + "2\n", 2, "", exitMarker + "0\nreal error\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := parseRun(c.stdout, c.stderr)
			if err != nil {
				t.Fatal(err)
			}
			if res.ExitCode != c.wantCode || res.Stdout != c.wantStdout || res.Stderr != c.wantSE {
				t.Fatalf("got %+v", res)
			}
		})
	}
	if _, err := parseRun("", "VBoxManage: error: Machine is not running\n"); err == nil {
		t.Fatal("no marker means the command never ran: it must be a channel error")
	}
	if _, err := parseRun("", "un programa imprimió "+exitMarker+"0\n"); err == nil {
		t.Fatal("a marker that does not start a line was not printed by the wrapper")
	}
}

func TestChannelErrorsAreExplained(t *testing.T) {
	cases := map[string]string{
		"VBoxManage: error: Machine \"balanceador\" is not running (currently powered off)!":                            "está apagada", // real VirtualBox 7.2 text
		"VBoxManage: error: Machine \"consola-pruebas\" is not currently running":                                       "está apagada",
		"VBoxManage: error: The guest execution service is not ready (yet)":                                             "Guest Additions",
		"VBoxManage: error: The specified user was not able to logon on guest":                                          "contraseña",
		"VBoxManage: error: Could not find a registered machine named 'x'":                                              "no existe en VirtualBox",
		"VBoxManage: error: algo raro":                                                                                  "Guest Additions",
		"VBoxManage: error: File \"/etc/no-existe\" not found (VERR_FILE_NOT_FOUND)":                                    "no existe en la VM",
		"VBoxManage: error: Querying guest filesystem object information failed: No such file or directory \"/tmp/x\".": "no existe en la VM",
		"warning: authentication fallida en el programa\nVBoxManage: error: algo raro":                                  "revisa que estén instaladas", // guest output never decides the message
	}
	for stderr, want := range cases {
		r := &recorder{stderr: stderr, exitCode: 1}
		g := newTestGC(t, r)
		_, err := g.Run(context.Background(), Command{Name: "hostname"})
		var ce *ChannelError
		if !errors.As(err, &ce) {
			t.Fatalf("%q: err = %v, want ChannelError", stderr, err)
		}
		if !strings.Contains(ce.Msg, want) || !strings.Contains(ce.Error(), stderr) {
			t.Errorf("%q: Msg = %q (want %q), Error() must keep the technical detail", stderr, ce.Msg, want)
		}
	}
}

func TestGuestControlTimeoutDuringTheCommandIsReported(t *testing.T) {
	// The deadline passes while VBoxManage runs: Go kills it, it returns no marker and
	// exit code -1 without a Go error. Only the context says it was a timeout.
	r := &recorder{exitCode: -1}
	r.onRun = func([]string) { time.Sleep(80 * time.Millisecond) }
	g := newTestGC(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := g.Run(ctx, Command{Name: "sleep", Args: []string{"100"}}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded so the runner reports a timeout", err)
	}
}

func TestGuestControlTimeoutIsReported(t *testing.T) {
	// What really happens: at the deadline Go kills VBoxManage, which then exits with no
	// marker and no Go error (exit code -1). Only the context says it was a timeout.
	r := &recorder{exitCode: -1}
	g := newTestGC(t, r)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := g.Run(ctx, Command{Name: "sleep", Args: []string{"100"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded so the runner reports a timeout", err)
	}
}

func TestRunWrapperInARealShell(t *testing.T) {
	// The wrapper is what runs inside the VM; /bin/sh here behaves like the guest's.
	// It is a fixed script: name and arguments are positional parameters, never part of it.
	cases := []struct {
		name     string
		args     []string
		wantOut  string
		wantCode string
	}{
		{"printf", []string{"[%s]", "a b", "$(id)", "it's", "", "x", "-n"}, "[a b][$(id)][it's][][x][-n]", "0"},
		{"false", nil, "", "1"},
		{"sh", []string{"-c", "exit 3"}, "", "3"},
		{"comando-que-no-existe", nil, "", "127"},
		{"env", []string{"printf", "externo"}, "externo", "0"}, // external programs found through PATH
		{"exit", []string{"0"}, "", "127"},                     // shell builtins never run: only programs
		{"eval", []string{"echo inyectado"}, "", "127"},
	}
	for _, c := range cases {
		argv := []string{"-c", runWrapper, c.name}
		for _, a := range c.args {
			argv = append(argv, argPrefix+a)
		}
		cmd := exec.Command("/bin/sh", argv...)
		cmd.Env = []string{"PATH="} // worst case for a guest login: an empty PATH, so the wrapper must set it
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		_ = cmd.Run()
		res, err := parseRun(stdout.String(), stderr.String())
		if err != nil {
			t.Fatalf("%s: %v (stderr %q)", c.name, err, stderr.String())
		}
		if stdout.String() != c.wantOut || strconv.Itoa(res.ExitCode) != c.wantCode {
			t.Errorf("%s: stdout %q code %d, want %q code %s", c.name, stdout.String(), res.ExitCode, c.wantOut, c.wantCode)
		}
	}
}

func TestCopyToPassesTheFullGuestPath(t *testing.T) {
	var staged string
	r := &recorder{onRun: func(args []string) {
		staged = args[len(args)-2]
		b, err := os.ReadFile(staged)
		if err != nil || string(b) != "contenido" {
			t.Errorf("staged file %s: %q, %v", staged, b, err)
		}
		if st, _ := os.Stat(staged); st.Mode().Perm() != 0o640 {
			t.Errorf("staged mode %v, want the source mode 0640", st.Mode().Perm())
		}
	}}
	g := newTestGC(t, r)
	src := writeTemp(t, "contenido")
	_ = os.Chmod(src, 0o640)
	old := syscall.Umask(0o077) // a strict umask must not change the copied mode
	defer syscall.Umask(old)
	if err := g.CopyTo(context.Background(), src, "/etc/consola/issue-nuevo"); err != nil {
		t.Fatal(err)
	}
	args := r.args[0]
	want := []string{"guestcontrol", "consola-pruebas", "copyto", "--username", "root", "--passwordfile", g.PasswordFile, "--quiet", staged, "/etc/consola/issue-nuevo"}
	if !slices.Equal(args, want) || !filepath.IsAbs(staged) {
		t.Fatalf("args = %q, want %q", args, want)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatal("the staging copy must be removed afterwards")
	}
}

func TestCopyFromLeavesThePrivateModeOnTheHost(t *testing.T) {
	r := &recorder{}
	r.onRun = func(args []string) { // VBoxManage writes the last argument
		_ = os.WriteFile(args[len(args)-1], []byte("root:$y$...\n"), 0o644)
	}
	g := newTestGC(t, r)
	local := filepath.Join(t.TempDir(), "shadow")
	if err := g.CopyFrom(context.Background(), "/etc/shadow", local); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(local); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v: a file copied from the VM may hold secrets, it must be 0600", st.Mode().Perm())
	}
}

func TestCopyFromFailureLeavesNothingBehind(t *testing.T) {
	r := &recorder{stderr: "VBoxManage: error: File \"/etc/x\" not found", exitCode: 1}
	g := newTestGC(t, r)
	dir := t.TempDir()
	if err := g.CopyFrom(context.Background(), "/etc/x", filepath.Join(dir, "x")); err == nil {
		t.Fatal("a failed copy must return an error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a failed copy left %d entries in the destination directory", len(entries))
	}
}

func TestCopyFromMovesTheFileToItsLocalPath(t *testing.T) {
	r := &recorder{}
	r.onRun = func(args []string) { // VBoxManage writes the last argument
		_ = os.WriteFile(args[len(args)-1], []byte("127.0.1.1 pruebas\n"), 0o644)
	}
	g := newTestGC(t, r)
	local := filepath.Join(t.TempDir(), "hosts-de-la-vm")
	if err := g.CopyFrom(context.Background(), "/etc/hosts", local); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(local); string(b) != "127.0.1.1 pruebas\n" {
		t.Fatalf("local file = %q", b)
	}
	if got := r.args[0]; got[2] != "copyfrom" || got[len(got)-2] != "/etc/hosts" || filepath.Dir(got[len(got)-1]) == filepath.Dir(local) {
		t.Fatalf("args = %q", got)
	}
}

func TestCopiesRejectUnsafeRemotePaths(t *testing.T) {
	g := newTestGC(t, &recorder{})
	for _, p := range []string{"relativo/x", "/tmp/../etc/shadow", "/tmp/a\nb", "/", ""} {
		if err := g.CopyTo(context.Background(), writeTemp(t, "x"), p); err == nil {
			t.Errorf("CopyTo(%q) must be rejected", p)
		}
		if err := g.CopyFrom(context.Background(), p, filepath.Join(t.TempDir(), "x")); err == nil {
			t.Errorf("CopyFrom(%q) must be rejected", p)
		}
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "origen")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

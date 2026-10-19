//go:build vm

// Tests against a real VM through Guest Additions. They run only on demand:
//
//	CONSOLA_VM=<VirtualBox VM name> CONSOLA_GUEST_PASSFILE=keys/guest-pass \
//	  go test -tags vm -count=1 -run VM ./internal/executor/
//
// Optional: CONSOLA_GUEST_USER (default root). The VM must be running with Guest Additions.
package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func realGuest(t *testing.T) *GuestControl {
	t.Helper()
	vm, pf := os.Getenv("CONSOLA_VM"), os.Getenv("CONSOLA_GUEST_PASSFILE")
	if vm == "" || pf == "" {
		t.Skip("set CONSOLA_VM and CONSOLA_GUEST_PASSFILE to run against a real VM")
	}
	user := os.Getenv("CONSOLA_GUEST_USER")
	if user == "" {
		user = "root"
	}
	return &GuestControl{VM: vm, User: user, PasswordFile: pf}
}

func runVM(t *testing.T, g *GuestControl, c Command) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := g.Run(ctx, c)
	if err != nil {
		t.Fatalf("%s: %v", c.String(), err)
	}
	return res
}

func TestVMExitCodesAreTheCommandsOwn(t *testing.T) {
	g := realGuest(t)
	for _, c := range []struct {
		cmd  Command
		code int
	}{
		{Command{Name: "true"}, 0},
		{Command{Name: "false"}, 1},
		{Command{Name: "sh", Args: []string{"-c", "exit 3"}}, 3},
		{Command{Name: "comando-que-no-existe"}, 127},
	} {
		if got := runVM(t, g, c.cmd).ExitCode; got != c.code {
			t.Errorf("%s: exit %d, want %d", c.cmd.String(), got, c.code)
		}
	}
}

func TestVMArgumentsArriveExactly(t *testing.T) {
	g := realGuest(t)
	res := runVM(t, g, Command{Name: "printf", Args: []string{"[%s]", "a b", "$(id)", "it's", "", "x;y"}})
	if want := "[a b][$(id)][it's][][x;y]"; res.Stdout != want {
		t.Fatalf("stdout %q, want %q", res.Stdout, want)
	}
	if res.Stderr != "" {
		t.Fatalf("stderr must be empty after removing the marker, got %q", res.Stderr)
	}
}

func TestVMStdoutAndStderrAreSeparate(t *testing.T) {
	g := realGuest(t)
	res := runVM(t, g, Command{Name: "sh", Args: []string{"-c", "echo salida; echo error >&2; exit 4"}})
	if res.Stdout != "salida\n" || res.Stderr != "error\n" || res.ExitCode != 4 {
		t.Fatalf("got %+v", res)
	}
}

func TestVMHostname(t *testing.T) {
	g := realGuest(t)
	if res := runVM(t, g, Command{Name: "hostname"}); strings.TrimSpace(res.Stdout) == "" {
		t.Fatal("hostname printed nothing")
	}
}

func TestVMCopyRoundTrip(t *testing.T) {
	g := realGuest(t)
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "origen")
	content := "línea 1\nlínea 2 con ñ\n"
	if err := os.WriteFile(src, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	remote := "/tmp/consola-prueba-" + time.Now().Format("150405.000000")
	t.Cleanup(func() { _, _ = g.Run(context.Background(), Command{Name: "rm", Args: []string{"-f", remote}}) })
	if err := g.CopyTo(ctx, src, remote); err != nil {
		t.Fatalf("CopyTo: %v", err)
	}
	back := filepath.Join(t.TempDir(), "vuelta")
	if err := g.CopyFrom(ctx, remote, back); err != nil {
		t.Fatalf("CopyFrom: %v", err)
	}
	if b, _ := os.ReadFile(back); string(b) != content {
		t.Fatalf("round trip = %q, want %q", b, content)
	}
}

func TestVMTimeoutStopsTheCommand(t *testing.T) {
	g := realGuest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, err := g.Run(ctx, Command{Name: "sleep", Args: []string{"60"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if time.Since(start) > 15*time.Second {
		t.Fatalf("took %v: the timeout did not stop the command", time.Since(start))
	}
}

func TestVMWrongNameIsExplained(t *testing.T) {
	g := realGuest(t)
	g.VM = "vm-que-no-existe-" + time.Now().Format("150405")
	_, err := g.Run(context.Background(), Command{Name: "true"})
	var ce *ChannelError
	if !errors.As(err, &ce) || !strings.Contains(ce.Msg, "no existe en VirtualBox") {
		t.Fatalf("err = %v, want the 'no existe en VirtualBox' explanation", err)
	}
}

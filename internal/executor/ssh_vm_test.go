//go:build vm

// Tests against a real VM over the SSH channel. They run only on demand:
//
//	CONSOLA_SSH_HOST=<VM IP> \
//	  go test -tags vm -count=1 -run VMSSH ./internal/executor/
//
// Optional: CONSOLA_SSH_USER (default root), CONSOLA_SSH_KEY
// (keys/consola_ed25519), CONSOLA_SSH_KNOWNHOSTS (keys/known_hosts) and
// CONSOLA_SSH_DOWN_HOST (an IP that never answers, to prove the channel
// error). The VM must have the console's public key in root's authorized_keys.
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

func realSSH(t *testing.T) *SSH {
	t.Helper()
	host := os.Getenv("CONSOLA_SSH_HOST")
	if host == "" {
		t.Skip("set CONSOLA_SSH_HOST to run against a real VM")
	}
	user := os.Getenv("CONSOLA_SSH_USER")
	if user == "" {
		user = "root"
	}
	key := os.Getenv("CONSOLA_SSH_KEY")
	if key == "" {
		key = "keys/consola_ed25519"
	}
	known := os.Getenv("CONSOLA_SSH_KNOWNHOSTS")
	if known == "" {
		known = "keys/known_hosts"
	}
	return &SSH{Host: host, User: user, KeyPath: key, KnownHosts: known, Port: 22}
}

func runSSHVM(t *testing.T, s *SSH, c Command) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := s.Run(ctx, c)
	if err != nil {
		t.Fatalf("%s: %v", c.String(), err)
	}
	return res
}

func TestVMSSHRunArgumentsArriveExactly(t *testing.T) {
	s := realSSH(t)
	res := runSSHVM(t, s, Command{Name: "printf", Args: []string{"[%s]", "a b", "$(id)", "it's", "", "x;y"}})
	if want := "[a b][$(id)][it's][][x;y]"; res.Stdout != want {
		t.Fatalf("stdout %q, want %q", res.Stdout, want)
	}
	if res.Stderr != "" {
		t.Fatalf("stderr must be empty, got %q", res.Stderr)
	}
}

func TestVMSSHStdoutAndStderrAreSeparate(t *testing.T) {
	s := realSSH(t)
	res := runSSHVM(t, s, Command{Name: "sh", Args: []string{"-c", "echo salida; echo error >&2; exit 4"}})
	if res.Stdout != "salida\n" || res.Stderr != "error\n" || res.ExitCode != 4 {
		t.Fatalf("got %+v", res)
	}
}

func TestVMSSHExitCodesAreTheCommandsOwn(t *testing.T) {
	s := realSSH(t)
	for _, c := range []struct {
		cmd  Command
		code int
	}{
		{Command{Name: "true"}, 0},
		{Command{Name: "false"}, 1},
		{Command{Name: "sh", Args: []string{"-c", "exit 3"}}, 3},
		{Command{Name: "comando-que-no-existe"}, 127},
	} {
		if got := runSSHVM(t, s, c.cmd).ExitCode; got != c.code {
			t.Errorf("%s: exit %d, want %d", c.cmd.String(), got, c.code)
		}
	}
}

func TestVMSSHHostname(t *testing.T) {
	s := realSSH(t)
	if res := runSSHVM(t, s, Command{Name: "hostname"}); strings.TrimSpace(res.Stdout) == "" {
		t.Fatal("hostname printed nothing")
	}
}

func TestVMSSHCopyRoundTrip(t *testing.T) {
	s := realSSH(t)
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "origen")
	content := "línea 1\nlínea 2 con ñ\n"
	if err := os.WriteFile(src, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	remote := "/tmp/consola-ssh-prueba-" + time.Now().Format("150405.000000")
	t.Cleanup(func() { _, _ = s.Run(context.Background(), Command{Name: "rm", Args: []string{"-f", remote}}) })
	if err := s.CopyTo(ctx, src, remote); err != nil {
		t.Fatalf("CopyTo: %v", err)
	}
	back := filepath.Join(t.TempDir(), "vuelta")
	if err := s.CopyFrom(ctx, remote, back); err != nil {
		t.Fatalf("CopyFrom: %v", err)
	}
	if b, _ := os.ReadFile(back); string(b) != content {
		t.Fatalf("round trip = %q, want %q", b, content)
	}
}

func TestVMSSHChannelErrorWhenTheHostDoesNotAnswer(t *testing.T) {
	down := os.Getenv("CONSOLA_SSH_DOWN_HOST")
	if down == "" {
		t.Skip("set CONSOLA_SSH_DOWN_HOST to an IP that never answers")
	}
	s := realSSH(t)
	s.Host = down
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	_, err := s.Run(ctx, Command{Name: "true"})
	var ce *ChannelError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a channel error", err)
	}
	if time.Since(start) > 15*time.Second {
		t.Fatalf("took %v: ConnectTimeout did not bound the wait", time.Since(start))
	}
}

package executor

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// newTestSSH builds an SSH adapter whose transport is replaced by a recorder.
func newTestSSH(record func(name string, args []string, stdin string) (string, string, int, error)) *SSH {
	s := &SSH{
		Host:       "192.168.40.60",
		User:       "root",
		KeyPath:    "keys/consola_ed25519",
		KnownHosts: "keys/known_hosts",
		Port:       22,
	}
	s.run = func(_ context.Context, name string, args []string, stdin string) (string, string, int, error) {
		return record(name, args, stdin)
	}
	return s
}

func TestSSHArgsAreFixedAndOrdered(t *testing.T) {
	var name string
	var args []string
	s := newTestSSH(func(n string, a []string, _ string) (string, string, int, error) {
		name, args = n, a
		return "consola-pruebas\n", "", 0, nil
	})
	res, err := s.Run(context.Background(), Command{Name: "hostname"})
	if err != nil || res.Stdout != "consola-pruebas\n" {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	if name != "ssh" {
		t.Fatalf("the adapter executed %q, want ssh", name)
	}
	want := []string{
		"-i", "keys/consola_ed25519",
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=keys/known_hosts",
		"-o", "ConnectTimeout=10",
		"-p", "22",
		"root@192.168.40.60",
		"--", "hostname",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %q, want %q", args, want)
	}
}

func TestSSHRemoteCommandIsOneQuotedArgument(t *testing.T) {
	var args []string
	s := newTestSSH(func(_ string, a []string, _ string) (string, string, int, error) {
		args = a
		return "", "", 0, nil
	})
	c := Command{Name: "printf", Args: []string{"[%s]", "a;b $(id) 't' with space"}}
	if _, err := s.Run(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	i := slices.Index(args, "--")
	if i < 0 {
		t.Fatalf("the ssh call must include the -- separator, got %q", args)
	}
	tail := args[i+1:]
	if len(tail) != 1 || tail[0] != c.String() {
		t.Fatalf("the remote command must be exactly one already quoted argument %q, got %d words: %q", c.String(), len(tail), tail)
	}
}

func TestSSHRunRejectsInvalidCommands(t *testing.T) {
	called := false
	s := newTestSSH(func(string, []string, string) (string, string, int, error) {
		called = true
		return "", "", 0, nil
	})
	for _, c := range []Command{
		{Name: ""},
		{Name: "FOO=bar", Args: []string{"x"}},
		{Name: "apt\x00get"},
		{Name: "apt-get", Args: []string{"tree\x00"}},
	} {
		if _, err := s.Run(context.Background(), c); err == nil {
			t.Errorf("Check must refuse %v, got no error", c)
		}
	}
	if called {
		t.Fatal("ssh must not run for a command that fails Check")
	}
}

func TestSSHExit255IsAChannelError(t *testing.T) {
	s := newTestSSH(func(string, []string, string) (string, string, int, error) {
		return "", "ssh: connect to host 192.168.40.60 port 22: Connection refused", 255, nil
	})
	res, err := s.Run(context.Background(), Command{Name: "hostname"})
	if res.ExitCode != 255 || res.Cmd != "hostname" {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	var ce *ChannelError
	if !errors.As(err, &ce) {
		t.Fatalf("exit 255 must be a channel error, got %v", err)
	}
	if !strings.Contains(err.Error(), "Connection refused") {
		t.Fatalf("the channel error must keep the technical detail, got %q", err.Error())
	}
}

func TestSSHRemoteExitCodeIsReturned(t *testing.T) {
	s := newTestSSH(func(string, []string, string) (string, string, int, error) {
		return "out\n", "err\n", 7, nil
	})
	res, err := s.Run(context.Background(), Command{Name: "false"})
	if err != nil {
		t.Fatalf("the remote exit code 7 is not a channel failure, got %v", err)
	}
	if res.ExitCode != 7 || res.Stdout != "out\n" || res.Stderr != "err\n" || res.Cmd != "false" {
		t.Fatalf("Run = %+v", res)
	}
}

func TestSSHStdinIsPassed(t *testing.T) {
	var got string
	s := newTestSSH(func(_ string, _ []string, stdin string) (string, string, int, error) {
		got = stdin
		return "", "", 0, nil
	})
	c := Command{Name: "chpasswd", Stdin: "ana:secreta-123"}
	if _, err := s.Run(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got != "ana:secreta-123" {
		t.Fatalf("the secret must travel through stdin, got %q", got)
	}
}

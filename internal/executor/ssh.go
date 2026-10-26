// SSH implements Executor using the system's ssh and scp clients, without new
// dependencies. The remote command travels as one single argument already
// quoted for the remote shell — Command.String applies the package's Quote —
// so no argument of the modules can be turned into injected syntax.
package executor

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// SSH connects to one VM as user@host with the console's key. The run field is
// replaceable in tests; when it is nil, the default runs the named system
// client, which is also what another package (the startup channel) gets.
type SSH struct {
	Host, User, KeyPath, KnownHosts string
	Port                            int // 22 if left at zero
	run                             func(ctx context.Context, name string, args []string, stdin string) (stdout, stderr string, exitCode int, err error)
}

// Name is the channel shown to the user and stored in the operation log.
func (s *SSH) Name() string { return "SSH" }

// Run delivers c to the remote shell and reports what it produced. The client
// exit code 255 is the transport failing (VM off, no key, changed host key):
// it becomes a ChannelError so the Runner can say "no channel" instead of
// blaming the command. Any other code is the remote command's own code.
func (s *SSH) Run(ctx context.Context, c Command) (Result, error) {
	start := time.Now()
	if err := c.Check(); err != nil {
		return Result{}, err
	}
	args := append(s.options("-p"), s.target(), "--", c.String())
	out, errOut, code, runErr := s.exec(ctx, "ssh", args, c.Stdin)
	res := Result{Cmd: c.String(), Stdout: out, Stderr: errOut, ExitCode: code, Duration: time.Since(start)}
	switch {
	case runErr != nil:
		// Solo el código 255 es fallo de transporte SSH; el resto son códigos del comando remoto.
		if code == 255 {
			return res, s.channelError(errOut, runErr)
		}
		// Código ≠ 255 → comando remoto falló, pero el canal SSH funcionó: devolvemos el Result normal.
		return res, nil
	case code == 255:
		return res, s.channelError(errOut, nil)
	default:
		return res, nil
	}
}

// CopyTo copies a host file into the VM and CopyFrom retrieves one, both with
// scp and the same fixed options. "--" before the paths stops option parsing,
// so a path that starts with "-" arrives as a name.
func (s *SSH) CopyTo(ctx context.Context, localPath, remotePath string) error {
	return s.copy(ctx, localPath, s.remote(remotePath))
}

func (s *SSH) CopyFrom(ctx context.Context, remotePath, localPath string) error {
	return s.copy(ctx, s.remote(remotePath), localPath)
}

func (s *SSH) copy(ctx context.Context, src, dst string) error {
	_, errOut, code, err := s.exec(ctx, "scp", append(s.options("-P"), "--", src, dst), "")
	switch {
	case err != nil, code == 255:
		return &ChannelError{Msg: "No se pudo copiar un archivo por SSH con " + s.target(), Err: errors.New(detail(errOut, err))}
	case code != 0:
		return errors.New("scp " + src + " to " + dst + ": " + detail(errOut, nil))
	}
	return nil
}

// options is the fixed non-interactive option set: BatchMode fails instead of
// waiting for a password, IdentitiesOnly pins the console's key, our own
// known_hosts file never touches the user's, accept-new records the VM the
// first time and refuses a changed fingerprint, and ConnectTimeout bounds the
// wait when the VM is off.
func (s *SSH) options(portFlag string) []string {
	return []string{
		"-i", s.KeyPath,
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + s.KnownHosts,
		"-o", "ConnectTimeout=10",
		portFlag, strconv.Itoa(s.portNumber()),
	}
}

func (s *SSH) portNumber() int {
	if s.Port <= 0 {
		return 22
	}
	return s.Port
}

func (s *SSH) target() string { return s.User + "@" + s.Host }

func (s *SSH) remote(p string) string { return s.target() + ":" + p }

// exec is the transport: it runs the named client, feeds stdin and captures
// the two streams separately. A canceled ctx kills the process.
func (s *SSH) exec(ctx context.Context, name string, args []string, stdin string) (string, string, int, error) {
	if s.run != nil {
		return s.run(ctx, name, args, stdin)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	return stdout.String(), stderr.String(), code, err
}

// channelError turns a transport failure into a ChannelError: Msg is written
// for the user and Err keeps the technical detail for the operation log.
func (s *SSH) channelError(stderr string, cause error) error {
	return &ChannelError{Msg: "No se pudo comunicar por SSH con " + s.target(), Err: errors.New(detail(stderr, cause))}
}

// detail picks the most useful text for a transport failure: the client's
// stderr when it produced one, otherwise the underlying cause.
func detail(stderr string, cause error) string {
	if d := strings.TrimSpace(stderr); d != "" {
		return d
	}
	if cause != nil {
		return cause.Error()
	}
	return "the client produced no output"
}

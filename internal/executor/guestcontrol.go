package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"runtime"
	"time"
)

// exitMarker precedes the real exit code of the command, printed by runWrapper.
const exitMarker = "__CONSOLA_EXIT__:"

// runWrapper is the fixed script every command runs in inside the VM. The command name and
// its arguments are positional parameters ("$0" "$@"), never part of the script, so no
// character in them can be interpreted by the shell. The script sets PATH (a Guest Additions
// login has none), runs the command through env (so only real programs run, never shell
// builtins such as exec, exit or eval) and prints its exit code after a marker on stderr,
// so the console knows the code of the command itself and not one of VBoxManage.
//
// VBoxManage drops empty arguments, so each argument travels with argPrefix in front and the
// script removes that first character before running the command.
const runWrapper = `PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin; export PATH; for a do shift; set -- "$@" "${a#?}"; done; env -- "$0" "$@"; s=$?; printf '\n` + exitMarker + `%d\n' "$s" >&2`

// argPrefix is added to every argument so none is empty on the VBoxManage command line.
const argPrefix = "x"

// GuestControl runs commands and copies files through VirtualBox Guest Additions
// (VBoxManage guestcontrol). It is the first channel to a VM, before SSH is set up.
//
// The password is never passed on the command line or in the environment: VBoxManage reads
// it from PasswordFile, which must be readable only by its owner (mode 600).
type GuestControl struct {
	VM           string // VirtualBox VM name
	User         string // guest user; root, so commands run with the privileges they need
	PasswordFile string // file with the guest user's password, mode 600

	// exec runs VBoxManage; tests replace it. It returns stdout, stderr and the exit code;
	// a non-nil error means VBoxManage could not be started or was cancelled.
	exec func(ctx context.Context, args []string) (stdout, stderr string, exitCode int, err error)
}

// Name implements Executor.
func (g *GuestControl) Name() string { return "Guest Additions" }

// Run implements Executor.
func (g *GuestControl) Run(ctx context.Context, c Command) (Result, error) {
	if err := c.Check(); err != nil {
		return Result{Cmd: c.String(), ExitCode: -1}, err
	}
	if c.Stdin != "" {
		return Result{Cmd: c.String(), ExitCode: -1}, &ChannelError{Msg: "Guest Additions no permite enviar datos por la entrada estándar; esta operación necesita el canal SSH"}
	}
	if err := g.checkPasswordFile(); err != nil {
		return Result{Cmd: c.String(), ExitCode: -1}, err
	}

	args := append(g.base("run"), "--exe", "/bin/sh", "--wait-stdout", "--wait-stderr")
	if deadline, ok := ctx.Deadline(); ok {
		if time.Until(deadline) <= 0 {
			return Result{Cmd: c.String(), ExitCode: -1}, context.DeadlineExceeded
		}
		// The guest process must stop too, not only VBoxManage on the host.
		args = append(args, "--timeout", guestTimeoutMS(deadline))
	}
	args = append(args, "--", "-c", runWrapper, c.Name)
	for _, a := range c.Args {
		args = append(args, argPrefix+a)
	}

	stdout, stderr, _, err := g.run(ctx, args)
	if ctx.Err() != nil {
		return Result{Cmd: c.String(), ExitCode: -1}, ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Result{Cmd: c.String(), ExitCode: -1}, err
	}
	if err != nil {
		return Result{Cmd: c.String(), ExitCode: -1}, &ChannelError{Msg: "No se pudo ejecutar VBoxManage en este equipo; revisa que VirtualBox esté instalado", Err: err}
	}
	res, perr := parseRun(stdout, stderr)
	if perr != nil {
		return Result{Cmd: c.String(), Stdout: stdout, Stderr: stderr, ExitCode: -1}, g.explain(stderr)
	}
	res.Cmd = c.String()
	return res, nil
}

// CopyTo implements Executor. The file is staged first, so VBoxManage always gets a clean
// absolute host path with the source mode, and the guest destination is the full file path
// (copyto rejects --target-directory when that directory already exists).
func (g *GuestControl) CopyTo(ctx context.Context, localPath, remotePath string) error {
	if err := checkRemotePath(remotePath); err != nil {
		return err
	}
	if err := g.checkPasswordFile(); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "consola-copia-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	staged := filepath.Join(dir, "archivo")
	if err := copyFile(localPath, staged); err != nil {
		return err
	}
	args := append(g.base("copyto"), staged, remotePath)
	return g.runCopy(ctx, args)
}

// CopyFrom implements Executor.
func (g *GuestControl) CopyFrom(ctx context.Context, remotePath, localPath string) error {
	if err := checkRemotePath(remotePath); err != nil {
		return err
	}
	if err := g.checkPasswordFile(); err != nil {
		return err
	}
	// Same directory as the destination, so the final rename never crosses file systems.
	dir, err := os.MkdirTemp(filepath.Dir(localPath), ".consola-copia-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	got := filepath.Join(dir, "archivo")
	args := append(g.base("copyfrom"), remotePath, got)
	if err := g.runCopy(ctx, args); err != nil {
		return err
	}
	// A file from the VM may hold secrets (keys, /etc/shadow): it stays private on the host.
	if err := os.Chmod(got, 0o600); err != nil {
		return err
	}
	return os.Rename(got, localPath)
}

// guestTimeoutMS is the time left until deadline, in milliseconds, at least 1: VirtualBox
// reads 0 as "no limit".
func guestTimeoutMS(deadline time.Time) string {
	ms := time.Until(deadline).Milliseconds()
	if ms < 1 {
		ms = 1
	}
	return strconv.FormatInt(ms, 10)
}

func (g *GuestControl) base(sub string) []string {
	return []string{"guestcontrol", g.VM, sub, "--username", g.User, "--passwordfile", g.PasswordFile, "--quiet"}
}

func (g *GuestControl) runCopy(ctx context.Context, args []string) error {
	_, stderr, code, err := g.run(ctx, args)
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case err != nil:
		return &ChannelError{Msg: "No se pudo ejecutar VBoxManage en este equipo; revisa que VirtualBox esté instalado", Err: err}
	case code != 0:
		return g.explain(stderr)
	}
	return nil
}

func (g *GuestControl) run(ctx context.Context, args []string) (string, string, int, error) {
	if g.exec != nil {
		return g.exec(ctx, args)
	}
	return runVBoxManage(ctx, args)
}

// checkPasswordFile refuses a password file other users can read, like ssh does with keys.
func (g *GuestControl) checkPasswordFile() error {
	st, err := os.Stat(g.PasswordFile)
	if err != nil || !st.Mode().IsRegular() {
		return &ChannelError{Msg: fmt.Sprintf("No se encontró el archivo con la contraseña de Guest Additions (%s); créalo con permisos 600 como indica el README (sección Configurar)", g.PasswordFile), Err: err}
	}
	// Windows has no POSIX permission bits: Go reports 0666 for any regular file, so this
	// check can never pass there (and chmod does not exist). Access is governed by NTFS ACLs.
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		return &ChannelError{Msg: fmt.Sprintf("El archivo de contraseña %s lo pueden leer otros usuarios; corrígelo con: chmod 600 %s", g.PasswordFile, g.PasswordFile)}
	}
	return nil
}

// explain turns a VBoxManage error into a message that says what to do. Only the lines
// VBoxManage writes ("VBoxManage: error: ...") decide it; output from the guest never does.
func (g *GuestControl) explain(stderr string) error {
	detail := strings.TrimSpace(stderr)
	var own []string
	for _, line := range strings.Split(detail, "\n") {
		if strings.HasPrefix(line, "VBoxManage: error:") {
			own = append(own, line)
		}
	}
	low := strings.ToLower(strings.Join(own, "\n"))
	var msg string
	switch {
	case strings.Contains(low, "not found") || strings.Contains(low, "no such file or directory") || strings.Contains(low, "verr_file_not_found") || strings.Contains(low, "verr_path_not_found"):
		msg = fmt.Sprintf("El archivo o la carpeta no existe en la VM «%s»; revisa la ruta", g.VM)
	case strings.Contains(low, "could not find a registered machine"):
		msg = fmt.Sprintf("La VM «%s» no existe en VirtualBox; revisa el campo vm de config.json", g.VM)
	case strings.Contains(low, "not currently running") || strings.Contains(low, "is not running"):
		msg = fmt.Sprintf("La VM «%s» está apagada; enciéndela en VirtualBox y vuelve a intentarlo", g.VM)
	case strings.Contains(low, "not able to logon") || strings.Contains(low, "authentication"):
		msg = fmt.Sprintf("La VM rechazó el usuario «%s» o la contraseña de Guest Additions; revisa el usuario en config.json y el archivo de contraseña", g.User)
	default:
		msg = fmt.Sprintf("No se pudo usar Guest Additions con la VM «%s»; revisa que estén instaladas y que la VM haya terminado de arrancar", g.VM)
	}
	return &ChannelError{Msg: msg, Err: errors.New(detail)}
}

// parseRun extracts the exit code printed by runWrapper (the last marker on stderr) and
// removes the marker line. No marker means the command never ran inside the VM.
func parseRun(stdout, stderr string) (Result, error) {
	i := strings.LastIndex(stderr, exitMarker)
	if i < 0 || (i > 0 && stderr[i-1] != '\n') {
		return Result{}, errors.New("no exit marker")
	}
	code, err := strconv.Atoi(strings.TrimRight(stderr[i+len(exitMarker):], "\r\n"))
	if err != nil {
		return Result{}, errors.New("malformed exit marker")
	}
	rest := strings.TrimSuffix(stderr[:i], "\n") // the newline printed before the marker
	return Result{Stdout: stdout, Stderr: rest, ExitCode: code}, nil
}

// checkRemotePath accepts only clean absolute guest paths.
func checkRemotePath(p string) error {
	if p == "" || !path.IsAbs(p) || path.Clean(p) != p || p == "/" || strings.ContainsAny(p, "\x00\r\n") {
		return fmt.Errorf("invalid guest path %q: it must be absolute and clean", p)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, st.Mode().Perm()) // OpenFile's mode is reduced by the umask
}

func runVBoxManage(ctx context.Context, args []string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, "VBoxManage", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), stderr.String(), exitErr.ExitCode(), nil
	}
	return stdout.String(), stderr.String(), 0, err
}

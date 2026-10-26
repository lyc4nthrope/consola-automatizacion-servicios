// Package llaves holds Part B's key logic: preparing and reusing the console's
// own key pair, building the SSH channel of a target from it, saying whether a
// VM can be driven over SSH right now, and merging the console's key into a
// VM's authorized_keys without dropping the keys it already had.
//
// Strict host checks never silently change what the console can log into: they
// use their own known_hosts file next to the key (executor.SSH covers the
// fixed options; here only paths and names are decided).
package llaves

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
)

// Dir is the console's keys directory, relative to the working directory: the
// console starts in the project folder (config.json travels with it), so
// "keys" is where its key pair and the SSH known_hosts live. It is the same
// directory cmd/consola/main.go names for Guest Additions' password files.
const Dir = "keys"

const (
	// keyName is the file name of the console's private key under Dir; the
	// public one travels in keyName + ".pub", like ssh-keygen leaves it.
	keyName = "consola_ed25519"

	// driverTimeout bounds the local ssh-keygen call; it is a quick local
	// program and this is only a guard against a wedged environment.
	driverTimeout = time.Minute
)

// llaveMu keeps the first-use generation single: the startup SSH probes ask for
// the key once per target, all at the same time, and the module page asks again.
// Every guarded section runs in microseconds except the generation itself.
var llaveMu sync.Mutex

// keygen runs ssh-keygen; tests replace it. A failing exit code and a client
// that could not start mean the same thing here: no key.
var keygen = func(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, "ssh-keygen", args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if detail := strings.TrimSpace(out.String()); detail != "" {
			return fmt.Errorf("%s", detail)
		}
		return err
	}
	return nil
}

// namedSSHKey is the full path of the console's private key in the keys dir.
func namedSSHKey(keys string) string { return filepath.Join(keys, keyName) }

// SSHForTarget returns the SSH channel of t driven by the console's key, with
// the SSH session state (known_hosts) of the console only. Port stays at SSH's
// default 22, like the configuration (no port field) assumes.
func SSHForTarget(t app.Target, keys string) *executor.SSH {
	return &executor.SSH{
		Host:       t.IP,
		User:       t.SSHUser,
		KeyPath:    namedSSHKey(keys),
		KnownHosts: filepath.Join(keys, "known_hosts"),
		Port:       22,
	}
}

// EnsureConsolaKey returns the path of the console's private key, generating
// the ed25519 pair without a passphrase the first time. Repeated calls always
// answer the same path; the generation is single even with concurrent callers.
func EnsureConsolaKey(keys string) (string, error) {
	llaveMu.Lock()
	defer llaveMu.Unlock()
	priv := namedSSHKey(keys)
	pub := priv + ".pub"
	if _, err := os.Stat(pub); err == nil {
		return priv, nil
	} else if _, err := os.Stat(priv); err == nil {
		return "", fmt.Errorf("la llave privada %s está huérfana (no existe su parte pública); restaura la parte pública o borra la privada y vuelve a intentarlo", priv)
	}
	if err := os.MkdirAll(keys, 0o700); err != nil {
		return "", fmt.Errorf("no se pudo preparar la carpeta de llaves %s: %w", keys, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), driverTimeout)
	defer cancel()
	if err := keygen(ctx, []string{"-q", "-t", "ed25519", "-N", "", "-f", priv}); err != nil {
		return "", fmt.Errorf("ssh-keygen no pudo crear la llave de la consola (%s): %w", priv, err)
	}
	if _, err := os.Stat(pub); err != nil {
		return "", fmt.Errorf("ssh-keygen terminó sin crear la llave pública %s; vuelve a intentarlo", pub)
	}
	return priv, nil
}

// PublicKey returns the console's public key (one "ssh-ed25519 AAAA… consola"
// line), generating the pair the first time, with the same single-run guard as
// EnsureConsolaKey.
func PublicKey(keys string) (string, error) {
	priv, err := EnsureConsolaKey(keys)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(priv + ".pub")
	if err != nil {
		return "", fmt.Errorf("no se pudo leer la llave pública de la consola (%s): %w", priv+".pub", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// MergeAuthorizedKeys keeps the existing authorized_keys exact — every line,
// blanks and comments included — and appends nueva as the last line when the
// same key material (the base64 field; comments are free) is not there already.
// The result always ends with one line break; when there is nothing to hold,
// the result is the empty string.
func MergeAuthorizedKeys(existentes, nueva string) string {
	nueva = strings.TrimSpace(nueva)
	if nueva != "" && !llavePresente(existentes, nueva) {
		if existentes != "" && !strings.HasSuffix(existentes, "\n") {
			existentes += "\n"
		}
		existentes += nueva + "\n"
		return existentes
	}
	if existentes != "" && !strings.HasSuffix(existentes, "\n") {
		return existentes + "\n"
	}
	return existentes
}

// llavePresente says whether existing already holds the key material of nueva
// (identifying it by its base64 field, ignoring every comment).
func llavePresente(existentes, nueva string) bool {
	material := materialDe(nueva)
	if material == "" {
		return false
	}
	for _, line := range strings.Split(existentes, "\n") {
		if materialDe(line) == material {
			return true
		}
	}
	return false
}

// materialDe is the identifying core of an authorized_keys line: the base64
// key field (the second space-separated field; a bare key stands for itself).
func materialDe(line string) string {
	f := strings.Fields(line)
	switch len(f) {
	case 1:
		return f[0]
	case 0:
		return ""
	default:
		return f[1]
	}
}

// ProbeTimeout is how long an SSH probe may take (the plan fixes three
// seconds): a VM that answers later counts as not ready for now.
const ProbeTimeout = 3 * time.Second

// ProveSSH says the console can already drive this channel over SSH: it runs
// hostname with ProbeTimeout and requires exit 0. Any miss (connection
// refused, timed out, key rejected) counts as no, and carries the reason.
func ProveSSH(ex executor.Executor) error {
	ctx, cancel := context.WithTimeout(context.Background(), ProbeTimeout)
	defer cancel()
	res, err := ex.Run(ctx, executor.Command{Name: "hostname"})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("hostname por este canal terminó con código %d", res.ExitCode)
	}
	return nil
}

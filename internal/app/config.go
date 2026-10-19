// Package app holds the console configuration and, from F1 on, its runtime state.
package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"regexp"
	"strconv"
)

// DefaultListen is the address used when the configuration leaves "listen" empty.
// The console runs commands as root on the VMs, so it only listens on loopback.
const DefaultListen = "127.0.0.1:8090"

// Target is a virtual machine managed by the console.
type Target struct {
	Name    string `json:"name"`     // identifier inside the console, e.g. "pruebas"
	VM      string `json:"vm"`       // VirtualBox VM name, used by Guest Additions
	IP      string `json:"ip"`       // address used once SSH is configured
	SSHUser string `json:"ssh_user"` // user for SSH connections

	// GuestUser is the guest user for Guest Additions; empty means root. Its password is
	// read from keys/guest-pass-<name> or keys/guest-pass (mode 600), never from this file.
	GuestUser string `json:"guest_user,omitempty"`
}

// Config is the content of config.json. Passwords are never stored here:
// they are read from environment variables.
type Config struct {
	Listen  string   `json:"listen"`
	Targets []Target `json:"targets"`
}

// Load reads and validates the configuration file at path.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("configuration file %s not found: copy config.example.json to %s and adjust it", path, path)
	}
	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}

	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	// Anything after the first JSON object would be silently ignored otherwise.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parsing %s: unexpected content after the configuration object", path)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid %s: %w", path, err)
	}
	return cfg, nil
}

// Validate applies defaults and checks the configuration.
func (c *Config) Validate() error {
	if c.Listen == "" {
		c.Listen = DefaultListen
	}
	if err := validateListen(c.Listen); err != nil {
		return err
	}

	if len(c.Targets) == 0 {
		return errors.New("at least one target is required")
	}
	seen := make(map[string]bool, len(c.Targets))
	for i, t := range c.Targets {
		switch {
		case t.Name == "":
			return fmt.Errorf("target %d: name is required", i)
		case !reTargetName.MatchString(t.Name):
			// The name becomes part of file names (keys/guest-pass-<name>) and URLs.
			return fmt.Errorf("target %q: name must use lowercase letters, digits and -", t.Name)
		case t.VM == "":
			return fmt.Errorf("target %q: vm is required", t.Name)
		case net.ParseIP(t.IP) == nil:
			return fmt.Errorf("target %q: ip %q is not a valid IP address", t.Name, t.IP)
		case seen[t.Name]:
			return fmt.Errorf("target %q: duplicate name", t.Name)
		}
		seen[t.Name] = true
	}
	return nil
}

var reTargetName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// validateListen accepts only a literal loopback IP with an explicit port,
// so the console is never reachable from the network.
func validateListen(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("listen %q: %w", listen, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen %q: host must be a loopback IP such as 127.0.0.1", listen)
	}
	n, err := strconv.Atoi(port)
	// strconv.Itoa(n) == port rejects non-canonical forms such as "+8090" or "08090".
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return fmt.Errorf("listen %q: port must be a number between 1 and 65535", listen)
	}
	return nil
}

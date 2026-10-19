// Package executor defines how the console sends commands and files to a VM.
//
// Modules depend only on the Executor interface; the Guest Additions and SSH
// adapters implement it, and Fake replaces it in tests.
package executor

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Command is a program to run on the VM. Args are passed one by one, already validated;
// they are never joined into a shell line by the caller.
type Command struct {
	Name  string   // executable, e.g. "apt-get"
	Args  []string // arguments, one element per argument
	Stdin string   // optional input (e.g. "user:password" for chpasswd); guaranteed over SSH
}

// Check rejects commands no channel can deliver intact. Adapters call it before running:
// a NUL byte cuts an argument (argv and SSH command lines end at NUL), and a name with "="
// would be read by the shell as a variable assignment instead of a program.
func (c Command) Check() error {
	switch {
	case c.Name == "":
		return errors.New("command name is empty")
	case strings.Contains(c.Name, "="):
		return errors.New("command name must not contain '='")
	case strings.ContainsRune(c.Name, 0):
		return errors.New("command name contains a NUL byte")
	}
	for _, a := range c.Args {
		if strings.ContainsRune(a, 0) {
			return errors.New("command argument contains a NUL byte")
		}
	}
	return nil
}

// String returns the command as a POSIX shell line. For a command that passes Check,
// /bin/sh runs exactly Name with exactly Args.
func (c Command) String() string {
	parts := make([]string, 0, len(c.Args)+1)
	name := Quote(c.Name)
	if name == c.Name && strings.Contains(c.Name, "=") {
		name = "'" + c.Name + "'" // an unquoted NAME=value word is an assignment, not a program
	}
	parts = append(parts, name)
	for _, a := range c.Args {
		parts = append(parts, Quote(a))
	}
	return strings.Join(parts, " ")
}

// ChannelError means the command could not be delivered to the VM (VM off, wrong
// credentials, channel not ready...). Msg is written for the user; Err keeps the
// technical detail for the operation log.
type ChannelError struct {
	Msg string
	Err error
}

func (e *ChannelError) Error() string {
	if e.Err == nil {
		return e.Msg
	}
	return e.Msg + ": " + e.Err.Error()
}

func (e *ChannelError) Unwrap() error { return e.Err }

// Result is the outcome of a command.
type Result struct {
	Cmd      string // full command, for the log
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// Executor runs commands and copies files on one VM.
type Executor interface {
	Name() string // channel shown to the user and stored in the log: "Guest Additions" or "SSH"
	Run(ctx context.Context, c Command) (Result, error)
	CopyTo(ctx context.Context, localPath, remotePath string) error   // host → VM
	CopyFrom(ctx context.Context, remotePath, localPath string) error // VM → host
}

// Quote returns s quoted for a POSIX shell so that the shell reads it back as exactly
// one word with the same bytes. Words made only of safe characters are left as they are.
// s must not contain NUL bytes (see Command.Check); no shell can represent them.
func Quote(s string) string {
	if s != "" && strings.Trim(s, safeChars) == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

const safeChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:@%+,"

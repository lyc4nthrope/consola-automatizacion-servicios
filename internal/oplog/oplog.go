// Package oplog records every operation the console runs: what, where, through which
// channel, each command with its output and exit code, and how long it took.
//
// Entries are appended as JSON lines to logs/operaciones.jsonl (private to the user,
// since command output may contain secrets) and the most recent ones are kept in memory
// to show them in the interface.
package oplog

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// FileName is the log file inside the log directory.
	FileName = "operaciones.jsonl"
	// MaxOutput is the maximum number of bytes kept for stdout and for stderr of a step.
	MaxOutput = 64 << 10
	// MaxInMemory is how many recent entries stay available to the interface.
	MaxInMemory = 200
)

// Step is one command of an operation.
type Step struct {
	Cmd        string `json:"cmd"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`     // channel error (VM off, timeout, ...)
	Truncated  bool   `json:"truncated,omitempty"` // output longer than MaxOutput
}

// Entry is one operation requested from the interface.
type Entry struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Action  string    `json:"action"`  // e.g. "Probar conexión"
	Target  string    `json:"target"`  // target name from config.json
	Channel string    `json:"channel"` // "Guest Additions", "SSH", "Simulado" or "Sin conexión"
	OK      bool      `json:"ok"`
	Message string    `json:"message"` // result explained for the user
	Steps   []Step    `json:"steps,omitempty"`

	// NotSaved marks an entry that could not be written to disk (kept only in memory).
	NotSaved bool `json:"-"`
}

// Duration is the total time of the steps.
func (e Entry) Duration() time.Duration {
	var ms int64
	for _, s := range e.Steps {
		ms += s.DurationMS
	}
	return time.Duration(ms) * time.Millisecond
}

// Log is an append-only operation log. It is safe for concurrent use.
type Log struct {
	mu      sync.Mutex
	f       *os.File
	entries []Entry // oldest first, at most MaxInMemory
	skipped int
}

// Open opens (or creates) the log in dir and loads its most recent entries.
func Open(dir string) (*Log, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating log directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("securing log directory: %w", err)
	}
	path := filepath.Join(dir, FileName)
	l := &Log{}
	if err := l.load(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening log file: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, fmt.Errorf("securing log file: %w", err)
	}
	l.f = f
	return l, nil
}

func (l *Log) load(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading log file: %w", err)
	}
	defer f.Close()
	// bufio.Reader has no line-length limit: an unusually long line is parsed or skipped,
	// it never prevents the console from starting.
	rd := bufio.NewReader(f)
	for {
		line, err := rd.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var e Entry
			if jerr := json.Unmarshal(line, &e); jerr != nil || e.ID == "" {
				l.skipped++
			} else {
				l.remember(e)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading log file: %w", err)
		}
	}
	if l.skipped > 0 {
		log.Printf("oplog: skipped %d unreadable lines in %s", l.skipped, path)
	}
	return nil
}

// Add stores e, assigning its ID and, if empty, its time, and returns the stored entry.
// If the file cannot be written, the entry is still kept in memory, marked NotSaved, and
// returned together with the error: the operation already ran and its result must reach
// the user.
func (l *Log) Add(e Entry) (Entry, error) {
	id, err := newID()
	if err != nil {
		return Entry{}, err
	}
	e.ID = id
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Steps = append([]Step(nil), e.Steps...)
	for i := range e.Steps {
		s := &e.Steps[i]
		if len(s.Stdout) > MaxOutput {
			s.Stdout, s.Truncated = cut(s.Stdout), true
		}
		if len(s.Stderr) > MaxOutput {
			s.Stderr, s.Truncated = cut(s.Stderr), true
		}
		// What is kept in memory must equal what is read back from disk.
		s.Stdout = validUTF8(s.Stdout)
		s.Stderr = validUTF8(s.Stderr)
	}
	var line bytes.Buffer
	enc := json.NewEncoder(&line)
	enc.SetEscapeHTML(false) // "<" stays 1 byte instead of 6: keeps lines close to the output size
	if err := enc.Encode(e); err != nil {
		return Entry{}, fmt.Errorf("encoding log entry: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	var werr error
	if _, err := l.f.Write(line.Bytes()); err != nil {
		e.NotSaved = true
		werr = fmt.Errorf("writing log entry: %w", err)
		log.Printf("oplog: %v (entry %s kept only in memory)", werr, e.ID)
	}
	l.remember(e)
	return e, werr
}

// remember must be called with l.mu held (or before the Log is shared).
func (l *Log) remember(e Entry) {
	l.entries = append(l.entries, e)
	if extra := len(l.entries) - MaxInMemory; extra > 0 {
		l.entries = append([]Entry(nil), l.entries[extra:]...)
	}
}

// Get returns the entry with that ID if it is still in memory.
func (l *Log) Get(id string) (Entry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.entries) - 1; i >= 0; i-- {
		if l.entries[i].ID == id {
			return l.entries[i], true
		}
	}
	return Entry{}, false
}

// Recent returns up to n entries, newest first.
func (l *Log) Recent(n int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, 0, n)
	for i := len(l.entries) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, l.entries[i])
	}
	return out
}

// Skipped reports how many unreadable lines were ignored when opening the log.
func (l *Log) Skipped() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.skipped
}

// Close closes the log file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// validUTF8 replaces each invalid byte with U+FFFD. Unlike strings.ToValidUTF8, which
// collapses a whole run into one replacement, binary output keeps its length and shape.
func validUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(utf8.RuneError)
		} else {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// cut shortens s to at most MaxOutput bytes without splitting a UTF-8 character. A UTF-8
// character is at most 4 bytes, so it backs up at most 3; binary data loses nothing more.
func cut(s string) string {
	n := MaxOutput
	for i := 0; i < utf8.UTFMax-1 && !utf8.RuneStart(s[n]); i++ {
		n--
	}
	return s[:n]
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating operation ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

package oplog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func sample(action string) Entry {
	return Entry{Action: action, Target: "pruebas", Channel: "Simulado", OK: true, Message: "ok",
		Steps: []Step{{Cmd: "hostname", Stdout: "pruebas\n", ExitCode: 0, DurationMS: 12}}}
}

func TestAddAssignsIDAndTime(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	e, err := l.Add(sample("Probar conexión"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(e.ID) {
		t.Fatalf("ID = %q, want 16 hex characters", e.ID)
	}
	if e.Time.IsZero() || time.Since(e.Time) > time.Minute {
		t.Fatalf("Time = %v, want now", e.Time)
	}
	got, ok := l.Get(e.ID)
	if !ok || got.Action != "Probar conexión" || got.Steps[0].Stdout != "pruebas\n" {
		t.Fatalf("Get = %+v, %v", got, ok)
	}
	if _, ok := l.Get("0000000000000000"); ok {
		t.Fatal("Get of an unknown ID must report false")
	}
}

func TestIDsAreUnique(t *testing.T) {
	l, _ := Open(t.TempDir())
	defer l.Close()
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		e, err := l.Add(sample("x"))
		if err != nil {
			t.Fatal(err)
		}
		if seen[e.ID] {
			t.Fatalf("duplicate ID %s", e.ID)
		}
		seen[e.ID] = true
	}
}

func TestRecentIsNewestFirstAndLimited(t *testing.T) {
	l, _ := Open(t.TempDir())
	defer l.Close()
	for _, a := range []string{"uno", "dos", "tres"} {
		if _, err := l.Add(sample(a)); err != nil {
			t.Fatal(err)
		}
	}
	r := l.Recent(2)
	if len(r) != 2 || r[0].Action != "tres" || r[1].Action != "dos" {
		t.Fatalf("Recent(2) = %v", actions(r))
	}
	if n := len(l.Recent(10)); n != 3 {
		t.Fatalf("Recent(10) returned %d entries, want 3", n)
	}
}

func TestEntriesSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir)
	e, _ := l.Add(sample("persistida"))
	l.Close()

	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if got, ok := l2.Get(e.ID); !ok || got.Action != "persistida" {
		t.Fatalf("after reopening, Get(%s) = %+v, %v", e.ID, got, ok)
	}
}

func TestCorruptLinesAreSkipped(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir)
	good, _ := l.Add(sample("buena"))
	l.Close()
	f, _ := os.OpenFile(filepath.Join(dir, FileName), os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("{esto no es json\n\n")
	f.Close()

	l2, err := Open(dir)
	if err != nil {
		t.Fatalf("a corrupt line must not prevent opening the log: %v", err)
	}
	defer l2.Close()
	if _, ok := l2.Get(good.ID); !ok {
		t.Fatal("valid entries before the corrupt line must still load")
	}
	if l2.Skipped() != 1 {
		t.Fatalf("Skipped() = %d, want 1", l2.Skipped())
	}
	if _, err := l2.Add(sample("después")); err != nil {
		t.Fatalf("appending after a corrupt line: %v", err)
	}
}

func TestFilePermissionsArePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := l.Add(sample("x")); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, FileName): 0o600} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("%s: mode %v, want %v (command output may contain secrets)", path, got, want)
		}
	}
}

func TestLongOutputIsTruncated(t *testing.T) {
	l, _ := Open(t.TempDir())
	defer l.Close()
	e := sample("grande")
	e.Steps[0].Stdout = strings.Repeat("x", MaxOutput+500)
	e.Steps[0].Stderr = strings.Repeat("y", 10)
	saved, err := l.Add(e)
	if err != nil {
		t.Fatal(err)
	}
	s := saved.Steps[0]
	if len(s.Stdout) != MaxOutput || !s.Truncated {
		t.Fatalf("Stdout length %d, Truncated %v; want %d and true", len(s.Stdout), s.Truncated, MaxOutput)
	}
	if s.Stderr != strings.Repeat("y", 10) {
		t.Fatal("short output must not be modified")
	}
}

func TestTruncationNeverSplitsACharacter(t *testing.T) {
	l, _ := Open(t.TempDir())
	defer l.Close()
	e := sample("utf8")
	e.Steps[0].Stdout = strings.Repeat("x", MaxOutput-1) + "ñandú" // "ñ" straddles the limit
	saved, err := l.Add(e)
	if err != nil {
		t.Fatal(err)
	}
	out := saved.Steps[0].Stdout
	if !utf8.ValidString(out) || len(out) > MaxOutput || !saved.Steps[0].Truncated {
		t.Fatalf("len %d, valid UTF-8 %v, truncated %v", len(out), utf8.ValidString(out), saved.Steps[0].Truncated)
	}
}

func TestMemoryKeepsOnlyTheLastEntries(t *testing.T) {
	l, _ := Open(t.TempDir())
	defer l.Close()
	first, _ := l.Add(sample("primera"))
	for i := 0; i < MaxInMemory; i++ {
		_, _ = l.Add(sample("x"))
	}
	if _, ok := l.Get(first.ID); ok {
		t.Fatal("the oldest entry should have left memory")
	}
	if n := len(l.Recent(MaxInMemory * 2)); n != MaxInMemory {
		t.Fatalf("memory holds %d entries, want %d", n, MaxInMemory)
	}
}

func TestEntryDuration(t *testing.T) {
	e := Entry{Steps: []Step{{DurationMS: 1200}, {DurationMS: 300}}}
	if got := e.Duration(); got != 1500*time.Millisecond {
		t.Fatalf("Duration() = %v, want 1.5s", got)
	}
}

func actions(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Action)
	}
	return out
}

func TestHTMLAndBinaryOutputCanBeReloaded(t *testing.T) {
	// Finding: json escaping made a 64 KB "<" output ~6x larger than the reader's buffer,
	// so the console could not start again.
	dir := t.TempDir()
	l, _ := Open(dir)
	e := sample("salida enorme")
	e.Steps[0].Stdout = strings.Repeat("<", MaxOutput)
	e.Steps[0].Stderr = strings.Repeat("\xff", MaxOutput)
	saved, err := l.Add(e)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	l2, err := Open(dir)
	if err != nil {
		t.Fatalf("the log must reopen after a large HTML/binary output: %v", err)
	}
	defer l2.Close()
	if got, ok := l2.Get(saved.ID); !ok || got.Steps[0].Stdout != saved.Steps[0].Stdout {
		t.Fatal("the large entry must be readable after reopening")
	}
}

func TestOverlongLineIsSkippedNotFatal(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir)
	good, _ := l.Add(sample("buena"))
	l.Close()
	f, _ := os.OpenFile(filepath.Join(dir, FileName), os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(`{"id":"x","action":"` + strings.Repeat("a", 2<<20) + "\n")
	f.Close()
	l2, err := Open(dir)
	if err != nil {
		t.Fatalf("an overlong line must be skipped, not stop the console: %v", err)
	}
	defer l2.Close()
	if _, ok := l2.Get(good.ID); !ok || l2.Skipped() != 1 {
		t.Fatalf("good entry kept: %v, skipped: %d", ok, l2.Skipped())
	}
}

func TestWriteFailureKeepsTheEntryInMemory(t *testing.T) {
	// Finding: the command already ran as root; the user must still see its result.
	l, _ := Open(t.TempDir())
	l.f.Close() // simulate a broken disk
	e, err := l.Add(sample("sin disco"))
	if err == nil {
		t.Fatal("Add must report the write failure")
	}
	if e.ID == "" || !e.NotSaved {
		t.Fatalf("entry = %+v: must have an ID and be marked NotSaved", e)
	}
	if got, ok := l.Get(e.ID); !ok || !got.NotSaved {
		t.Fatal("the unsaved entry must stay available in memory")
	}
}

func TestTruncationOfBinaryOutputKeepsData(t *testing.T) {
	l, _ := Open(t.TempDir())
	defer l.Close()
	e := sample("binario")
	e.Steps[0].Stdout = strings.Repeat("\x80", MaxOutput+10) // only continuation bytes
	saved, _ := l.Add(e)
	if n := len(saved.Steps[0].Stdout); n < MaxOutput-3 {
		t.Fatalf("kept %d bytes of binary output, want at least %d", n, MaxOutput-3)
	}
}

package executor

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestQuoteRoundTripsThroughARealShell(t *testing.T) {
	// The guarantee lives in the shell, so the test asks /bin/sh to parse the quoted text back.
	inputs := []string{
		"tree",
		"",
		"with space",
		"it's",
		"a;b",
		"$(reboot)",
		"`id`",
		"x && rm -rf /",
		"line1\nline2",
		`back\slash`,
		"'",
		`"double"`,
		"*",
		"~root",
		"--flag=value",
	}
	for _, in := range inputs {
		out, err := exec.Command("/bin/sh", "-c", "printf %s "+Quote(in)).Output()
		if err != nil {
			t.Fatalf("Quote(%q) = %s: shell error: %v", in, Quote(in), err)
		}
		if string(out) != in {
			t.Errorf("Quote(%q) = %s: shell produced %q", in, Quote(in), out)
		}
	}
}

func TestQuoteAlwaysProducesExactlyOneArgument(t *testing.T) {
	// An empty or blank argument must not disappear or split when the shell parses it.
	for _, in := range []string{"", " ", "a b", "\t", "*"} {
		out, err := exec.Command("/bin/sh", "-c", "set -- "+Quote(in)+"; printf %d $#").Output()
		if err != nil {
			t.Fatalf("Quote(%q): shell error: %v", in, err)
		}
		if string(out) != "1" {
			t.Errorf("Quote(%q) = %s: shell saw %s arguments, want 1", in, Quote(in), out)
		}
	}
}

func TestQuoteLeavesSafeWordsReadable(t *testing.T) {
	for _, in := range []string{"apt-get", "-y", "/etc/hosts", "tree", "DEBIAN_FRONTEND=noninteractive", "getty@tty1"} {
		if got := Quote(in); got != in {
			t.Errorf("Quote(%q) = %s, want it unchanged", in, got)
		}
	}
}

func TestCommandString(t *testing.T) {
	c := Command{Name: "apt-get", Args: []string{"install", "-y", "tree; reboot"}}
	want := "apt-get install -y 'tree; reboot'"
	if got := c.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestFakeRecordsCallsInOrder(t *testing.T) {
	f := &Fake{}
	ctx := context.Background()
	if _, err := f.Run(ctx, Command{Name: "hostname"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Run(ctx, Command{Name: "apt-get", Args: []string{"update"}}); err != nil {
		t.Fatal(err)
	}
	calls := f.Calls()
	if len(calls) != 2 || calls[0].Name != "hostname" || calls[1].String() != "apt-get update" {
		t.Fatalf("Calls = %+v", calls)
	}
	calls[0].Name = "changed"
	if f.Calls()[0].Name != "hostname" {
		t.Fatal("Calls must return a copy")
	}
}

func TestFakeDefaultResultDescribesTheCommand(t *testing.T) {
	f := &Fake{}
	res, err := f.Run(context.Background(), Command{Name: "apt-get", Args: []string{"install", "-y", "tree"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Cmd != "apt-get install -y tree" || res.ExitCode != 0 {
		t.Fatalf("Result = %+v", res)
	}
}

func TestFakeUsesRunFunc(t *testing.T) {
	boom := errors.New("vm off")
	f := &Fake{RunFunc: func(c Command) (Result, error) {
		return Result{Stderr: "no such vm", ExitCode: 1}, boom
	}}
	res, err := f.Run(context.Background(), Command{Name: "hostname"})
	if !errors.Is(err, boom) || res.ExitCode != 1 || res.Stderr != "no such vm" {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	if res.Cmd != "hostname" {
		t.Fatalf("Cmd must be filled even with RunFunc, got %q", res.Cmd)
	}
}

func TestFakeRecordsCopies(t *testing.T) {
	f := &Fake{}
	ctx := context.Background()
	_ = f.CopyTo(ctx, "/tmp/issue", "/tmp/consola-issue")
	_ = f.CopyFrom(ctx, "/etc/hosts", "/tmp/hosts")
	want := []Copy{{ToVM: true, Local: "/tmp/issue", Remote: "/tmp/consola-issue"}, {ToVM: false, Local: "/tmp/hosts", Remote: "/etc/hosts"}}
	got := f.Copies()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Copies = %+v, want %+v", got, want)
	}
}

func TestCommandCheck(t *testing.T) {
	bad := []Command{
		{Name: ""},
		{Name: "FOO=bar", Args: []string{"x"}},
		{Name: "apt\x00get"},
		{Name: "apt-get", Args: []string{"install", "tr\x00ee"}},
	}
	for _, c := range bad {
		if err := c.Check(); err == nil {
			t.Errorf("Check(%q %q) = nil, want an error", c.Name, c.Args)
		}
	}
	if err := (Command{Name: "apt-get", Args: []string{"install", "-y", "", "a b"}}).Check(); err != nil {
		t.Errorf("valid command rejected: %v", err)
	}
}

func TestFakeRefusesInvalidCommands(t *testing.T) {
	f := &Fake{}
	if _, err := f.Run(context.Background(), Command{Name: "apt-get", Args: []string{"tr\x00ee"}}); err == nil {
		t.Fatal("Fake.Run must refuse a command that fails Check, like the real adapters")
	}
	if n := len(f.Calls()); n != 0 {
		t.Fatalf("a refused command must not be recorded, got %d calls", n)
	}
}

func TestCommandStringRunsExactlyNameAndArgs(t *testing.T) {
	// The shell must run the program with exactly these arguments: count and bytes.
	c := Command{Name: "printf", Args: []string{"[%s]", "", "a b", "it's", "$(id)", "x=1"}}
	out, err := exec.Command("/bin/sh", "-c", c.String()).Output()
	if err != nil {
		t.Fatalf("sh -c %s: %v", c.String(), err)
	}
	if want := "[][a b][it's][$(id)][x=1]"; string(out) != want {
		t.Fatalf("shell ran %q, want %q", out, want)
	}
}

func TestCommandStringNeverTurnsTheNameIntoAnAssignment(t *testing.T) {
	// Even if a caller skips Check, a NAME=value word must stay a program name.
	got := Command{Name: "FOO=bar", Args: []string{"x"}}.String()
	if got != "'FOO=bar' x" {
		t.Fatalf("String() = %q, want 'FOO=bar' x", got)
	}
}

func FuzzQuoteRoundTrip(f *testing.F) {
	for _, s := range []string{"", "tree", "it's", "a b", "$(reboot)", "`id`", "\n", "\\", "*", `'\''`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		if strings.ContainsRune(in, 0) {
			t.Skip("NUL cannot be represented in a shell word; Command.Check rejects it")
		}
		out, err := exec.Command("/bin/sh", "-c", "set -- "+Quote(in)+"; printf %s \"$#:$1\"").Output()
		if err != nil {
			t.Fatalf("Quote(%q): shell error: %v", in, err)
		}
		if string(out) != "1:"+in {
			t.Fatalf("Quote(%q) = %s: shell read %q", in, Quote(in), out)
		}
	})
}

func TestFakeName(t *testing.T) {
	if got := (&Fake{}).Name(); got != "Simulado" {
		t.Fatalf("default Name = %q", got)
	}
	if got := (&Fake{NameValue: "SSH"}).Name(); got != "SSH" {
		t.Fatalf("Name = %q", got)
	}
}

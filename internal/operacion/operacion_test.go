package operacion

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
)

func setup(t *testing.T, ex executor.Executor) (*Runner, *oplog.Log) {
	t.Helper()
	state := app.NewState(app.Config{Listen: app.DefaultListen, Targets: []app.Target{{Name: "pruebas", VM: "vm", IP: "10.0.0.1"}}})
	if ex != nil {
		if err := state.SetExecutor("pruebas", ex); err != nil {
			t.Fatal(err)
		}
	}
	log, err := oplog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	return New(state, log), log
}

func cmd(name string, args ...string) executor.Command {
	return executor.Command{Name: name, Args: args}
}

func TestSuccessfulOperationIsRecorded(t *testing.T) {
	f := &executor.Fake{NameValue: "SSH", RunFunc: func(c executor.Command) (executor.Result, error) {
		return executor.Result{Stdout: "pruebas\n"}, nil
	}}
	r, log := setup(t, f)
	e, err := r.Run(context.Background(), Plan{
		Action: "Probar conexión", Target: "pruebas", Commands: []executor.Command{cmd("hostname")},
		Success: func(res []executor.Result) string { return "La VM respondió como " + strings.TrimSpace(res[0].Stdout) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !e.OK || e.Channel != "SSH" || e.Target != "pruebas" || e.Action != "Probar conexión" {
		t.Fatalf("entry = %+v", e)
	}
	if e.Message != "La VM respondió como pruebas" {
		t.Fatalf("Message = %q", e.Message)
	}
	if len(e.Steps) != 1 || e.Steps[0].Cmd != "hostname" || e.Steps[0].Stdout != "pruebas\n" {
		t.Fatalf("Steps = %+v", e.Steps)
	}
	if got, ok := log.Get(e.ID); !ok || got.Message != e.Message {
		t.Fatal("the operation must be stored in the log")
	}
}

func TestStopsAtTheFirstFailingCommand(t *testing.T) {
	f := &executor.Fake{RunFunc: func(c executor.Command) (executor.Result, error) {
		if c.Name == "falla" {
			return executor.Result{Stderr: "E: Unable to locate package xyz", ExitCode: 100}, nil
		}
		return executor.Result{}, nil
	}}
	r, _ := setup(t, f)
	e, err := r.Run(context.Background(), Plan{Action: "x", Target: "pruebas",
		Commands: []executor.Command{cmd("uno"), cmd("falla"), cmd("nunca")}})
	if err != nil {
		t.Fatal(err)
	}
	if e.OK {
		t.Fatal("an operation with a failing command must not be OK")
	}
	if len(e.Steps) != 2 {
		t.Fatalf("ran %d steps, want 2 (stop after the failure)", len(e.Steps))
	}
	if names := f.Calls(); len(names) != 2 || names[1].Name != "falla" {
		t.Fatalf("executor received %+v", names)
	}
	if !strings.Contains(e.Message, "falla") || !strings.Contains(e.Message, "100") {
		t.Fatalf("Message %q must name the command and its exit code", e.Message)
	}
}

func TestExplainTurnsKnownErrorsIntoAdvice(t *testing.T) {
	f := &executor.Fake{RunFunc: func(c executor.Command) (executor.Result, error) {
		return executor.Result{Stderr: "Could not get lock /var/lib/dpkg/lock-frontend", ExitCode: 100}, nil
	}}
	r, _ := setup(t, f)
	e, _ := r.Run(context.Background(), Plan{Action: "Instalar", Target: "pruebas", Commands: []executor.Command{cmd("apt-get")},
		Explain: func(s oplog.Step) string {
			if strings.Contains(s.Stderr, "Could not get lock") {
				return "Otro proceso está usando apt; intenta de nuevo en un minuto."
			}
			return ""
		}})
	if e.Message != "Otro proceso está usando apt; intenta de nuevo en un minuto." {
		t.Fatalf("Message = %q", e.Message)
	}
}

func TestChannelErrorIsRecorded(t *testing.T) {
	f := &executor.Fake{RunFunc: func(c executor.Command) (executor.Result, error) {
		return executor.Result{ExitCode: -1}, errors.New("VBoxManage: VM is not running")
	}}
	r, _ := setup(t, f)
	e, _ := r.Run(context.Background(), Plan{Action: "x", Target: "pruebas", Commands: []executor.Command{cmd("hostname")}})
	if e.OK || e.Steps[0].Error != "VBoxManage: VM is not running" {
		t.Fatalf("entry = %+v", e)
	}
	if !strings.Contains(e.Message, "comunicar") {
		t.Fatalf("Message %q should explain that the VM could not be reached", e.Message)
	}
}

func TestNoChannelIsRecordedAsAFailure(t *testing.T) {
	r, log := setup(t, nil)
	e, err := r.Run(context.Background(), Plan{Action: "Probar conexión", Target: "pruebas", Commands: []executor.Command{cmd("hostname")}})
	if err != nil {
		t.Fatal(err)
	}
	if e.OK || e.Channel != "Sin conexión" || len(e.Steps) != 0 {
		t.Fatalf("entry = %+v", e)
	}
	if !strings.Contains(e.Message, "pruebas") {
		t.Fatalf("Message %q must name the target", e.Message)
	}
	if _, ok := log.Get(e.ID); !ok {
		t.Fatal("failed operations must be logged too")
	}
}

func TestTimeoutCancelsTheOperation(t *testing.T) {
	f := &blocking{}
	r, _ := setup(t, f)
	start := time.Now()
	e, _ := r.Run(context.Background(), Plan{Action: "x", Target: "pruebas", Commands: []executor.Command{cmd("sleep", "100")}, Timeout: 50 * time.Millisecond})
	if time.Since(start) > 2*time.Second {
		t.Fatal("the timeout did not stop the operation")
	}
	if e.OK || !strings.Contains(e.Message, "tiempo límite") {
		t.Fatalf("entry = %+v", e)
	}
}

func TestClosingTheBrowserDoesNotCancelTheOperation(t *testing.T) {
	f := &blocking{release: make(chan struct{})}
	r, _ := setup(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan oplog.Entry)
	go func() {
		e, _ := r.Run(ctx, Plan{Action: "x", Target: "pruebas", Commands: []executor.Command{cmd("apt-get")}, Timeout: time.Minute})
		done <- e
	}()
	time.Sleep(20 * time.Millisecond)
	cancel() // the HTTP request is gone
	time.Sleep(50 * time.Millisecond)
	close(f.release)
	if e := <-done; !e.OK {
		t.Fatalf("a cancelled request must not abort a running command: %+v", e)
	}
}

func TestUnknownTargetIsAnError(t *testing.T) {
	r, _ := setup(t, &executor.Fake{})
	_, err := r.Run(context.Background(), Plan{Action: "x", Target: "otra", Commands: []executor.Command{cmd("hostname")}})
	if !errors.Is(err, ErrUnknownTarget) {
		t.Fatalf("err = %v, want ErrUnknownTarget", err)
	}
}

func TestDurationsAreMeasured(t *testing.T) {
	f := &executor.Fake{RunFunc: func(c executor.Command) (executor.Result, error) {
		time.Sleep(15 * time.Millisecond)
		return executor.Result{}, nil
	}}
	r, _ := setup(t, f)
	e, _ := r.Run(context.Background(), Plan{Action: "x", Target: "pruebas", Commands: []executor.Command{cmd("a")}})
	if e.Steps[0].DurationMS < 15 {
		t.Fatalf("DurationMS = %d, want >= 15", e.Steps[0].DurationMS)
	}
}

// blocking waits until release is closed, or until its context ends.
type blocking struct{ release chan struct{} }

func (b *blocking) Name() string                                   { return "Simulado" }
func (b *blocking) CopyTo(context.Context, string, string) error   { return nil }
func (b *blocking) CopyFrom(context.Context, string, string) error { return nil }
func (b *blocking) Run(ctx context.Context, c executor.Command) (executor.Result, error) {
	select {
	case <-b.release:
		return executor.Result{}, nil
	case <-ctx.Done():
		return executor.Result{ExitCode: -1}, ctx.Err()
	}
}

func TestHumanize(t *testing.T) {
	for d, want := range map[time.Duration]string{30 * time.Second: "30 s", 10 * time.Minute: "10 min", 90 * time.Second: "90 s", 50 * time.Millisecond: "50ms"} {
		if got := humanize(d); got != want {
			t.Errorf("humanize(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestNonZeroExitNearTheDeadlineIsNotATimeout(t *testing.T) {
	f := &executor.Fake{RunFunc: func(executor.Command) (executor.Result, error) {
		time.Sleep(80 * time.Millisecond) // finishes after the deadline, on its own
		return executor.Result{ExitCode: 2, Stderr: "fallo propio"}, nil
	}}
	r, _ := setup(t, f)
	e, _ := r.Run(context.Background(), Plan{Action: "x", Target: "pruebas", Commands: []executor.Command{cmd("comando")}, Timeout: 30 * time.Millisecond})
	if strings.Contains(e.Message, "tiempo límite") || !strings.Contains(e.Message, "código 2") {
		t.Fatalf("Message = %q: the command failed by itself, not by the timeout", e.Message)
	}
}

func TestEmptyPlanIsRejected(t *testing.T) {
	r, _ := setup(t, &executor.Fake{})
	if _, err := r.Run(context.Background(), Plan{Action: "x", Target: "pruebas", Success: func(res []executor.Result) string { return res[0].Stdout }}); err == nil {
		t.Fatal("a plan without commands is a programming error and must be rejected")
	}
}

func TestLogWriteFailureStillReturnsTheEntry(t *testing.T) {
	r, log := setup(t, executor.NewSimulated("pruebas"))
	log.Close() // disk failure
	e, err := r.Run(context.Background(), Plan{Action: "x", Target: "pruebas", Commands: []executor.Command{cmd("hostname")}})
	if err == nil || e.ID == "" || !e.NotSaved || !e.OK {
		t.Fatalf("entry = %+v, err = %v: the result must reach the user even if the log fails", e, err)
	}
}

func TestAlsoOKExitCodesAreNotFailures(t *testing.T) {
	// `systemctl status` exits 3 for a stopped unit: a state, not an error.
	f := &executor.Fake{RunFunc: func(executor.Command) (executor.Result, error) {
		return executor.Result{Stdout: "inactive (dead)", ExitCode: 3}, nil
	}}
	r, _ := setup(t, f)
	e, _ := r.Run(context.Background(), Plan{Action: "Estado", Target: "pruebas", Commands: []executor.Command{cmd("systemctl", "status", "x")}, AlsoOK: []int{3}})
	if !e.OK {
		t.Fatalf("exit 3 listed in AlsoOK must count as success: %+v", e)
	}
	e, _ = r.Run(context.Background(), Plan{Action: "Estado", Target: "pruebas", Commands: []executor.Command{cmd("systemctl", "status", "x")}})
	if e.OK {
		t.Fatal("without AlsoOK, exit 3 is a failure")
	}
}

func TestAlsoOKDoesNotHideChannelErrors(t *testing.T) {
	f := &executor.Fake{RunFunc: func(executor.Command) (executor.Result, error) {
		return executor.Result{ExitCode: 3}, errors.New("ssh: connection refused")
	}}
	r, _ := setup(t, f)
	e, _ := r.Run(context.Background(), Plan{Action: "x", Target: "pruebas", Commands: []executor.Command{cmd("systemctl")}, AlsoOK: []int{3}})
	if e.OK {
		t.Fatal("a channel error is a failure even if its exit code is in AlsoOK")
	}
}

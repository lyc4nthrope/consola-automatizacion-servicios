package app

import (
	"sync"
	"testing"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
)

func twoTargets() Config {
	return Config{Listen: DefaultListen, Targets: []Target{
		{Name: "pruebas", VM: "consola-pruebas", IP: "192.168.40.60"},
		{Name: "balanceador", VM: "balanceador", IP: "192.168.40.53"},
	}}
}

func TestNewStateSelectsFirstTarget(t *testing.T) {
	s := NewState(twoTargets())
	if got := s.Current().Name; got != "pruebas" {
		t.Fatalf("Current = %q, want pruebas", got)
	}
	if n := len(s.Targets()); n != 2 {
		t.Fatalf("Targets = %d, want 2", n)
	}
}

func TestTargetsReturnsACopy(t *testing.T) {
	s := NewState(twoTargets())
	ts := s.Targets()
	ts[0].Name = "changed"
	if s.Targets()[0].Name != "pruebas" {
		t.Fatal("callers must not be able to modify the state through Targets()")
	}
}

func TestSelectTarget(t *testing.T) {
	s := NewState(twoTargets())
	if err := s.SelectTarget("balanceador"); err != nil {
		t.Fatal(err)
	}
	if got := s.Current().Name; got != "balanceador" {
		t.Fatalf("Current = %q, want balanceador", got)
	}
	if err := s.SelectTarget("no-existe"); err == nil {
		t.Fatal("expected an error for an unknown target")
	}
	if got := s.Current().Name; got != "balanceador" {
		t.Fatalf("a failed selection must not change the current target, got %q", got)
	}
}

func TestExecutorPerTarget(t *testing.T) {
	s := NewState(twoTargets())
	if _, ok := s.Executor("pruebas"); ok {
		t.Fatal("no executor must be active before SetExecutor")
	}
	ssh := &executor.Fake{NameValue: "SSH"}
	if err := s.SetExecutor("pruebas", ssh); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Executor("pruebas")
	if !ok || got != ssh {
		t.Fatal("Executor(pruebas) must return the executor that was set")
	}
	if _, ok := s.Executor("balanceador"); ok {
		t.Fatal("setting an executor for one target must not affect the other")
	}
	if err := s.SetExecutor("no-existe", ssh); err == nil {
		t.Fatal("expected an error for an unknown target")
	}
	if err := s.SetExecutor("pruebas", nil); err == nil {
		t.Fatal("expected an error for a nil executor")
	}
}

func TestChannelName(t *testing.T) {
	s := NewState(twoTargets())
	if got := s.Channel("pruebas"); got != "Sin conexión" {
		t.Fatalf("Channel without executor = %q", got)
	}
	_ = s.SetExecutor("pruebas", &executor.Fake{NameValue: "Guest Additions"})
	if got := s.Channel("pruebas"); got != "Guest Additions" {
		t.Fatalf("Channel = %q", got)
	}
}

// TestStateIsSafeForConcurrentUse only proves something under the race detector
// (go test -race, as CI runs it): it exercises every method from many goroutines.
func TestStateIsSafeForConcurrentUse(t *testing.T) {
	s := NewState(twoTargets())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); _ = s.SelectTarget("balanceador"); _ = s.SelectTarget("pruebas") }()
		go func() { defer wg.Done(); _ = s.SetExecutor("pruebas", &executor.Fake{}) }()
		go func() {
			defer wg.Done()
			_ = s.Current()
			_ = s.Targets()
			_, _ = s.Executor("pruebas")
			_ = s.Channel("pruebas")
		}()
	}
	wg.Wait()
}

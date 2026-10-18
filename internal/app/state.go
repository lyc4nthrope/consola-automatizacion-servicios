package app

import (
	"errors"
	"fmt"
	"sync"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
)

// State is the runtime state of the console: the configured targets, the target the
// user is working on, and the active Executor (channel) of each target.
// It is safe for concurrent use by HTTP handlers.
type State struct {
	mu        sync.RWMutex
	targets   []Target
	current   string
	executors map[string]executor.Executor
}

// NewState builds the state from a validated configuration; the first target is selected.
func NewState(cfg Config) *State {
	s := &State{
		targets:   append([]Target(nil), cfg.Targets...),
		executors: make(map[string]executor.Executor, len(cfg.Targets)),
	}
	if len(s.targets) > 0 {
		s.current = s.targets[0].Name
	}
	return s
}

// Targets returns a copy of the configured targets.
func (s *State) Targets() []Target {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Target(nil), s.targets...)
}

// Current returns the target the user is working on.
func (s *State) Current() Target {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, _ := s.find(s.current)
	return t
}

// SelectTarget changes the current target.
func (s *State) SelectTarget(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.find(name); !ok {
		return fmt.Errorf("unknown target %q", name)
	}
	s.current = name
	return nil
}

// SetExecutor makes ex the active channel of the target. Part B calls it after the SSH bootstrap.
func (s *State) SetExecutor(target string, ex executor.Executor) error {
	if ex == nil {
		return errors.New("executor must not be nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.find(target); !ok {
		return fmt.Errorf("unknown target %q", target)
	}
	s.executors[target] = ex
	return nil
}

// Executor returns the active channel of the target, if any.
func (s *State) Executor(target string) (executor.Executor, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ex, ok := s.executors[target]
	return ex, ok
}

// Channel returns the name of the active channel of the target, for display.
func (s *State) Channel(target string) string {
	if ex, ok := s.Executor(target); ok {
		return ex.Name()
	}
	return "Sin conexión"
}

// find must be called with s.mu held.
func (s *State) find(name string) (Target, bool) {
	for _, t := range s.targets {
		if t.Name == name {
			return t, true
		}
	}
	return Target{}, false
}

package executor

import (
	"context"
	"sync"
)

// Copy records one file copy made through a Fake.
type Copy struct {
	ToVM   bool // true for CopyTo, false for CopyFrom
	Local  string
	Remote string
}

// Fake is an in-memory Executor for tests and for developing without a VM.
// It records every call; RunFunc, when set, decides the result of Run.
// It is safe for concurrent use; read the records with Calls and Copies.
type Fake struct {
	NameValue string
	RunFunc   func(Command) (Result, error)
	CopyErr   error

	mu     sync.Mutex
	calls  []Command
	copies []Copy
}

// Calls returns a copy of the commands received by Run, in order.
func (f *Fake) Calls() []Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Command(nil), f.calls...)
}

// Copies returns a copy of the file copies received, in order.
func (f *Fake) Copies() []Copy {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Copy(nil), f.copies...)
}

// Name returns NameValue, or "Simulado" when it is empty.
func (f *Fake) Name() string {
	if f.NameValue == "" {
		return "Simulado"
	}
	return f.NameValue
}

// Run checks and records c, then returns RunFunc's result, or a successful empty result.
// Like the real adapters, it refuses commands that fail Check.
func (f *Fake) Run(_ context.Context, c Command) (Result, error) {
	if err := c.Check(); err != nil {
		return Result{Cmd: c.String(), ExitCode: -1}, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()

	var (
		res Result
		err error
	)
	if f.RunFunc != nil {
		res, err = f.RunFunc(c)
	}
	res.Cmd = c.String()
	return res, err
}

// CopyTo records a host → VM copy.
func (f *Fake) CopyTo(_ context.Context, localPath, remotePath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.copies = append(f.copies, Copy{ToVM: true, Local: localPath, Remote: remotePath})
	return f.CopyErr
}

// CopyFrom records a VM → host copy.
func (f *Fake) CopyFrom(_ context.Context, remotePath, localPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.copies = append(f.copies, Copy{ToVM: false, Local: localPath, Remote: remotePath})
	return f.CopyErr
}

// NewSimulated returns a Fake that behaves like a healthy VM named hostname: "hostname"
// prints it and any other command succeeds and echoes what would have run. It lets the
// team develop and demo the interface without a VM (consola -simular).
func NewSimulated(hostname string) *Fake {
	return &Fake{
		NameValue: "Simulado",
		RunFunc: func(c Command) (Result, error) {
			if c.Name == "hostname" && len(c.Args) == 0 {
				return Result{Stdout: hostname + "\n"}, nil
			}
			return Result{Stdout: "(simulación) " + c.String() + "\n"}, nil
		},
	}
}

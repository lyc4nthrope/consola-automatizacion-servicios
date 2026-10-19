// Package operacion runs an operation on a VM: it sends the commands through the target's
// active channel, stops at the first failure, measures each step, explains the result for
// the user and stores everything in the operation log.
//
// Every module (Parts A, B and C) builds a Plan and calls Runner.Run.
package operacion

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
)

// DefaultTimeout applies when a Plan does not set one.
const DefaultTimeout = 30 * time.Second

// ErrUnknownTarget is returned when a Plan names a target that is not in config.json.
var ErrUnknownTarget = errors.New("unknown target")

// Plan describes an operation requested from the interface.
type Plan struct {
	Action   string             // shown to the user, e.g. "Instalar aplicación"
	Target   string             // target name
	Commands []executor.Command // run in order; the first failure stops the operation
	Timeout  time.Duration      // limit for the whole operation; DefaultTimeout if zero
	AlsoOK   []int              // exit codes besides 0 that mean success (e.g. 3 for `systemctl status` of a stopped unit)

	// Success, if set, writes the message for a successful operation from the results.
	Success func([]executor.Result) string
	// Explain, if set, turns a known failure into advice; "" keeps the default message.
	Explain func(oplog.Step) string
}

// Runner executes plans. It is safe for concurrent use.
type Runner struct {
	state *app.State
	log   *oplog.Log
}

// New returns a Runner that uses the active channels in state and records to log.
func New(state *app.State, log *oplog.Log) *Runner {
	return &Runner{state: state, log: log}
}

// Run executes p and returns the stored log entry. The returned error is only for
// problems of the console itself (unknown target, log not writable); a failing command
// is a normal result with Entry.OK == false.
//
// The operation is detached from ctx's cancellation: if the browser tab is closed, a
// half-done command (an apt install, for example) still finishes. Only p.Timeout stops it.
func (r *Runner) Run(ctx context.Context, p Plan) (oplog.Entry, error) {
	if !r.knownTarget(p.Target) {
		return oplog.Entry{}, fmt.Errorf("%w %q", ErrUnknownTarget, p.Target)
	}
	if len(p.Commands) == 0 {
		return oplog.Entry{}, errors.New("plan without commands")
	}
	e := oplog.Entry{Time: time.Now(), Action: p.Action, Target: p.Target, Channel: "Sin conexión"}

	// The channel is read once: the one logged is always the one used.
	ex, ok := r.state.Executor(p.Target)
	if ok {
		e.Channel = ex.Name()
	}
	if !ok {
		e.Message = fmt.Sprintf("No hay un canal de comunicación activo con la VM «%s». Conéctala por Guest Additions o SSH y vuelve a intentarlo.", p.Target)
		return r.log.Add(e)
	}

	timeout := p.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	results := make([]executor.Result, 0, len(p.Commands))
	e.OK = true
	for _, c := range p.Commands {
		start := time.Now()
		res, err := ex.Run(ctx, c)
		step := oplog.Step{
			Cmd:        c.String(),
			Stdout:     res.Stdout,
			Stderr:     res.Stderr,
			ExitCode:   res.ExitCode,
			DurationMS: time.Since(start).Milliseconds(),
		}
		if err != nil {
			step.Error = err.Error()
		}
		e.Steps = append(e.Steps, step)
		results = append(results, res)

		if err == nil && (res.ExitCode == 0 || slices.Contains(p.AlsoOK, res.ExitCode)) {
			continue
		}
		e.OK = false
		e.Message = failure(ctx, p, step, err, timeout)
		break
	}

	if e.OK {
		e.Message = "La operación terminó correctamente."
		if p.Success != nil {
			e.Message = p.Success(results)
		}
	}
	return r.log.Add(e)
}

func failure(ctx context.Context, p Plan, step oplog.Step, err error, timeout time.Duration) string {
	// Only a command that was cut off counts as a timeout: one that failed on its own
	// right before the deadline keeps its real exit code.
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return fmt.Sprintf("La operación superó el tiempo límite de %s y se canceló. Revisa que la VM responda y vuelve a intentarlo.", humanize(timeout))
	}
	if p.Explain != nil {
		if msg := p.Explain(step); msg != "" {
			return msg
		}
	}
	var ce *executor.ChannelError
	if errors.As(err, &ce) { // the channel knows what went wrong: VM off, wrong password...
		return ce.Msg + ". El detalle técnico está abajo."
	}
	if err != nil {
		return "No se pudo comunicar con la VM para ejecutar «" + step.Cmd + "». Revisa que esté encendida y que el canal funcione; el detalle técnico está abajo."
	}
	return fmt.Sprintf("El comando «%s» terminó con código %d. Revisa la salida de abajo para ver la causa.", step.Cmd, step.ExitCode)
}

func (r *Runner) knownTarget(name string) bool {
	for _, t := range r.state.Targets() {
		if t.Name == name {
			return true
		}
	}
	return false
}

// humanize writes a limit the way a person would say it: "30 s", "10 min".
func humanize(d time.Duration) string {
	switch {
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%d min", d/time.Minute)
	case d >= time.Second:
		return fmt.Sprintf("%d s", d/time.Second)
	default:
		return d.String()
	}
}

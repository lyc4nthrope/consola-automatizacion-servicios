// Command consola starts the web console that automates services on Debian VMs.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/oplog"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/web"
	assets "github.com/lyc4nthrope/consola-automatizacion-servicios/web"
)

func main() {
	configPath := flag.String("config", "config.json", "path to the configuration file")
	logDir := flag.String("logs", "logs", "directory of the operation log")
	simulate := flag.Bool("simular", false, "use a simulated channel for every target (no VM needed)")
	flag.Parse()

	cfg, err := app.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	opLog, err := oplog.Open(*logDir)
	if err != nil {
		log.Fatal(err)
	}
	defer opLog.Close()

	handler, err := web.NewHandler(web.Deps{
		Static:    assets.Static,
		Templates: assets.Templates,
		State:     newState(cfg, *simulate),
		Log:       opLog,
		Listen:    cfg.Listen,
	})
	if err != nil {
		log.Fatal(err)
	}

	srv := newServer(cfg.Listen, handler)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		mode := ""
		if *simulate {
			mode = ", modo simulado"
		}
		log.Printf("consola escuchando en http://%s (%d destinos configurados%s)", cfg.Listen, len(cfg.Targets), mode)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("cierre forzado: %v", err)
	}
}

// newServer sets read and idle limits so slow or abandoned connections cannot pile up.
// There is no WriteTimeout on purpose: an apt operation may take minutes before the response.
func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

// keysDir holds the console's private files (Guest Additions passwords, SSH keys).
const keysDir = "keys"

// newState builds the runtime state with the default keys directory.
func newState(cfg app.Config, simulate bool) *app.State {
	return newStateWithKeys(cfg, simulate, keysDir)
}

// sshChannel, when set, returns the SSH channel of a target that is already prepared for
// SSH, and a short fixed description for the startup log (for example the key file it
// uses; never ssh's own error output). Part B sets it from its own file,
// cmd/consola/canal_ssh.go:
//
//	func init() { sshChannel = func(t app.Target, keys string) (executor.Executor, string, bool) { ... } }
//
// It is tried before Guest Additions, so after a restart a VM that already works over SSH
// keeps using SSH (the assignment: after the first contact, only SSH). It runs once per
// target at startup, for every target at the same time, so it should finish within a few
// seconds.
var sshChannel func(t app.Target, keys string) (ex executor.Executor, detail string, ok bool)

// newStateWithKeys builds the runtime state. With simulate, every target gets a simulated
// channel so the interface can be developed and demonstrated without a VM. Otherwise each
// target uses SSH where sshChannel says it is ready, and else Guest Additions where it has
// a password file.
func newStateWithKeys(cfg app.Config, simulate bool, keys string) *app.State {
	state := app.NewState(cfg)
	ssh := sshChannels(cfg, simulate, keys)
	for i, t := range cfg.Targets {
		if simulate {
			_ = state.SetExecutor(t.Name, executor.NewSimulated(t.Name))
			continue
		}
		if ssh[i].ok && ssh[i].ex != nil && state.SetExecutor(t.Name, ssh[i].ex) == nil {
			log.Printf("destino %s: canal SSH (%s)", t.Name, ssh[i].detail)
			continue
		}
		if ex, file, ok := guestChannel(t, keys); ok {
			_ = state.SetExecutor(t.Name, ex)
			log.Printf("destino %s: canal Guest Additions con la contraseña de %s", t.Name, file)
		}
	}
	return state
}

// sshResult is what sshChannel answered for one target.
type sshResult struct {
	ex     executor.Executor
	detail string
	ok     bool
}

// sshChannels asks sshChannel about every target at the same time, so VMs that are off
// delay the start by one probe, not one per VM. The results keep the order of the targets.
func sshChannels(cfg app.Config, simulate bool, keys string) []sshResult {
	out := make([]sshResult, len(cfg.Targets))
	if simulate || sshChannel == nil {
		return out
	}
	var wg sync.WaitGroup
	for i, t := range cfg.Targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ex, detail, ok := sshChannel(t, keys)
			out[i] = sshResult{ex, detail, ok}
		}()
	}
	wg.Wait()
	return out
}

// guestChannel returns the Guest Additions channel of a target if its password file exists:
// keys/guest-pass-<target> first, then keys/guest-pass for every target.
func guestChannel(t app.Target, keys string) (executor.Executor, string, bool) {
	user := t.GuestUser
	if user == "" {
		user = "root"
	}
	for _, f := range []string{filepath.Join(keys, "guest-pass-"+t.Name), filepath.Join(keys, "guest-pass")} {
		if _, err := os.Stat(f); err == nil {
			abs, err := filepath.Abs(f) // VBoxManage expects an absolute path
			if err != nil {
				return nil, "", false
			}
			return &executor.GuestControl{VM: t.VM, User: user, PasswordFile: abs}, f, true
		}
	}
	return nil, "", false
}

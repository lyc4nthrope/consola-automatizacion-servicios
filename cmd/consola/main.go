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

// newStateWithKeys builds the runtime state. With simulate, every target gets a simulated
// channel so the interface can be developed and demonstrated without a VM. Otherwise each
// target that has a Guest Additions password file starts with that channel.
func newStateWithKeys(cfg app.Config, simulate bool, keys string) *app.State {
	state := app.NewState(cfg)
	for _, t := range cfg.Targets {
		if simulate {
			_ = state.SetExecutor(t.Name, executor.NewSimulated(t.Name))
			continue
		}
		if ex, file, ok := guestChannel(t, keys); ok {
			_ = state.SetExecutor(t.Name, ex)
			log.Printf("destino %s: canal Guest Additions con la contraseña de %s", t.Name, file)
		}
	}
	return state
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

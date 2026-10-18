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

// newState builds the runtime state. With simulate, every target gets a simulated channel
// so the interface can be developed and demonstrated without a VM.
func newState(cfg app.Config, simulate bool) *app.State {
	state := app.NewState(cfg)
	if simulate {
		for _, t := range cfg.Targets {
			_ = state.SetExecutor(t.Name, executor.NewSimulated(t.Name))
		}
	}
	return state
}

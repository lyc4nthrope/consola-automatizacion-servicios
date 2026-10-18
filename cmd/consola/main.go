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
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/web"
	assets "github.com/lyc4nthrope/consola-automatizacion-servicios/web"
)

func main() {
	configPath := flag.String("config", "config.json", "path to the configuration file")
	flag.Parse()

	cfg, err := app.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	handler, err := web.NewHandler(assets.Static, assets.Templates, app.NewState(cfg), cfg.Listen)
	if err != nil {
		log.Fatal(err)
	}

	srv := newServer(cfg.Listen, handler)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("consola escuchando en http://%s (%d destinos configurados)", cfg.Listen, len(cfg.Targets))
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

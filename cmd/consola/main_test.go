package main

import (
	"net/http"
	"testing"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
)

func TestNewServerLimitsSlowConnections(t *testing.T) {
	srv := newServer("127.0.0.1:8090", http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("timeouts must be set: header=%v read=%v idle=%v", srv.ReadHeaderTimeout, srv.ReadTimeout, srv.IdleTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %v: long operations would be cut off", srv.WriteTimeout)
	}
	if srv.Addr != "127.0.0.1:8090" {
		t.Fatalf("Addr = %q", srv.Addr)
	}
}

func TestNewStateSimulatedGivesEveryTargetAChannel(t *testing.T) {
	cfg := app.Config{Listen: app.DefaultListen, Targets: []app.Target{{Name: "a", VM: "a", IP: "10.0.0.1"}, {Name: "b", VM: "b", IP: "10.0.0.2"}}}
	for _, name := range []string{"a", "b"} {
		if got := newState(cfg, true).Channel(name); got != "Simulado" {
			t.Errorf("simulated: Channel(%s) = %q, want Simulado", name, got)
		}
		if got := newState(cfg, false).Channel(name); got != "Sin conexión" {
			t.Errorf("normal: Channel(%s) = %q, want Sin conexión", name, got)
		}
	}
}

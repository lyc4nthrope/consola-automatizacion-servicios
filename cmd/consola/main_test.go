package main

import (
	"net/http"
	"testing"
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

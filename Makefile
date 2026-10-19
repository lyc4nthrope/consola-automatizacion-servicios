# Targets for building, running and checking the console. Everything is plain Go:
# `make` is a shortcut, not a requirement.

BIN := consola

.PHONY: build run simular test check vm-test clean

# One static binary with the pages, styles and scripts inside (go:embed).
build:
	CGO_ENABLED=0 go build -trimpath -o $(BIN) ./cmd/consola

run: build
	./$(BIN)

# Without VMs: every target answers through a simulated channel.
simular: build
	./$(BIN) -simular

test:
	go test -race -count=1 ./...

# What CI runs: format, vet, tests and build.
check:
	@test -z "$$(gofmt -l .)" || { echo "Run gofmt on:"; gofmt -l .; exit 1; }
	go vet ./...
	go test -race -count=1 ./...
	go build ./cmd/consola

# Tests against a real VM through Guest Additions, e.g.
#   make vm-test VM=consola-pruebas PASSFILE=keys/guest-pass-pruebas
vm-test:
	@test -n "$(VM)" -a -n "$(PASSFILE)" || { echo "Usage: make vm-test VM=<VirtualBox VM> PASSFILE=keys/guest-pass-<target>"; exit 1; }
	CONSOLA_VM=$(VM) CONSOLA_GUEST_PASSFILE=$(abspath $(PASSFILE)) go test -tags vm -count=1 -run TestVM -v ./internal/executor/

clean:
	rm -f $(BIN)

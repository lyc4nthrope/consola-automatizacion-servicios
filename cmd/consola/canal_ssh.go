package main

import (
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/executor"
	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/llaves"
)

// canal_ssh.go is Part B's startup SSH channel. It fills the sshChannel hook of
// main.go: for one target it prepares the console's key (the first-use
// generation is single, so the concurrent startup probes are safe), answers
// whether the VM opens an SSH session with it, and hands the ready channel back.
//
// A miss is answered with ok=false and no noise: sshChannels keeps the console
// quiet and falls back to Guest Additions for that target (a VM that is off or
// not prepared yet stays there; "Probar conexión" and the module actions have
// their own outward voice through the operation log).
func init() {
	sshChannel = canalSSH
}

// canalSSH is one sshChannel call (contract in main.go): it must finish fast —
// the key generation happens at most once for all the targets, and the SSH
// probe takes at most a few seconds (ProveSSH bounds it) per target, with every
// target asking at the same time.
func canalSSH(t app.Target, keys string) (executor.Executor, string, bool) {
	priv, err := llaves.EnsureConsolaKey(keys)
	if err != nil {
		return nil, "", false
	}
	ex := llaves.SSHForTarget(t, keys)
	if err := llaves.ProveSSH(ex); err != nil {
		return nil, "", false
	}
	return ex, "llave " + priv, true
}

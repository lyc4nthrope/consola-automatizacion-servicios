# Security rules for consola-automatizacion-servicios

Context: a Go web console that runs commands **as root** on VirtualBox VMs (Guest Additions, then SSH).
Any injection here is remote code execution as root, so treat these rules as blocking.

## Command execution

- Commands reach a VM only as `executor.Command{Name, Args}` through `operacion.Runner.Run`. Flag any
  `exec.Command("sh", "-c", ...)`, `bash -c`, string concatenation or `fmt.Sprintf` that builds a shell line.
- Every user-supplied value must pass a `internal/validate` function before it is placed in `Args`.
  Flag a handler that puts a form value in a command without validating it first.
- Executors must call `Command.Check()` before running. When building an SSH command line, each argument
  must go through `executor.Quote`; flag any other quoting or escaping.
- Never pass secrets (passwords) in `Args`: they appear in `ps` and in the operation log. Use `Command.Stdin`.

## Files written on the VM

- Content for files such as `/etc/issue`, `/etc/hosts` or `haproxy.cfg` is generated in Go and copied with
  `CopyTo` to a temporary path, then installed. Flag `sed -i`, `echo >>` or `tee` with user text.
- Remote paths are fixed constants or built from validated names; flag a path that contains raw user input
  (path traversal).

## HTTP layer

- The server listens only on a loopback address; flag any change that binds `0.0.0.0`, `:port` or a LAN IP.
- State-changing routes are `POST` and stay behind `onlyHosts` and `sameOrigin` middleware (DNS rebinding and
  CSRF protection). Flag new `GET` routes that change state, or handlers registered outside `NewHandler`.
- Output from the VM (stdout, stderr, file contents) is untrusted: render it only through `html/template`.
  Flag `template.HTML`, `text/template` for pages, or writing VM output directly to the response.
- Redirects go only to console paths (see `safeBack` and `/resultado/{id}`); flag redirects built from input.
- Request bodies are size-limited with `http.MaxBytesReader`.

## Keys, credentials and logs

- Private keys generated for root or users are delivered once for download and must not stay on the VM or be
  committed. `keys/`, `logs/`, `config.json` and `.env` are git-ignored; flag code that writes secrets elsewhere.
- Guest Additions credentials come only from environment variables, never from `config.json` or source code.
- The operation log (`logs/operaciones.jsonl`) is created with 0700/0600 permissions; flag looser modes.

## sshd and system changes

- `sshd` changes go to a drop-in under `/etc/ssh/sshd_config.d/` and are validated with `sshd -t` before the
  restart. Flag a restart without validation (it can lock the console out of the VM).
- HAProxy changes are validated with `haproxy -c -f` before replacing the config and applied with `reload`.

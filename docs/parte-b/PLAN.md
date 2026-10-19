# Plan — Parte B: Seguridad y usuarios

**Responsable:** Daniel Perez
**Rama de trabajo:** `daniel` (se sube a `dev` al cerrar cada fase verificada)
**Ventana:** lunes 19 – miércoles 21 de octubre de 2026 · **Entrega obligatoria a la Parte C:** adaptador SSH (B1) el **martes 20**
**Planes relacionados:** [general](../PLAN.md) · [Parte A](../parte-a/PLAN.md) · [Parte C](../parte-c/PLAN.md) · [Guía de la base](../base-para-modulos.md)

> La Parte B convierte la consola en "**solo SSH con llaves**", como pide el enunciado: crea el canal SSH,
> hace el paso de Guest Additions a SSH (bootstrap), genera y entrega las llaves de root, y administra usuarios.
> Es la parte con más impacto de seguridad: un error aquí puede dejar a la consola sin acceso a la VM o
> dejar una VM accesible con contraseña. Por eso cada fase termina **verificada contra la VM real**.

---

## 0. Cómo usar este plan

### Para la persona

1. Lee este plan completo una vez, luego la [guía de la base](../base-para-modulos.md).
2. Trabaja **una fase a la vez**, en orden. No empieces la siguiente sin cerrar la anterior (ver "Regla de cierre" en §5).
3. Antes de cada fase: `git switch daniel && git pull && git merge origin/dev` para tener lo último de la base.
4. Al cerrar una fase: commit en `daniel`, push, y avisa al equipo para subirla a `dev`.

### Para el asistente de IA que te ayude

Copia esta sección tal cual al inicio de tu conversación con la IA:

```text
Contexto: proyecto Go en ./consola (consola web local que ejecuta comandos como root en VMs Debian).
Soy responsable de la Parte B. Lee, en este orden, antes de proponer código:
  1. docs/parte-b/PLAN.md (este plan; es la especificación, síguela literalmente)
  2. docs/base-para-modulos.md (API de la base: registerModule, Runner, validate, Form, runAndShow)
  3. .claude/claude-security-guidance.md (reglas de seguridad del proyecto; son obligatorias)
  4. internal/web/registro.go, internal/operacion/operacion.go, internal/executor/executor.go
Reglas obligatorias:
  - Nunca construyas una línea de shell con texto del usuario. Un comando es executor.Command{Name, Args}.
  - Todo valor del usuario pasa por internal/validate antes de usarse. Si falta un validador, créalo en
    un archivo NUEVO de internal/validate (no edites validate.go, es de la Parte A).
  - Todo lo que toca la VM pasa por operacion.Runner (s.runAndShow en los handlers).
  - Solo edita archivos de la Parte B (lista en §4). Si necesitas cambiar la base, detente y dilo.
  - Escribe primero el test que falla, luego el código. Corre `go vet ./... && go test -race ./...`.
  - Commits convencionales (feat:, fix:, test:, docs:), sin "Co-Authored-By" ni firmas de IA.
  - Una llave privada nunca pasa por Run (su contenido terminaría en el registro de operaciones).
  - No declares una fase terminada sin cumplir su criterio "Terminada cuando" contra la VM real.
```

---

## 1. Requisitos del enunciado que cubre la Parte B

| # | Requisito (texto del enunciado) | Fase |
|---|---|---|
| RB1 | "Luego de la instalación de SSH, la comunicación será exclusivamente a través de SSH, con llaves de autenticación" | B1, B2 |
| RB2 | §3.1.2 "crear las llaves SSH para el usuario root, agregar la llave al archivo de llaves autorizadas y asegurarse de que no haya acceso a través de login y password" | B3 |
| RB3 | §3.1.2 "permitir la descarga de la llave privada … debe salir un mensaje recomendando el uso correcto de la llave privada" | B3 |
| RB4 | §3.2.1 "crear un usuario … casilla de verificación para … privilegios de sudo" | B4 |
| RB5 | §3.2.1 "otra casilla … generar un par de llaves … instalar la llave pública y descargar la llave privada … deberá deshabilitar el acceso a través de login y password" | B4 |
| RB6 | §3.2.2 "eliminar un usuario … casilla … también debe eliminar todos sus archivos" | B5 |
| RB7 | "Validar los datos antes de ejecutar … mensaje comprensible … información técnica útil" | todas (la base ya lo resuelve; ver §3) |
| RB8 | Pruebas en video con estado antes y después (V5–V8) y consolidar el documento final | B6, B7 |

---

## 2. Qué ya existe en la base (no lo reconstruyas)

| Necesitas | Ya existe | Dónde |
|---|---|---|
| Ejecutar comandos en la VM | `executor.Executor` (interfaz): `Name()`, `Run`, `CopyTo`, `CopyFrom` | `internal/executor/executor.go` |
| Comillas seguras para SSH | `executor.Quote(s)` y `Command.String()`; `Command.Check()` rechaza NUL y nombres con `=` | `internal/executor/executor.go` |
| Simular la VM en tests | `executor.Fake` (`RunFunc`, `Calls()`, `Copies()`) y `executor.NewSimulated` | `internal/executor/fake.go` |
| Canal activo por destino | `state.SetExecutor(destino, ex)`, `state.Executor(destino)`, `state.Current()` | `internal/app/state.go` |
| Ejecutar y registrar una operación | `operacion.Plan{Action, Target, Commands, Then, Timeout, AlsoOK, Success, Explain}` + `Runner.Run` (`Then`: leer → cambiar en Go → escribir en una sola operación) | `internal/operacion/operacion.go` |
| Validar entradas | `validate.Usuario`, `validate.Hostname`, `validate.IPv4`, … (`*validate.Error{Field, Msg}`) | `internal/validate/validate.go` |
| Pantallas, errores junto al campo | `registerModule`, `ModuleRouter`, `s.renderModule`, `s.runAndShow`, `FieldErrors`, parciales `error-campo` y `resumen-errores`, función `aria` | `internal/web/registro.go`, `web/templates/_formulario.html` |
| Seguridad HTTP | Host permitido, mismo origen, CSP, límite de cuerpo: ya envuelven todas las rutas | `internal/web/server.go` |
| Canal Guest Additions (para el bootstrap) | Fase F3 de la Parte A, lunes 19 | `internal/executor/guestcontrol.go` |
| Arrancar por SSH las VMs que ya lo tienen | Punto de conexión `sshChannel` (Parte A, 19 de octubre): la consola lo prueba **antes** que Guest Additions al arrancar. Tú lo llenas desde tu archivo (paso 6 de B2) | `cmd/consola/main.go` (no lo edites) |
| VM de pruebas con SSH, Guest Additions y root con contraseña | Fase C1 de la Parte C, domingo 18 | ver Parte C |

---

## 3. Decisiones de la Parte B

Cada una es la opción recomendada. Si cambias alguna, anótala aquí con el motivo.

| # | Decisión | Por qué |
|---|---|---|
| DB1 | El adaptador SSH ejecuta el cliente del sistema: `ssh` y `scp` con `exec.CommandContext`, **nunca** `sh -c` | Igual que el resto del proyecto, sin librerías nuevas; `exec` no pasa por un shell local |
| DB2 | Opciones fijas de `ssh`: `-i keys/consola_ed25519 -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=keys/known_hosts -o ConnectTimeout=10 -p 22 root@<ip> -- <Command.String()>` | `BatchMode` falla en vez de pedir contraseña (no interactivo); `known_hosts` propio no toca el del usuario; `accept-new` acepta la VM la primera vez y rechaza si su huella cambia después |
| DB3 | El código de salida `255` de `ssh` es **error del canal** (se devuelve `error`); cualquier otro es el código del comando remoto (`Result.ExitCode`) | El Runner distingue "no se pudo comunicar" de "el comando falló" con mensajes distintos |
| DB4 | La **llave de la consola** (`keys/consola_ed25519`, 0600, carpeta `keys/` 0700) se genera en el host con `ssh-keygen -t ed25519 -N "" -C consola@host` la primera vez y nunca sale del host | D7 del plan general: es distinta de las llaves que se entregan a root y a los usuarios |
| DB5 | Archivos de la VM (`authorized_keys`, configuración de `sshd`) se generan con **funciones puras de Go** y se instalan con `CopyTo` a un temporal + `install -m <modo> -o <dueño> -g <grupo> <tmp> <destino>` | No hay `echo >>` ni `sed`; la función se prueba con tablas; agregar una llave dos veces no la duplica |
| DB6 | Contraseña desactivada con archivos *drop-in* en `/etc/ssh/sshd_config.d/`: root → `10-consola-root.conf` con `PermitRootLogin prohibit-password`; cada usuario → `20-consola-<usuario>.conf` con `Match User <usuario>` + `PasswordAuthentication no` + `KbdInteractiveAuthentication no` | El enunciado pide quitar la contraseña **para ese usuario**, no para todos; Debian carga `sshd_config.d/*.conf` al inicio, y en `sshd` gana el primer valor |
| DB7 | Antes de aplicar cambios de `sshd`: `sshd -t`. Si falla, se borra el drop-in recién copiado y no se recarga. Se aplica con `systemctl reload ssh` | Una configuración rota no debe dejar a la consola sin acceso; `reload` no corta las sesiones abiertas |
| DB8 | La verificación de que la contraseña quedó apagada se hace con `sshd -T -C user=<u>,host=consola,addr=127.0.0.1` y se lee `passwordauthentication no` / `permitrootlogin prohibit-password` (según la versión de OpenSSH puede mostrarse como `without-password`; ambos significan lo mismo) | Prueba la configuración **efectiva**, que es lo que importa (con `Match` el orden de los archivos influye) |
| DB9 | Las llaves que se entregan (root y usuarios) se generan **en la VM** con `ssh-keygen -t ed25519 -N "" -C <usuario>@<host> -f <dir-temporal>/id_ed25519`, el directorio temporal se crea con `mktemp -d`, la privada se trae con `CopyFrom` y se borra de la VM con `rm -f` | El enunciado pide que la consola las cree y permita descargarlas; borrarla de la VM evita dejar una copia olvidada |
| DB10 | **La llave privada nunca se lee con `Run`** (ni `cat`, ni salida de `ssh-keygen`): solo con `CopyFrom` | Todo lo que pasa por `Run` queda guardado en `logs/operaciones.jsonl` |
| DB11 | Descarga de una sola vez: la privada se guarda en `keys/descargas/<token>` (0600), con `token` aleatorio de 128 bits; `GET /usuarios/llaves/{token}` la entrega con `Content-Disposition: attachment`, `Cache-Control: no-store`, y la borra al terminar; caduca a los 15 minutos | Nadie puede volver a bajarla después; no queda guardada en el host |
| DB12 | Contraseña de usuario: `chpasswd` recibe `usuario:contraseña` por **`Command.Stdin`** (nunca en `Args`), solo por SSH | En `Args` aparecería en `ps` y en el registro |
| DB13 | Validador nuevo `validate.Contrasena` en **`internal/validate/contrasena.go`**: 8 a 128 caracteres, sin `:` ni saltos de línea ni caracteres de control | `chpasswd` usa `:` como separador y lee por líneas |
| DB14 | Usuarios listados con `getent passwd` y filtrados en Go: UID 1000 a 59999, sin `nobody`. No se puede eliminar `root`, el usuario de `ssh_user` del destino, ni el usuario con el que se conecta la consola | Evita que la consola se quite el acceso a sí misma |
| DB15 | Rutas de la Parte B, todas bajo `/usuarios/` (lo exige `ModuleRouter`): `GET /usuarios`, `POST /usuarios/crear`, `POST /usuarios/eliminar`, `POST /usuarios/llaves-root`, `POST /usuarios/conectar-ssh`, `GET /usuarios/llaves/{token}` | Una ruta fuera de `/usuarios/` hace que la consola no arranque (protección contra choques entre partes) |
| DB16 | Las llaves de root aparecen en la pantalla **Preparación del sistema** (diseño de referencia), que es de la Parte A: la Parte B entrega el parcial `web/templates/_llaves-root.html` con `{{define "llaves-root"}}` y la Parte A lo incluye en su página | Cada parte edita solo sus archivos; el formulario apunta a `POST /usuarios/llaves-root` |

---

## 4. Archivos de la Parte B

Solo estos. Todo lo demás es de otra parte (si necesitas un cambio allí, pídelo).

```
internal/executor/ssh.go              B1  adaptador SSH (implementa executor.Executor)
internal/executor/ssh_test.go         B1
cmd/consola/canal_ssh.go              B2  llena sshChannel: al arrancar, la VM que ya tiene SSH usa SSH
cmd/consola/canal_ssh_test.go         B2
internal/llaves/                      B2–B4  lógica pura: authorized_keys, drop-ins de sshd, planes
internal/llaves/*_test.go
internal/usuarios/                    B4–B5  lógica pura: parseo de getent, planes de crear/eliminar
internal/usuarios/*_test.go
internal/validate/contrasena.go       B4  validate.Contrasena (archivo nuevo)
internal/validate/contrasena_test.go
internal/web/usuarios.go              B2–B5  registerModule("usuarios", ...) y handlers
internal/web/usuarios_test.go
web/templates/usuarios.html           B4–B5  página del módulo
web/templates/llave-lista.html        B3  página "tu llave está lista" con la descarga
web/templates/_llaves-root.html       B3  parcial para la página de Preparación (Parte A)
docs/parte-b/                         plan y notas
```

---

## 5. Fases

**Regla de cierre de cada fase** (no está terminada si falta algo):

1. Tests nuevos escritos **antes** del código y en verde: `go vet ./... && go test -race ./...`.
2. El criterio "Terminada cuando" probado contra la **VM real** (no solo con el `Fake`).
3. Si la fase tiene pantalla: navegable con teclado, un solo `<h1>`, etiquetas en todos los campos.
4. Commit convencional en `daniel`, push, CI en verde en GitHub, aviso al equipo para pasarla a `dev`.

| Fase | Objetivo | Fecha | Estado |
|---|---|---|---|
| B0 | Preparación | dom 18 – lun 19 | ⬜ |
| B1 | Adaptador SSH | lun 19 – **mar 20** (entrega a C) | ⬜ |
| B2 | Bootstrap: Guest Additions → SSH | mar 20 | ⬜ |
| B3 | Llaves de root y descarga | mar 20 – mié 21 | ⬜ |
| B4 | Crear usuario | mié 21 | ⬜ |
| B5 | Eliminar usuario | mié 21 | ⬜ |
| B6 | Videos V5–V8 | vie 23 | ⬜ |
| B7 | Consolidar el documento | vie 23 – sáb 24 | ⬜ |

### B0 — Preparación

- `git switch daniel && git merge origin/dev`, luego `cp config.example.json config.json` y ajusta la IP de la VM de pruebas (te la da Wiliam en C1).
- `go test -race ./...` en verde; `go run ./cmd/consola -simular` y abre `http://127.0.0.1:8090`.
- Lee `docs/base-para-modulos.md` y prueba "Probar conexión" en modo simulado.

**Terminada cuando:** tests en verde en tu equipo y la consola corre en modo simulado.

### B1 — Adaptador SSH (`internal/executor/ssh.go`)

**Qué:** una implementación de `executor.Executor` que habla con la VM por SSH (DB1–DB3).

```go
type SSH struct {
    Host, User, KeyPath, KnownHosts string // KeyPath = keys/consola_ed25519
    Port int                               // 22
    run func(ctx context.Context, name string, args []string, stdin string) (stdout, stderr string, exitCode int, err error) // reemplazable en tests
}
func (s *SSH) Name() string { return "SSH" }
func (s *SSH) Run(ctx context.Context, c executor.Command) (executor.Result, error)
func (s *SSH) CopyTo(ctx context.Context, local, remote string) error   // scp con las mismas opciones
func (s *SSH) CopyFrom(ctx context.Context, remote, local string) error
```

**Pasos:**
1. `Run` llama primero a `c.Check()`; si falla, devuelve el error sin ejecutar nada.
2. Construye los argumentos de `ssh` con una **función pura** `sshArgs(...) []string` (para probarla sin red). El comando remoto es **un solo argumento**: `c.String()`.
3. `c.Stdin` se pasa al `Stdin` del proceso `ssh`.
4. Salida 255 → `error` del canal (DB3). `ctx` cancelado → el proceso se mata (lo hace `exec.CommandContext`).
5. `scp`: `-i`, `-o BatchMode=yes`, mismas opciones de `known_hosts`, `-P` (mayúscula) para el puerto, y `--` antes de las rutas.

**Tests (escríbelos primero):**
- `TestSSHArgsAreFixedAndOrdered`: los argumentos exactos, incluido `BatchMode=yes` y el `--`.
- `TestSSHRemoteCommandIsOneQuotedArgument`: un `Command` con `;`, `$()`, comillas y espacios produce **un** argumento que `/bin/sh -c` reconstruye igual (usa el mismo patrón que `TestCommandStringRunsExactlyNameAndArgs`).
- `TestSSHRunRejectsInvalidCommands`: NUL o `Name` con `=` no llegan a ejecutar `ssh`.
- `TestSSHExit255IsAChannelError` y `TestSSHRemoteExitCodeIsReturned` (con `run` simulado).
- `TestSSHStdinIsPassed`.

**Terminada cuando:** contra la VM real (con tu llave de prueba ya en `/root/.ssh/authorized_keys`), un programa de prueba o test con etiqueta `//go:build vm` ejecuta `hostname` por el adaptador y devuelve el nombre de la VM; con la VM apagada devuelve error del canal en menos de 15 s.

**Entrega a C:** avisa a Wiliam cuando B1 esté en `dev`; él lo usa para operar el balanceador.

### B2 — Bootstrap: Guest Additions → SSH

**Qué:** el botón "Conectar por SSH" de la página de usuarios (o del panel) deja la VM lista para SSH usando el canal Guest Additions de la Parte A, y cambia el canal activo a SSH.

**Pasos del plan (`internal/llaves`), en este orden, ejecutados con el canal Guest Additions:**
1. Si no existe, generar en el host `keys/consola_ed25519` (DB4).
2. `systemctl is-active ssh`; si no está activo: `apt-get install -y openssh-server` (con `DEBIAN_FRONTEND=noninteractive`) y `systemctl enable --now ssh`.
3. `install -d -m 700 -o root -g root /root/.ssh`.
4. Un solo `Plan` con `Then` (ver `docs/base-para-modulos.md`): `Commands` lee el archivo actual con un script fijo que no falla si no existe (`cat /root/.ssh/authorized_keys 2>/dev/null; exit 0`; por Guest Additions, `CopyFrom` de un archivo que no existe devuelve error) → `Then` llama `MergeAuthorizedKeys(actual, nueva) string` (función pura, sin duplicar) y devuelve el comando que lo escribe con `install -d -m 700 /root/.ssh` y el script de escritura de la Parte A (`writeFileScript` en `internal/web/issue.go`: contenido en `"$1"`, ruta en `"$2"`, reemplazo atómico; en `"$3"` va el SHA-256 de lo que se leyó, y si el archivo cambió entre la lectura y la escritura sale con código 12 sin tocarlo; ver `planHostname` en `internal/web/hosts.go` como ejemplo completo), seguido de `chmod 600` → todo queda en una sola operación y un solo registro.
5. Probar con el adaptador SSH: `hostname`. Solo si responde: `state.SetExecutor(destino, ssh)`.
6. **Al arrancar la consola, la VM que ya tiene SSH vuelve a SSH** (ver el hallazgo de abajo). Crea `cmd/consola/canal_ssh.go` con:

   ```go
   package main

   func init() {
       sshChannel = func(t app.Target, keys string) (executor.Executor, string, bool) {
           key := filepath.Join(keys, "consola_ed25519")        // usa siempre la carpeta que recibes
           known := filepath.Join(keys, "known_hosts")          // (en las pruebas es una temporal)
           // 1. Si no existe key (DB4): return nil, "", false, sin intentar conectarte.
           // 2. Crea el adaptador SSH de B1 para t.IP y t.SSHUser con key y known.
           // 3. Pruébalo con el comando `true` (no imprime nada: solo importa si responde)
           //    y un tiempo límite de 3 s (context.WithTimeout; corta antes que el
           //    ConnectTimeout=10 de DB2). Si falla (VM apagada, SSH aún no configurado,
           //    huella cambiada tras restaurar el snapshot): escribe en la terminal un motivo
           //    fijo, por ejemplo log.Printf("destino %s: SSH no responde, se usa Guest Additions", t.Name),
           //    nunca la salida de error de ssh, y return nil, "", false: la consola sigue
           //    con Guest Additions como hoy.
           // 4. Si responde: return ssh, "llave keys/consola_ed25519", true. El texto es fijo:
           //    sin salida de ssh ni rutas fuera de keys/.
       }
   }
   ```

   Para que esto funcione, el adaptador de B1 debe poder crearse desde otro paquete: si su campo `run` (el que reemplazan las pruebas) queda vacío, usa el `ssh` real.

   No edites `main.go`: ya llama a `sshChannel` para cada destino, antes de Guest Additions, **todos a la vez** (con VMs apagadas, el arranque espera una sola prueba de 3 s, no una por VM), y escribe en la terminal `destino pruebas: canal SSH (llave keys/consola_ed25519)`. Si devuelves `true` sin adaptador, la consola lo ignora y sigue con Guest Additions.

**Tests:** `MergeAuthorizedKeys` con archivo vacío, con la llave ya presente (no la duplica), con comentarios y líneas en blanco; el plan exacto de comandos con `Fake`; si la prueba SSH falla, el canal **no** cambia. En `canal_ssh_test.go`, con una carpeta temporal como `keys`: sin la llave, `sshChannel` devuelve `false` sin intentar conectarse; con la llave y la IP `192.0.2.1` (reservada para documentación: nunca responde), devuelve `false` en menos de 4 s. Esa prueba necesita el programa `ssh`; si no está (`exec.LookPath`), sáltala con `t.Skip`.

**Terminada cuando:** con la VM recién restaurada del snapshot, el botón termina en "Operación exitosa", la barra superior muestra **SSH**, y "Probar conexión" funciona por SSH. **Además:** detienes la consola, la vuelves a abrir, y la barra superior sigue diciendo **SSH** (no Guest Additions).

> **Hallazgo de integración (19 de octubre, Parte A).** Al revisar qué necesitaba cada parte de la otra apareció un hueco que no estaba en ningún plan: al arrancar, `cmd/consola/main.go` solo conectaba cada VM por Guest Additions. El canal SSH que deja el bootstrap vive en memoria, así que al reiniciar la consola la VM volvía a Guest Additions, y el enunciado pide que, después del primer contacto, todo vaya por SSH. **Corrección:** la Parte A agregó en `main.go` la variable `sshChannel`, que se prueba antes que Guest Additions para cada destino (con prueba automática: SSH gana donde está listo, Guest Additions queda para las demás, y `-simular` gana sobre ambos). La Parte B solo la llena desde su propio archivo (paso 6); nadie edita archivos del otro.

### B3 — Llaves de root y descarga (§3.1.2)

**Qué:** generar el par de llaves de root, autorizarlo, quitar el acceso con contraseña a root, y entregar la privada para descargar con un mensaje de buen uso (DB6–DB11, DB16).

**Pasos:**
1. Plan en la VM (canal SSH): `mktemp -d` → `ssh-keygen -t ed25519 -N "" -C root@<hostname> -f <dir>/id_ed25519` → leer **solo** la `.pub` con `CopyFrom` → `MergeAuthorizedKeys` → instalar como en B2.
2. Drop-in `10-consola-root.conf` (función pura) → `CopyTo` temporal → `install -m 644 -o root -g root` en `/etc/ssh/sshd_config.d/` → `sshd -t` (si falla: borrar el drop-in y terminar con error) → `systemctl reload ssh`.
3. `CopyFrom <dir>/id_ed25519` → `keys/descargas/<token>` (0600) → `rm -rf <dir>` en la VM.
4. Redirigir a `GET /usuarios/llaves/listo/{token}` que muestra la página `llave-lista.html`: botón "Descargar llave privada", el enlace al detalle técnico (`/resultado/{id}`) y el **mensaje de buen uso**:
   > Guarda la llave en `~/.ssh/` con permisos `600` (`chmod 600 archivo`). No la compartas ni la subas a un repositorio. Ponle una frase de seguridad con `ssh-keygen -p -f archivo`. Esta descarga funciona una sola vez.
5. `GET /usuarios/llaves/{token}`: entrega y borra el archivo (DB11).
6. Parcial `_llaves-root.html` con el formulario (`POST /usuarios/llaves-root`, `data-operacion="Generar llaves de root"`), para que la Parte A lo incluya en Preparación.

**Tests:** la página de descarga sirve el archivo una sola vez (segunda vez 404); token con formato inválido → 404; cabeceras `Content-Disposition` y `Cache-Control: no-store`; el contenido de la privada **no aparece** en `logs/operaciones.jsonl` (test que lee el archivo del registro); si `sshd -t` falla, el drop-in se borra y no hay `reload` (comandos exactos con `Fake`); drop-in generado con contenido exacto.

**Terminada cuando:** en la VM real, con la llave descargada `ssh -i llave root@<ip>` entra; `ssh -o PubkeyAuthentication=no root@<ip>` es rechazado; `sshd -T -C user=root,host=consola,addr=127.0.0.1 | grep permitrootlogin` muestra `prohibit-password` (o `without-password`).

### B4 — Crear usuario (§3.2.1)

**Formulario** (`usuarios.html`, según el diseño de referencia): nombre de usuario, contraseña, casilla "Asignar privilegios de sudo", casilla "Generar llave SSH". Usa `{{aria .Form "usuario"}}`, `{{template "error-campo" (campo .Form "usuario")}}` y `{{template "resumen-errores" .Form}}`.

**Validación:** `validate.Usuario` + `validate.Contrasena` (nuevo, DB13) + el usuario no debe existir (`getent passwd <u>` con código 2 = no existe; usa `AlsoOK: []int{2}` y lee el código). Errores → `s.renderModule(..., http.StatusUnprocessableEntity, Form{Values, Errors: FieldErrors(...)}, datos)`. **Nunca** devuelvas la contraseña en `Form.Values`.

**Plan:** `useradd -m -s /bin/bash -- <u>` → `chpasswd` con `Stdin: "<u>:<contraseña>"` (DB12) → si sudo: `usermod -aG sudo -- <u>` → si llave: como B3 pero en `/home/<u>/.ssh` (`install -d -m 700 -o <u> -g <u>`, `authorized_keys` 600 dueño `<u>`) + drop-in `20-consola-<u>.conf` (DB6) + `sshd -t` + `reload` → descarga de una vez (DB11).

**Tests:** comandos exactos con `Fake` para las 4 combinaciones de casillas; la contraseña aparece solo en `Stdin` y nunca en `Calls()[i].Args` ni en el registro; usuario existente → error junto al campo; drop-in `Match User` con contenido exacto.

**Terminada cuando:** en la VM real el usuario entra con su llave; `sudo -l` funciona solo si se marcó la casilla; `sshd -T -C user=<u>,host=consola,addr=127.0.0.1 | grep passwordauthentication` muestra `no` solo para ese usuario, y otro usuario sin llave sigue con `yes`.

### B5 — Eliminar usuario (§3.2.2)

**Formulario:** lista desplegable con los usuarios de `getent passwd` filtrados (DB14) y casilla "Eliminar todos los archivos". El botón pide confirmación: una segunda pantalla "¿Eliminar a `<u>`? Esta acción no se puede deshacer" con botones "Eliminar" y "Cancelar" (la eliminación nunca ocurre con un solo clic).

**Plan:** `userdel -- <u>` o `userdel -r -- <u>` según la casilla → `rm -f /etc/ssh/sshd_config.d/20-consola-<u>.conf` → `sshd -t` → `systemctl reload ssh`. `Explain`: si `stderr` contiene "is currently used by process", el mensaje es "El usuario tiene procesos activos. Ciérralos en la VM o espera a que terminen y vuelve a intentarlo."

**Tests:** el filtro de `getent` (UID fuera de rango, `nobody`, protegidos); comandos exactos con y sin `-r`; intentar eliminar `root` o el usuario protegido → error y **ningún** comando enviado.

**Terminada cuando:** en la VM real, con la casilla desaparecen el usuario y `/home/<u>`; sin ella, el home queda; el drop-in del usuario ya no existe.

### B6 — Videos (V5–V8)

Guion de cada video: **antes** (estado en la VM) → **acción** en la consola → **después** (estado en la VM).

| Video | Antes | Después |
|---|---|---|
| `V5_B_bootstrap_ssh.mp4` | barra superior: "Guest Additions"; `ls -la /root/.ssh` | barra: "SSH"; `authorized_keys` con la llave de la consola |
| `V6_B_llaves_root.mp4` | `ssh root@<ip>` pide contraseña | entra con la llave descargada; con contraseña es rechazado |
| `V7_B_crear_usuario.mp4` | `getent passwd <u>` vacío | entra con su llave; `sudo -l` según la casilla |
| `V8_B_eliminar_usuario.mp4` | `ls /home` muestra `<u>` | con y sin la casilla de borrar archivos |

### B7 — Consolidar el documento

Junta las secciones de A, B y C en el formato del enunciado ("Documentación"):
1. Descripción de la solución y decisiones (tablas D, DA, DB, DC de los planes).
2. Diseño de pruebas y resultados con evidencias (tests automáticos + videos V1–V11).
3. Problemas encontrados y cómo se resolvieron (cada parte aporta los suyos).
4. Referencias bibliográficas y técnicas.
5. Herramientas de IA usadas y para qué.

**Terminada cuando:** el documento cubre los 5 puntos y cada parte revisó su sección.

---

## 6. Riesgos

| Riesgo | Cómo se nota | Qué hacer |
|---|---|---|
| La consola se queda sin acceso a la VM después de cambiar `sshd` | "Probar conexión" falla por SSH | Por eso DB7 (`sshd -t` antes de recargar). Recuperación: consola de VirtualBox o restaurar el snapshot |
| La huella de la VM cambia (se restauró o recreó) | `ssh` dice "REMOTE HOST IDENTIFICATION HAS CHANGED" | Borrar la línea de esa IP en `keys/known_hosts`; documentarlo como problema y solución |
| `Match User` en un drop-in no se comporta como se espera | `sshd -T -C user=…` muestra `yes` | Esa es la verificación de DB8: si falla, ajustar el orden o el contenido del drop-in hasta que `sshd -T` diga `no` |
| La C1 (VM de pruebas) se atrasa | No hay VM para B1–B5 | Avanzar con `Fake` y tests; probar contra la VM en cuanto esté |
| La F3 (Guest Additions) se atrasa | No hay canal para el bootstrap | Probar B1 con la llave puesta a mano en la VM (`ssh-copy-id`) y hacer B2 al final |

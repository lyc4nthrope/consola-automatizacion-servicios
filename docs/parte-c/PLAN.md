# Plan — Parte C: Servicios e infraestructura

**Responsable:** Wiliam Carmona
**Rama de trabajo:** `wiliam` (se sube a `dev` al cerrar cada fase verificada)
**Ventana:** sábado 17 – miércoles 21 de octubre de 2026 · **Entrega obligatoria al equipo:** VM de pruebas (C1) el **domingo 18**
**Planes relacionados:** [general](../PLAN.md) · [Parte A](../parte-a/PLAN.md) · [Parte B](../parte-b/PLAN.md) · [Guía de la base](../base-para-modulos.md)

> La Parte C prepara el **escenario** (las máquinas virtuales donde todo se prueba), convierte aplicaciones en
> **servicios de systemd** controlados desde la consola, y administra los servidores del **balanceador HAProxy**.
> La primera fase (C1) es la que más urge: sin la VM de pruebas, las Partes A y B no pueden probar contra una VM real.

---

## 0. Cómo usar este plan

### Para la persona

1. Haz **C1 primero, hoy o mañana**: los demás dependen de ella. Avisa al equipo con la IP y el nombre de la VM.
2. Lee este plan completo, luego la [guía de la base](../base-para-modulos.md).
3. Trabaja **una fase a la vez**, en orden (ver "Regla de cierre" en §5).
4. Antes de cada fase: `git switch wiliam && git pull && git merge origin/dev`.
5. Al cerrar una fase: commit en `wiliam`, push, y avisa al equipo para subirla a `dev`.

### Para el asistente de IA que te ayude

Copia esta sección tal cual al inicio de tu conversación con la IA:

```text
Contexto: proyecto Go en ./consola (consola web local que ejecuta comandos como root en VMs Debian).
Soy responsable de la Parte C. Lee, en este orden, antes de proponer código:
  1. docs/parte-c/PLAN.md (este plan; es la especificación, síguela literalmente)
  2. docs/base-para-modulos.md (API de la base: registerModule, Runner, validate, Form, runAndShow)
  3. .claude/claude-security-guidance.md (reglas de seguridad del proyecto; son obligatorias)
  4. internal/web/registro.go, internal/operacion/operacion.go, internal/executor/executor.go
Reglas obligatorias:
  - Nunca construyas una línea de shell con texto del usuario. Un comando es executor.Command{Name, Args}.
  - Todo valor del usuario pasa por internal/validate antes de usarse. Si falta un validador, créalo en
    un archivo NUEVO de internal/validate (no edites validate.go, es de la Parte A).
  - Archivos de la VM (unidades systemd, haproxy.cfg) se generan con funciones puras de Go probadas
    con tablas y se instalan con CopyTo a un temporal + `install`. Nada de sed, echo >> ni tee.
  - Todo lo que toca la VM pasa por operacion.Runner (s.runAndShow en los handlers).
  - Solo edita archivos de la Parte C (lista en §4). Si necesitas cambiar la base, detente y dilo.
  - Escribe primero el test que falla, luego el código. Corre `go vet ./... && go test -race ./...`.
  - Commits convencionales (feat:, fix:, test:, docs:), sin "Co-Authored-By" ni firmas de IA.
  - No declares una fase terminada sin cumplir su criterio "Terminada cuando" contra la VM real.
```

---

## 1. Requisitos del enunciado que cubre la Parte C

| # | Requisito (texto del enunciado) | Fase |
|---|---|---|
| RC1 | Precondiciones a) VM Debian 13.7 actualizada, SSH en ejecución, adaptador en puente; b) VM con HAProxy por defecto; c) disco multiconexión listo para balanceo | C1 |
| RC2 | §3.3 "configuración de una aplicación como servicio de systemd … transferencia de los archivos requeridos … gestión de permisos" | C2 |
| RC3 | §3.3 "botones … start, stop, restart, status, enable y disable" | C3 |
| RC4 | §3.3 y precondición e) "un script … que cada vez que se ejecuta, agrega al final del archivo un mensaje con la fecha y hora" | C4 |
| RC5 | §3.4 "agregar un servidor al back-end del balanceador de carga como eliminarlo" | C5 |
| RC6 | "Escenario de aplicación": diagrama con host, software de virtualización, VMs, SO, interfaces, IPs, mecanismo de comunicación, disco, flujo | C6 |
| RC7 | Pruebas en video con estado antes y después (V9–V11) | C7 |

---

## 2. Qué ya existe en la base (no lo reconstruyas)

| Necesitas | Ya existe | Dónde |
|---|---|---|
| Ejecutar comandos y copiar archivos | `executor.Executor`: `Run`, `CopyTo`, `CopyFrom` (copian **un archivo**) | `internal/executor/executor.go` |
| Simular la VM en tests | `executor.Fake` (`RunFunc`, `Calls()`, `Copies()`), `executor.NewSimulated` | `internal/executor/fake.go` |
| Ejecutar y registrar una operación | `operacion.Plan{Action, Target, Commands, Timeout, AlsoOK, Success, Explain}` | `internal/operacion/operacion.go` |
| Códigos de salida que no son error | `Plan.AlsoOK: []int{3}` para `systemctl status` de un servicio detenido | idem |
| Validar entradas | `validate.NombreServicio`, `validate.Hostname`, `validate.IPv4`, `validate.Puerto` | `internal/validate/validate.go` |
| Pantallas y errores junto al campo | `registerModule`, `ModuleRouter`, `s.renderModule`, `s.runAndShow`, `FieldErrors`, parciales y función `aria` | `internal/web/registro.go`, `web/templates/_formulario.html` |
| Dos destinos (VM de pruebas y balanceador) | `config.json` con `targets`; selector en la barra superior; `state.Current()` | `internal/app` |
| Canal SSH | Fase B1 de la Parte B, martes 20. Mientras tanto: canal Guest Additions (A F3) o `-simular` | — |
| Aplicación de ejemplo | Bookzilla: **un solo binario** (plantillas, estilos y datos iniciales van dentro); al arrancar crea `data/` e `images/` en su carpeta de trabajo; puerto por variable `PORT` (8080 por defecto); muestra el hostname | `~/Downloads/Bookzilla` (Cristhian), `compilarlinux.sh` |

---

## 3. Decisiones de la Parte C

| # | Decisión | Por qué |
|---|---|---|
| DC1 | VM de pruebas `consola-pruebas` creada sobre el disco multiconexión `base-webapp.vdi`, adaptador en puente (`wlo1`), IP por DHCP fija en el router o anotada al arrancar | Ahorra disco (solo guarda diferencias) y ya tiene Debian configurado |
| DC2 | Root con contraseña en la VM de pruebas y en el balanceador (`passwd root`), y Guest Additions instaladas y de la misma versión que VirtualBox 7.2 | Guest Additions (Parte A, F3) se autentica con usuario y contraseña; sin root con contraseña no hay primer contacto |
| DC3 | Snapshot `base-consola` de cada VM al terminar C1 | Cada prueba (y cada video) parte de un estado conocido; si algo se rompe, se restaura |
| DC4 | Las aplicaciones a desplegar viven en el host en `aplicaciones/<nombre>/` (carpeta en `.gitignore`); el formulario ofrece una **lista** de esas carpetas, no una ruta libre | Nadie puede escribir una ruta arbitraria del host; la lista es fácil de usar |
| DC5 | Transferencia: la carpeta se empaqueta en el host con `archive/tar` + `compress/gzip` de Go (un `.tar.gz`), `CopyTo` a `/tmp/consola-<app>.tar.gz`, `install -d -m 755 /opt/<app>`, `tar -xzf … -C /opt/<app> --no-same-owner`, `rm -f` del `.tar.gz` | `CopyTo` copia un archivo; un paquete hace la copia en un paso y conserva los permisos de ejecución |
| DC6 | Usuario de sistema por servicio: `useradd --system --no-create-home --shell /usr/sbin/nologin -- <app>` (si no existe). Programa propiedad de root (`chown -R root:root /opt/<app>`, ejecutable 755); carpeta de datos `/var/lib/<app>` propiedad del usuario del servicio | El servicio no corre como root y no puede modificar su propio programa; solo escribe en sus datos |
| DC7 | La unidad `/etc/systemd/system/<app>.service` se genera con una **función pura** `UnitFile(cfg) string` con: `Description`, `After/Wants=network-online.target`, `User/Group=<app>`, `WorkingDirectory=/var/lib/<app>`, `ExecStart=/opt/<app>/<ejecutable>`, `Environment=PORT=<puerto>`, `Restart=on-failure`, `NoNewPrivileges=yes`, `ProtectSystem=full`, `PrivateTmp=yes`, `WantedBy=multi-user.target` | Probada con tablas, sin la dependencia `RequiresMountsFor=/mnt/datos` de la unidad vieja de Bookzilla (la VM de pruebas no tiene ese disco) |
| DC8 | Instalar la unidad: `CopyTo` temporal → `install -m 644 -o root -g root` → `systemctl daemon-reload` → `systemd-analyze verify /etc/systemd/system/<app>.service` | `daemon-reload` hace que systemd la lea; `verify` detecta errores antes de arrancarla |
| DC9 | Acciones permitidas (lista cerrada): `start`, `stop`, `restart`, `status`, `enable`, `disable`. `status` usa `systemctl status --no-pager --lines=20 -- <unidad>` con `AlsoOK: []int{3}` | El enunciado pide esas seis; `status` devuelve 3 cuando el servicio está detenido, y eso es un estado, no un error |
| DC10 | Unidades protegidas que la consola **no** detiene ni deshabilita: `ssh`, `sshd`, `systemd-*`, `dbus`, `networking`, `NetworkManager`, `getty@*`, `haproxy` (este solo desde el módulo del balanceador) | Detener `ssh` dejaría a la consola sin acceso a la VM |
| DC11 | Script de fecha `deploy/fecha/fecha.sh` (`#!/bin/sh`, `echo "$(date '+%F %T') ejecución desde la consola" >> /var/log/consola-fecha.log`), desplegado como servicio `Type=oneshot` con la misma lógica de C2 | Cada `start` agrega una línea: se ve con `tail`, ideal para el video |
| DC12 | HAProxy: se trabaja sobre el destino `balanceador`; el bloque administrado se llama `frontend consola_front` (`bind *:80`, `default_backend consola_back`) y `backend consola_back` (`balance roundrobin`). Si no existen (la configuración por defecto de Debian no los trae), el primer "agregar" los crea | Bloques con nombre propio: la consola nunca toca otras secciones del archivo |
| DC13 | Editar `haproxy.cfg`: `CopyFrom` → función pura `AddServer(cfg, srv)` / `RemoveServer(cfg, nombre)` → `CopyTo /tmp/consola-haproxy.cfg` → `haproxy -c -f /tmp/consola-haproxy.cfg` → solo si pasa: `install -b -m 644 … /etc/haproxy/haproxy.cfg` (`-b` deja respaldo `haproxy.cfg~`) → `systemctl reload haproxy` | `-c` impide aplicar un archivo roto; `reload` no corta las conexiones abiertas |
| DC14 | Línea de servidor: `server <nombre> <ip>:<puerto> check`. Validación: nombre con `validate.Hostname`, IP con `validate.IPv4`, puerto con `validate.Puerto`; nombre o `ip:puerto` repetidos se rechazan junto al campo | `check` hace que HAProxy deje de enviar tráfico a un servidor caído |
| DC15 | Rutas de la Parte C (todas bajo su prefijo, lo exige `ModuleRouter`): `GET /servicios`, `POST /servicios/desplegar`, `POST /servicios/accion`; `GET /balanceador`, `POST /balanceador/agregar`, `POST /balanceador/quitar` | Una ruta fuera del prefijo hace que la consola no arranque |
| DC16 | Servidores backend para la prueba de C5: las VMs `servidor1..3` (disco multiconexión) con Bookzilla desplegado **desde la propia consola** (C2) | La prueba del balanceador demuestra también el módulo de servicios; Bookzilla muestra el hostname, así que se ve el reparto |

---

## 4. Archivos de la Parte C

Solo estos. Todo lo demás es de otra parte (si necesitas un cambio allí, pídelo).

```
internal/servicios/                   C2–C4  lógica pura: UnitFile, empaquetado tar.gz, planes, lista protegida
internal/servicios/*_test.go
internal/balanceador/                 C5  lógica pura: AddServer, RemoveServer, ListServers
internal/balanceador/*_test.go
internal/balanceador/testdata/        C5  haproxy.cfg por defecto de Debian (copiado del balanceador real) y variantes
internal/web/servicios.go             C2–C4  registerModule("servicios", ...) y handlers
internal/web/servicios_test.go
internal/web/balanceador.go           C5  registerModule("balanceador", ...) y handlers
internal/web/balanceador_test.go
web/templates/servicios.html          C2–C4
web/templates/balanceador.html        C5
deploy/fecha/fecha.sh                 C4
docs/parte-c/                         plan, diagrama (C6) y notas
```

`aplicaciones/` (carpeta local con las apps a desplegar) va en `.gitignore`: pide a Cristhian que la agregue, o agrégala tú en tu primer commit y avísalo.

---

## 5. Fases

**Regla de cierre de cada fase** (no está terminada si falta algo):

1. Tests nuevos escritos **antes** del código y en verde: `go vet ./... && go test -race ./...`.
2. El criterio "Terminada cuando" probado contra la **VM real**.
3. Si la fase tiene pantalla: navegable con teclado, un solo `<h1>`, etiquetas en todos los campos.
4. Commit convencional en `wiliam`, push, CI en verde en GitHub, aviso al equipo para pasarla a `dev`.

| Fase | Objetivo | Fecha | Estado |
|---|---|---|---|
| C1 | Entorno de pruebas (VMs) | sáb 17 – **dom 18** (entrega al equipo) | ⬜ |
| C2 | Desplegar una aplicación como servicio | lun 19 – mar 20 | ⬜ |
| C3 | Botones de systemd | mar 20 | ⬜ |
| C4 | Script de fecha y hora | mar 20 | ⬜ |
| C5 | HAProxy: agregar y quitar servidores | mié 21 | ⬜ |
| C6 | Diagrama del escenario | jue 22 | ⬜ |
| C7 | Videos V9–V11 y sección del documento | vie 23 | ⬜ |

### C1 — Entorno de pruebas

**Pasos en el host (VirtualBox 7.2):**
1. Crear `consola-pruebas` sobre `base-webapp.vdi` (multiconexión), 1 GB de RAM, adaptador en puente sobre `wlo1`.
2. Dentro de la VM: `apt update && apt upgrade -y`, `cat /etc/debian_version` (debe decir 13.7 o superior), `systemctl status ssh` activo, `passwd root` (DC2), `hostnamectl set-hostname consola-pruebas`.
3. Guest Additions: insertar la imagen desde VirtualBox, `apt install -y build-essential linux-headers-$(uname -r)`, ejecutar el instalador, reiniciar. Comprobar **desde el host**:
   `VBoxManage guestcontrol consola-pruebas run --exe /usr/bin/hostname --username root --password '<clave>' --wait-stdout`
4. Balanceador: en la VM `balanceador`, dejar `haproxy.cfg` **por defecto** (`apt install --reinstall -o Dpkg::Options::="--force-confmiss" haproxy` o restaurar `/etc/haproxy/haproxy.cfg` desde el paquete), root con contraseña y Guest Additions como en el paso 3. Copiar ese `haproxy.cfg` por defecto a `internal/balanceador/testdata/haproxy-debian-default.cfg`.
5. Anotar IP de cada VM y escribir el ejemplo para el equipo (sin contraseñas) en el chat del grupo; cada uno lo pone en su `config.json`.
6. Snapshot `base-consola` de `consola-pruebas` y `balanceador` (DC3).

**Terminada cuando:** el comando del paso 3 devuelve `consola-pruebas` desde el host, el equipo tiene las IPs, y existen los dos snapshots.

### C2 — Desplegar una aplicación como servicio (§3.3)

**Formulario** (`servicios.html`): aplicación (lista de carpetas de `aplicaciones/`, DC4), nombre del servicio (`validate.NombreServicio`), ejecutable dentro de la carpeta (se valida que exista y sea ejecutable en el host), puerto (`validate.Puerto`, opcional). Botón "Desplegar" con `data-operacion="Desplegar aplicación"`.

**Plan:** empaquetar (DC5) → `CopyTo` → `install -d -m 755 /opt/<app>` → `tar -xzf` → `chown -R root:root /opt/<app>` → `useradd --system …` si no existe (DC6; `id -u <app>` con `AlsoOK: []int{1}` para saber si existe) → `install -d -m 750 -o <app> -g <app> /var/lib/<app>` → unidad (DC7, DC8) → `systemctl daemon-reload` → `systemd-analyze verify`.

**Para Bookzilla:** compilar con `compilarlinux.sh` y poner el binario en `aplicaciones/bookzilla/bookzilla`. Solo hace falta el binario.

**Tests (escríbelos primero):** `UnitFile` con tablas (contenido exacto; sin `RequiresMountsFor`; `Environment=PORT` solo si hay puerto); el `.tar.gz` contiene los archivos con su modo (ejecutable 755) y rechaza enlaces simbólicos y rutas con `..`; plan exacto de comandos con `Fake`; nombre de servicio o carpeta inválidos → error junto al campo y **ningún** comando enviado.

**Terminada cuando:** en la VM real, después del despliegue, `systemctl cat bookzilla` muestra la unidad generada y, tras `systemctl start bookzilla` (C3), `curl http://<ip>:8080` responde con la página del catálogo.

### C3 — Botones de systemd

**Formulario:** nombre del servicio (texto, con sugerencias de los desplegados) y los seis botones (DC9), cada uno `name="accion" value="<acción>"`. Como hay varios botones de envío, **no** los deshabilites al enviar (la base ya usa `aria-disabled` y bloquea el doble clic).

**Plan:** `systemctl <acción> -- <unidad>`; para `status`, DC9. Las unidades protegidas (DC10) se rechazan para `stop` y `disable` con un mensaje claro junto al campo.

**Tests:** cada acción produce el comando exacto; acción fuera de la lista → 400 sin comandos; `status` de un servicio detenido (código 3) es "Operación exitosa" y muestra la salida; `stop` de `ssh` se rechaza sin enviar nada.

**Terminada cuando:** los seis botones funcionan sobre Bookzilla en la VM real y `enable` sobrevive a un reinicio de la VM (`systemctl is-enabled bookzilla` → `enabled` y la página responde tras reiniciar).

### C4 — Script de fecha y hora

**Pasos:** crear `deploy/fecha/fecha.sh` (DC11), copiarlo a `aplicaciones/fecha/`, desplegarlo con C2 como `Type=oneshot` (agrega a `UnitFile` el soporte de `Type` y sin `Restart`).

**Tests:** `UnitFile` para `oneshot` sin `Restart=on-failure`.

**Terminada cuando:** cada "Iniciar" en la consola agrega una línea nueva a `/var/log/consola-fecha.log` (se ve con `tail` en la VM).

### C5 — HAProxy: agregar y quitar servidores (§3.4)

**Página** (`balanceador.html`): tabla con los servidores actuales de `consola_back` (leídos con `CopyFrom` + `ListServers`), formulario "Agregar servidor" (nombre, IP, puerto) y un botón "Quitar" por servidor (con confirmación, como cualquier acción destructiva). Todo sobre el destino `balanceador`.

**Plan:** DC12–DC14. `Explain`: si `haproxy -c` falla, el mensaje es "La configuración resultante no es válida; no se aplicó ningún cambio" y la salida técnica muestra el error de HAProxy.

**Tests (con `testdata/`):** `AddServer` sobre la configuración por defecto de Debian crea `frontend` y `backend` correctos; sobre una que ya los tiene, solo agrega la línea; nombre o `ip:puerto` duplicados → error; `RemoveServer` quita solo esa línea y deja el resto del archivo **byte por byte igual**; `ListServers` lee los servidores; si `haproxy -c` falla no hay `install` ni `reload` (comandos exactos con `Fake`).

**Terminada cuando:** con Bookzilla en `servidor1` y `servidor2` (desplegados con C2), al agregarlos y recargar varias veces `http://<ip-balanceador>` en el navegador aparecen los dos hostnames; al quitar uno, deja de aparecer.

### C6 — Diagrama del escenario

Debe mostrar como mínimo (lista del enunciado): equipo host · VirtualBox 7.2 · cada VM con su SO (Debian 13.7) · interfaces de red (puente sobre `wlo1`) · direcciones IP · mecanismo de comunicación (Guest Additions primero, SSH con llaves después) · disco (`base-webapp.vdi` multiconexión) · flujo de una operación (navegador → consola → validación → `Executor` → VM → resultado → registro).

Usa los **mismos nombres, IPs y canales** que el mapa vivo de la consola (Parte A, F6b). Guárdalo en `docs/parte-c/escenario.png` (y su fuente editable) con una descripción breve.

**Terminada cuando:** el diagrama tiene los 9 elementos y la descripción está en el documento.

### C7 — Videos y documento

| Video | Antes | Después |
|---|---|---|
| `V9_C_desplegar_bookzilla.mp4` | `systemctl status bookzilla` → no existe | la unidad existe y la página responde |
| `V10_C_botones_systemd.mp4` | `systemctl is-active bookzilla` | cada botón cambia el estado; `tail` del log de fecha crece |
| `V11_C_balanceador.mp4` | `haproxy.cfg` sin `consola_back`; la página muestra un solo hostname | aparecen dos hostnames al recargar; al quitar uno, desaparece |

Sección de la Parte C para el documento (Daniel la consolida en B7): decisiones (tabla DC), pruebas y resultados, problemas y soluciones, referencias.

---

## 6. Riesgos

| Riesgo | Cómo se nota | Qué hacer |
|---|---|---|
| Guest Additions de otra versión que VirtualBox | `guestcontrol` falla o se queda esperando | Reinstalar las Guest Additions de la versión 7.2 (paso 3 de C1); documentarlo |
| Poco disco en el host (~16 GB libres) | VirtualBox no crea la VM | Usar el disco multiconexión (DC1); no clonar discos completos |
| La IP de una VM cambia (DHCP) | La consola no llega a la VM | Reserva de IP en el router o actualizar `config.json` y avisar al equipo |
| Detener `ssh` o `haproxy` desde la consola por error | La consola pierde la VM o el balanceador | Lista protegida DC10 |
| `haproxy -c` no existe en el `PATH` del usuario | Error "command not found" | Usar la ruta completa `/usr/sbin/haproxy` en el comando |
| El canal SSH (B1) se atrasa | No se puede operar el balanceador por SSH | Trabajar con Guest Additions (A F3) o `-simular`; el código no cambia porque usa la interfaz `Executor` |

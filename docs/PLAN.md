# Plan — Parcial 2: Automatización de servicios

**Equipo:** Cristhian Osorio · Daniel Pérez · Wiliam Carmona
**Entrega:** domingo 25 de octubre de 2026, 11:59 pm · **Sustentación:** desde el 26 de octubre, 2:00 pm
**Fuente:** `Parcial 2 - Automatización de servicios.docx`

> El trabajo se divide en **tres partes con la misma carga** (11 puntos de esfuerzo cada una, ver §4).
> Cada parte es dueña de sus carpetas del código, así nadie pisa el trabajo de otro.
> Antes de repartir hay un **día común** (Hito 0) donde se acuerda el contrato que todos usan.

---

## 1. Qué pide el parcial y qué tenemos

Una **aplicación web en Go que corre en el host** y funciona como consola de automatización de una VM Debian 13.7. Cada botón ejecuta en la VM, sin intervención humana, una tarea que en los labs hicimos a mano.

| Requisito del enunciado | Estado actual | Brecha |
|---|---|---|
| App web en Go con módulos y formularios (§3) | Experiencia con Bookzilla (`~/Downloads/Bookzilla`) | Todo el código de la consola |
| Comunicación por Guest Additions antes de SSH | El Parcial 1 ya usa `guestcontrol` desde Go (`scriptAlmVM4_fix.go`) | Adaptarlo como pieza reutilizable |
| Después, solo SSH con llaves | LabSSH hecho a mano | Automatizarlo |
| VM Debian 13.7 en puente con SSH (precondición a) | `servidor1..3` en Debian 13, puente | Verificar versión exacta (`cat /etc/debian_version`) y Guest Additions |
| VM con HAProxy en configuración por defecto (b) | `balanceador`, pero con configuración modificada | Restaurar `haproxy.cfg` por defecto |
| Disco multiconexión listo para balanceo (c) | `base-webapp.vdi` | Ninguna |
| Ejecutable de Bookzilla y sus archivos (d) | `bookzilla` (linux), `templates/`, `static/`, `images/`, `data/`, `deploy/bookzilla.service` | La unidad existente exige `/mnt/datos`; hay que generar una sin esa dependencia |
| Script que agrega fecha y hora a un archivo (e) | No existe | Escribirlo (5 líneas de bash) |
| Validar entradas, reportar resultado, conservar datos técnicos del error | — | Paquetes `validate` y `oplog` |
| Video por prueba (antes y después), diagrama y documento | — | Hito 3 |

**Restricción del host:** quedan ~16 GB libres. Toda VM nueva se crea sobre el disco multiconexión, porque solo guarda diferencias.

---

## 2. Decisiones técnicas

Cada una es una recomendación; si el equipo cambia alguna, se anota aquí.

| # | Decisión | Por qué |
|---|---|---|
| D1 | La app corre en el host Debian | Solo ahí existe `VBoxManage`, necesario para usar Guest Additions |
| D2 | Interfaz `Executor` con dos implementaciones: `GuestControl` y `SSH` (contrato en §3) | Los módulos no saben por qué canal van; pasar de Guest Additions a SSH es cambiar una pieza, no reescribir módulos |
| D3 | El adaptador SSH llama a `ssh`/`scp` del sistema con `-o BatchMode=yes` | Mismo estilo `exec.Command` del Parcial 1, sin librerías nuevas. `BatchMode` hace que falle en vez de quedarse esperando una contraseña: eso es "no interactivo" |
| D4 | Validación por lista blanca (regex) y argumentos separados, **nunca** concatenar texto del usuario en un comando de shell | Evita inyección de comandos (`tree; rm -rf /`) y cumple "validar antes de ejecutar" |
| D5 | Cambios de `sshd` en archivos de `/etc/ssh/sshd_config.d/` y probar con `sshd -t` antes de reiniciar | Si la configuración queda mal, no se reinicia y no perdemos el acceso a la VM |
| D6 | Root: `PermitRootLogin prohibit-password`. Usuario nuevo con llave: bloque `Match User <u>` con `PasswordAuthentication no` | El enunciado pide apagar la contraseña para *ese* usuario, no para todos |
| D7 | La consola tiene **su propia llave** (en el host), distinta de las llaves que se generan y descargan para root y usuarios | Si el usuario borra o pierde la llave descargada, la consola no pierde el acceso |
| D8 | Registro por operación: comando, salida, errores, código de salida, duración, en `logs/` (JSON por línea) | Es el "conservar información técnica útil para el diagnóstico" |
| D9 | HAProxy: editar el bloque `backend`, validar con `haproxy -c -f`, aplicar con `systemctl reload` | `reload` no corta las conexiones abiertas; `-c` impide aplicar un archivo roto |
| D10 | Interfaz con `html/template` y Bootstrap, copiando los diseños de referencia del enunciado | Ya lo usamos en Bookzilla |
| D11 | Los destinos (VM de pruebas y balanceador) se definen en `config.json`: nombre de la VM en VirtualBox, IP, usuario. Las contraseñas van en variables de entorno | Hay dos VMs que administrar, y las contraseñas no deben quedar en el repositorio |

---

## 3. Contrato común (se acuerda el día 1, antes de repartir)

Este es el único punto donde las tres partes se tocan. Con esto acordado, cada uno trabaja en paralelo usando un `Fake` mientras la implementación real no está lista.

```go
// internal/executor/executor.go
type Command struct {
    Name  string   // ejecutable, ej. "apt-get"
    Args  []string // argumentos YA validados, cada uno por separado
    Stdin string   // opcional (ej. "usuario:clave" para chpasswd). Garantizado en SSH; verificar en GuestControl
}

type Result struct {
    Cmd      string        // comando completo, para el registro
    Stdout   string
    Stderr   string
    ExitCode int
    Duration time.Duration
}

type Executor interface {
    Run(ctx context.Context, c Command) (Result, error)
    CopyTo(ctx context.Context, localPath, remotePath string) error   // host → VM
    CopyFrom(ctx context.Context, remotePath, localPath string) error // VM → host (descargar llaves, leer haproxy.cfg)
}
```

**Estructura del repositorio y dueño de cada carpeta:**

```
consola/
├── cmd/consola/main.go          A
├── config.json                  A  (D11)
├── internal/
│   ├── executor/
│   │   ├── executor.go          común (día 1)
│   │   ├── fake.go              común (día 1) — devuelve respuestas fijas para probar sin VM
│   │   ├── guestcontrol.go      A
│   │   └── ssh.go               B
│   ├── validate/                A  (D4)
│   ├── oplog/                   A  (D8)
│   ├── preparacion/             A  apps, /etc/issue, hostname
│   ├── llaves/                  B  bootstrap SSH, llaves root, llaves de usuario
│   ├── usuarios/                B
│   ├── servicios/               C  systemd
│   └── balanceador/             C  HAProxy
├── web/templates/, web/static/  cada quien la vista de su módulo; A el layout común
└── deploy/                      C  script de fecha, plantilla de unidad systemd
```

**Reglas de trabajo:**

- Un repositorio Git privado. Cada parte trabaja en su rama (`parte-a/...`, `parte-b/...`, `parte-c/...`) y hace merge a `main` al cerrar cada fase.
- Mensajes de commit convencionales (`feat:`, `fix:`, `docs:`).
- Una fase está **terminada** solo cuando su criterio de verificación pasa contra la VM real y hay commit.
- `logs/`, `keys/` y cualquier llave privada van en `.gitignore`.

---

## 4. Las tres partes

Puntos de esfuerzo: 1 = tarea sencilla de pocas horas, 3 = la parte más difícil de su bloque. Cada parte suma **11**: una pieza de infraestructura difícil, funcionalidades de dificultad media y un entregable de cierre.

| Parte | Tema | Pieza difícil | Funcionalidades | Cierre | Total |
|---|---|---|---|---|---|
| A | Plataforma y preparación del sistema | Esqueleto web + `GuestControl` (5) | Apps, `/etc/issue`, hostname (3) | Validación y registro (3) | 11 |
| B | Seguridad y usuarios | Adaptador `SSH` + bootstrap (4) | Llaves root, crear y eliminar usuarios (6) | Consolidar el documento (1) | 11 |
| C | Servicios e infraestructura | Despliegue systemd (5) | Script de fecha, HAProxy (4) | Entorno de VMs + diagrama (2) | 11 |

Responsables (lo decide el equipo): **A:** Cristhian Osorio · **B:** Daniel Perez · **C:** Wiliam Carmona

---

### Parte A — Plataforma y preparación del sistema

Es la base sobre la que corren los demás módulos y cubre el §3.1 del enunciado excepto las llaves.

| Fase | Objetivo | Cómo se hace y por qué | Terminada cuando | Pts | Estado |
|---|---|---|---|---|---|
| A1 | Esqueleto web | Servidor `net/http`, layout común con encabezado y pie del grupo, portada con las 4 tarjetas de módulo (diseño de referencia), página de resultado reutilizable (éxito / error + detalle técnico desplegable). Carga `config.json` (D10, D11) | La portada y las 4 páginas de módulo vacías se navegan en el navegador | 3 | ⬜ |
| A2 | Adaptador `GuestControl` | `VBoxManage guestcontrol <vm> run --exe <ruta> --username root --password $PASS -- <nombre> <args...>` y `copyto`/`copyfrom`. Cada argumento va separado (D4). Reutilizar lo aprendido en el Parcial 1 (D2) | Un botón de prueba ejecuta `hostname` en la VM y muestra la salida | 2 | ⬜ |
| A3 | `validate` + `oplog` | Funciones `Paquete`, `Usuario`, `Hostname`, `Puerto`, `IP`, `NombreServicio` con regex (ej. hostname RFC 1123: `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`; usuario Debian: `^[a-z_][a-z0-9_-]{0,31}$`). `oplog` escribe una línea JSON por operación (D8). Pruebas unitarias con `go test` | `go test ./internal/validate/...` pasa con casos válidos e inválidos (incluido `tree; rm -rf /`) | 3 | ⬜ |
| A4 | Instalar / eliminar aplicaciones (3.1.1) | `DEBIAN_FRONTEND=noninteractive apt-get install -y <pkg>` y `apt-get remove -y <pkg>`. La variable y `-y` evitan cualquier pregunta: eso es "no interactivo" | Instalar `tree` → `which tree` lo encuentra → eliminarlo → ya no está | 1 | ⬜ |
| A5 | Personalizar `/etc/issue` (3.1.3) | Casillas → códigos de agetty: `\n` hostname, `\d` fecha, `\t` hora, `\4` IP, más el mensaje. Se escribe el archivo y se reinicia `getty@tty1` para verlo sin reiniciar la VM | La consola de VirtualBox muestra el mensaje con los datos elegidos | 1 | ⬜ |
| A6 | Hostname + `/etc/hosts` (3.1.4) | `hostnamectl set-hostname <nombre>` y reemplazar la línea `127.0.1.1` de `/etc/hosts`. Sin esa línea, `sudo` tarda porque no resuelve el nombre propio | `hostname` y `cat /etc/hosts` muestran el nombre nuevo | 1 | ⬜ |

**Depende de:** nada (es la base). **Le entrega a B y C:** layout, `validate`, `oplog`, página de resultado (A1 y A3 deben estar en `main` el **lunes 19**).

**Calidad del front (dos revisiones con distinto alcance):**

| Cuándo | Herramienta | Qué revisa | Por qué en ese momento |
|---|---|---|---|
| Al cerrar A1 y cada pantalla (A4, A5, A6) | Skill `web-design-guidelines` | Los archivos de esa pantalla: etiquetas de formularios, errores junto al campo, estado de "procesando", foco visible, confirmación antes de acciones destructivas, mensajes de error con el siguiente paso | Lo que se corrige en el layout y la página de resultado lo heredan B y C; corregirlo tarde obliga a tocar todas las pantallas |
| Una vez, el jueves 22 (integración), con las pantallas de A, B y C ya hechas | Skill `frontend-ux-audit` | Todo el front y la app funcionando en el navegador (escritorio y móvil): consistencia entre módulos, jerarquía visual, flujos completos | Solo tiene sentido con todas las pantallas existentes; antes sería auditar algo a medias |

Los hallazgos de cada revisión se corrigen dentro de la misma fase, antes de marcarla ✅.

---

### Parte B — Seguridad y usuarios

Cubre el cambio de canal a SSH, las llaves (§3.1.2) y la gestión de usuarios (§3.2). Es lo que convierte la consola en "solo SSH con llaves".

| Fase | Objetivo | Cómo se hace y por qué | Terminada cuando | Pts | Estado |
|---|---|---|---|---|---|
| B1 | Adaptador `SSH` | `ssh -i keys/consola -o BatchMode=yes -o StrictHostKeyChecking=accept-new root@<ip> -- <cmd>` y `scp` para copiar. **Ojo:** `ssh` une los argumentos en un solo texto que interpreta el shell remoto, así que cada argumento se debe entrecomillar (función `shellQuote`) para no reabrir la inyección que A evitó (D3, D4) | `hostname` funciona igual por SSH que por `GuestControl`; un argumento con `;` llega como texto literal | 3 | ⬜ |
| B2 | Bootstrap: Guest Additions → SSH | Por `GuestControl`: asegurar que `openssh-server` esté activo, crear `/root/.ssh` (700) e instalar la llave pública **de la consola** en `authorized_keys` (600). Desde ahí la app usa solo SSH (D7) | Un botón "Conectar por SSH" hace el bootstrap y las siguientes operaciones aparecen en el registro como `ssh` | 1 | ⬜ |
| B3 | Llaves de root + descarga (3.1.2) | `ssh-keygen -t ed25519 -N "" -f /root/.ssh/id_root`, agregar la `.pub` a `authorized_keys`, escribir `PermitRootLogin prohibit-password` en `sshd_config.d/`, `sshd -t`, reiniciar `ssh` (D5, D6). `CopyFrom` trae la privada al host, se borra de la VM y se sirve con `Content-Disposition: attachment`. Mensaje: guardarla con `chmod 600`, no compartirla, ponerle passphrase con `ssh-keygen -p` | Con la llave descargada se entra como root; `ssh -o PubkeyAuthentication=no root@<ip>` es rechazado | 2 | ⬜ |
| B4 | Crear usuario (3.2.1) | `useradd -m -s /bin/bash <u>`, contraseña por `chpasswd` vía `Stdin` (no en los argumentos, porque se vería en `ps`). Casilla sudo → `usermod -aG sudo <u>`. Casilla llave → igual que B3 pero en `/home/<u>/.ssh` con `chown` + bloque `Match User <u>` / `PasswordAuthentication no` (D6) | El usuario entra con su llave, `sudo -l` funciona, y `sshd -T -C user=<u>,host=x,addr=1.2.3.4` muestra `passwordauthentication no` | 3 | ⬜ |
| B5 | Eliminar usuario (3.2.2) | Lista desplegable desde `getent passwd` (UID 1000–59999). `userdel <u>` o `userdel -r <u>` según la casilla, y borrar su archivo de `sshd_config.d/`. Bloquear el borrado de root y del usuario que usa la consola | Con la casilla, desaparecen el usuario y `/home/<u>`; sin ella, el home queda | 1 | ⬜ |
| B6 | Consolidar el documento | Juntar las secciones de A, B y C en el formato del enunciado: solución y decisiones (§2 de este plan), pruebas y resultados, problemas y soluciones, referencias, uso de IA | El documento cubre los 5 puntos de "Documentación" del enunciado | 1 | ⬜ |

**Depende de:** A1 y A3 (layout, validación). **Le entrega a C:** el adaptador SSH, que C necesita para operar sobre el balanceador (B1 en `main` el **martes 20**; mientras tanto C usa `Fake` o `GuestControl`).

---

### Parte C — Servicios e infraestructura

Cubre systemd (§3.3), HAProxy (§3.4), el entorno de pruebas (precondiciones del §2) y el diagrama del escenario.

| Fase | Objetivo | Cómo se hace y por qué | Terminada cuando | Pts | Estado |
|---|---|---|---|---|---|
| C1 | Entorno de pruebas | VM `consola-pruebas` sobre `base-webapp.vdi` (multiconexión, ahorra disco), en puente, Debian 13.7 (`apt update && apt upgrade`), Guest Additions y SSH. Restaurar `haproxy.cfg` por defecto en `balanceador`. Snapshot de cada VM antes de empezar | `cat /etc/debian_version` muestra 13.7 y `VBoxManage guestcontrol consola-pruebas run ... hostname` responde | 1 | ⬜ |
| C2 | Desplegar una app como servicio (3.3) | `CopyTo` del binario, `templates/`, `static/`, `images/` y `data/` a `/opt/<app>/`. Crear usuario de sistema, `chown -R` y `chmod 755` al binario. Generar la unidad desde una plantilla (basada en `Bookzilla/deploy/bookzilla.service`, **sin** `RequiresMountsFor=/mnt/datos`, porque la VM de pruebas no tiene ese disco) y `systemctl daemon-reload` | Después del despliegue, Bookzilla responde en `http://<ip>:8080` | 3 | ⬜ |
| C3 | Botones start, stop, restart, status, enable, disable | `systemctl <acción> <unidad>`, con el nombre validado. **Ojo:** `systemctl status` devuelve código 3 cuando el servicio está detenido; eso es un estado, no un error, y la interfaz no debe mostrarlo en rojo | Los 6 botones funcionan y `enable` sobrevive a un reinicio de la VM | 2 | ⬜ |
| C4 | Script de fecha y hora (precondición e) | Script bash que hace `echo "$(date '+%F %T') ejecutado" >> /var/log/fecha.log`, desplegado con la misma lógica de C2 como servicio `Type=oneshot` | Cada `start` agrega una línea nueva; se ve con `tail` | 1 | ⬜ |
| C5 | HAProxy: agregar y quitar servidor (3.4) | `CopyFrom` de `haproxy.cfg`, editar en Go el bloque `backend` (agregar o quitar la línea `server <nombre> <ip>:<puerto> check`), subir a un temporal, `haproxy -c -f` y, solo si pasa, reemplazar y `systemctl reload haproxy` (D9). **Ojo:** la configuración por defecto de Debian no trae `frontend` ni `backend`; el primer "agregar" debe crearlos | Al recargar el navegador varias veces en `http://<ip-balanceador>`, aparece el hostname del servidor nuevo (Bookzilla lo muestra en la página); al quitarlo, deja de aparecer | 3 | ⬜ |
| C6 | Diagrama del escenario | Host, VirtualBox 7.2, cada VM con su SO, adaptador puente, IPs, Guest Additions y SSH como canales, disco multiconexión y flujo de una operación (navegador → consola → `Executor` → VM → resultado) | El diagrama tiene los 9 elementos mínimos que pide el enunciado | 1 | ⬜ |

**Depende de:** A1 y A3; B1 para correr sobre SSH. **Le entrega a todos:** el entorno (C1 debe estar listo el **domingo 18**).

**Para las pruebas de C5** se necesitan servidores backend: se crean como VMs sobre `base-webapp.vdi` y se les despliega Bookzilla con el propio módulo de C2. Así la prueba del balanceador demuestra también el módulo de servicios.

---

## 5. Cronograma

| Hito | Fecha | Qué se hace | Quién |
|---|---|---|---|
| H0 | sáb 17 – dom 18 | Repositorio, contrato del §3 con `fake.go`, `config.json`. C1 (entorno). Preguntar al profesor las dos dudas del §7 | Todos |
| H1 | lun 19 – mié 21 | Fases A1–A6, B1–B5 y C2–C5 en paralelo | Cada parte |
| H2 | jue 22 | Integración: pasada completa de punta a punta, todo por SSH después del bootstrap, desde una VM recién restaurada del snapshot | Todos |
| H3 | vie 23 – sáb 24 | Videos (§6), C6 (diagrama), cada parte escribe su sección y B6 consolida | Cada parte |
| — | dom 25 | Margen para imprevistos y entrega antes de las 11:59 pm | Todos |

**Por qué este orden:** el contrato se cierra primero para que nadie dependa de otro para avanzar. La integración tiene un día propio porque es donde aparecen los errores entre partes. Los videos se graban al final, cuando todo funciona junto, para no grabar dos veces.

---

## 6. Pruebas y videos

Cada quien graba las pruebas de su parte. Cada video sigue el mismo guion de tres tiempos, que es lo que pide el enunciado:

1. **Antes:** mostrar el estado actual en la VM (ej. `which tree` no encuentra nada).
2. **Acción:** ejecutar la operación desde la consola web y mostrar el mensaje de resultado.
3. **Después:** mostrar en la VM que el estado cambió (ej. `which tree` → `/usr/bin/tree`).

| # | Prueba | Parte |
|---|---|---|
| V1 | Instalar y eliminar una aplicación | A |
| V2 | Personalizar `/etc/issue` | A |
| V3 | Cambiar hostname y `/etc/hosts` | A |
| V4 | Validación: una entrada inválida es rechazada con mensaje claro y queda en el registro | A |
| V5 | Bootstrap Guest Additions → SSH | B |
| V6 | Llaves de root, descarga y contraseña rechazada | B |
| V7 | Crear usuario con sudo y llave | B |
| V8 | Eliminar usuario con y sin borrar archivos | B |
| V9 | Desplegar Bookzilla como servicio | C |
| V10 | Los 6 botones de systemd + script de fecha | C |
| V11 | Agregar y quitar un servidor del balanceador | C |

Nombre de archivo: `V<n>_<parte>_<tema>.mp4` (ej. `V6_B_llaves_root.mp4`).

---

## 7. Dudas para el profesor (resolver en H0)

1. **SSH:** las precondiciones dicen que la VM ya tiene SSH instalado, pero el texto pide usar Guest Additions "antes de instalar SSH". Este plan asume: bootstrap por Guest Additions (B2) y desde ahí solo SSH.
2. **Balanceador:** ¿"agregar un servidor" incluye crear la VM desde el disco multiconexión con `VBoxManage`, o basta con editar `haproxy.cfg`? Este plan cubre lo segundo. Si pide lo primero, se agrega una fase C7 de 2 puntos y se compensa pasando C6 (diagrama) a la Parte A.

# Plan — Parte A: Plataforma y preparación del sistema

**Responsable:** Cristhian Osorio
**Plan general:** [../PLAN.md](../PLAN.md) (este documento detalla la Parte A; si algo choca, se corrige primero el plan general)
**Ventana de trabajo:** sábado 17 – miércoles 21 de octubre de 2026 · **Entrega obligatoria a B y C:** lunes 19 (fases F0–F2 en `main`)

> La Parte A construye **la base de la consola**: el servidor web, el diseño común, la validación de datos, el registro de operaciones, la primera vía de comunicación con la VM (Guest Additions) y tres funcionalidades del módulo *Preparación del sistema*.
> Todo lo que se haga aquí lo heredan Daniel (B) y Wiliam (C), por eso cada fase termina **verificada** antes de pasar a la siguiente.

---

## 1. Requisitos del enunciado → estado actual

Cada frase del enunciado que le toca a la Parte A, dónde se cumple y qué hay hoy.

| # | Requisito (texto del enunciado) | Se cumple en | Estado actual |
|---|---|---|---|
| R1 | "Desarrolle una aplicación web en Golang" | F0, F1 | Repositorio creado, sin código |
| R2 | "Organizar las funciones por módulos" | F1 (portada con tarjetas, rutas por módulo) | — |
| R3 | "Mostrar formularios adecuados" | F1 (patrón de formulario), F4–F6 | Diseños de referencia del enunciado (4 imágenes) |
| R4 | "Validar los datos antes de ejecutar cada operación" | F2 (`validate`), F4–F6 | Regex definidas en el plan general |
| R5 | "Informar si esta terminó correctamente" | F2 (página de resultado) | — |
| R6 | "En caso de error, presentar un mensaje comprensible y conservar información técnica útil para el diagnóstico" | F2 (`oplog` + detalle técnico desplegable) | — |
| R7 | "De manera no interactiva" | F3 (adaptador), F4 (`DEBIAN_FRONTEND`, `-y`) | El Parcial 1 ya usa `guestcontrol` sin intervención |
| R8 | "Antes de instalar SSH, la comunicación … a través de las Guest Additions" | F3 (`GuestControl`) | Código de referencia: `scriptAlmVM4_fix.go` |
| R9 | §3.1.1 Instalar y eliminar aplicaciones, notificar el resultado | F4 | — |
| R10 | §3.1.3 `/etc/issue` con hostname, fecha, hora, IP y mensaje personalizado | F5 | — |
| R11 | §3.1.4 Asignar nombre de host y actualizar `/etc/hosts` | F6 | Hecho a mano en el lab de balanceo |
| R12 | Pruebas grabadas en video con estado antes y después (V1–V4) | F8 | — |
| R13 | Documentación: decisiones, pruebas, problemas, referencias, uso de IA | F8 | — |

**Herramientas disponibles en el host:** Go 1.24.4, VirtualBox 7.2.16, `gh` autenticado, skills `web-design-guidelines` y `frontend-ux-audit`.

---

## 2. Decisiones de la Parte A

Complementan D1–D11 del plan general. Cada una es la opción recomendada; si se cambia, se anota aquí con el motivo.

| # | Decisión | Alternativa descartada | Por qué |
|---|---|---|---|
| DA1 | Solo librería estándar: `net/http` con el enrutador de Go 1.22+ (`GET /preparacion`, `POST /preparacion/apps`) y `html/template` | Gin, Echo, Chi | El enrutador estándar ya distingue método y ruta; cero dependencias que justificar en la sustentación |
| DA2 | Bootstrap 5 y Bootstrap Icons **copiados al repositorio** y todo empaquetado con `go:embed` | Cargarlos desde un CDN | La consola funciona sin internet (aula de sustentación) y se entrega como **un solo binario** |
| DA3 | El servidor escucha solo en `127.0.0.1:8090` | `0.0.0.0` | La consola ejecuta comandos como root en la VM; nadie más en la red debe poder usarla |
| DA4 | Arquitectura por capas: **handlers web** (adaptador de entrada) → **servicios del módulo** (lógica) → **`Executor`** (puerto de salida) | Handlers que llaman a `exec.Command` directamente | La lógica de cada módulo se prueba con el `Fake`, sin VirtualBox; cambiar de canal no toca los módulos |
| DA5 | Patrón **POST → Redirect → GET**: cada operación se guarda con un ID en `oplog` y se muestra en `GET /resultado/{id}` | Mostrar el resultado en la respuesta del POST | Recargar la página no repite la instalación, y cada resultado queda con una URL propia para el documento y el video |
| DA6 | Operaciones **síncronas** con límite de tiempo (`context.WithTimeout`, 10 min para `apt`, 30 s para el resto) y botón deshabilitado con "Procesando…" mientras corre | Cola de trabajos en segundo plano | Suficiente para una consola de un solo usuario; mucho más simple de construir y explicar |
| DA7 | Archivos de la VM (`/etc/issue`, `/etc/hosts`) se generan **en el host con funciones puras de Go**, se copian a un temporal con `CopyTo` y se instalan con `install -m 644` | `sed` o `echo >>` con el texto del usuario | Las funciones puras se prueban con `go test`; el texto del usuario nunca pasa por un shell; si la copia falla, el archivo original no se toca |
| DA8 | Antes de ejecutar, una **verificación semántica** además de la regex: ¿el paquete existe? (`apt-cache show`), ¿está instalado? (`dpkg -s`) | Solo regex | Convierte errores técnicos de `apt` en mensajes claros: "El paquete `xyz` no existe en los repositorios" |
| DA9 | `apt-get remove` (no `purge`) | `purge` | Conserva la configuración del paquete; es reversible |
| DA10 | Integración continua mínima en GitHub Actions: `gofmt -l`, `go vet`, `go test ./...` en cada push | Sin CI | Detecta que alguien rompió la base antes de que B o C lo sufran; en la sustentación muestra disciplina de trabajo |
| DA11 | Diseño "centro de control" con cuatro piezas propias: **terminal de resultados**, **identidad visual** (cuadrícula sutil de fondo, barra de estado inferior, acento solo para lo activo), **mapa vivo de la infraestructura** y **paleta de comandos Ctrl + K** | Decorar más (degradados, vidrio, animaciones sin función) | Piezas que solo tienen sentido en esta app: muestran qué se ejecutó, en qué VM y por qué canal. El mapa vivo además cumple dentro de la app el diagrama del escenario que pide el enunciado. Todas respetan teclado, `prefers-reduced-motion` y tema claro |

**Por acordar con B y C el día 1** (afecta el contrato común):

- **Canal activo por destino.** La app guarda, para cada VM, qué `Executor` está activo. La Parte A expone `app.SetExecutor(destino, ex)`; la Parte B lo llama al terminar el bootstrap SSH.
- **Usuario de Guest Additions.** `guestcontrol` necesita usuario y contraseña de la VM. Se usa **root** con contraseña definida, leída de `CONSOLA_GUEST_USER` y `CONSOLA_GUEST_PASS`. C1 (Wiliam) debe dejar root con contraseña en la VM de pruebas.

---

## 3. Estructura que crea la Parte A

```
consola/
├── cmd/consola/main.go            F0  arranque: config, plantillas, rutas, escucha en 127.0.0.1
├── config.example.json            F0  destinos de ejemplo (el real, config.json, va en .gitignore)
├── internal/
│   ├── app/                       F1  estado: config, destinos y Executor activo por destino
│   ├── web/                       F1  servidor, rutas, render de plantillas, middleware de registro
│   ├── validate/                  F2  validaciones (con pruebas)
│   ├── oplog/                     F2  registro de operaciones (JSON por línea en logs/)
│   ├── executor/guestcontrol.go   F3  adaptador Guest Additions
│   └── preparacion/               F4–F6  servicios: apps.go, issue.go, hostname.go (con pruebas)
├── web/
│   ├── templates/                 F1  layout.html, inicio.html, resultado.html, preparacion.html
│   └── static/                    F1  bootstrap/, bootstrap-icons/, consola.css
└── .github/workflows/ci.yml       F0  gofmt + vet + test
```

---

## 4. Fases

**Regla de cierre de todas las fases** (no se marca ✅ si falta algo):

1. `gofmt -l .` sin salida, `go vet ./...` y `go test ./...` en verde.
2. El criterio de verificación de la fase, probado de verdad (contra la VM cuando aplica).
3. Si la fase tocó pantallas: revisión con la skill `web-design-guidelines` sobre esas plantillas y hallazgos corregidos.
4. Commit convencional en la rama `cristhian` y merge a `dev` para revisión (flujo de ramas en el plan general, §3).

| Fase | Objetivo | Alcance | Decisiones | Terminada cuando | Fecha | Estado |
|---|---|---|---|---|---|---|
| F0 | Andamiaje | `go.mod` (`github.com/lyc4nthrope/consola-automatizacion-servicios`), carpetas del §3, `config.example.json`, `main.go` que arranca y responde, Bootstrap e iconos copiados, CI | DA2, DA3, DA10 | `go run ./cmd/consola` responde en `http://127.0.0.1:8090` y la CI pasa en GitHub | sáb 17 – dom 18 | ✅ |
| F1 | Esqueleto web | Layout común (encabezado con logo y título del curso, pie con el equipo), portada con 4 tarjetas de módulo, página vacía por módulo, estado de la app con destinos y `SetExecutor` | DA1, DA4 | Se navega portada → cada módulo → portada; con el teclado también; prueba `httptest` de cada ruta devuelve 200 | dom 18 | ✅ |
| F2 | Validación, registro, resultado y terminal | `validate` (paquete, usuario, hostname, IP, puerto, nombre de servicio, mensaje libre), `oplog` (ID, fecha, destino, canal, comando, salida, error, código, duración), página `/resultado/{id}` con mensaje claro y detalle técnico desplegable. **Terminal de resultados:** comando, salida, cronómetro mientras corre, chip con código de salida y duración, botón copiar. **Identidad visual:** cuadrícula sutil de fondo y barra de estado inferior (VM, canal, última operación) | DA5, D4, D8, DA11 | Pruebas de tabla para cada validación, incluidas entradas maliciosas (`tree; rm -rf /`, `$(reboot)`, saltos de línea); una operación del `Fake` aparece en `logs/` y en `/resultado/{id}` | dom 18 – **lun 19** | ✅ |
| — | **Hito: base entregada** | Merge de F0–F2 a `main` y aviso a Daniel y Wiliam | — | B y C pueden crear sus páginas sobre el layout y usar `validate` y `oplog` | **lun 19** | ⬜ |
| F3 | Adaptador Guest Additions | `Run`, `CopyTo`, `CopyFrom` con `VBoxManage guestcontrol`. La construcción de argumentos es una función pura separada de la ejecución | DA4, D2 | Prueba unitaria de los argumentos generados (sin VirtualBox); contra la VM, un botón de diagnóstico ejecuta `hostname` y muestra la salida; con la VM apagada, el mensaje dice eso y no un error críptico | lun 19 | ⬜ |
| F4 | Instalar / eliminar aplicaciones (§3.1.1) | Formulario con un campo y dos botones. Instalar: validar → `apt-cache show` → `apt-get update` → `apt-get install -y`. Eliminar: validar → `dpkg -s` → `apt-get remove -y`. Siempre con `DEBIAN_FRONTEND=noninteractive` | DA6, DA8, DA9 | `tree`: no está → instalar → `which tree` lo encuentra → eliminar → ya no está. Paquete inexistente: mensaje claro sin ejecutar nada. Pruebas con el `Fake` verifican los comandos exactos | mar 20 | ⬜ |
| F5 | Mensaje de entrada `/etc/issue` (§3.1.3) | Casillas (hostname `\n`, fecha `\d`, hora `\t`, IP `\4`, sistema `\s`, terminal `\l`) y mensaje libre. Vista previa en la página. Generar → copiar → instalar → `systemctl restart getty@tty1` | DA7 | La consola de VirtualBox muestra el mensaje con los datos elegidos. Prueba unitaria: una `\` escrita por el usuario se guarda como `\\` (si no, agetty la interpreta como código) | mar 20 | ⬜ |
| F6 | Nombre de host y `/etc/hosts` (§3.1.4) | `hostnamectl set-hostname`. `/etc/hosts`: `CopyFrom` → función pura que reemplaza la línea `127.0.1.1` (o la agrega si no existe) sin tocar las demás → `CopyTo` + `install` | DA7 | `hostname` y `cat /etc/hosts` muestran el nombre nuevo y el resto del archivo queda igual. Pruebas unitarias: con línea `127.0.1.1`, sin ella y con comentarios | mié 21 | ⬜ |
| F6b | Mapa vivo de la infraestructura | SVG en el panel: host → VirtualBox → VMs de `config.json`, con el canal de cada una (punteado = Guest Additions, continuo = SSH, sin línea = sin conexión) y un pulso durante una operación. Clic o Enter sobre una VM la vuelve destino. Texto alternativo con la misma información para lectores de pantalla | DA11 | Las VMs y canales del mapa coinciden con el estado real; se opera con teclado; sin animación con `prefers-reduced-motion`; se ve bien en ambos temas | mié 21 – jue 22 | ⬜ |
| F7 | Pulido de la base | Revisar que los mensajes de error de F3–F6 digan qué pasó y qué hacer; `README` con instrucciones de compilación y ejecución; `make build` genera el binario único. **Paleta Ctrl + K:** diálogo accesible para buscar y saltar a cualquier acción o destino | — | Una persona que no programó la Parte A compila y usa la consola solo con el README | mié 21 | ⬜ |
| F8 | Evidencias de la Parte A | Videos V1–V4 (antes → acción → después). Sección de la Parte A para el documento: decisiones (este §2), pruebas y resultados, problemas y soluciones, referencias | — | Cuatro videos con nombre `V<n>_A_<tema>.mp4` y sección entregada a Daniel (B6) | vie 23 – sáb 24 | ⬜ |

**Jueves 22:** día de integración del equipo (plan general, H2). Ese día se corre `frontend-ux-audit` sobre todo el front.

**Coordinación con la Parte C:** el mapa vivo (F6b) muestra el escenario dentro de la app, pero el documento sigue necesitando el diagrama estático de C6 (Wiliam). Conviene que ambos usen los mismos nombres, IPs y canales.

---

## 5. Estrategia de pruebas

Qué se prueba y con qué, para que la sustentación tenga evidencia y no solo "funciona en mi máquina".

| Capa | Herramienta | Qué demuestra |
|---|---|---|
| `validate` | Pruebas de tabla con `go test` | Cada regla acepta lo válido y rechaza lo inválido, incluidos intentos de inyección |
| Funciones puras (`/etc/issue`, `/etc/hosts`, argumentos de `guestcontrol`) | Pruebas de tabla con `go test` | El texto generado es exactamente el esperado, sin depender de la VM |
| Servicios de `preparacion` | `go test` con el `Fake` del contrato común | Se envían a la VM los comandos exactos, en el orden correcto, con los argumentos separados |
| Handlers web | `net/http/httptest` | Rutas responden, el formulario inválido no llega a ejecutar nada, el POST redirige al resultado |
| Extremo a extremo | Manual contra la VM, grabado en video | Estado antes → operación en la consola → estado después |

---

## 6. Riesgos y cómo se manejan

| Riesgo | Señal | Respuesta |
|---|---|---|
| La VM no tiene contraseña de root (instalación de Debian con root deshabilitado) | `guestcontrol` devuelve error de autenticación | C1 define contraseña de root; mientras tanto, desarrollar contra el `Fake` |
| `apt` bloqueado por actualizaciones automáticas | `Could not get lock /var/lib/dpkg/lock-frontend` | Detectar ese texto y mostrar: "Otro proceso está usando apt; intenta de nuevo en un minuto" |
| Guest Additions de la VM desactualizadas frente a VirtualBox 7.2 | `guestcontrol` falla o no responde | Reinstalar las Guest Additions en C1; queda anotado como problema y solución en el documento |
| Reiniciar `getty@tty1` cierra una sesión abierta en esa consola | Se pierde lo que había en la ventana de la VM | Advertencia en la pantalla del módulo antes de aplicar |
| La base no está lista el lunes 19 | B y C sin layout | F0–F2 tienen prioridad sobre todo lo demás; si hace falta, F3 se mueve un día |

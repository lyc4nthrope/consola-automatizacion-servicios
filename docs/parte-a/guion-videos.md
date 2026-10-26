# Guion de los videos de la Parte A (V1–V4)

Cada video sigue los tres tiempos que pide el enunciado: **Antes → Acción → Después**.
Nombre de archivo: `V<n>_A_<tema>.mp4`. Duración sugerida: 1 a 3 minutos cada uno.

---

## 0. Preparación (una sola vez, sin grabar)

### 0.1 Pantalla para grabar
Organiza dos ventanas lado a lado:

| Izquierda | Derecha |
|---|---|
| Navegador en `http://127.0.0.1:8090` (la consola) | Ventana de VirtualBox de `consola-pruebas`, con sesión de **root** abierta |

Una terminal del host abajo o en otra pestaña, solo para arrancar la consola.

### 0.2 Dejar la VM en un estado limpio
En el host:
```bash
VBoxManage snapshot consola-pruebas take "antes-videos-A"     # para poder repetir una toma
VBoxManage startvm consola-pruebas                            # con ventana (no headless): se ve en el video
```
En la ventana de la VM, inicia sesión como `root` y comprueba que no exista lo que vamos a instalar:
```bash
which tree          # no debe imprimir nada
hostname            # anota el nombre actual
```
> Si `tree` ya está instalado, elimínalo antes: `apt-get remove -y tree`.

### 0.3 Arrancar la consola
En el host:
```bash
cd ~/Downloads/context_nube/consola
make build
./consola
```
La terminal debe decir:
```
destino pruebas: canal Guest Additions con la contraseña de keys/guest-pass-pruebas
consola escuchando en http://127.0.0.1:8090 (2 destinos configurados)
```
Abre `http://127.0.0.1:8090`, elige el destino **pruebas** y pulsa **Probar conexión**: debe responder con el nombre de la VM.
Si responde, todo está listo para grabar.

> **Si falla Probar conexión:** la VM no terminó de arrancar o las Guest Additions no han cargado. Espera 30 s y repite.

---

## V1 — Instalar y eliminar una aplicación (`V1_A_instalar_eliminar.mp4`)

**Qué demuestra:** §3.1.1. La consola instala y elimina paquetes de forma no interactiva e informa el resultado.

| # | Tiempo | Dónde | Qué hacer | Qué decir |
|---|---|---|---|---|
| 1 | Antes | VM | `which tree` → no imprime nada. `tree` → *command not found* | "La aplicación tree no está instalada." |
| 2 | Acción | Consola | Panel → **Preparación del sistema**. En *Instalar y eliminar aplicaciones* escribe `tree` y pulsa **Instalar** | "La consola valida el nombre, actualiza la lista de paquetes, comprueba que el paquete existe y lo instala sin preguntas." |
| 3 | Resultado | Consola | Muestra la página de resultado (éxito). Despliega el **detalle técnico**: comandos, salida, código de salida, duración | "Cada paso queda registrado con su salida y su duración." |
| 4 | Después | VM | `which tree` → `/usr/bin/tree`. Ejecuta `tree /etc/apt` | "Ahora está instalado y funciona." |
| 5 | Acción | Consola | Vuelve a Preparación, escribe `tree` y pulsa **Eliminar** | "Ahora la eliminamos." |
| 6 | Resultado | Consola | Página de resultado (éxito) | — |
| 7 | Después | VM | `which tree` → nada | "El paquete ya no está." |

**Extra (recomendado):** escribe `paquete-que-no-existe` y pulsa **Instalar**. La consola debe detenerse con un mensaje claro de que el paquete no existe, sin intentar instalarlo.

---

## V2 — Personalizar `/etc/issue` (`V2_A_issue.mp4`)

**Qué demuestra:** §3.1.3. El mensaje de entrada muestra hostname, fecha, hora, IP y un mensaje personalizado.

| # | Tiempo | Dónde | Qué hacer | Qué decir |
|---|---|---|---|---|
| 1 | Antes | VM | `cat /etc/issue` (el mensaje por defecto de Debian). Cambia a otra terminal con **Ctrl + Alt + F2** (en VirtualBox: *Host + F2*) para ver la pantalla de login | "Este es el mensaje que aparece antes de iniciar sesión." |
| 2 | Acción | Consola | En *Mensaje de entrada (/etc/issue)* marca las casillas **Nombre del equipo, Dirección IP, Fecha y Hora**. En *Mensaje* escribe `Bienvenido - Computación en la Nube 2026-2` | "Cada casilla se traduce a un código de agetty, como `\n` para el nombre o `\4` para la IP." |
| 3 | Vista previa | Consola | Pulsa **Vista previa** | "Antes de tocar la VM vemos cómo va a quedar." |
| 4 | Acción | Consola | Pulsa **Instalar en la VM** → página de resultado (éxito), que muestra el mensaje tal como lo ve agetty | "La consola guarda una copia del original, escribe el archivo y ejecuta `agetty --reload`: la pantalla de login se actualiza sin cerrar las sesiones abiertas." |
| 5 | Después | VM | En la terminal 2 (*Host + F2*) la pantalla de login ya muestra el nuevo mensaje con datos reales. Vuelve a la terminal 1 (*Host + F1*) y ejecuta `cat /etc/issue` para mostrar los códigos | "Los códigos se reemplazan por los valores reales de la máquina." |
| 6 | Restaurar | Consola | Pulsa **Restaurar** (enlace debajo del formulario) → resultado (éxito) | "También se puede volver al mensaje original." |
| 7 | Después | VM | `cat /etc/issue` → mensaje original de Debian | — |

---

## V3 — Cambiar hostname y `/etc/hosts` (`V3_A_hostname.mp4`)

**Qué demuestra:** §3.1.4. El nombre cambia en el sistema y en `/etc/hosts`.

| # | Tiempo | Dónde | Qué hacer | Qué decir |
|---|---|---|---|---|
| 1 | Antes | VM | `hostname` y `cat /etc/hosts` (señala la línea `127.0.1.1`) | "El equipo se llama así, y esta línea de /etc/hosts lo asocia a sí mismo." |
| 2 | Acción | Consola | En *Nombre de host y /etc/hosts* escribe `servidor-nube` y pulsa **Cambiar nombre** | "La consola cambia el nombre con hostnamectl y reemplaza la línea 127.0.1.1." |
| 3 | Resultado | Consola | Página de resultado (éxito) + detalle técnico | — |
| 4 | Después | VM | `hostname` → `servidor-nube`. `cat /etc/hosts` → `127.0.1.1  servidor-nube`. `hostnamectl` | "Si no se actualiza /etc/hosts, sudo tarda porque no resuelve su propio nombre." |
| 5 | Después | Consola | Pulsa **Probar conexión** en el Panel → responde `servidor-nube` | "La consola sigue llegando a la VM con el nombre nuevo." |

> Al terminar, si quieres devolver el nombre original, cámbialo de nuevo desde la consola (no hace falta grabarlo).

---

## V4 — Validación de entradas (`V4_A_validacion.mp4`)

**Qué demuestra:** "validar los datos antes de ejecutar" y "mensaje comprensible + información técnica para el diagnóstico".

| # | Qué escribir | Dónde | Resultado esperado | Qué decir |
|---|---|---|---|---|
| 1 | `tree; rm -rf /` | Nombre del paquete → **Instalar** | Rechazado **sin ejecutar nada**: el mensaje explica qué caracteres se permiten y da un ejemplo | "Esto sería una inyección de comandos. La validación por lista blanca la bloquea antes de llegar a la VM." |
| 2 | `Mi_Servidor` | Nombre nuevo → **Cambiar nombre** | Rechazado: el hostname solo admite minúsculas, números y guiones | "Las reglas siguen el estándar de nombres de host (RFC 1123)." |
| 3 | `localhost` | Nombre nuevo | Rechazado con la explicación de por qué no se debe usar | — |
| 4 | `12345` | Nombre nuevo | Rechazado: un nombre solo de números se confunde con una dirección | — |
| 5 | `openssh-server` | Nombre del paquete → **Eliminar** | Rechazado: la consola lo necesita para llegar a la VM por SSH | "La consola también se protege de cortarse su propio acceso." |
| 6 | Ninguna casilla y mensaje vacío | /etc/issue → **Instalar en la VM** | "Escribe un mensaje o elige al menos un dato para mostrar." | — |
| 7 | `paquete-que-no-existe` | Nombre del paquete → **Instalar** | Error comprensible: el paquete no existe. Despliega el **detalle técnico** | "Este sí llegó a la VM: el error queda con el comando, la salida y el código." |
| 8 | — | Panel → *Últimas operaciones* | Aparece la operación fallida del paso 7 | "Todo queda en el registro de operaciones." |
| 9 | — | Terminal del host | `tail -n 3 logs/operaciones.jsonl` | "El registro también se guarda en un archivo, una línea JSON por operación, solo legible por el usuario." |

En los pasos 1 a 6 muestra que **en la VM no cambió nada** (por ejemplo `which tree` sigue vacío y `hostname` sigue igual).

---

## Después de grabar

```bash
# En el host: apaga la VM y vuelve al estado anterior a los videos
VBoxManage controlvm consola-pruebas acpipowerbutton
VBoxManage snapshot consola-pruebas restore "antes-videos-A"
```

### Lista de control

| Video | Antes | Acción | Después | Detalle técnico visible |
|---|---|---|---|---|
| V1 instalar/eliminar | ⬜ | ⬜ | ⬜ | ⬜ |
| V2 /etc/issue | ⬜ | ⬜ | ⬜ | ⬜ |
| V3 hostname | ⬜ | ⬜ | ⬜ | ⬜ |
| V4 validación | ⬜ | ⬜ | ⬜ | ⬜ |

### Consejos de grabación
- Sube el tamaño de letra del navegador (Ctrl + +) y de la terminal de la VM para que se lea en el video.
- Antes de cada video, limpia la terminal de la VM (`clear`).
- Si una toma sale mal, restaura el snapshot `antes-videos-A` y repite.

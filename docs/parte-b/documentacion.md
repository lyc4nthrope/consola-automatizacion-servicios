**Parte B: Seguridad y usuarios**

**Responsable:** Daniel Stiven Perez Cordoba

**Video de pruebas de la Parte B (parte_b.mp4):** [enlace]

# **1\. Descripción de la solución**

La Parte B convierte la consola en **"solo SSH con llaves"**, como pide el enunciado: crea el canal SSH, hace el paso de Guest Additions a SSH (bootstrap), genera y entrega las llaves de root y administra las cuentas de la VM. Cubre el §3.1.2 (llaves SSH de root y descarga de la privada) y el §3.2 (crear y eliminar usuarios con sudo y llave propia).

| Requisito del enunciado | Cómo se cumple |
| :---- | :---- |
| Comunicación exclusivamente por SSH con llaves después del primer contacto | Adaptador `SSH` con `BatchMode` y `IdentitiesOnly`; el canal del destino cambia a SSH solo si la prueba de sesión pasa, y se vuelve a probar al arrancar la consola |
| §3.1.2 Crear llaves de root, autorizarlas y quitar el login por contraseña | `ssh-keygen` en la VM, fusión en `authorized_keys` y drop-in `PermitRootLogin prohibit-password` |
| §3.1.2 Permitir la descarga de la llave privada con recomendaciones de uso | Descarga de una sola vez con token de 128 bits, caducidad de 15 minutos y mensaje de buen uso |
| §3.2.1 Crear usuario con casilla de sudo | `useradd`, `chpasswd` por entrada estándar y `usermod -aG sudo` |
| §3.2.1 Generar llave, instalar la pública y descargar la privada, sin contraseña | Flujo de llaves sobre `/home/<usuario>` y drop-in `Match User` con `PasswordAuthentication no` |
| §3.2.2 Eliminar usuario, con casilla de borrar archivos | `userdel` o `userdel -r`, retiro del drop-in y recarga del sshd |
| Validar antes de ejecutar y conservar información técnica | `validate.Usuario`, `validate.Contrasena` y registro en `logs/operaciones.jsonl` |

## **1.1 Arquitectura del canal**

La Parte B no cambia los módulos: implementa un `Executor` más. Los módulos siguen construyendo un `operacion.Plan` con comandos y argumentos separados; lo único que cambia es quién los entrega a la VM.

| Pieza | Archivo | Responsabilidad |
| :---- | :---- | :---- |
| Adaptador SSH | `internal/executor/ssh.go` | Ejecutar comandos y copiar archivos con `ssh` y `scp` del sistema |
| Lógica de llaves | `internal/llaves/` | Llave de la consola, fusión de `authorized_keys`, drop-ins, descargas de una sola vez |
| Lógica de cuentas | `internal/usuarios/` | Parseo de `getent`, filtro de cuentas y planes de crear, editar y eliminar |
| Rutas y manejadores | `internal/web/usuarios.go` | Registro del módulo, formularios y operaciones |
| Página | `web/templates/usuarios.html` | Vista del módulo |
| Arranque | `cmd/consola/canal_ssh.go` | Elegir SSH para un destino que ya está preparado |

El adaptador ejecuta el cliente del sistema, sin librerías nuevas:

```bash
ssh -i keys/consola_ed25519 \
    -o BatchMode=yes \
    -o IdentitiesOnly=yes \
    -o StrictHostKeyChecking=accept-new \
    -o UserKnownHostsFile=keys/known_hosts \
    -o ConnectTimeout=10 \
    -p 22 root@<ip> -- '<comando>'
```

- `-i`: identidad de la consola, distinta de las llaves que se entregan a root y a los usuarios.
- `BatchMode=yes`: si no puede autenticar, falla; nunca se queda esperando una contraseña.
- `IdentitiesOnly=yes`: usa solo esa llave, sin depender del agente ni de `~/.ssh`.
- `StrictHostKeyChecking=accept-new`: registra la VM la primera vez y rechaza una huella distinta.
- `UserKnownHostsFile`: `known_hosts` propio del proyecto; no toca el del usuario del sistema.
- `ConnectTimeout=10`: acota la espera cuando la VM está apagada.
- `-p` (y `-P` en `scp`): puerto. `--`: fin de las opciones.
- El comando remoto viaja como **un solo argumento** ya entrecomillado (`executor.Quote`), de modo que un `;` o un `$( )` escrito por el usuario llega como texto.
- El código **255** de `ssh` es fallo de transporte (VM apagada, llave rechazada, huella cambiada). Cualquier otro código es el del comando remoto y se registra como tal.

## **1.2 Bootstrap: Guest Additions → SSH**

El botón **Conectar por SSH** usa el canal activo (Guest Additions en una VM recién preparada) para instalar la llave pública de la consola en `/root/.ssh/authorized_keys`, sin borrar las llaves que ya estuvieran:

1. Genera en el host el par `keys/consola_ed25519` la primera vez (`ssh-keygen -t ed25519 -N ""`, carpeta `keys/` en 700).
2. Lee el archivo actual con un script fijo que no falla si no existe.
3. Fusiona en Go: añade la llave solo si su material no está ya (comparando el campo base64, ignorando comentarios).
4. Escribe de forma atómica y protegida: respaldo `.consola-original` la primera vez, SHA-256 de lo leído y salida con código 12 si el archivo cambió entre la lectura y la escritura.
5. Prueba la sesión con `hostname`. Solo si responde, cambia el canal del destino a SSH.

La operación es **idempotente**: repetirla no duplica la llave ni reescribe el respaldo.

## **1.3 Llaves de root y descarga**

**Generación:** `mktemp -d` crea una carpeta temporal en la VM; `ssh-keygen -t ed25519 -N "" -C root@<host> -f <dir>/id_ed25519` genera el par; solo la `.pub` se lee con `CopyFrom` y se fusiona en `authorized_keys`.

**Contraseña fuera:** se instala el drop-in `/etc/ssh/sshd_config.d/10-consola-root.conf` con `PermitRootLogin prohibit-password`. Antes de aplicarlo, `sshd -t` valida toda la configuración; si falla, se borra el drop-in y **no** se recarga nada. Después de `systemctl reload ssh`, `sshd -T -C user=root,host=consola,addr=127.0.0.1` confirma el efecto real.

**Descarga de una sola vez:** la privada se trae con `CopyFrom` a `keys/descargas/<token>` (0600), se borra de la VM y se sirve desde `GET /usuarios/llaves/{token}` con `Content-Disposition: attachment` y `Cache-Control: no-store`. El token es aleatorio de 128 bits, caduca a los 15 minutos y el archivo se aparta y se borra al entregarlo: una segunda descarga responde 404.

**Mensaje de buen uso:** la página "Tu llave está lista" indica guardarla en `~/.ssh/` con permisos 600 (`chmod 600`), no compartirla, no subirla a un repositorio y protegerla con `ssh-keygen -p -f archivo`.

## **1.4 Gestión de usuarios**

| Acción | Ruta | Qué hace |
| :---- | :---- | :---- |
| Listar | `GET /usuarios` | Lee `getent passwd` y muestra solo cuentas administrables (UID 1000–59999, sin `nobody`, sin `root` ni la cuenta de la consola) |
| Crear | `POST /usuarios/crear` | Valida, comprueba que el nombre esté libre, crea la cuenta y, si se pidió, sudo y llave |
| Editar | `GET/POST /usuarios/editar` | Cambia contraseña, activa o desactiva sudo y, si se pide, regenera la llave |
| Eliminar | `POST /usuarios/eliminar` | Pide confirmación en una segunda pantalla y borra con o sin archivos |
| Conectar | `POST /usuarios/conectar-ssh` | Bootstrap de la sección 1.2 |
| Llaves de root | `POST /usuarios/llaves-root` | Flujo de la sección 1.3 |
| Descargar | `GET /usuarios/llaves/{token}` | Entrega la privada una sola vez |

**Crear:** `getent passwd <usuario>` (código 2 = libre), `useradd -m -s /bin/bash -- <usuario>`, `chpasswd` con `usuario:contraseña` por **entrada estándar** (nunca en los argumentos, donde se vería en `ps`), y `usermod -aG sudo -- <usuario>` si se marcó la casilla. Con llave, se repite el flujo de 1.3 sobre `/home/<usuario>/.ssh/authorized_keys`, con `chown` al usuario y drop-in `20-consola-<usuario>.conf`.

**Editar:** `chpasswd` si hay contraseña nueva; `usermod -aG sudo -- <usuario>` para activar sudo y `gpasswd -d <usuario> sudo` para desactivarlo. El panel muestra el estado actual de sudo (leyendo `groups`) y de la llave (comprobando el drop-in), para que la pantalla no parezca vacía al volver a entrar.

**Eliminar:** `userdel -- <usuario>` o `userdel -r -- <usuario>` según la casilla, `rm -f -- /etc/ssh/sshd_config.d/20-consola-<usuario>.conf`, `sshd -t` y `systemctl reload ssh`. Si el usuario tiene procesos activos, `userdel` falla y la consola lo traduce a un consejo en lugar de mostrar el error crudo.

## **1.5 Archivos de la VM: authorized_keys y drop-ins**

Los archivos se generan en Go y se instalan con un script fijo; el texto del usuario nunca se concatena.

**authorized_keys:** el script recibe la ruta en `$1`, el contenido en `$2` y el SHA-256 de lo leído en `$3`; usa `set -e`, rechaza el cambio concurrente (código 12), guarda el original una sola vez, crea el temporal con `mktemp` en la misma carpeta, fija 600 y reemplaza con `mv` (atómico).

**Drop-ins de sshd:** Debian carga `/etc/ssh/sshd_config.d/*.conf` al inicio y gana el primer valor, por eso los nombres llevan prefijo (`10-` para root, `20-` para usuarios).

```
# 10-consola-root.conf
PermitRootLogin prohibit-password

# 20-consola-<usuario>.conf
Match User <usuario>
PasswordAuthentication no
KbdInteractiveAuthentication no
```

`KbdInteractiveAuthentication no` cierra también la vía PAM de teclado-interactivo; apagar solo `PasswordAuthentication` no basta en todas las configuraciones.

## **1.6 Interfaz del módulo**

La página se organiza como un CRUD:

- **Conexión con la VM:** badge del canal (`SSH`, `Guest Additions`, `Simulado`) y el botón de bootstrap solo cuando hace falta.
- **Cuentas de la VM:** tabla con una fila por cuenta y acciones **Editar** y **Eliminar** por fila.
- **Nuevo usuario:** panel desplegable nativo (`<details>`), que funciona con y sin JavaScript.
- **Editar:** panel con el estado actual de sudo y de la llave, contraseña opcional y regeneración de llave.
- **Eliminar:** segunda pantalla de confirmación; nunca se borra con un solo clic.
- **Resultado en línea:** al terminar, la operación muestra su mensaje y la salida técnica con los comandos ejecutados, sin recargar la página.

# **2\. Decisiones tomadas**

## **2.1 Decisiones generales del equipo que aplica la Parte B**

| \# | Decisión | Por qué |
| :---- | :---- | :---- |
| D1 | La aplicación corre en el host | Solo ahí existe VBoxManage, necesario para el bootstrap por Guest Additions |
| D2 | Interfaz `Executor` con dos implementaciones: `GuestControl` y `SSH` | Pasar de un canal al otro es cambiar una pieza, no reescribir los módulos |
| D3 | El adaptador SSH llama a `ssh` y `scp` del sistema con `BatchMode=yes` | No agrega dependencias y falla en vez de esperar una contraseña: eso es "no interactivo" |
| D4 | Validación por lista blanca y argumentos separados; nunca concatenar texto del usuario en un comando | Evita inyección de comandos y cumple "validar antes de ejecutar" |
| D5 | Cambios de sshd en `/etc/ssh/sshd_config.d/` y `sshd -t` antes de recargar | Una configuración rota no debe dejar a la consola sin acceso |
| D6 | Root con `PermitRootLogin prohibit-password`; cada usuario con `Match User` y contraseña apagada | El enunciado pide quitar la contraseña para ese usuario, no para todos |
| D7 | La consola tiene su propia llave, distinta de las que se entregan | Si se pierde una llave descargada, la consola no pierde el acceso |
| D8 | Registro por operación con comando, salida, error, código y duración en `logs/operaciones.jsonl` | Es la "información técnica útil para el diagnóstico" |
| D11 | Destinos y contraseñas fuera del repositorio (`config.json`, `keys/guest-pass`) | Evita publicar credenciales |

## **2.2 Decisiones propias de la Parte B**

| \# | Decisión | Alternativa descartada | Por qué |
| :---- | :---- | :---- | :---- |
| DB1 | Ejecutar `ssh`/`scp` del sistema con `exec.CommandContext` | Librería `golang.org/x/crypto/ssh` | Cero dependencias nuevas y el mismo estilo del resto del proyecto |
| DB2 | Opciones fijas de `ssh` (`BatchMode`, `IdentitiesOnly`, `accept-new`, `known_hosts` propio, `ConnectTimeout`) | Opciones por defecto del cliente | No depende de la configuración del usuario y no se queda esperando |
| DB3 | El código 255 de `ssh` es error de canal; el resto es código del comando remoto | Tratar cualquier código distinto de 0 como error de canal | Permite decir "no hay canal" o "el comando falló" con mensajes distintos |
| DB4 | La llave de la consola se genera en el host y nunca sale de él | Usar la llave personal del integrante | Separa el acceso de la consola del acceso de las personas |
| DB5 | Archivos generados con funciones puras de Go e instalados con un script fijo (temporal + `mv`) | `echo >>` o `sed` con texto del usuario | Se prueba con `go test`, no duplica llaves y el original queda intacto si algo falla |
| DB6 | Drop-ins por cuenta (`10-` root, `20-<usuario>`) | Editar `sshd_config` completo | Aísla el cambio, se revierte borrando el archivo y no afecta a otros usuarios |
| DB7 | `sshd -t` antes de `systemctl reload ssh` | `systemctl restart ssh` | Una configuración rechazada no se aplica y no se cortan sesiones |
| DB8 | Verificar el efecto con `sshd -T -C user=…` | Confiar en el archivo escrito | Comprueba la configuración efectiva, que es la que importa con `Match` |
| DB9 | Generar las llaves en la VM y traer la privada con `CopyFrom` | Generarlas en el host | El enunciado pide que la consola las cree y las entregue |
| DB10 | La llave privada nunca pasa por `Run` | `cat` o leer la salida de `ssh-keygen` | Todo lo que pasa por `Run` queda en el registro |
| DB11 | Descarga de una sola vez con token de 128 bits y caducidad de 15 minutos | Descarga permanente | No queda una llave privada guardada en el host |
| DB12 | Contraseña por `Command.Stdin` (`chpasswd`) | Pasarla como argumento | En los argumentos aparecería en `ps` y en el registro |
| DB13 | `validate.Contrasena`: 8–128 caracteres, sin `:` ni saltos de línea ni control | Validar solo en el navegador | `chpasswd` usa `:` como separador y lee por líneas |
| DB14 | Listar con `getent passwd` y filtrar en Go (UID 1000–59999, protegidos) | Listar `/etc/passwd` o confiar en el cliente | La consola nunca puede quitarte el acceso a ti misma |
| DB15 | Todas las rutas bajo `/usuarios/` | Rutas sueltas | El registro de módulos lo exige y evita choques entre partes |
| DB16 | Las llaves de root se muestran en Preparación mediante el parcial `_llaves-root.html` | Duplicar la página en la Parte A | Cada parte edita solo sus archivos |
| DB17 | El bootstrap no instala `openssh-server` | Instalarlo desde el botón | El enunciado lo da como precondición; si el servicio no responde, la prueba de sesión no pasa y el canal no cambia |
| DB18 | Probar SSH al arrancar antes de Guest Additions | Guardar el canal solo en memoria | Un reinicio de la consola no debe devolver a Guest Additions una VM ya preparada |

## **2.3 Agregados posteriores al plan**

Estas piezas no estaban en las fases originales de la Parte B y se añadieron al integrar la vista y probar el flujo completo.

| \# | Agregado | Por qué |
| :---- | :---- | :---- |
| DB19 | **Editar usuario** (`GET/POST /usuarios/editar`) | El plan cubría crear y eliminar; el CRUD completo pedía modificar contraseña, sudo y llave sin borrar la cuenta |
| DB20 | **Vista CRUD**: tabla con acciones por fila, alta en panel desplegable y confirmación de borrado | La vista original tenía cuatro formularios apilados y el botón "Editar" escondido dentro de "Eliminar" |
| DB21 | **Envío sin recarga** con fragmentos (`X-Consola-Parcial`) y `fetch` | Evita recargar la página en cada botón; sin JavaScript se conserva el `POST → 303 → GET /resultado` |
| DB22 | **`readForm`**: aceptar `application/x-www-form-urlencoded` y `multipart/form-data` | `r.ParseForm` no lee cuerpos multipart; los campos llegaban vacíos y la validación rechazaba el formulario |
| DB23 | **Estado de la llave en el panel de edición** (`test -e` sobre el drop-in) | Al volver a editar parecía que la cuenta no tenía llave; ahora dice "Llave SSH instalada" o "Sin llave SSH" |
| DB24 | **Salida técnica visible** en el resultado en línea | El comando ejecutado quedaba dentro de un detalle cerrado y no se veía al terminar |

# **3\. Diseño de pruebas y resultados**

Las pruebas tienen dos niveles: automáticas, que verifican la lógica sin VirtualBox, y contra la VM real, grabadas en video con el estado anterior y posterior.

## **3.1 Pruebas automáticas**

| Capa | Herramienta | Qué demuestra |
| :---- | :---- | :---- |
| Fusión de llaves y drop-ins | Pruebas de tabla en `internal/llaves` | No duplica una llave existente, conserva comentarios y líneas en blanco, y genera el contenido exacto de cada drop-in |
| Descarga de una sola vez | Pruebas con carpetas temporales | El token solo acepta el formato generado, la segunda descarga falla, la caducidad se respeta y el archivo se borra |
| Cuentas | Pruebas de tabla en `internal/usuarios` | Parseo de `getent`, filtro DB14 y comandos exactos de crear, editar y eliminar, con y sin casillas |
| Adaptador SSH | Pruebas con el ejecutor reemplazado | Opciones fijas y en orden, comando remoto como un solo argumento entrecomillado, `stdin`, código 255 como error de canal y `scp` con `-P` y `--` |
| Handlers y formularios | `net/http/httptest` | Rutas, errores junto al campo, formulario inválido que no envía comandos, redirección al resultado y modo fragmento |
| Arranque | Pruebas de `cmd/consola` | SSH gana donde está listo, Guest Additions queda para las demás y `-simular` gana sobre ambos |

**Resultado:** `gofmt -l .` sin archivos pendientes, `go vet ./...` sin hallazgos y `go test -race -count=1 ./...` en verde. Esas mismas comprobaciones, más el build, son las que ejecuta la integración continua del repositorio (GitHub Actions, `ubuntu-latest`) en cada push. La cifra actualizada por paquete la imprime `go test ./... -v`.

## **3.2 Pruebas contra la VM real (video)**

Las pruebas se hicieron contra la VM de pruebas Debian 13.7, con la consola comunicándose inicialmente por **Guest Additions** y luego por **SSH**.

**Video:** [parte_b.mp4 (enlace)]

| \# | Prueba | Antes | Acción en la consola | Después (resultado) |
| :---- | :---- | :---- | :---- | :---- |
| P8 | Bootstrap Guest Additions → SSH | Barra superior: Guest Additions; `/root/.ssh/authorized_keys` sin la llave de la consola | Gestión de usuarios → Conectar por SSH | Barra superior: SSH; la llave de la consola aparece en `authorized_keys` (si el archivo ya existía, su respaldo quedó en `.consola-original`) |
| P9 | Arranque de la consola con el canal ya preparado | Consola detenida, VM encendida | Volver a abrir la consola | La barra sigue en SSH; "Probar conexión" responde por SSH |
| P10 | Llaves de root y descarga | `ssh root@<ip>` pide contraseña | Preparación → Generar llaves de root → Descargar llave privada | `ssh -i <llave> root@<ip>` entra; con `PubkeyAuthentication=no` es rechazado; `sshd -T -C user=root,host=consola,addr=127.0.0.1` muestra `prohibit-password`; la segunda descarga da 404 |
| P11 | Crear usuario con sudo y llave | `getent passwd <usuario>` vacío | Gestión de usuarios → Nuevo usuario (sudo y llave) → Crear | `getent passwd` lo muestra; `sudo -l -U <usuario>` lo incluye en sudo; entra con su llave y `sshd -T` muestra `passwordauthentication no` solo para él |
| P12 | Editar usuario | Cuenta sin llave, sin sudo | Editar → marcar sudo y regenerar llave | El panel muestra "Llave SSH instalada"; `groups` y `sshd -T` confirman el cambio |
| P13 | Eliminar usuario | `ls /home` muestra la cuenta | Eliminar → confirmar con la casilla de archivos | La cuenta y `/home/<usuario>` desaparecen; el drop-in ya no está |
| P14 | Eliminar sin archivos | Otra cuenta de prueba | Eliminar → confirmar sin la casilla | La cuenta desaparece y `/home/<usuario>` queda |
| P15 | Cuenta protegida | — | Intento de eliminar `root` o la cuenta de la consola | La consola rechaza la operación y no envía ningún comando |

Correspondencia con los videos del plan: **V5** (P8–P9), **V6** (P10), **V7** (P11–P12), **V8** (P13–P15).

## **3.3 Registro de operaciones**

Cada operación queda en `logs/operaciones.jsonl` con destino, canal, comando, salida, código de salida y duración. La tabla siguiente se completa con la sesión de grabación:

| Hora | Operación | Canal | Resultado | Mensaje de la consola |
| :---- | :---- | :---- | :---- | :---- |
| [hh:mm:ss] | Conectar por SSH | Guest Additions | Éxito | La llave de la consola quedó instalada en `/root/.ssh/authorized_keys`… |
| [hh:mm:ss] | Generar llaves de root | SSH | Éxito | El sshd de la VM ahora deja entrar a root solo con llaves… |
| [hh:mm:ss] | Crear la cuenta de usuario | SSH | Éxito | La cuenta quedó creada con su carpeta `/home`, sesión bash y sudo… |
| [hh:mm:ss] | Editar la cuenta de usuario | SSH | Éxito | Cuenta actualizada: se activó sudo… |
| [hh:mm:ss] | Eliminar el usuario | SSH | Éxito | El usuario y su carpeta `/home` fueron eliminados… |

## **3.4 Evidencias**

Las figuras deben mostrar la página de resultado de cada operación, porque en ella se ve el canal, la hora y la terminal con los comandos reales.

**Figura B1.** Módulo Gestión de usuarios: conexión por SSH, tabla de cuentas y panel de alta.
**Figura B2.** Resultado de "Conectar por SSH": lectura de `authorized_keys`, fusión y escritura protegida.
**Figura B3.** Página "Tu llave está lista" con el mensaje de buen uso y el botón de descarga.
**Figura B4.** Resultado de "Generar llaves de root": `ssh-keygen`, `sshd -t`, `systemctl reload ssh` y la verificación con `sshd -T`.
**Figura B5.** Crear usuario: `useradd`, `chpasswd`, `usermod`, drop-in del usuario y verificación.
**Figura B6.** Editar usuario: panel con el estado de la llave y el comando `usermod` o `gpasswd`.
**Figura B7.** Eliminar usuario: confirmación y comandos `userdel`, `rm` del drop-in y recarga.
**Figura B8.** Terminal de la VM: `sshd -T` mostrando `passwordauthentication no` para el usuario y `yes` para otro.

# **4\. Problemas encontrados y soluciones**

| Problema | Causa | Solución |
| :---- | :---- | :---- |
| `ssh` devolvía error aunque el comando remoto hubiera fallado con su propio código | El cliente usa 255 para el fallo de transporte y el resto son códigos del comando | Solo 255 se trata como error de canal; los demás se registran como resultado del comando |
| Un argumento con `;` o `$( )` podía reabrirse como sintaxis en la VM | `ssh` une el comando remoto en un solo texto que interpreta el shell remoto | `Command.String()` entrecomilla cada parte con `Quote` y el comando viaja como un único argumento |
| La primera conexión fallaba porque `authorized_keys` no existía | El archivo no existe en una VM recién preparada | El script de lectura devuelve un marcador cuando no hay archivo, en vez de fallar |
| Una escritura podía pisar un cambio hecho entre la lectura y la escritura | Otra consola u otra operación tocó el archivo | Se compara el SHA-256 de lo leído y se sale con código 12 sin escribir |
| Un drop-in inválido podía dejar el sshd sin arrancar | La configuración se aplicaba sin probarla | `sshd -t` antes de recargar; si falla, se borra el drop-in y no se recarga |
| Apagar solo `PasswordAuthentication` no cerraba todas las vías de contraseña | PAM puede usar teclado-interactivo | El drop-in apaga también `KbdInteractiveAuthentication` |
| La contraseña aparecía en `ps` y en el registro | Viajaba como argumento de `chpasswd` | Se envía por entrada estándar (`Command.Stdin`) |
| Tras restaurar un snapshot, `ssh` rechazaba la VM | La huella del host cambió | `known_hosts` propio con `accept-new`; se documenta borrar la línea de esa IP |
| Al reiniciar la consola, una VM ya preparada volvía a Guest Additions | El canal SSH vivía solo en memoria | `canal_ssh.go` prueba SSH al arrancar, antes de Guest Additions |
| Al enviar el formulario con JavaScript, el nombre y la contraseña llegaban vacíos | El JS enviaba `FormData` (multipart) y `r.ParseForm` no lee ese cuerpo | El JS envía `application/x-www-form-urlencoded` y el servidor usa `readForm`, que acepta ambos |
| El botón de guardar enviaba el POST a la ruta equivocada (405) | `button.formAction` devuelve la URL del documento cuando no hay atributo `formaction` | Se lee el atributo con `getAttribute("formaction")` y se usa la acción del formulario como respaldo |
| El panel de edición nunca aparecía | El handler no marcaba el formulario como "editar" y la plantilla usaba un nombre de campo con mayúsculas que el validador rechaza | Se marca `Formulario: "editar"` y el campo pasa a `contrasena` |
| Al volver a editar, parecía que la cuenta no tenía llave | El panel no leía el estado del drop-in | Se comprueba con `test -e` y se muestra "Llave SSH instalada" o "Sin llave SSH" |
| El comando usado no se veía al activar o desactivar sudo | La salida técnica quedaba dentro de un detalle cerrado | El resultado en línea muestra la salida técnica desplegada |
| `userdel` fallaba y el mensaje no era claro | El usuario tenía procesos activos | `Explain` detecta "is currently used by process" y explica qué hacer |
| Borrar una cuenta podía dejar configuración huérfana en sshd | El drop-in del usuario quedaba en `/etc/ssh/sshd_config.d/` | El plan de eliminación borra el drop-in antes de validar y recargar |
| Una descarga simultánea podía entregar la llave dos veces | Dos lectores podían abrir el mismo archivo | El archivo se renombra a `.borrando` antes de leerlo y se borra al terminar |

# **5\. Uso de herramientas de IA en la Parte B**

| Herramienta | Para qué se usó |
| :---- | :---- |
| Claude Code (Anthropic) | Revisión del plan de la Parte B y de la guía de módulos; apoyo en la escritura del adaptador SSH, la lógica de llaves y usuarios, las plantillas y las pruebas; revisión de seguridad (inyección, manejo de la llave privada) y de la interfaz; redacción de esta sección y del guion de los videos |
| Gemini AI (Google) | Análisis de salidas de `ssh`, `sshd -t` y `sshd -T` para diagnosticar rechazos de configuración y huellas cambiadas; apoyo en la redacción técnica |

Todas las decisiones y la validación contra la VM real fueron responsabilidad del autor de la parte.

# **Referencias de la Parte B**

1. OpenSSH. `ssh(1)`, `scp(1)`, `ssh-keygen(1)`, `sshd(8)`, `sshd_config(5)` y `authorized_keys(5)`. [https://manpages.debian.org](https://manpages.debian.org)
2. Debian. `sshd_config.d`: fragmentos de configuración de OpenSSH en Debian. [https://manpages.debian.org](https://manpages.debian.org)
3. Debian. `useradd(8)`, `usermod(8)`, `gpasswd(1)`, `userdel(8)`, `chpasswd(8)` y `getent(1)`. [https://manpages.debian.org](https://manpages.debian.org)
4. IETF. RFC 4253 — The Secure Shell (SSH) Transport Layer Protocol. [https://www.rfc-editor.org/rfc/rfc4253](https://www.rfc-editor.org/rfc/rfc4253)
5. OWASP. OS Command Injection Defense Cheat Sheet. [https://cheatsheetseries.owasp.org/cheatsheets/OS_Command_Injection_Defense_Cheat_Sheet.html](https://cheatsheetseries.owasp.org/cheatsheets/OS_Command_Injection_Defense_Cheat_Sheet.html)
6. The Go Authors. `os/exec`, `crypto/rand`, `net/http`. [https://pkg.go.dev/std](https://pkg.go.dev/std)
7. OpenSSH. Project History and Release Notes. [https://www.openssh.com/releasenotes.html](https://www.openssh.com/releasenotes.html)

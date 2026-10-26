# Prueba completa del parcial

`scripts/prueba-completa.sh` comprueba **todo el parcial en una sola pasada** contra una VM real: cada módulo del enunciado, usado desde los formularios de la consola (igual que el navegador), y cada efecto verificado dentro de la VM por SSH. Sirve para demostrar que la rama `main` está completa y para repetir la prueba antes de la sustentación.

## Qué comprueba

| Sección del enunciado | Qué hace el script | Cómo lo verifica en la VM |
|---|---|---|
| Comunicación | Probar conexión por Guest Additions, luego «Conectar por SSH» | La siguiente operación del registro va por el canal **SSH** |
| Validación | Envía `tree; rm -rf /`, `Mi_Servidor`, `openssh-server` (eliminar), un ejecutable con inyección y una acción `mask` | Respuesta 422 y **ninguna** operación nueva en el registro |
| 3.1.1 Aplicaciones | Instala y elimina `tree`; intenta un paquete inexistente | `which tree` antes y después; mensaje «no existe» |
| 3.1.2 Llaves de root | Genera las llaves y descarga la privada | La página recomienda `chmod 600`; la descarga es de una sola vez; con la llave se entra como root; `sshd -T` muestra `permitrootlogin without-password` |
| 3.1.3 `/etc/issue` | Mensaje con hostname, IP, fecha y hora; luego restaurar | El archivo tiene el mensaje y los códigos; luego vuelve al original |
| 3.1.4 Hostname | Cambia el nombre y lo devuelve | `hostname` y la línea `127.0.1.1` de `/etc/hosts` |
| 3.2.1 Crear usuario | `prueba1` con sudo y llave; `prueba2` sin ellas | Grupo `sudo`; entra con su llave; `sshd -T -C user=prueba1…` → `passwordauthentication no` |
| 3.2.2 Eliminar usuario | `prueba1` sin archivos, `prueba2` con archivos; intenta root | `/home/prueba1` queda, `/home/prueba2` no; se borra el drop-in de sshd; root es rechazado |
| 3.3 systemd | Despliega el **Bookzilla real** (binario Go), usa los 6 botones, lo vuelve a desplegar; intenta `stop` sobre `ssh` | Binario de root en `/opt/bookzilla`; corre como el usuario `bookzilla`; responde en `:8080`; `is-active` / `is-enabled` cambian; `status` de un servicio detenido no es un error |
| 3.3 Script de fecha | Despliega `fecha.sh` como tarea única y lo inicia dos veces | `/var/log/consola-fecha.log` tiene 2 líneas |
| 3.4 HAProxy | Instala `haproxy` (configuración predeterminada), agrega dos servidores, intenta uno repetido, quita uno, intenta quitar el último | El primer «Agregar» crea `frontend` y `backend`; el puerto 80 entrega Bookzilla; queda `haproxy.cfg.consola-anterior`; quitar el último servidor es rechazado (evita 503) |
| Registro | — | `logs/operaciones.jsonl` con permisos 600 |

## Antes de ejecutarlo

1. **Snapshot de la VM.** La prueba instala paquetes, crea y borra usuarios y despliega servicios (al final devuelve el hostname y `/etc/issue`):
   ```bash
   VBoxManage snapshot consola-pruebas take "antes-prueba-completa"
   ```
2. **Red.** La VM va en modo puente sobre la interfaz que esté conectada en el host (`ip -4 addr`). Si cambió (por ejemplo de Wi-Fi a cable), con la VM encendida:
   ```bash
   VBoxManage controlvm consola-pruebas nic1 bridged <interfaz>
   ```
3. **Aplicaciones a desplegar** en `aplicaciones/` (carpeta local, no se sube al repositorio):
   ```bash
   mkdir -p aplicaciones/bookzilla aplicaciones/fecha
   (cd ../Bookzilla && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o ../consola/aplicaciones/bookzilla/bookzilla .)
   cp deploy/fecha/fecha.sh aplicaciones/fecha/
   ```
4. **Consola en marcha** con la VM de pruebas como destino actual: `make build && ./consola`.

Para probar también el primer contacto por Guest Additions, la VM no debe tener todavía la llave de la consola en `/root/.ssh/authorized_keys` (por ejemplo, una VM restaurada del snapshot `guest-additions-ok`).

## Ejecutar

```bash
scripts/prueba-completa.sh                         # VM 192.168.40.57 por defecto
VM_IP=192.168.40.60 scripts/prueba-completa.sh     # otra VM
```

Cada línea dice `OK` o `FALLA` con el mensaje que mostró la consola; al final, el total. El script termina con código 0 solo si todo pasó.

## Resultado del 26 de octubre de 2026

Rama `fix/despliegue-servicios` (después unida a `main`), VM `consola-pruebas` (Debian 13, 192.168.40.57), empezando por Guest Additions: **81 OK, 0 FALLA**.

## Prueba desde el navegador

`scripts/interfaz/prueba-interfaz.js` hace lo mismo **desde la interfaz**: un Chromium real elige el destino, llena cada formulario, pulsa los botones (incluidos «Nuevo usuario», «Vista previa», la confirmación de «Eliminar» y las descargas de llaves) y verifica el efecto en la VM por SSH. Así se comprueba que todo lo del enunciado se puede hacer desde el front, no solo por HTTP. Prueba HAProxy en la VM `balanceador` real y guarda una captura de cada resultado en `evidencias/interfaz/`.

```bash
(cd scripts/interfaz && npm install)               # una vez: playwright-core 1.49.1
node scripts/interfaz/prueba-interfaz.js
```

Usa el Chromium de Playwright (`~/.cache/ms-playwright/chromium-1148`) o el que indique `CHROMIUM`. Resultado del 26 de octubre de 2026, empezando por Guest Additions: **54 OK, 0 FALLA**.

## Límites de esta prueba

- El script prueba HAProxy en `consola-pruebas`. La VM `balanceador` (192.168.40.53) se probó aparte el 26 de octubre: agregar y quitar `192.168.40.57:8080` en `http_back` funcionó, y el puerto 80 entregó Bookzilla. Para que la consola entre a esa VM, su `/root/.ssh/authorized_keys` debe tener `keys/consola_ed25519.pub` (sus Guest Additions 6.0 no responden con VirtualBox 7.2). Sus servidores del laboratorio (`servidor1`–`servidor3`, `.50`–`.52:8000`) deben estar encendidos: sus líneas no tienen `check` y HAProxy les envía peticiones aunque estén apagados.
- La consola **no crea VMs** desde el disco multiconexión `base-webapp.vdi`: «Agregar servidor» edita `haproxy.cfg` (duda abierta en el §7 del plan general).
- No se reinicia la VM, así que «enable sobrevive a un reinicio» no se comprueba aquí.

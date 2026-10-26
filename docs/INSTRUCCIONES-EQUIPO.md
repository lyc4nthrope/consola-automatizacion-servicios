# Instrucciones para el equipo: cierre del Parcial 2

**Para:** Daniel (Parte B) y Wiliam (Parte C) · **Coordina e integra:** Cristhian (Parte A)

> Lee primero la sección 1 (5 minutos). Después, solo tu sección (3 o 4) y la 6 (pruebas finales).

---

## 1. Dónde estamos

- El trabajo de los tres está unido en la rama **`integracion`**. Compila y las 12 carpetas de pruebas pasan (`make check`).
- Probado en **modo simulado**: las 5 páginas cargan, las operaciones válidas corren y las inválidas se rechazan sin ejecutar nada.
- **No probado todavía contra VMs reales:** nada de la Parte B ni de la C. Es lo más importante que falta.

### Qué se cambió en su código al integrar (revísenlo)

| Quién | Qué pasaba | Qué se hizo |
|---|---|---|
| Daniel | El commit "no se xd" usaba `runtime` sin importarlo: **no compilaba nada** | Import agregado; tu idea para Windows se mantiene |
| Daniel | Un script de `usuarios.go` se armaba con `Sprintf` | El usuario va como argumento `$1` |
| Wiliam | El balanceador **nunca leía** `haproxy.cfg`: "agregar" borraba `global` y `defaults` | Lee el archivo real, valida con `haproxy -c` y solo entonces lo reemplaza y recarga (tu DC13) |
| Wiliam | `sh -c "echo '<datos del formulario>' > archivo"` permitía **inyección de comandos** | El contenido viaja como argumento de un script fijo; todos los campos se validan |
| Wiliam | Las plantillas enviaban `servicio` / `backend` y el código leía `nombre`: **ningún formulario funcionaba** | Alineado. Los formularios repetidos usan `backend-quitar`, `ip-quitar` y `servicio-accion` |
| Wiliam | `reload`, acciones sin lista cerrada, `useradd` fallaba al redesplegar, sin pruebas | Lista cerrada de 6 acciones (DC9), usuario solo si no existe, `systemd-analyze verify` y pruebas nuevas |

El detalle completo está en [PLAN.md](PLAN.md), sección 8.2.

---

## 2. Primeros pasos (los dos, una sola vez)

```bash
cd consola-automatizacion-servicios
git fetch
git switch integracion
git pull
make check                    # debe terminar sin errores
git switch -c <tu-nombre>-final   # daniel-final o wiliam-final
```

> **No sigan trabajando en sus ramas viejas** (`daniel`, `wiliam`): no tienen las correcciones y al unirlas volverían los errores.

### Reglas mientras trabajan

1. **Antes de cada commit:** `make check` en verde. Si falla, no se sube.
2. **Nunca** armar texto de shell con datos del usuario (`Sprintf` dentro de `sh -c`, `echo '...'`). Los datos van como argumentos (`Args`) o como `$1`, `$2` de un script fijo. Ejemplo correcto: `applyHaproxyScript` en `internal/web/balanceador.go`.
3. Cada dato del formulario pasa por `validate` (o por un validador del módulo) **antes** de armar el `Plan`. Error → `http.StatusUnprocessableEntity` (422).
4. Toda función que genera o edita un archivo es **pura** y tiene prueba de tabla.
5. Commits con formato convencional y mensaje que diga qué cambió: `feat(servicios): copy the app from the host`. No `f` ni `no se xd`.
6. Al terminar: `git push -u origin <tu-nombre>-final` y abrir un **PR hacia `integracion`**, no hacia `dev` ni `main`.

---

## 3. Daniel (Parte B)

El código de B1–B5 está completo y con pruebas. Falta comprobarlo en una VM real, grabar los videos y armar el documento.

| # | Tarea | Pasos | Cómo saber que quedó bien |
|---|---|---|---|
| D1 | Probar el canal y el bootstrap (V5) | Restaurar `consola-pruebas` al snapshot `guest-additions-ok` → `./consola` → en el Panel el destino aparece con **Guest Additions** → página *Gestión de usuarios* → botón **Conectar por SSH** | El canal cambia a **SSH**. Al reiniciar `./consola`, el destino arranca directo por SSH. En la VM: `cat /root/.ssh/authorized_keys` tiene la llave de la consola |
| D2 | Llaves de root (V6) | Botón **Generar llaves de root** → descargar la privada | `ssh -i <llave> root@<ip>` entra. `ssh -o PubkeyAuthentication=no root@<ip>` es **rechazado**. Una segunda descarga ya no es posible |
| D3 | Crear usuario con sudo y llave (V7) | Crear `prueba1` con sudo y llave | `id prueba1` muestra `sudo`. Entra con su llave y no con contraseña. `sshd -T -C user=prueba1,host=x,addr=127.0.0.1 \| grep passwordauth` → `passwordauthentication no` |
| D4 | Eliminar usuario (V8) | Eliminar `prueba1` sin borrar archivos; crear `prueba2` y eliminarlo borrando archivos | `getent passwd prueba1` vacío y `/home/prueba1` sigue; `/home/prueba2` ya no existe; desaparece `/etc/ssh/sshd_config.d/20-consola-prueba2.conf` |
| D5 | Guion de videos V5–V8 | Copiar el formato de `docs/parte-a/guion-videos.md` (antes → acción → después) | `docs/parte-b/guion-videos.md` |
| D6 | **Documento final (B7). Es lo más urgente** | Juntar A, B y C con el formato del enunciado: solución y decisiones (PLAN §2 + tablas DA, DB, DC), pruebas y resultados (tabla de videos), problemas y soluciones (PLAN §8.2 sirve casi tal cual), referencias, uso de IA | Cristhian y Wiliam te mandan su sección; tú entregas un solo documento |

**Si algo falla en la VM real:** corrígelo en `daniel-final` con una prueba que habría detectado el fallo, `make check` y PR.

---

## 4. Wiliam (Parte C)

Servicios y balanceador funcionan y tienen pruebas. Falta copiar la app desde el host, el script de fecha y la prueba en VMs reales.

### W1. Copiar la aplicación desde el host (DC4, DC5): la tarea más grande

Hoy **"Desplegar" instala la unidad de un programa que ya está en la VM**. El enunciado pide desplegar la aplicación, así que falta copiarla.

1. **Función pura** en `internal/servicios/paquete.go`:
   ```go
   // Empaqueta la carpeta del host en un .tar.gz (archive/tar + compress/gzip).
   func Empaquetar(dir string, w io.Writer) error
   // Lista las carpetas de aplicaciones/ que se pueden desplegar.
   func Aplicaciones(raiz string) ([]string, error)
   ```
   Con pruebas en `paquete_test.go`. Usa `t.TempDir()`, crea dos archivos, empaqueta, desempaqueta y compara.
2. **En `planDesplegar`** (`internal/web/servicios.go`), **antes** del `test -x`:
   1. `CopyTo` del `.tar.gz` a `/tmp/consola-<app>.tar.gz`. `CopyTo` ya existe en el `Executor`.
   2. `install -d -m 755 /opt/<app>`
   3. `tar -xzf /tmp/consola-<app>.tar.gz -C /opt/<app> --no-same-owner`
   4. `chown -R root:root /opt/<app>`
   5. `rm -f /tmp/consola-<app>.tar.gz`
3. **Formulario:** "Ruta del ejecutable" pasa a ser:
   - una **lista** (`<select>`) con las carpetas de `aplicaciones/`;
   - un campo con el nombre del ejecutable.

   La ruta queda `/opt/<app>/<ejecutable>`. Valida el nombre con `servicios.ValidateExec("/opt/<app>/<ejecutable>")`.
4. Agrega `aplicaciones/` a `.gitignore`.

**Verificación en la VM:**
- `ls -l /opt/bookzilla` → propiedad de `root`.
- `systemctl status bookzilla` → `active (running)`, con `ps -o user= -C bookzilla` → `bookzilla`.
- `curl localhost:8080` responde.

### W2. Script de fecha como oneshot (C4, DC11)
- Casilla "Tarea única (oneshot)" en el formulario → `Config.Oneshot = true`. Ya existe y tiene prueba.
- Desplegar `deploy/fecha/fecha.sh` por el mismo camino de W1.
- **Verificación:** `systemctl start fecha` dos veces → `tail /var/log/consola-fecha.log` muestra dos líneas.

### W3. Los 6 botones en VM real (V10)
Con Bookzilla desplegado, en este orden: `status` (en ejecución) → `stop` → `status` (**detenido, y no debe salir como error**) → `start` → `restart` → `disable` → `enable`.
- **Verificación:** `systemctl is-active bookzilla` y `systemctl is-enabled bookzilla` cambian en cada paso.
- `stop` sobre `ssh` debe ser **rechazado** junto al campo.

### W4. Balanceador real (V11)
- Destino **balanceador**. Su `haproxy.cfg` debe tener un `backend` (el del lab de balanceo, por ejemplo `web_servers`).
- Agregar `servidor3` (IP y puerto de Bookzilla) → probar → quitarlo.
- **Verificación:**
  - `grep server /etc/haproxy/haproxy.cfg`.
  - Repetir `curl http://<ip-balanceador>` muestra el reparto entre servidores.
  - Existe `haproxy.cfg.consola-anterior`.
  - Quitar el **último** servidor es rechazado.

### W5. Documento y videos
Diagrama del escenario (C6), guion y videos V9–V11, y tu sección para Daniel (C7).

### W6. Opcional, si sobra tiempo
En la página del balanceador, mostrar los back-ends que existen (`balanceador.Backends(cfg)`) en un `<select>`.

---

## 5. Integración (Cristhian)

### 5.1 Cada vez que llega un PR hacia `integracion`

```bash
git fetch
git switch integracion && git pull
git merge --no-ff origin/<rama-del-PR>      # o el botón Merge del PR
make check                                   # si falla: no se sube, se avisa al autor
```

Revisar en el PR, antes del merge:

| Revisar | Por qué |
|---|---|
| ¿Hay `sh -c` con `Sprintf` o con datos del formulario? | Inyección de comandos (regla 2) |
| ¿Cada campo nuevo se valida antes del `Plan`? | Requisito del enunciado |
| ¿Hay pruebas para la lógica nueva? | Sin pruebas no sabemos si sigue funcionando |
| ¿El `name=` del HTML coincide con el `r.FormValue(...)`? | Fue el error que dejó a C sin funcionar |

### 5.2 Orden de los merge

1. PR de **Daniel** (B) y PR de **Wiliam** (C) → `integracion`, uno a la vez, con `make check` después de cada uno.
2. Prueba de punta a punta (sección 6) sobre `integracion`.
3. Si todo pasa: `integracion` → `dev` → `main`.
   ```bash
   git switch dev && git pull && git merge --no-ff integracion && make check && git push
   git switch main && git pull && git merge --no-ff dev && make check && git push
   ```

---

## 6. Prueba de punta a punta antes de entregar

Se hace **una vez**, con todo unido en `integracion` y desde VMs recién restauradas. Es también el orden para grabar los videos.

### 6.1 Preparación

| Paso | Comando o acción | Resultado esperado |
|---|---|---|
| 1 | `git switch integracion && git pull && make check && make build` | Sin errores |
| 2 | `VBoxManage snapshot consola-pruebas restore guest-additions-ok` (con la VM apagada) | Estado limpio |
| 3 | `VBoxManage snapshot consola-pruebas take antes-prueba-final` | Para repetir si algo falla |
| 4 | Encender `consola-pruebas`, `balanceador` y `servidor1..3` | Todas arrancan |
| 5 | En la VM: `ip -br a`. Copiar la IP real a `config.json` (`targets[].ip`) | La IP del archivo es la de la VM |
| 6 | `ls -l keys/` | Archivos `guest-pass-*` con permisos `-rw-------` |
| 7 | `./consola` | La terminal dice qué canal usa cada destino |
| 8 | Abrir `http://127.0.0.1:8090` → **Probar conexión** | La VM responde con su nombre |

### 6.2 Recorrido (marcar cada casilla)

| # | Módulo | Prueba | Resultado esperado | ✓ |
|---|---|---|---|---|
| 1 | B | *Gestión de usuarios* → **Conectar por SSH** | El canal del destino pasa a SSH | ⬜ |
| 2 | A | Instalar `tree` y luego eliminarlo | `which tree` → `/usr/bin/tree` y luego nada | ⬜ |
| 3 | A | `/etc/issue` con nombre, IP, fecha y hora | La pantalla de login lo muestra | ⬜ |
| 4 | A | Hostname `servidor-nube` | `hostname` y la línea `127.0.1.1` de `/etc/hosts` cambian | ⬜ |
| 5 | B | Llaves de root + descarga | Entra con la llave; con contraseña es rechazado | ⬜ |
| 6 | B | Crear usuario con sudo y llave | `id <u>` con sudo; entra solo con su llave | ⬜ |
| 7 | B | Eliminar usuario con y sin archivos | `/home/<u>` según la casilla | ⬜ |
| 8 | C | Desplegar Bookzilla | `systemctl status bookzilla` activo; `curl` responde | ⬜ |
| 9 | C | 6 botones + script de fecha | Estados cambian; `status` detenido no es error; log de fecha crece | ⬜ |
| 10 | C | Agregar y quitar servidor en HAProxy | El reparto cambia; quitar el último es rechazado | ⬜ |
| 11 | A | Validación: `tree; rm -rf /`, hostname `Mi_Servidor`, `stop` sobre `ssh` | Rechazados junto al campo, sin ejecutar nada | ⬜ |
| 12 | Todos | Panel → *Últimas operaciones* y `tail logs/operaciones.jsonl` | Cada operación anterior aparece, incluidas las fallidas | ⬜ |
| 13 | Todos | Cerrar y volver a abrir `./consola` | La terminal dice que el destino usa SSH con la llave de la consola, y **Probar conexión** responde | ⬜ |

Si una fila falla: anotar qué pasó (captura y la línea de `logs/operaciones.jsonl`), restaurar `antes-prueba-final`, corregir en la rama del responsable y volver a probar solo esa fila.

### 6.3 Problemas comunes

| Síntoma | Causa probable | Qué hacer |
|---|---|---|
| "Probar conexión" no responde por Guest Additions | La VM no terminó de arrancar | Esperar 30 s y repetir |
| La consola no llega por SSH a una VM en puente sobre **Wi-Fi** | Algunas tarjetas Wi-Fi no dejan ver VMs en puente desde el host | Usar cable (`enp44s0`) en el adaptador puente, o un adaptador *solo anfitrión* para la consola |
| La IP de `config.json` no responde | La VM tomó otra IP por DHCP | `ip -br a` en la VM y actualizar `config.json` |
| `make check` falla en `gofmt` | Archivos sin formato | `gofmt -w .` y volver a correr |
| Balanceador: "El back-end «x» no existe" | El nombre no coincide con `haproxy.cfg` | El mensaje lista los back-ends que hay; usar uno de ellos |
| Servicios: "No existe un programa ejecutable en …" | La app no está en la VM (W1 pendiente) o le falta `chmod 755` | Terminar W1, o copiarla a mano solo para probar |

---

## 7. Lista final de entrega

| Entregable | Responsable | ✓ |
|---|---|---|
| `integracion` → `dev` → `main` con `make check` en verde | Cristhian | ⬜ |
| Prueba de punta a punta (6.2) completa | Los tres | ⬜ |
| Videos V1–V4 | Cristhian | ⬜ |
| Videos V5–V8 | Daniel | ⬜ |
| Videos V9–V11 + diagrama | Wiliam | ⬜ |
| Documento consolidado | Daniel (con las secciones de los otros dos) | ⬜ |

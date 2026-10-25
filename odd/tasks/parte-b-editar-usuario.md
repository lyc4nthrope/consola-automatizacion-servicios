# Feature: Editar usuario existente (CRUD completo)

**Objetivo:** Permitir editar un usuario ya existente en la VM: cambiar contraseña, activar/desactivar sudo, generar/regenerar llave SSH. Completa el CRUD (Create, Read, Delete → Update).

**Rama:** `parte-b-editar-usuario` (basada en `daniel`)

**Estrategia de entrega:** `ask-on-risk` (cambio pequeño, un commit por tarea, tests y docs junto al código)

**Test runner:** `go test ./internal/usuarios ./internal/web -run "TestPlanEditar|TestUsuarioEditar"`

---

## Specs

### S1. Ruta GET para mostrar formulario de edición
`GET /usuarios/editar?usuario=<nombre>` muestra un formulario precargado con los datos actuales del usuario (nombre solo lectura, checkbox sudo actual, campo contraseña vacío por seguridad, checkbox llave SSH). Solo accesible si la consola tiene canal SSH con la VM.

### S2. Ruta POST para procesar edición
`POST /usuarios/editar` recibe: `usuario` (nombre, solo lectura), `nuevaContrasena` (opcional, si vacío no cambia), `sudo` (bool, activar/desactivar), `llave` (bool, generar/regenerar llave SSH). Valida localmente (validate.Usuario para el nombre, validate.Contrasena si se proporciona nueva contraseña). Ejecuta PlanEditar. En éxito: redirige a `/resultado/<id>` (o a `/usuarios/llaves/listo/<token>?id=<opId>` si se generó llave).

### S3. PlanEditar en internal/usuarios/usuarios.go
Función pura `PlanEditar(target, usuario, nuevaContrasena string, sudo bool) operacion.Plan` que construye los comandos necesarios:
- Si `nuevaContrasena != ""`: `chpasswd` con stdin `usuario:nuevaContrasena`
- Si `sudo` cambió: `usermod -aG sudo -- usuario` o `gpasswd -d usuario sudo` (según toggle)
- Devuelve plan con Action "Editar la cuenta de usuario", AlsoOK vacío, Explain que mapea errores de chpasswd/usermod/gpasswd.

### S4. Handler usuarioEditar en internal/web/usuarios.go
- GET: verifica canal SSH, lee usuario de query, renderiza template con datos precargados (Formulario: "editar").
- POST: valida, ejecuta PlanEditar, luego si `llave==true` encadena generación/instalación de llave (igual que usuarioCrear). Maneja limpieza y redirección igual que usuarioCrear.

### S5. Template: botón "Editar" en la lista de usuarios
En `web/templates/usuarios.html`, en el `<select>` de eliminar y/o en la tabla (si se cambia a tabla), añadir un botón/enlace "Editar" que lleve a `/usuarios/editar?usuario=<nombre>`. Mantener el flujo de confirmación de eliminación intacto.

### S6. Validaciones y mensajes de error
- Nombre: validate.Usuario (ya existe).
- Contraseña: validate.Contrasena solo si `nuevaContrasena != ""`.
- Si usuario no existe (getent exit 2): error "El usuario no existe en la VM".
- Si usuario es protegido (root, nadie, ssh_user, guest_user): error "No se puede editar esta cuenta: la consola la necesita".
- chpasswd falla → "Error al cambiar la contraseña".
- usermod/gpasswd falla → "Error al cambiar los privilegios de sudo".

### S7. Tests
- `TestPlanEditar`: verifica comandos generados (chpasswd, usermod, gpasswd).
- `TestUsuarioEditarGET`: formulario se renderiza con datos correctos.
- `TestUsuarioEditarPOST`: cambio de contraseña, toggle sudo, generación de llave.
- `TestUsuarioEditarProtegeCuentasProtegidas`: rechaza editar root/ssh_user.
- `TestUsuarioEditarUsuarioNoExistente`: rechaza usuario inexistente.

---

## Tasks

| ID  | Linked Specs | Route | Description |
|-----|--------------|-------|-------------|
| T1  | S3           | inline | Añadir `PlanEditar` en `internal/usuarios/usuarios.go` con tests `TestPlanEditar`. |
| T2  | S1, S2, S4   | inline | Añadir rutas `GET /usuarios/editar` y `POST /usuarios/editar` en `init()` de `internal/web/usuarios.go`. |
| T3  | S4           | inline | Implementar handlers `usuarioEditarGET` y `usuarioEditarPOST` en `internal/web/usuarios.go`. |
| T4  | S5           | inline | Modificar `web/templates/usuarios.html`: añadir botón "Editar" junto a cada usuario en el dropdown/tabla. |
| T5  | S6           | inline | Añadir validaciones y mensajes de error en handler y template. |
| T6  | S7           | inline | Añadir tests en `internal/web/usuarios_test.go` y `internal/usuarios/usuarios_test.go`. |
| T7  | S1-S7        | inline | Ejecutar `go test ./internal/usuarios ./internal/web -run "TestPlanEditar|TestUsuarioEditar"` y `go vet ./...`; `go build ./...`. |

---

## Log

**L1** 2026-10-24 — Usuario solicita completar CRUD con edición de usuario (cambio contraseña, toggle sudo, llave SSH). Decidido crear fase separada `parte-b-editar-usuario` con su propio documento ODD, commit y push aparte.
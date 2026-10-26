# Feature: CRUD de usuarios sin recarga

**Objetivo:** Reestructurar la vista de Gestión de usuarios como un CRUD (tabla + acciones por fila,
alta y edición en panel, borrado con confirmación) y enviar sus formularios por `fetch`, de modo que
solo se repinte el contenido de la página con el resultado de la operación en línea.

**Rama:** `crud-usuarios` · **Entrega:** `ask-on-risk` · **Runner:** `go test ./internal/web ./internal/usuarios`

---

## Specs

### S1. Tabla de cuentas (Read)
`GET /usuarios` muestra una tabla con una fila por cuenta eliminable (`getent passwd` filtrado por
DB14) y, por fila, dos acciones: «Editar» (`/usuarios/editar?usuario=<u>`) y «Eliminar»
(`/usuarios?confirmar=<u>`). Sin cuentas se muestra el aviso existente en lugar de la tabla.
Las cuentas protegidas (root, nobody, ssh_user, guest_user) nunca aparecen.

### S2. Alta (Create)
La tabla va precedida por un control «Nuevo usuario» que despliega el formulario
(`POST /usuarios/crear`) con los campos actuales: usuario, contraseña, sudo, llave SSH.
Sin JavaScript el control funciona como `<details>` nativo; con JavaScript es el mismo `<details>`.
Si el formulario se rechaza, vuelve abierto con los errores junto al campo y sin contraseña.

### S3. Edición (Update)
«Editar» abre un panel con el formulario (`POST /usuarios/editar`) precargado: nombre de solo
lectura, sudo actual, contraseña vacía, llave. El estado se marca con `Formulario: "editar"`
(antes faltaba y el formulario nunca se mostraba).

### S4. Borrado (Delete)
«Eliminar» abre una pantalla de confirmación (`GET /usuarios?confirmar=<u>`) que nombra la cuenta,
avisa de que es irreversible, ofrece «Eliminar todos los archivos» y exige el segundo clic.
El POST (`/usuarios/eliminar`) sigue rechazando cuentas protegidas sin enviar ningún comando.

### S5. Modo fragmento
Un handler devuelve solo `{{template "content"}}` (sin layout) cuando la petición trae
`X-Consola-Parcial: 1`. El header no cambia ninguna validación ni el filtro CSRF: solo elige
qué plantilla se ejecuta. `render` usa `layout` por defecto.

### S6. Resultado en línea
Cuando la petición es parcial, un POST que antes redirigía a `/resultado/<id>` responde 200 con el
fragmento de la página: bloque de resultado (éxito/fallo + terminal plegable) y tabla actualizada.
Sin el header se conserva el `POST → 303 → GET /resultado/<id>` de siempre.
Los flujos que terminan en la descarga de una llave (`/usuarios/llaves/listo/<token>`) siguen
redirigiendo; el JS detecta `response.redirected` y navega a esa URL.

### S7. JS progresivo
`usuarios.js` intercepta los formularios y enlaces marcados `data-crud`, envía `fetch` al mismo
origen con el header de S5 y sustituye el contenido. Si `fetch` falla, navega normalmente.
Reenlaza tras cada cambio los formularios de ejecución y los botones de copiar. Anuncia el
resultado con `aria-live` y enfoca el primer campo inválido.

### S8. Indicador de ejecución reutilizable
`consola.js` expone `window.consola.enlazar(root)`, `ejecutando(nombre)` y `terminado()` para que
el contenido sustituido vuelva a mostrar «Ejecutando…» y el cronómetro.

### S9. Cuerpo del formulario: urlencoded y multipart
El formulario se envía como `application/x-www-form-urlencoded` (la misma codificación que un
envío nativo, que `ParseForm` lee). El servidor además acepta `multipart/form-data` con `readForm`,
porque `ParseForm` no lee ese cuerpo y los campos llegarían vacíos. `ErrNotMultipart` no es un error.

### S10. Estado actual de la llave en el panel de edición
`GET /usuarios/editar?usuario=<u>` lee si existe el drop-in por usuario
(`/etc/ssh/sshd_config.d/20-consola-<u>.conf`) y el panel muestra «Llave SSH instalada» o
«Sin llave SSH» con una explicación. La casilla pasa a llamarse «Regenerar llave SSH (la actual
dejará de funcionar)» cuando ya hay llave.

### S11. El resultado en línea enseña el comando
La salida técnica del resultado en línea se muestra desplegada, como la página `/resultado`, para
que el comando ejecutado (`usermod`, `gpasswd`, …) se vea al terminar sin un clic extra.

---

## Tasks

| ID  | Specs     | Route | Description | Estado |
|-----|-----------|-------|-------------|--------|
| T1  | S5, S6    | inline | `view.Parcial`, `esParcial`, `render` parcial y partial `_resultado.html` | hecho |
| T2  | S6        | inline | `usuariosDatos.Resultado`, `terminarOperacion`, handlers parciales, `porFallo`, fix de S3 | hecho |
| T3  | S1-S4     | inline | Reescribir `web/templates/usuarios.html` como CRUD | hecho |
| T4  | S1-S4     | inline | CSS de tabla, `<details>` y resultado en línea | hecho |
| T5  | S7, S8    | inline | `consola.js` reutilizable + `usuarios.js` con fetch | hecho |
| T6  | S1-S8     | inline | Ajustar tests existentes y añadir los del modo fragmento | hecho |
| T7  | —         | inline | Verificación visual y de rutas | hecho |

---

## Log

### L1 — Petición original (verbatim)

> Puedes hacer un CRUD bien estructurado?, me refiero a la vista porque creo que en el back ya esta. Ademas, hay una forma de evitar que cada vez que se presione un boton se recarge la pagina?

### L2 — Decisión de alcance

El usuario eligió «CRUD + sin recarga»: tabla + acciones por fila y envíos por `fetch` que solo
repintan el panel de usuarios con el resultado en línea. El resto de vistas no se toca.

### L3 — Evidencia

- `GET /usuarios` con `X-Consola-Parcial: 1` → 200, 4 KB, sin `<html>` ni `.topbar`.
- `POST /usuarios/crear` parcial con nombre inválido → 422 con el fragmento y el error ligado al campo.
- `POST /usuarios/eliminar` parcial (fake SSH) → 200 con «Resultado de la operación» + tabla releída
  (`TestEliminarParcialDevuelveResultadoYLista`).
- Navegador (Edge headless vía CDP): formulario inválido → sin recarga (`window` conserva su estado),
  `<details>` sigue abierto y el foco va al título de cuentas; «Cancelar» del panel de edición hace
  solo un `GET /usuarios` y `history.back()` devuelve al panel sin recargar.
- Bug encontrado y corregido: `button.formAction` devuelve la URL del documento cuando el botón no
  tiene `formaction`, así que el POST iba a `/usuarios` (405). Se usa `getAttribute("formaction")`.
- Bug latente encontrado: el panel de edición nunca se mostraba (`Formulario` no se marcaba) y su
  `aria` usaba `nuevaContrasena`, nombre que el validador de campos rechaza; el campo pasa a
  llamarse `contrasena` en el formulario y en el handler.
- `go build ./...` OK; `go test ./internal/usuarios` OK; `internal/web` con los tests de Windows
  no ejecutables por `syscall.Umask`/`/bin/sh` (preexistente) se corrió con un overlay local:
  pasan todos los tests de usuarios y fragmento. Fallan solo `TestIssueFormKeepsALeadingBlankLine`
  y `TestPaletteListsEveryPageActionAndTarget`, por CRLF del checkout (`core.autocrlf=true`).

### L4 — Fallo reportado: editar con llave SSH

> «Cuando intento editar un usuario para agregarle una llave ssh me sale el error: El nombre de
> usuario empieza con una minúscula o _…»

Causa: el JS enviaba `FormData`, que viaja como `multipart/form-data`, y `r.ParseForm()` **no lee
ese cuerpo**. `usuario` y `contrasena` llegaban vacíos y la validación los rechazaba. Reproducido
con `TestCrearAceptaFormularioMultipart` antes de corregir (422 con los dos errores de campo).

Arreglo doble: el JS envía `application/x-www-form-urlencoded` (`URLSearchParams(FormData)`, con
caída a multipart si el navegador no lo soporta) y los handlers de usuarios leen el cuerpo con
`readForm`, que acepta ambas codificaciones. Tests: `TestCrearAceptaFormularioMultipart` y
`TestEditarAceptaFormularioMultipart` (este último es el flujo reportado).

### L5 — Fallo reportado: la edición no refleja la llave y el resultado no enseña el comando

> «cuando edito un usuario para que use ssh, y vuelvo a editar parece que no usara el ssh… Cuando
> desactivo sudo o cuando activo no aparece el comando usado como en las demas operaciones.»

Dos causas distintas:

1. El panel de edición no leía ni mostraba el estado de la llave: solo nombre, contraseña, sudo y
   una casilla siempre vacía. Ahora `usuarioEditarGET`/`POST` consultan el drop-in por usuario
   (`test -e`, una lectura que no se registra) y el panel muestra el badge y la explicación (S10).
2. El comando sí estaba en el HTML, pero dentro de un `<details>` cerrado. Ahora la salida técnica
   se muestra desplegada (S11), igual que en `/resultado`.

Tests: `TestEditarMuestraElEstadoDeLaLlave` (instalada / sin llave) y
`TestEditarConSudoMuestraElComando` (activar y desactivar), escritos antes del arreglo.


# Base para los módulos (Partes B y C)

Lo que la Parte A deja listo para que cada módulo se construya encima, sin repetir lógica y **sin editar
archivos de otra parte**. Este documento es la referencia de la API; los planes de cada parte dicen qué construir.

## Correr la consola

```bash
cp config.example.json config.json   # ajustar nombres de VM e IPs; config.json no se sube al repo
go run ./cmd/consola -simular        # sin VM: cada destino responde con un canal simulado
go run ./cmd/consola                 # con VM real: ver "Conectar una VM real" más abajo
go test -race ./...                  # siempre en verde antes de un commit
```

Abrir `http://127.0.0.1:8090`. La consola solo responde en esa dirección (o `localhost:8090`).

| Opción | Para qué |
|---|---|
| `-config` | Archivo de configuración (por defecto `config.json`) |
| `-logs` | Carpeta del registro de operaciones (por defecto `logs/`, solo legible por tu usuario) |
| `-simular` | Canal simulado para todos los destinos: desarrollar y hacer demos sin VM |

## Conectar una VM real (canal Guest Additions)

Cada integrante tiene su propio VirtualBox: la VM de pruebas vive en su equipo (procedimiento en el plan de la Parte C, C1).

1. En la VM: contraseña de root (`passwd root`) y Guest Additions instaladas.
2. En `config.json`, el destino con el nombre exacto de la VM en VirtualBox (`vm`) y su IP. Opcional: `guest_user` (por defecto `root`).
3. La contraseña va en un archivo privado, nunca en la terminal como argumento ni en el chat de la IA. En **tu propia terminal**:
   ```bash
   install -d -m 700 keys
   read -rs -p "Contraseña de root de la VM: " p && (umask 077 && printf '%s' "$p" > keys/guest-pass) && unset p
   ```
   Un destino con otra contraseña usa `keys/guest-pass-<destino>` (por ejemplo `keys/guest-pass-balanceador`).
4. `go run ./cmd/consola`: el destino aparece con el canal **Guest Additions**; "Probar conexión" ejecuta `hostname` en la VM.

La consola se niega a usar un archivo de contraseña que otros usuarios puedan leer (pide `chmod 600`). `keys/` está en `.gitignore`.

Pruebas del adaptador contra la VM real (opcionales, solo cuando se piden):
```bash
CONSOLA_VM=consola-pruebas CONSOLA_GUEST_PASSFILE=keys/guest-pass go test -tags vm -count=1 -run TestVM ./internal/executor/
```

## Cómo se agrega un módulo (sin tocar `server.go`)

Un módulo son 3 cosas, todas en archivos propios:

| Qué | Dónde | Ejemplo (Parte B) |
|---|---|---|
| Lógica pura (construir los `Plan`, generar archivos) | `internal/<modulo>/` + tests | `internal/usuarios/` |
| Registro de rutas y manejadores HTTP | `internal/web/<modulo>.go` + tests | `internal/web/usuarios.go` |
| Página | `web/templates/<modulo>.html` (bloque `{{define "content"}}`) | `web/templates/usuarios.html` |

```go
// internal/web/usuarios.go
package web

func init() {
	registerModule("usuarios", func(s *server, r *ModuleRouter) {
		r.HandleFunc("GET /usuarios", s.usuariosPagina)       // obligatoria: la página del módulo
		r.HandleFunc("POST /usuarios/crear", s.crearUsuario)  // acciones: POST, bajo /usuarios/
	})
}

func (s *server) usuariosPagina(w http.ResponseWriter, r *http.Request) {
	s.renderModule(w, r, "usuarios", http.StatusOK, Form{}, datosDeLaPagina)
}
```

La consola **no arranca** (y el error nombra el módulo) si un módulo registrado:
- no registra `GET /<slug>`,
- registra una ruta fuera de `/<slug>` o `/<slug>/...`, o sin método en mayúsculas,
- no tiene `web/templates/<slug>.html`,
- o si una página no define `content`, o redefine un nombre del layout o de un parcial.

Las páginas y los parciales (`web/templates/_*.html`) se descubren solos.

Para que la paleta **Ctrl + K** abra una acción directamente en su formulario, agrega en `internal/web/modules.go` el campo `Anchors` de tu módulo: cada acción apunta al `id` del campo (o del título) al que salta, por ejemplo `"Crear usuario": "usuario"`. Al llegar por ese enlace, el cursor queda en el campo. Una prueba revisa que cada `id` exista en la página del módulo.

## Cómo se ejecuta una operación en la VM

Un módulo **no** ejecuta comandos directamente: construye un `operacion.Plan` y llama a `s.runAndShow`, que
ejecuta, registra y lleva al usuario a la página de resultado con la terminal.

```go
func (s *server) instalar(w http.ResponseWriter, r *http.Request) {
	nombre := r.PostFormValue("paquete")

	// 1. Validar SIEMPRE antes de ejecutar. Si falla, la página vuelve con lo escrito y el error junto al campo.
	if err := validate.Paquete(nombre); err != nil {
		s.renderModule(w, r, "preparacion", http.StatusUnprocessableEntity,
			Form{Values: map[string]string{"paquete": nombre}, Errors: FieldErrors(err)}, nil)
		return
	}

	// 2. Describir la operación. Cada argumento va separado: nunca una línea de shell.
	s.runAndShow(w, r, operacion.Plan{
		Action:  "Instalar aplicación",
		Target:  s.state.Current().Name,
		Timeout: 10 * time.Minute,
		Commands: []executor.Command{
			{Name: "apt-get", Args: []string{"update"}},
			{Name: "apt-get", Args: []string{"install", "-y", nombre}}, // validate.Paquete ya impide que empiece con "-"
		},
		Success: func(res []executor.Result) string { return "Se instaló " + nombre + "." },
		Explain: func(st oplog.Step) string { // opcional: convertir errores conocidos en consejos
			if strings.Contains(st.Stderr, "Could not get lock") {
				return "Otro proceso está usando apt; intenta de nuevo en un minuto."
			}
			return ""
		},
	})
}
```

| Campo de `Plan` | Para qué |
|---|---|
| `Action` | Nombre de la operación para el usuario (título del resultado, registro, barra de estado) |
| `Target` | Destino de `config.json` (normalmente `s.state.Current().Name`) |
| `Commands` | Se ejecutan en orden; el primero que falla detiene la operación. Vacío = error de programación |
| `Timeout` | Límite de toda la operación (30 s si se deja en cero). Cerrar la pestaña **no** corta la operación |
| `AlsoOK` | Otros códigos de salida que significan éxito, por ejemplo `[]int{3}` para `systemctl status` de un servicio detenido |
| `Then` | Opcional. Cuando todos los `Commands` terminan bien, recibe sus resultados y devuelve los comandos siguientes, que corren en la misma operación y el mismo registro. Sirve para **leer → cambiar en Go → escribir**: `cat /etc/hosts` → función pura → escribir el archivo. Si devuelve un error, la operación se detiene y el texto del error es el mensaje para el usuario; si devuelve cero comandos, la operación termina bien. Ejemplo completo: `planHostname` en `internal/web/hosts.go` |
| `Success` | Mensaje para el usuario cuando todo salió bien (recibe los resultados de `Commands` y luego los de `Then`) |
| `Explain` | Convierte un fallo conocido en un consejo; `""` deja el mensaje por defecto |

`executor.Command` tiene `Name`, `Args` y `Stdin` (para datos secretos como contraseñas: nunca en `Args`).

## Formularios con errores junto al campo

```html
{{define "content"}}
<h1>{{.Module.Title}}</h1>
<form method="post" action="/preparacion/instalar" data-operacion="Instalar aplicación">
  {{template "resumen-errores" .Form}}
  <label for="paquete">Paquete</label>
  <input id="paquete" name="paquete" class="form-control" value="{{index .Form.Values "paquete"}}" {{aria .Form "paquete"}}>
  {{template "error-campo" (campo .Form "paquete")}}
  <button class="btn btn-accent" type="submit">Instalar</button>
</form>
{{end}}
```

| Pieza | Qué hace |
|---|---|
| `data-operacion="…"` en el `<form>` | Muestra "Ejecutando…" con cronómetro y bloquea el doble envío (sin deshabilitar el botón, así se envía su `name`/`value`) |
| `{{aria .Form "campo"}}` | Pone `aria-invalid` y `aria-describedby` solo cuando el campo tiene error |
| `{{aria .Form "campo" "campo-ayuda"}}` | Igual, y además enlaza siempre los textos de ayuda con esos `id` (el lector de pantalla los lee al entrar al campo) |
| `data-operacion="…"` en un `<button formaction="/modulo/otra">` | Un mismo formulario con dos acciones (por ejemplo Instalar y Eliminar): el botón usa su propio texto de "Ejecutando…" |
| `data-local` en un botón | El botón no ejecuta nada en la VM (por ejemplo "Vista previa"): no muestra "Ejecutando…" ni bloquea el formulario |
| `{{template "error-campo" (campo .Form "campo")}}` | El mensaje de error debajo del campo, enlazado a él |
| `{{template "resumen-errores" .Form}}` | Resumen anunciado (`role="alert"`) con enlaces a cada campo |
| `FieldErrors(err1, err2, …)` | Convierte errores de `validate` en `Form.Errors`; un error sin campo va en `Errors[""]` |

Al volver con errores, el foco va solo al primer campo inválido. Los nombres de campo usan minúsculas, números y `-`.

## Mostrar el parcial de otra parte sin depender de él

`{{opcional "nombre" .}}` muestra el parcial `{{define "nombre"}}` si algún `web/templates/_*.html` lo define, y no muestra
nada si todavía no existe. Así una página incluye el trabajo de otra parte antes de que llegue, sin que nadie edite archivos
ajenos. Con `with` se pone un texto mientras tanto:

```html
{{with opcional "llaves-root" .}}{{.}}{{else}}<p>En construcción</p>{{end}}
```

Solo las páginas pueden usar `opcional`; si un parcial lo usa, la consola no arranca (un parcial que se llamara a sí mismo
no terminaría nunca). La página de Preparación ya lo usa para el formulario de llaves de root de la Parte B: basta con que
exista `web/templates/_llaves-root.html` con `{{define "llaves-root"}}` para que aparezca.

## Piezas disponibles

| Paquete | Qué ofrece |
|---|---|
| `internal/validate` | `Paquete`, `Usuario`, `Hostname`, `IPv4`, `Puerto`, `NombreServicio`, `Mensaje` (+ `LineasUnix`). Devuelven `*validate.Error{Field, Msg}` con un mensaje en español y un ejemplo. Validadores nuevos: en un **archivo nuevo** del paquete |
| `internal/operacion` | `Runner.Run(ctx, Plan)` (los handlers usan `s.runAndShow`), `ErrUnknownTarget` |
| `internal/executor` | `Command` (+ `Check`, `String`), `Result`, la interfaz `Executor`, `Quote`, `Fake` (`RunFunc`, `Calls()`, `Copies()`), `NewSimulated` |
| `internal/app` | `State`: destinos, `Current()`, `SetExecutor(destino, ex)` (la Parte B lo usa al terminar el bootstrap SSH) |
| `internal/web` | `registerModule`, `ModuleRouter.HandleFunc`, `s.renderModule`, `s.runAndShow`, `Form`, `FieldErrors`; para una página propia distinta a la del módulo: `s.newView(r, título, slug)` + `s.render(w, estado, "pagina.html", v)` |

## Reglas que ya cumple la base (no hay que repetirlas)

- Solo responde a `127.0.0.1`/`localhost` con su puerto, rechaza formularios de otros sitios y no se deja incrustar en otras páginas.
- Todo lo que viene de la VM se muestra escapado.
- Cada operación queda en `logs/operaciones.jsonl` con comando, salida, código y duración.
- Las reglas de seguridad del proyecto están en `.claude/claude-security-guidance.md`; el revisor automático las usa en cada commit.

## Pruebas

En los tests de `internal/web` puedes reutilizar los ayudantes existentes: `newTestState()`, `newTestHandler(t, state)`,
`serve(t, h, req)`, `postForm(path, url.Values)`, `body(rec)`.

```go
func TestCrearUsuarioEnviaLosComandosExactos(t *testing.T) {
	state := newTestState()
	f := &executor.Fake{NameValue: "SSH"}
	_ = state.SetExecutor("pruebas", f)
	h := newTestHandler(t, state)

	rec := serve(t, h, postForm("/usuarios/crear", url.Values{"usuario": {"ana"}, "contrasena": {"segura-123"}}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	calls := f.Calls() // []executor.Command, en orden: compara Name y Args exactos
	_ = calls
}
```

Para la lógica pura (archivos generados, parseo), tests de tabla en `internal/<modulo>/`, sin VM.

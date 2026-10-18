# Base para los módulos (Partes B y C)

Lo que la Parte A deja listo para que cada módulo se construya encima, sin repetir lógica.

## Correr la consola

```bash
cp config.example.json config.json   # ajustar nombres de VM e IPs; config.json no se sube al repo
go run ./cmd/consola -simular        # sin VM: cada destino responde con un canal simulado
go run ./cmd/consola                 # con VM real (Guest Additions llega en la fase F3 de la Parte A)
```

Abrir `http://127.0.0.1:8090`. La consola solo responde en esa dirección (o `localhost:8090`).

| Opción | Para qué |
|---|---|
| `-config` | Archivo de configuración (por defecto `config.json`) |
| `-logs` | Carpeta del registro de operaciones (por defecto `logs/`, solo legible por tu usuario) |
| `-simular` | Canal simulado para todos los destinos: desarrollar y hacer demos sin VM |

## Dónde va el código de cada módulo

| Qué | Dónde | Ejemplo (Parte B) |
|---|---|---|
| Lógica: construir los `Plan` a partir de los datos validados | `internal/<modulo>/` | `internal/usuarios/usuarios.go` + su `_test.go` |
| Manejadores HTTP del módulo | `internal/web/<modulo>.go`, como métodos de `server` (tienen `s.state`, `s.runner`, `s.render`) | `internal/web/usuarios.go` |
| Registro de rutas | Una línea por ruta en `NewHandler` (`internal/web/server.go`) | `mux.HandleFunc("POST /usuarios/crear", s.crearUsuario)` |
| Pantalla | El bloque `content` de la página del módulo en `web/templates/` | `web/templates/usuarios.html` |

Así cada quien trabaja en sus propios archivos; el único archivo compartido es `server.go`, y solo para agregar sus rutas.

## Cómo se arma una operación

Un módulo **no** ejecuta comandos directamente. Construye un `operacion.Plan` y se lo da al `Runner`, que:

1. usa el canal activo del destino (Guest Additions, SSH o simulado);
2. ejecuta los comandos en orden y se detiene en el primero que falle;
3. mide cada paso, explica el resultado y lo guarda en el registro;
4. no se corta si el usuario cierra la pestaña, pero sí respeta el tiempo límite.

```go
// 1. Validar SIEMPRE antes de ejecutar: el error ya trae el mensaje para el usuario.
if err := validate.Paquete(nombre); err != nil {
    // mostrar err.Error() junto al campo (errors.As(err, &ve) da ve.Field)
}

// 2. Describir la operación. Cada argumento va separado: nunca armar una línea de shell.
e, err := s.runner.Run(r.Context(), operacion.Plan{
    Action:  "Instalar aplicación",
    Target:  s.state.Current().Name,
    Timeout: 10 * time.Minute,
    Commands: []executor.Command{
        {Name: "apt-get", Args: []string{"update"}},
        {Name: "apt-get", Args: []string{"install", "-y", nombre}},
    },
    Success: func(res []executor.Result) string { return "Se instaló " + nombre + "." },
    Explain: func(s oplog.Step) string { // opcional: convertir errores conocidos en consejos
        if strings.Contains(s.Stderr, "Could not get lock") {
            return "Otro proceso está usando apt; intenta de nuevo en un minuto."
        }
        return ""
    },
})

// 3. Redirigir al resultado: la página con la terminal ya existe.
http.Redirect(w, r, "/resultado/"+e.ID, http.StatusSeeOther)
```

## Piezas disponibles

| Paquete | Qué ofrece |
|---|---|
| `internal/validate` | `Paquete`, `Usuario`, `Hostname`, `IPv4`, `Puerto`, `NombreServicio`, `Mensaje`. Devuelven `*validate.Error` con `Field` y un mensaje en español con ejemplo |
| `internal/operacion` | `Runner.Run(ctx, Plan)` como arriba |
| `internal/executor` | `Command`, `Result`, la interfaz `Executor`, `Quote` (comillas POSIX seguras), `Command.Check`, el `Fake` para pruebas y `NewSimulated` |
| `internal/app` | `State`: destinos, destino actual, `SetExecutor(destino, ex)` (la Parte B lo usa al terminar el bootstrap SSH) |
| `internal/oplog` | El registro; los módulos solo lo usan a través del `Runner` |

## Reglas que ya cumple la base (no hay que repetirlas)

- Los formularios `POST` de otros sitios se rechazan; el servidor solo responde a su propia dirección.
- Todo lo que viene de la VM se muestra escapado en la página.
- Un formulario con `data-operacion="Nombre de la acción"` muestra el indicador de ejecución y bloquea el doble clic.

## Pruebas

En los tests de un módulo, usar `executor.Fake` y revisar `fake.Calls()` para comprobar los comandos exactos que se enviarían a la VM:

```go
f := &executor.Fake{}
_ = state.SetExecutor("pruebas", f)
// ... ejecutar el handler ...
calls := f.Calls() // []executor.Command en orden
```

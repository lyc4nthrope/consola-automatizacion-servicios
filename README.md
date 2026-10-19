# Consola de automatización de servicios

Aplicación web en Go que funciona como **consola de automatización** para máquinas virtuales con GNU/Linux Debian 13.7 en Oracle VirtualBox. Convierte en botones y formularios las tareas de administración que en el curso se hicieron a mano: instalar paquetes, configurar SSH con llaves, gestionar usuarios, administrar servicios con systemd y operar un balanceador de carga con HAProxy.

Proyecto del **Parcial 2 — Automatización de servicios**, curso Computación en la Nube 2026-2, Ingeniería de Sistemas y Computación, Universidad del Quindío.

## Qué hace

La consola corre en el equipo host y ejecuta cada operación en la VM **de forma no interactiva**: valida los datos del formulario, ejecuta los comandos, informa si terminó bien y, si falla, muestra un mensaje comprensible y guarda el detalle técnico (cada comando, su salida, su código y su duración) en el registro de operaciones.

| Módulo | Operaciones | Estado |
|---|---|---|
| Preparación del sistema | Instalar y eliminar aplicaciones · Mensaje de entrada (`/etc/issue`) · Nombre de host y `/etc/hosts` | ✅ Listo |
| Preparación del sistema | Llaves SSH de root (generar, autorizar, quitar el acceso con contraseña, descargar la privada) | En construcción (Parte B) |
| Gestión de usuarios | Crear usuarios con `sudo` y llave SSH opcionales · Eliminar usuarios | En construcción (Parte B) |
| Servicios systemd | Desplegar una aplicación como servicio y controlarla: start, stop, restart, status, enable, disable | En construcción (Parte C) |
| Balanceador HAProxy | Agregar y quitar servidores del back-end | En construcción (Parte C) |

El **Panel** muestra el destino actual, un mapa de la infraestructura (qué VM recibe las operaciones y por qué canal llega la consola a cada una) y las últimas operaciones. Con **Ctrl + K** (o el botón «Ir a…») se busca y abre cualquier página, acción o destino.

## Cómo se comunica con la máquina virtual

1. **Guest Additions** (`VBoxManage guestcontrol`): para el primer contacto, mientras la VM no tiene acceso por SSH configurado.
2. **SSH con llaves de autenticación**: a partir de ahí, las operaciones van por SSH (Parte B).

Los módulos no dependen del canal: ambos implementan la misma interfaz `Executor`. El detalle está en [docs/PLAN.md](docs/PLAN.md).

## Requisitos

- Go 1.24 o posterior (solo para compilar).
- Oracle VirtualBox 7.2 con `VBoxManage` en el `PATH`, y en cada VM las Guest Additions instaladas.
- Una VM Debian 13.7 encendida para las operaciones reales. Sin VMs, la consola funciona en modo simulado.

## Compilar

```sh
make build        # o: CGO_ENABLED=0 go build -trimpath -o consola ./cmd/consola
```

El resultado es **un solo binario** (`consola`) con las páginas, los estilos y los scripts incluidos: para usarlo en otro equipo basta copiar el binario y su `config.json`.

## Configurar

1. Copia el ejemplo y pon tus VMs:

   ```sh
   cp config.example.json config.json
   ```

   | Campo | Qué es |
   |---|---|
   | `listen` | Dirección donde escucha la consola. Por defecto `127.0.0.1:8090` (solo este equipo) |
   | `targets[].name` | Nombre del destino dentro de la consola: minúsculas, números y guiones, por ejemplo `pruebas` |
   | `targets[].vm` | Nombre exacto de la VM en VirtualBox |
   | `targets[].ip` | Dirección IP de la VM, para el canal SSH |
   | `targets[].ssh_user` | Usuario para SSH |
   | `targets[].guest_user` | Opcional: usuario de Guest Additions; si falta, `root` |

2. Guarda la contraseña de Guest Additions **escribiéndola tú en la terminal**, nunca en `config.json`, en un argumento ni en una variable de entorno:

   ```sh
   mkdir -p keys && chmod 700 keys
   read -rs -p "Contraseña de root de la VM: " p && (umask 077 && printf '%s' "$p" > keys/guest-pass-pruebas) && unset p
   ```

   La consola busca primero `keys/guest-pass-<destino>` y después `keys/guest-pass` (una para todas las VMs). Si el archivo lo pueden leer otros usuarios, la consola lo rechaza y dice cómo corregirlo (`chmod 600`).

## Ejecutar

```sh
./consola            # o: make run
```

Abre `http://127.0.0.1:8090`. La terminal dice qué canal usa cada destino:

```
destino pruebas: canal Guest Additions con la contraseña de keys/guest-pass-pruebas
consola escuchando en http://127.0.0.1:8090 (2 destinos configurados)
```

| Opción | Para qué |
|---|---|
| `-simular` | Todas las VMs responden con un canal simulado: sirve para conocer la interfaz sin VirtualBox (`make simular`) |
| `-config <archivo>` | Otro archivo de configuración (por defecto `config.json`) |
| `-logs <carpeta>` | Dónde se guarda el registro de operaciones (por defecto `logs/`) |

La consola se ejecuta desde la carpeta que tiene `config.json` y `keys/`. Para comprobar el canal, en el Panel pulsa **Probar conexión**: la VM responde con su nombre.

## Probar

```sh
make test                                                      # pruebas sin VMs
make check                                                     # lo mismo que la CI: formato, vet, pruebas y compilación
make vm-test VM=consola-pruebas PASSFILE=keys/guest-pass-pruebas   # contra una VM real por Guest Additions
```

## Estructura

| Carpeta | Contenido |
|---|---|
| `cmd/consola` | Programa principal: lee la configuración, elige el canal de cada destino y arranca el servidor |
| `internal/executor` | Interfaz `Executor` y sus canales: Guest Additions, simulado (y SSH, Parte B) |
| `internal/operacion` | Ejecuta una operación en la VM: pasos, tiempo límite, mensaje para el usuario y registro |
| `internal/validate` | Validación de cada dato del usuario antes de ejecutar nada |
| `internal/oplog` | Registro de operaciones en `logs/` |
| `internal/web` | Rutas, páginas y módulos |
| `web/` | Plantillas, estilos, scripts e íconos (incluidos en el binario) |
| `docs/` | Plan general, planes de cada parte y guía para crear módulos |

## Seguridad

- El repositorio **nunca** contiene llaves privadas, contraseñas ni registros: `keys/`, `logs/` y `config.json` están en `.gitignore`.
- Las contraseñas solo existen en archivos con permisos 600 que el usuario escribe en su terminal.
- Los datos del usuario se validan con listas blancas y viajan a la VM como argumentos separados, nunca como texto de un comando de shell.
- La consola solo acepta peticiones dirigidas a su propia dirección y formularios enviados desde sus propias páginas.

## Equipo

- Cristhian Eduardo Osorio Restrepo
- Daniel Stiven Perez Cordoba
- Wiliam Carmona Diaz

**Docente:** Ing. Carlos Eduardo Gómez Montoya, Mg, PhD

**Entrega:** 25 de octubre de 2026 · **Sustentación:** desde el 26 de octubre de 2026

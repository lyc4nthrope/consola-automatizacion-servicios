# Consola de automatización de servicios

Aplicación web en Go que funciona como **consola de automatización** para una máquina virtual con GNU/Linux Debian 13.7 en Oracle VirtualBox. Convierte en botones y formularios las tareas de administración que en el curso se hicieron a mano: instalar paquetes, configurar SSH con llaves, gestionar usuarios, administrar servicios con systemd y operar un balanceador de carga con HAProxy.

Proyecto del **Parcial 2 — Automatización de servicios**, curso Computación en la Nube 2026-2, Ingeniería de Sistemas y Computación, Universidad del Quindío.

## Qué hace

La aplicación corre en el equipo host y ejecuta cada operación en la VM **de forma no interactiva**: valida los datos del formulario, ejecuta la acción, informa si terminó bien y, si falla, muestra un mensaje comprensible y guarda el detalle técnico para el diagnóstico.

| Módulo | Operaciones |
|---|---|
| Preparación del sistema | Instalar y eliminar aplicaciones · Generar y configurar las llaves SSH de root, deshabilitar el acceso por contraseña y descargar la llave privada · Personalizar el mensaje de entrada (`/etc/issue`) · Asignar el nombre de host y actualizar `/etc/hosts` |
| Gestión de usuarios | Crear usuarios con privilegios de `sudo` y llaves SSH opcionales · Eliminar usuarios, con opción de borrar sus archivos |
| Gestión de servicios | Desplegar una aplicación como servicio de systemd (por ejemplo, el catálogo de libros) y controlarla: start, stop, restart, status, enable, disable |
| Balanceador de carga | Agregar y quitar servidores del back-end de HAProxy |

## Cómo se comunica con la máquina virtual

1. **Guest Additions** (`VBoxManage guestcontrol`): solo para el primer contacto, mientras la VM no tiene acceso por SSH configurado.
2. **SSH con llaves de autenticación**: a partir de ahí, todas las operaciones van exclusivamente por SSH.

Los módulos no dependen del canal: ambos implementan la misma interfaz `Executor` (ver [docs/PLAN.md](docs/PLAN.md), §3).

## Escenario

- **Host:** Debian 13, Go 1.24, Oracle VirtualBox 7.2.
- **VM de pruebas:** Debian 13.7, adaptador en modo puente, servidor SSH y Guest Additions.
- **VM balanceador:** la misma base, con HAProxy instalado.
- **Servidores back-end:** VMs creadas sobre un disco multiconexión, ejecutando el catálogo de libros como servicio.

## Estado

🚧 En desarrollo. El plan de trabajo, las decisiones técnicas y la división en tres partes están en [docs/PLAN.md](docs/PLAN.md).

Las instrucciones de compilación y ejecución se agregan cuando exista la primera versión funcional.

## Seguridad

Este repositorio **nunca** debe contener llaves privadas, contraseñas ni registros de ejecución. Las contraseñas se leen de variables de entorno y las carpetas `keys/` y `logs/` están en `.gitignore`.

## Equipo

- Cristhian Eduardo Osorio Restrepo
- Daniel Stiven Perez Cordoba
- Wiliam Carmona Diaz

**Docente:** Ing. Carlos Eduardo Gómez Montoya, Mg, PhD

**Entrega:** 25 de octubre de 2026 · **Sustentación:** desde el 26 de octubre de 2026

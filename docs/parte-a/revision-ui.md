# Revisión de la interfaz (antislop-ui)

Revisión de F7 sobre todas las pantallas de la Parte A con la lista «UI Skill Checklist» de la skill `antislop-ui`. Un patrón señalado es una pregunta, no un defecto: se conserva cuando sirve al producto, con la razón escrita, y se quita cuando solo decora. La auditoría completa del jueves 22 (`frontend-ux-audit`) parte de este documento.

**Producto:** una consola de operaciones para administrar VMs. La pantalla responde a una pregunta: *qué VM recibe las operaciones, por qué canal y qué pasó con lo último que se ejecutó*.

## Patrones conservados, con su razón

| Patrón | Dónde | Por qué se queda |
|---|---|---|
| Cuadrícula de fondo | `body` (`--c-grid`, opacidad 3–5 %) | Es la identidad «consola de operaciones» que eligió el equipo en F1: evoca un tablero de control. Está casi invisible para no competir con el contenido |
| Panel de terminal con tres puntos | Página de resultado | No es una terminal falsa: muestra la salida **real** de cada comando ejecutado en la VM, con su código y su duración, y se puede copiar |
| Punto verde / ámbar | Barra superior y barra de estado | Marca un estado real: hay o no hay canal con la VM actual. Sin brillo ni pulso; el texto del canal lo dice también |
| Animaciones | Barra de progreso superior y pulso del mapa | Solo corren **mientras una operación está en curso** (`body.is-running`) y se apagan con `prefers-reduced-motion` |
| Tema oscuro por defecto | `data-bs-theme="dark"` | Herramienta de operación, de uso prolongado; hay un botón para cambiar de tema y la elección se recuerda |
| Monoespaciada | IPs, nombres de VM, comandos, rutas | Son datos que se copian o se comparan carácter a carácter, no decoración |
| Desenfoque (`backdrop-filter`) | Solo la barra superior fija | Un único elemento (dentro del límite de 1–2), para que el contenido que pasa por debajo no se confunda con la barra |
| Sombra de elevación | Solo la paleta Ctrl + K | Es lo único que flota sobre la página. Las demás sombras son anillos de foco o marcas de estado (opción activa, destino actual) |
| Marca izquierda | Opción activa de la paleta | Marca un estado real: la opción que abre Enter |
| Radio de borde | Un solo valor (`--c-radius`), círculos solo en los puntos | Jerarquía consistente, sin todo en forma de píldora |
| Color de acento | Botón principal de cada formulario, sección activa, destino actual, foco | Cada uso señala «aquí actúas» o «esto es lo actual»; el verde queda reservado a «salió bien» |

## A revisar en la auditoría del jueves

| Patrón | Dónde | Duda |
|---|---|---|
| Rótulo en mayúsculas sobre el título | `Panel` («Consola de automatización de servicios»), módulos («Módulo») | En el Panel repite el nombre que ya está en la barra lateral; en los módulos aporta poco. Candidato a quitarse o a llevar información propia (por ejemplo, la ruta de navegación, como ya hace la página de resultado) |
| Tarjetas de módulo iguales en el Panel | `inicio.html` | Hoy cada módulo tiene el mismo peso porque todos tienen la misma importancia para el parcial; revisar cuando los cuatro estén terminados |

## Lista de la skill

| Pregunta | Resultado |
|---|---|
| Paleta propia, sin degradado azul-violeta | Sí: verde azulado de acento, neutros y tres colores de estado (ok, aviso, fallo) |
| Acento solo en el momento clave | Sí (tabla de arriba) |
| Sin emoji decorativos | Sí; íconos de Bootstrap Icons elegidos por significado |
| Composición variada | Sí: datos (Panel), árbol (mapa), formularios (Preparación), terminal (resultado) |
| Sin bento, terminal falsa, tres columnas de precios ni franjas sin significado | Sí |
| Sin píldora sobre el H1 que repite el título | No hay píldoras; los rótulos de texto están en «a revisar» |
| Todo control tiene destino o comportamiento real | Sí; lo que no existe aún dice «En construcción» (el README dice qué parte lo entrega) |
| Movimiento con propósito, sin bucles sin fin | Sí |
| Vidrio, brillo, sombra y radio dentro de sus límites | Sí |
| Cada punto de color marca un estado real | Sí |
| La pantalla se organiza alrededor de la decisión del usuario | Sí: destino actual primero, luego el mapa para cambiarlo, luego los módulos y lo último ejecutado |
| Números y filas reales, sin datos inventados | Sí: todo sale de `config.json`, del canal real y del registro de operaciones |
| Campos vacíos sin datos falsos | Sí; las ayudas usan ejemplos marcados como tales («por ejemplo `tree`») |
| Estados vacío, de carga y de error dicen la causa y el siguiente paso | Sí: «Aún no hay operaciones… Empieza con Probar conexión», «Ejecutando…» con cronómetro, y cada error de F3–F6 dice qué pasó y qué hacer (revisados en F7) |
| Resiste anchos, temas, estados y uso solo con teclado | Sí, verificado en Chromium a 1280 y 390 px, en ambos temas, con Tab, Enter, Esc y Ctrl + K |

# ADR-0018: TUI estilo BIOS, formularios y asistente de primer uso

- **Estado:** Aceptada
- **Fecha:** 2026-10-27
- **Decisiones relacionadas:** ADR-0017 (TUI y puerto Backend), ADR-0004 (CLI y TUI), sección 27 (V1 CLI/TUI)

## Contexto

Con la primera TUI (ADR-0017) solo se consultaba y se aprobaban precios:
registrar productos o configurar el negocio exigía comandos. El usuario
pidió que la TUI permita **hacer todo** sin escribir comandos, con una
interfaz al estilo de la configuración de una BIOS.

## Parámetros ratificados por el usuario (2026-10-27)

| Tema | Decisión |
|---|---|
| Estilo | **BIOS clásico**: barra de menús arriba, fondo azul, panel de ayuda a la derecha y teclas abajo |
| Orden | Primero la TUI completa de lo que ya existe (M1–M3), luego M4 con backend y pantalla en PRs seguidos |
| Primer uso | **Asistente** en la TUI que hace lo de `balanzapro setup` |

## Decisión

1. **Marco único.** La raíz dibuja siempre el mismo marco: menús (las
   pestañas del menú principal, que siguen visibles dentro de cada
   pantalla), regla, título y contenido, panel de ayuda (30 columnas, solo
   con 90 o más y fuera de las tablas anchas), línea de resultado y barra
   de teclas. Las pantallas solo devuelven su contenido, sus teclas y su
   texto de ayuda (`Info`). Con `--no-color` quedan la negrita y la
   inversión, que marcan la selección.

2. **Menús por módulo.** Inventario, Precios y Sistema, cambiando con
   ←/→. Una opción aparece con sesión y con el permiso de lectura de su
   módulo; un menú vacío no aparece. Los atajos por número desaparecen: con
   menús que crecerán, la posición deja de ser estable.

3. **Teclas de función para acciones.** En las listas, **F2** crea, **F3**
   edita y **F4** activa o inactiva; las letras quedan para navegar
   (**P** precios, **R** recargar, **/** filtrar). Las pantallas muestran
   solo las acciones que el usuario puede ejecutar.

4. **Formulario genérico.** Campos de texto, contraseña y selección
   (`‹ … ›`, ←/→). Enter avanza y guarda en el último campo; **F10** o
   Ctrl+S guardan desde cualquiera; Esc cancela sin guardar. El guardado
   corre fuera del ciclo de la interfaz y, si el sistema rechaza los datos,
   el formulario sigue abierto con el motivo. Las acciones irreversibles o
   de estado (inactivar) pasan por una confirmación.

5. **Edición con revisión.** Editar o inactivar envía la revisión que se
   mostró (`ExpectedRevision`): si otra persona cambió el producto, no se
   guarda y se pide volver a abrirlo.

6. **Asistente de primer uso.** Si la CLI no encuentra la instalación
   (base o organización inexistente), entrega a la TUI una función `Setup`
   en vez del backend. El asistente pide negocio, razón social, RIF,
   moneda, usuario, nombre y contraseña dos veces, y llama a esa función,
   que ejecuta el mismo camino que `balanzapro setup` (`install`: crear la
   base, migrar, registrar organización y administrador, iniciar sesión) y
   devuelve el backend ya conectado. La empresa, la sucursal y el almacén
   se llaman `PRINCIPAL`, como en el comando sin opciones.

7. **Backend.** `tui.Backend` suma `CreateProduct`, `UpdateProduct`,
   `Units` y `CreateUnit`, sobre los mismos casos de uso y permisos que los
   comandos.

## Consecuencias

- Configurar el negocio y mantener el catálogo ya no necesita comandos.
- Los movimientos de inventario, compras, tasas, listas de precios y la
  administración siguen en comandos hasta sus tickets de TUI (TUI-3 a
  TUI-5), que reutilizan el marco, el formulario y la confirmación.
- Las teclas de función dependen de la terminal: Windows Terminal, conhost
  y las terminales de Linux las envían; si alguna no, Ctrl+S guarda los
  formularios.
- Las pruebas del modelo comparan el texto sin estilos; la verificación
  sobre una terminal real se hace con el binario en una pseudoterminal.

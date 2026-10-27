# Pantalla interactiva (TUI)

Sin argumentos, `balanzapro` (en Windows, `.\balanzapro.exe`) abre la
pantalla interactiva. Se maneja solo con el teclado, al estilo de la
configuración de una BIOS:

```
 BalanzaPro   Inventario  Precios  Sistema               admin · PRINCIPAL/PRINCIPAL
─────────────────────────────────────────────────────────────────────────────────────
 Inventario                                                  │ Ayuda
                                                             │
 Hola, Administrador.                                        │ Catálogo de productos:
                                                             │ alta (F2), edición y
  ► Productos                                                │ códigos de barras (F3),
    Unidades de medida                                       │ activar o inactivar (F4).
    Existencias y kardex                                     │

 ←→ Menú  ↑↓ Elegir  Enter Abrir  Esc/q Salir
```

- Arriba, los **menús** y su usuario con la empresa y la sucursal.
- A la derecha, la **ayuda** de la opción o pantalla actual (se oculta si la
  ventana tiene menos de 90 columnas y en las tablas anchas).
- Abajo, el **resultado** de la última acción y las **teclas** disponibles.

**Esc** vuelve a la pantalla anterior y **q** sale (desde el menú también
Esc). En Windows conviene usar **Windows Terminal** con la ventana
maximizada.

Las reglas y los permisos son los mismos que en los comandos: la TUI solo
muestra lo que su usuario puede usar, y quien decide es el sistema.

## Primer uso: el asistente

Si el equipo todavía no tiene instalación, la TUI abre la **Configuración
inicial**:

| Campo | Qué poner |
|---|---|
| Nombre del negocio | Obligatorio, p. ej. `Bodega Ana` |
| Razón social | Opcional; vacío usa el nombre |
| RIF | Opcional, `J-12345678-9` |
| Moneda de valoración | **USD** o **VES** (←/→). Es la moneda del costo del inventario y **no se cambia después** |
| Usuario administrador | `admin` por defecto |
| Nombre visible | Cómo lo saluda la TUI |
| Contraseña y repetición | Al menos 8 caracteres; no se muestran |

Al guardar (**Enter** en el último campo o **F10**) crea la base de datos,
la organización con la empresa, la sucursal y el almacén `PRINCIPAL`, y el
administrador, y entra con su sesión iniciada. Es lo mismo que
`balanzapro setup` (ver [Primeros pasos](primeros-pasos.md)). **Esc** sale
sin crear nada.

## Iniciar sesión

Si ya inició sesión en este equipo (en la TUI o con `balanzapro auth
login`), entra directo al menú. Si no, pide usuario y contraseña. **Tab**
cambia de campo y **Enter** entra. La sesión es la misma para la TUI y los
comandos: cerrar sesión en uno la cierra en el otro.

## Menús

**←/→** cambian de menú, **↑/↓** eligen y **Enter** abre.

| Menú | Opción | Qué hace | Permiso |
|---|---|---|---|
| Inventario | Productos | Catálogo: alta, edición, códigos de barras, inactivar; existencias y precios del producto | ver catálogo |
| | Unidades de medida | Unidades y sus decimales; alta de unidades | ver catálogo |
| | Existencias y kardex | Física, reservada, bloqueada y disponible por almacén, con costo promedio y valor; **Enter** abre el kardex | ver existencias |
| Precios | Precios vigentes | Precio por lista, el programado y la propuesta pendiente | ver precios |
| | Propuestas de precio | Pendientes (**T** muestra todas); aprobar o rechazar | ver precios |
| Sistema | Diagnóstico | Las verificaciones de `balanzapro doctor`; **R** repite | — |
| | Versión | Versión, commit y fecha de compilación | — |
| | Cerrar sesión | Cierra la sesión de este equipo | — |
| | Salir | Cierra BalanzaPro | — |

Un menú sin opciones permitidas no aparece.

## Listas

- **↑/↓**, **Re Pág/Av Pág** e **Inicio/Fin** mueven la selección.
- **/** abre el filtro: escriba y quedan las filas que contienen el texto.
  **Enter** lo fija y **Esc** lo borra.
- **R** recarga.

## Formularios

Todos funcionan igual:

| Tecla | Acción |
|---|---|
| ↑/↓ o Tab | Campo anterior o siguiente |
| ←/→ o Espacio | Cambia la opción de un campo `‹ … ›` |
| Enter | Pasa al siguiente campo; en el último, guarda |
| F10 (o Ctrl+S) | Guarda desde cualquier campo |
| Esc | Cancela sin guardar |

Si el sistema rechaza los datos (un SKU repetido, un código de barras con
dígito verificador incorrecto…), el formulario sigue abierto con el motivo
en rojo: corrija y vuelva a guardar.

## Catálogo

Con permiso para gestionar el catálogo, en **Productos**:

- **F2 Nuevo**: nombre, SKU (vacío asigna `P-000001`, `P-000002`…), unidad
  y códigos de barras separados por comas. La unidad no se cambia después.
- **F3 Editar**: nombre, SKU, códigos de barras para agregar y para quitar.
  Si otra persona modificó el producto mientras tanto, no se guarda: vuelva
  a abrirlo para ver su versión.
- **F4 Activar/Inactivar**: pide confirmación. Un producto inactivo no se
  compra ni se vende y conserva su historial.

**Enter** abre sus existencias y **P** sus precios.

En **Unidades de medida**, con permiso para gestionarlas, **F2 Nueva** pide
código (`SACO`, `CAJA12`), nombre y decimales (0 para unidades enteras, 3
para kilos con gramos). Vea [Catálogo](catalogo.md).

## Aprobar o rechazar un precio

En una propuesta pendiente, con permiso para aprobar:

- **A Aprobar** abre la confirmación:
  - **Precio**: el propuesto, que puede ajustar;
  - **Vigencia**: vacía rige ya; `AAAA-MM-DD HH:MM` (hora de Caracas) la
    programa.
- **X Rechazar** pide el motivo.

Si el precio cambió después de la propuesta, o el nuevo queda por debajo
del costo sin el permiso extraordinario, la confirmación muestra el motivo y
no cambia nada. Vea [Listas y precios](precios.md).

## Qué viene después

Las próximas versiones suman a la TUI, en este orden: movimientos de
inventario (entradas, salidas, ajustes, transferencias, reservas, bloqueos y
conteos); compras y tasas; listas de precios y administración (usuarios,
roles, estructura y contraseña). Mientras tanto, eso se hace con los
comandos de las demás guías.

# Pantalla interactiva (TUI)

Sin argumentos, `balanzapro` (en Windows, `.\balanzapro.exe`) abre la
pantalla interactiva. Se maneja solo con el teclado, al estilo de la
configuración de una BIOS:

```
 BalanzaPro  Inventario  Movimientos  Compras  Precios  Sistema   admin · PRINCIPAL
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
| | Existencias y kardex | Física, reservada, bloqueada y disponible por almacén, con costo promedio y valor; **Enter** abre el kardex y **F8** revierte un movimiento | ver existencias |
| Movimientos | Entrada de mercancía | Carga inicial, corrección u otra entrada, con costo unitario | ajustar existencias |
| | Salida de mercancía | Merma, consumo interno, corrección u otra salida | ajustar existencias |
| | Transferencia entre almacenes | De un almacén a otro de la sucursal | transferir |
| | Reservas | Apartar existencia para un pedido; despachar o liberar | ver existencias (gestionar: reservas) |
| | Bloqueos | Retener mercancía no vendible; liberar o dar de baja | ver existencias (gestionar: bloqueos) |
| | Conteos físicos | Captura rápida, revisión y aprobación | contar o aprobar conteos |
| | Política de existencia negativa | Si una salida puede dejar el disponible en negativo | gestionar la política |
| Compras | Recepciones | Mercancía que llega de un proveedor: líneas, gastos y confirmación | ver compras |
| | Órdenes de compra | Lo pedido con su costo pactado; recibir contra la orden | ver compras |
| | Proveedores | Proveedores de la organización | ver compras |
| | Costos de un producto | Costo promedio, última compra y costo de reposición | ver compras |
| | Tasas de cambio | Tasas con su fuente, versionadas | ver tasas |
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
| F5 | En un campo **Producto**, abre la lista de productos |
| Enter | Pasa al siguiente campo; en el último, guarda |
| F10 (o Ctrl+S) | Guarda desde cualquier campo |
| Esc | Cancela sin guardar |

Si el sistema rechaza los datos (un SKU repetido, un código de barras con
dígito verificador incorrecto…), el formulario sigue abierto con el motivo
en rojo: corrija y vuelva a guardar.

### El campo Producto

Acepta el SKU, parte del nombre o el código de barras; un **lector de
códigos** funciona igual que el teclado. Al salir del campo, la TUI lo
reconoce y muestra debajo el producto, su unidad y lo disponible en los
almacenes de la sucursal (`→ HARINA Harina PAN 1 kg (UND) · disponible
PRINCIPAL 20`). Si no lo reconoce o coinciden varios, **F5** abre la lista:
escriba para filtrar y **Enter** lo elige.

### El campo Almacén

Muestra los almacenes activos de su sucursal; empieza en el primero y se
cambia con **←/→**.

### Guardar dos veces no duplica

Cada formulario de movimiento lleva su propia clave: si guarda, falla la
conexión y vuelve a guardar, la operación se registra una sola vez.

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

## Movimientos de inventario

### Entradas y salidas

**Movimientos → Entrada de mercancía** pide almacén, producto, cantidad,
costo unitario (vacío entra al costo promedio vigente, que debe existir),
motivo y nota. **Salida de mercancía** es igual, sin costo: sale al costo
promedio. El motivo **Otro** exige una nota. Las compras a proveedores no
van aquí, sino por Compras.

Una salida no deja el disponible en negativo. Si la política de la empresa
lo permite y usted tiene el permiso extraordinario, el formulario muestra
**Permitir negativo** (ver [Inventario](inventario.md)).

### Revertir un movimiento

En el kardex, elija el movimiento y pulse **F8**: pide el motivo y registra
el movimiento contrario; el original queda a la vista. Cada movimiento se
revierte una sola vez, y una transferencia no se revierte por partes:
registre otra en sentido contrario.

### Transferencias

**Transferencia entre almacenes**: origen, destino (empieza en otro
almacén), producto y cantidad. La mercancía sale al costo promedio del
origen y entra con ese mismo valor, en una sola operación.

### Reservas

Lista las reservas activas (**T** alterna con todas). Con permiso:

- **F2 Nueva**: almacén, producto, cantidad, referencia del pedido
  (obligatoria) y vencimiento opcional `AAAA-MM-DD HH:MM` (hora de
  Caracas). Nunca reserva más que el disponible.
- **F3 Despachar**: saca lo reservado; vacío despacha todo lo pendiente,
  una cantidad menor deja el resto reservado.
- **F4 Liberar**: lo devuelve al disponible, con motivo.

### Bloqueos

Para mercancía que no se puede vender (dañada, en cuarentena…). **F2
Nuevo** retiene la cantidad con su motivo, sin sacarla del almacén. **F3
Liberar** la devuelve al disponible; **F4 Dar de baja** la saca como merma
al costo promedio (no se deshace).

### Conteos físicos

1. **F2 Nuevo**: elija el almacén y una nota; se abre la captura.
2. Escanee o escriba el producto, **Enter**, la cantidad contada, **Enter**.
   La línea aparece con lo registrado y la diferencia, y el cursor vuelve al
   producto para el siguiente. **Tab** lleva al costo, solo necesario si el
   producto no tiene costo y sobran unidades. Volver a contar un producto
   reemplaza su línea.
3. **F9 Enviar a revisión** cuando termine.
4. Otra persona, con permiso para aprobar, abre el conteo y pulsa **A
   Aprobar** (aplica las diferencias como ajustes) o **X Rechazar**. Quien
   contó o envió el conteo no puede decidirlo.

### Política de existencia negativa

Muestra si la empresa permite salidas en negativo y con qué límite, y los
productos con política propia. **F2 Empresa** la habilita (con límite por
producto y almacén) o la deshabilita; **F3 Producto** fija para un producto
*Hereda*, *Prohíbe* o *Permite con límite*.

## Compras

### Recepciones

La lista muestra los borradores (**T** alterna con todas). **F2 Nueva**
pide almacén, proveedor, orden de compra (o *ninguna* para una compra
directa), moneda, fecha de la factura (vacía: hoy), número de factura y
nota, y abre el **documento**:

```
 Recepción RC-000001 · borrador
 DIST · Distribuidora · PRINCIPAL/PRINCIPAL/PRINCIPAL
 Factura FAC-1 del 2026-10-28 en VES
 Tasa: 1 USD = 45.725 VES (oficial BCV, desde 2026-10-28, versión 1); se aplica al confirmar
  #  SKU     Producto  Cantidad  Costo unit.  Compra       Gastos  Total
  1  HARINA  Harina    30 UND    57           1710.00 VES  0.00    —
 Total compra: 1710.00 VES ≈ 37.40 USD
 Gasto: Flete 2 USD, por valor; se prorratea al confirmar
```

En borrador:

- **F2 Línea**: producto, cantidad y costo unitario de la factura (en la
  moneda del documento, sin gastos). **Enter** sobre una línea la edita y
  **F8** la quita.
- **F3 Gasto**: flete, seguro u otro, con su moneda y el reparto *por
  valor* o *por cantidad*.
- **F9 Confirmar** (con permiso): la mercancía entra al almacén, los gastos
  se reparten, se recalcula el costo promedio y pueden quedar propuestas de
  precio para aprobar en *Precios → Propuestas*. Es definitivo.
- **F4 Anular**: descarta el borrador sin mover existencia.

Si el documento no está en la moneda de valoración, el encabezado muestra
la tasa vigente a la fecha de la factura. Si falta, lo avisa y **F6** la
registra ahí mismo (con permiso de tasas), con la fecha ya puesta.

Ya confirmada, **F3 Gasto tardío** registra un gasto que llegó después:
revalora lo que queda en el almacén.

### Órdenes de compra

**F2 Nueva**: proveedor, moneda y nota; después se cargan las líneas como
en un conteo (producto, Enter, cantidad, Enter, costo pactado, Enter) y
**F10** crea la orden. **F8** quita la última línea. En una orden abierta,
**F5 Recibir** abre una recepción contra ella y **F4 Anular** deja de
esperar lo pendiente.

### Proveedores, costos y tasas

- **Proveedores**: **F2 Nuevo** (código, nombre, RIF). Son de toda la
  organización.
- **Costos de un producto**: escriba el producto y **Enter**: costo
  promedio por almacén, última compra y costo de reposición. **F2**
  registra un costo de reposición nuevo (no cambia el costo promedio).
- **Tasas de cambio**: **F2 Registrar**: base y cotizada, tasa, fuente
  (oficial, manual o proveedor) con su nombre, fecha desde la que rige y
  referencia. Corregir es registrar otra versión. Vea
  [Tasas de cambio](tasas.md).

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

La próxima versión suma a la TUI las listas de precios y la administración
(usuarios, roles, estructura y contraseña). Mientras tanto, eso se hace con
los comandos de las demás guías.

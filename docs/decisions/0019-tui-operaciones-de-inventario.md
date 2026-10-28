# ADR-0019: Operaciones de inventario en la TUI

- **Estado:** Aceptada
- **Fecha:** 2026-10-28
- **Decisiones relacionadas:** ADR-0018 (TUI estilo BIOS), ADR-0011 y ADR-0012 (inventario, reservas, bloqueos y conteos)

## Contexto

Con TUI-2 se configura el negocio y el catálogo sin comandos. Los
movimientos de inventario (BP-M2-001 a BP-M2-003) seguían solo en la CLI.

## Parámetros ratificados por el usuario (2026-10-28)

| Tema | Decisión |
|---|---|
| Alcance | Todo en un PR: entradas, salidas, transferencias, reversos, reservas, bloqueos, conteos y política de existencia negativa |
| Producto | Campo con buscador: SKU, nombre o código de barras (también con lector); F5 abre una lista; al reconocerlo muestra nombre y disponible |
| Almacén | El de la sesión, cambiable: selección con los almacenes activos de la sucursal |
| Conteos | Tabla de captura rápida: producto, Enter, cantidad, Enter |

## Decisión

1. **Menú Movimientos** con una opción por operación. Reservas y bloqueos se
   ven con el permiso de lectura de existencias; crearlos, despacharlos o
   cerrarlos exige su permiso de gestión. Conteos aparece con el permiso de
   contar o el de aprobar.

2. **Campo Producto.** Al salir del campo la TUI busca el texto: si coincide
   el SKU o un código de barras, o la búsqueda da un solo producto, lo
   muestra con su unidad y su disponible por almacén de la sucursal. Es solo
   informativo: el backend vuelve a resolver la referencia al guardar, con
   las mismas reglas que los comandos. **F5** abre la lista de productos
   activos con el filtro listo.

3. **Campo Almacén.** El formulario carga los almacenes activos de la
   sucursal antes de mostrarse. Si el usuario no puede listarlos, queda el
   almacén por defecto de la sucursal (el backend exige elegir si hay más de
   uno). La empresa y la sucursal son siempre las de la sesión.

4. **Idempotencia por formulario.** Cada formulario de movimiento genera una
   clave al abrirse y la envía en cada intento: un reintento tras perder la
   respuesta no duplica la operación, y corregir datos tras un rechazo no
   choca (el intento rechazado no guardó nada).

5. **Reverso desde el kardex** (F8), para movimientos que no son reversos ni
   patas de transferencia.

6. **Lista genérica** (`recordsScreen`) para reservas, bloqueos y conteos:
   filtro, recarga, alternar activos/todos y acciones por tecla que solo se
   muestran cuando aplican a la fila elegida y el usuario tiene permiso.

7. **Captura de conteos** en una sola pantalla: producto (o código de
   barras), Enter, cantidad, Enter registra y vuelve al producto. F9 envía a
   revisión; en revisión, A aprueba y X rechaza. La separación de funciones
   (quien contó no aprueba) la aplica el servicio.

8. Pantallas como «Crear conteo → captura» se encadenan con un mensaje que
   reemplaza la pantalla actual, para que Esc vuelva a la lista.

## Consecuencias

- El inventario completo de M2 se opera sin comandos.
- Compras, tasas, listas de precios y administración siguen en comandos
  hasta TUI-4 y TUI-5.
- El backend de la TUI suma un método por caso de uso; el puerto crece con
  cada módulo, pero cada método es una llamada directa al servicio.

# ADR-0013: Compras, recepciones, gastos atribuibles y costo de reposición

- **Estado:** Aceptada
- **Fecha:** 2026-10-27
- **Decisiones relacionadas:** DEC-007.E1/E2, DEC-007.P2, secciones 19.2–19.7; BP-M3-001

## Contexto

El criterio de BP-M3-001 es: "CPP cambia, costo de reposición queda
separado". La sección 19.3 pide:

- incorporar al CPP solo costos capitalizables, con flete, seguro y gastos
  atribuibles prorrateados;
- registrar el criterio, la base y el residuo del prorrateo;
- no alterar ventas cerradas cuando un gasto llega tarde;
- mantener el costo de reposición como una estimación comercial que nunca
  sobrescribe el CPP.

## Parámetros ratificados por el usuario (2026-10-27)

| Tema | Decisión |
|---|---|
| Documentos | Proveedores, órdenes de compra y recepciones. Una recepción puede venir de una orden (parcial) o ser directa |
| Prorrateo | Cada gasto elige su criterio: **por valor** (predeterminado) o **por cantidad**. Se guardan base y residuo, y el residuo va a la línea mayor |
| Gasto tardío | Se permite. La parte de unidades que **siguen** en el almacén revaloriza el inventario; la de unidades que ya salieron queda como **diferencia** |
| Moneda | BP-M3-001 opera en la moneda de valoración de la empresa. La compra en otra moneda llega con BP-M3-002 (tasas versionadas) |

## Decisión

1. **Dominio** (`internal/domain/purchase`): proveedores por organización;
   órdenes por sucursal (abierta → recibida o cancelada, con lo recibido por
   línea); recepciones por almacén (borrador → confirmada o cancelada, una
   línea por producto); gastos con prorrateo; costo de reposición.
2. **Confirmar una recepción** es una transacción:
   - prorratea los gastos del borrador;
   - registra por línea una entrada `purchase_in` de costo fijo (compra más
     gastos), que recalcula el CPP;
   - avanza la orden;
   - cierra el documento.

   Las líneas se bloquean en orden de producto y la orden antes que los
   saldos. Confirmar dos veces devuelve la recepción ya confirmada sin
   repetir nada; con varias confirmaciones simultáneas entra una sola, y la
   condición `status = 'draft'` del `UPDATE` decide cuál.
3. **Prorrateo:** `gasto × base ÷ total`, redondeado a 6 decimales. La base
   es el costo de compra de la línea o su cantidad. El residuo va a la línea
   de mayor base (la primera si empatan), así que la suma es exacta. Con
   costo total cero se rechaza el prorrateo por valor.
4. **Gasto tardío:** se prorratea entre las líneas. Lo que queda en el
   almacén se toma como `min(recibido, existencia actual)`. Esa proporción
   del gasto se capitaliza con un movimiento nuevo `revaluation`, que tiene
   cantidad cero y solo ajusta el valor; el resto queda en el prorrateo como
   `variance`. Exige `purchase.receipt.confirm`, porque cambia la valoración.

   Con CPP no se sabe qué unidades exactas salieron. La aproximación por
   existencia actual es explícita y queda registrada en cada línea del
   prorrateo.
5. **Existencia sin valorar:** una compra puede valorar la existencia
   anterior a M2-002 a su costo (`Costing.ValuesPrior`). Una transferencia o
   un reverso de costo fijo no pueden.
6. **Costos separados** (`purchase cost show`):
   - CPP por almacén (valoración);
   - último costo de compra, derivado de las recepciones confirmadas, con y
     sin gastos;
   - costo de reposición estimado, con historial de solo inserción.

   Ninguno sobrescribe a otro y ninguno cambia precios de venta.
7. **Permisos:**

   | Permiso | Qué permite | Plantillas |
   |---|---|---|
   | `purchase.read` | Ver compras | ADMIN_EMPRESA, ENCARGADO, ALMACENISTA |
   | `purchase.manage` | Borradores, órdenes y gastos; proveedores solo con ámbito de organización | ADMIN_EMPRESA, ENCARGADO, ALMACENISTA |
   | `purchase.receipt.confirm` | Confirmar recepciones y registrar gastos tardíos | ADMIN_EMPRESA, ENCARGADO |
   | `purchase.replacement_cost.manage` | Costo de reposición | ADMIN_EMPRESA, ENCARGADO |

   ADMIN_ORG los recibe todos.
8. **Base de datos** (migración 0008):
   - tablas de proveedores, órdenes, líneas, recepciones, líneas, gastos,
     prorrateos y costos de reposición;
   - disparadores que impiden cambiar documentos cerrados y líneas fuera de
     borrador;
   - gastos, prorrateos y costos de reposición son de solo inserción;
   - lo recibido de una orden solo crece y no supera lo pedido;
   - el kardex admite `purchase_in` y `revaluation`; una revaloración tiene
     cantidad cero, costo cero y ajuste positivo sobre existencia positiva.

   En SQLite se reconstruye otra vez el kardex. Las tablas que lo
   referencian se crean después, porque renombrar una tabla con las claves
   foráneas activas reescribe las referencias que otras tablas tienen hacia
   ella.

## Alternativas consideradas

- **Prorrateo por peso o volumen.** El catálogo no tiene esos atributos. Se
  agregarán con el criterio cuando existan.
- **Gastos solo en borrador.** Más simple, pero la sección 19.3 prevé gastos
  confirmados después de vender.
- **Revalorar con una salida y una entrada.** Mueve cantidad ficticia; la
  revaloración sin cantidad es más honesta en el kardex.
- **Costo de reposición derivado del último costo.** El documento lo define
  como estimación comercial con su propia fuente, así que se registra
  aparte.

## Consecuencias

- BP-M3-002 agregará moneda original, tasa y fuente a órdenes, recepciones y
  gastos, y convertirá a la moneda de valoración.
- BP-M3-003 usará el último costo y el costo de reposición para sugerir
  precios, sin publicarlos sin aprobación (DEC-007.P2).
- Quedan pendientes, por decisión del documento:
  - devoluciones a proveedor (19.4: política de variación de costo antes de
    automatizar);
  - impuestos recuperables (M4);
  - reverso de una recepción confirmada.

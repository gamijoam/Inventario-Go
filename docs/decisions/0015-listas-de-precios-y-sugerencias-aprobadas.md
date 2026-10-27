# ADR-0015: Listas de precios y sugerencias de precio aprobadas

- **Estado:** Aceptada
- **Fecha:** 2026-10-27
- **Decisiones relacionadas:** DEC-007.P2 (aprobada), DEC-007.B1, DEC-006.A/TZ, secciones 12.15, 16.6, 19.3 y 20.1; BP-M3-003

## Contexto

El criterio de BP-M3-003 es "el precio vigente no cambia antes de la
aprobación". DEC-007.P2 dice que, al recibir mercancía, BalanzaPro sugiere
precios y exige la aprobación de un usuario autorizado antes de publicar
cualquier cambio. Fija además estas reglas:

- la recepción y el cambio de precio son flujos separados;
- recibir no da facultad sobre las listas;
- el precio aprobado rige para las ventas futuras de toda la lista, sin
  importar cuándo entraron las unidades;
- las ventas pasadas conservan su precio;
- una aprobación obsoleta no pisa en silencio un precio ya cambiado.

## Parámetros ratificados por el usuario (2026-10-27)

| Tema | Decisión |
|---|---|
| Listas | **Por empresa**, cada una en una sola moneda (USD o VES), con un precio por producto |
| Fórmula | **Margen objetivo sobre venta**: `costo ÷ (1 − margen)`. El costo es el de reposición vigente o, si no hay, el último de compra. El margen va por lista, con excepción opcional por producto. Si el costo está en otra moneda, se convierte con la tasa vigente y queda guardada |
| Aprobación | Un permiso para proponer y otro para aprobar. Quien tenga los dos puede aprobar su propia propuesta, y queda auditado |
| Vigencia | Las sugerencias se generan al confirmar la recepción, en la misma transacción. El precio aprobado rige **ya o desde una fecha y hora de Caracas** |

## Decisión

1. **Dominio** (`internal/domain/pricing`):
   - `List`: empresa, código, nombre y moneda; la moneda no cambia.
   - `MarginChange`: margen de la lista o excepción de un producto. Es de
     solo inserción y rige el último; sin valor, quita el margen.
   - `Proposal`: pendiente → aprobada, rechazada o sustituida.
   - `Revision`: versión por lista y producto, con su vigencia. Una
     revisión publicada solo puede anularse mientras esté programada.

   `Suggest` redondea a la escala de la moneda, con la mitad alejándose de
   cero: 100 al 20 % da 125. `Current` elige el precio vigente: la revisión
   publicada de vigencia más reciente que ya rija y, con la misma vigencia,
   la de mayor versión.
2. **Recepción ≠ precio:** al confirmar, `SuggestWithin` deja en la misma
   transacción una propuesta por cada lista activa con margen y cada
   producto recibido, solo si el precio sugerido difiere del vigente. La
   nueva propuesta **sustituye** a la pendiente anterior: hay una sola
   pendiente por lista y producto, y la base lo garantiza con un índice
   único parcial.

   Si falta la tasa o el costo, la recepción no falla: la omisión se informa
   en la confirmación. Repetir la confirmación no repite sugerencias. Las
   propuestas manuales siguen el mismo flujo.
3. **Costo de referencia:** cada propuesta guarda:
   - de dónde sale el costo (reposición o último de compra);
   - el costo en su moneda;
   - la tasa usada y el costo convertido a la moneda de la lista;
   - el margen objetivo, el precio vigente al proponer y la versión base.

   Se ve así qué se aprobó y por qué.
4. **Aprobar:**
   - publica la revisión `versión base + 1`, ya o desde `--at`;
   - si el precio del producto cambió desde la propuesta, la rechaza con
     `ErrStale` (código 6) y no pisa nada (DEC-007.P2, `CONFLICTO_POR_VERSION`);
   - permite ajustar el precio;
   - si el precio queda **por debajo del costo de referencia**, exige además
     el permiso extraordinario `pricing.override.approve_below_cost`, que ya
     existía y no está en ninguna plantilla.

   Aprobar, rechazar o anular se bloquea por propuesta. Con aprobaciones
   simultáneas solo una publica.
5. **Permisos:**

   | Permiso | Qué permite | Plantillas |
   |---|---|---|
   | `pricing.price.read` | Ver listas, precios y propuestas | ADMIN_EMPRESA, ENCARGADO, ALMACENISTA, CAJERO |
   | `pricing.list.manage` | Crear listas y fijar márgenes, con **ámbito de empresa** | ADMIN_EMPRESA |
   | `pricing.price.propose` | Proponer precios; basta un ámbito dentro de la empresa | ADMIN_EMPRESA, ENCARGADO, ALMACENISTA |
   | `pricing.price.approve` | Aprobar, rechazar y anular, con **ámbito de empresa** | ADMIN_EMPRESA, ENCARGADO |

   ADMIN_ORG los recibe todos. Confirmar una recepción no exige ningún
   permiso de precios, porque solo deja propuestas.
6. **Base de datos** (migración 0010):
   - tablas `price_lists`, `price_margins`, `price_revisions` y
     `price_proposals`;
   - nada se borra; los márgenes son de solo inserción;
   - una lista conserva su empresa, código y moneda;
   - una revisión solo pasa de publicada a anulada, sin cambiar su precio,
     y solo antes de regir (CHECK `cancelled_at < effective_at`);
   - una propuesta se decide una vez, sin cambiar lo propuesto;
   - la revisión que publica una propuesta es de su misma lista y producto;
   - una sugerencia siempre viene de una recepción y de un margen.

## Alternativas consideradas

- **Listas por organización o por sucursal.** La primera mezcla razones
  sociales con monedas de valoración distintas. La segunda multiplica la
  administración.
- **Recargo sobre el CPP.** El CPP es valoración por almacén, no referencia
  comercial; la sección 19.3 usa el margen sobre la reposición.
- **Aprobación por un segundo usuario.** Bloquea al comercio pequeño. Queda
  como regla posible por empresa.
- **Reprecio automático preautorizado (modo 3).** No está aprobado.

## Consecuencias

- Las ventas de M4 resolverán el precio vigente con `Current` y guardarán en
  cada línea la lista, la revisión, la moneda y el precio aplicado.
- Quedan pendientes, por decisión del documento:
  - precio mínimo;
  - redondeo comercial (p. ej. a 0.05);
  - impuestos en la base del margen;
  - delegación desde la nube (DEC-006);
  - caducidad de propuestas;
  - listas por cliente o canal.

# ADR-0011: Costo promedio ponderado por almacén y transferencias

- **Estado:** Aceptada
- **Fecha:** 2026-10-27
- **Decisiones relacionadas:** DEC-007.E1, DEC-007.E2, DEC-013.P1, secciones 19, 21.1, 23.6, 24.7, 25.3 y 25.8; BP-M2-002

## Contexto

El criterio de BP-M2-002 es "conservación de valor, redondeos,
cero/negativo". DEC-007.E1/E2 aprobaron el **CPP móvil por producto y
almacén**. La sección 21.1 exige que una transferencia interna saque el
valor del origen sin utilidad y que el destino lo integre a su CPP, y que la
existencia cero no deje valor fantasma. La 25.3 pide conservar el valor
exacto del saldo y no redondear el CPP a 2 decimales.

## Parámetros ratificados por el usuario (2026-10-27)

| Tema | Decisión |
|---|---|
| Moneda de valoración | **Por empresa**, USD por defecto (USD o VES); se fija al crearla y no cambia |
| Costo de las entradas de ajuste | `--unit-cost` **opcional**; sin él, el **CPP vigente**. Obligatorio si el saldo no tiene CPP (carga inicial o existencia en cero) |
| Existencia cargada antes de esta versión | Queda **sin valorar** hasta que una entrada con costo la valore; `verify` la señala |
| Residuo al llegar a cero | **La salida que agota se lo lleva**: nunca queda valor con existencia cero |
| Transferencias | Solo **entre almacenes de la misma sucursal**; permiso nuevo `inventory.transfer.create` en ambos almacenes, en `ADMIN_ORG`, `ADMIN_EMPRESA`, `ENCARGADO` y `ALMACENISTA` |

## Decisión

1. **Se guarda el valor, no el CPP.** Cada saldo guarda su valor total
   (`value`, 6 decimales). El CPP es `valor ÷ existencia`, una derivación
   que se muestra con 12 decimales. Una salida cuesta
   `valor × cantidad ÷ existencia` redondeado a 6 decimales (mitad lejos de
   cero). La salida que agota la existencia cuesta exactamente el valor
   restante, así que el residuo de redondeo nunca sobrevive a la existencia
   y el valor se conserva sin pérdidas. En la práctica no hay un movimiento
   de "regularización": el redondeo de las salidas parciales ya está dentro
   del valor guardado.
2. **Entradas.** Con `--unit-cost` cuestan `cantidad × costo` (6 decimales).
   Sin él entran al CPP vigente y, si no lo hay, se rechazan con un mensaje
   que pide el costo: nunca se inventa costo cero. Un costo cero **declarado**
   es válido (mercancía recibida sin costo).
3. **Reversos.** Deshacen exactamente el costo del movimiento original, no
   el CPP actual: corregir un costo mal digitado no deja el error mezclado
   en el promedio. Si el reverso agota la existencia, se lleva el valor
   restante y la diferencia queda a la vista como **ajuste de valor**. Si no
   la agota y el valor quedaría negativo (el costo ya se consumió), se
   rechaza con conflicto.
4. **Transferencias** (`inventory_transfers` + dos movimientos
   `transfer_out`/`transfer_in` vinculados): la salida del origen se valora
   al CPP del origen y la entrada al destino recibe exactamente ese valor;
   el destino recalcula su CPP. Todo en una transacción. Ambos saldos se
   bloquean en un orden fijo (por ID de almacén) para que dos
   transferencias en sentidos opuestos no se bloqueen mutuamente; sin ese
   orden, PostgreSQL detectaba interbloqueos en la prueba de concurrencia.
   Una transferencia no se revierte por partes: se anula con otra en
   sentido contrario. Tiene su propia clave de idempotencia.
5. **Existencia sin valorar.** La migración deja los saldos y movimientos
   anteriores con valor `NULL`. Mientras tanto, las salidas siguen
   funcionando sin costo conocido, una entrada sin costo se rechaza y no se
   puede transferir desde ni hacia esa existencia. La primera entrada con
   costo valora también las unidades previas a ese costo y lo registra como
   ajuste de valor en el kardex. La existencia cero vale cero aunque nunca
   se haya valorado.
6. **Kardex valorado.** Cada movimiento guarda costo unitario, costo (con el
   signo de la cantidad), ajuste de valor y valor resultante. `Replay`
   reproduce también el valor y solo admite ajustes en sus dos casos
   legítimos: valorar existencia previa y agotar con costo fijo. `verify`
   comprueba además que cada transferencia saca y recibe la misma cantidad y
   el mismo valor, e informa la existencia sin valorar como aviso.
7. **Base de datos** (migración 0006, ambos motores):
   - `companies.valuation_currency` con `CHECK` y disparador que impide
     cambiarla.
   - `inventory_balances.value` con `CHECK`: no negativo y cero si no hay
     existencia.
   - El disparador de coincidencia exige ahora también el valor del saldo.
   - `inventory_transfers` de solo inserción. Sus dos almacenes se
     referencian con FK compuestas a la **misma** empresa y sucursal, así
     que la base impide una transferencia entre sucursales.
   - Índice único por transferencia y tipo: una salida y una entrada.
   - En SQLite, ampliar los `CHECK` del kardex exige reconstruir la tabla.
     Se hace dentro de la transacción de la migración con
     `legacy_alter_table`, para que la clave foránea a sí misma del kardex
     no apunte a la tabla vieja. `defer_foreign_keys` no sirve aquí: el
     contador de violaciones diferidas no se corrige al renombrar.
   - La bajada falla sin cambios si ya hay transferencias.
   - Importes: `TEXT` exacto en SQLite; `NUMERIC(28,6)` para valores y
     `NUMERIC(40,12)` para costo unitario en PostgreSQL.
8. **CLI.**
   - `inventory in --unit-cost`.
   - `inventory transfer`.
   - Columnas CPP, VALOR y MONEDA en `stock`; COSTO y VALOR en `kardex`.
   - `setup` y `company create` aceptan `--currency`.
   - El JSON lleva los importes exactos, o `null` si no se conocen; el
     texto los redondea a 2 decimales solo para mostrarlos.

## Alternativas consideradas

- **Guardar el CPP y multiplicar.** `cantidad × CPP_redondeado` deja
  residuos que se acumulan y exigen una regularización periódica; guardar el
  valor lo evita por construcción.
- **Movimiento de regularización aparte al llegar a cero.** Con valor
  guardado no hace falta: la salida que agota toma el resto exacto.
- **Valorar la existencia previa a costo cero en la migración.** El
  documento lo desaconseja (24.7): produce márgenes ficticios.
- **Reverso al CPP actual.** Más simple, pero deja el error de costo mezclado
  en el promedio.

## Consecuencias

- Las compras (M3) usarán el mismo mecanismo de entrada con costo; los
  gastos atribuibles y las devoluciones a proveedor se agregarán como
  costo de la entrada o ajustes de valor.
- La venta (M4) tomará el costo de la salida como snapshot del costo de
  ventas.
- La política de existencia negativa (BP-M2-003) deberá definir el costo
  provisional de las salidas en déficit (24.7). Hoy no hay negativos y
  ninguna salida se valora con existencia cero.
- Transferencias entre sucursales: flujo propio con despacho, tránsito y
  recepción (DEC-006), fuera de este ADR.

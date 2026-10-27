# ADR-0012: Reservas, bloqueos, conteos y existencia negativa por política

- **Estado:** Aceptada
- **Fecha:** 2026-10-27
- **Decisiones relacionadas:** DEC-007.F1, DEC-007.F2, secciones 23.2–23.9, 24.1–24.10; BP-M2-003

## Contexto

El criterio de BP-M2-003 es "disponibilidad coherente en concurrencia".

- DEC-007.F2 aprobó la existencia negativa **configurable por empresa y
  producto** (opción C), con negación por defecto.
- Las secciones 23.3 y 24.4–24.6 proponen reservas con estados, bloqueos
  por motivo y conteos con aprobación.
- Las pruebas de referencia son las de las tablas 23.8 (ST-01 a ST-15) y
  24.10 (NG, RS, BL, CT).

## Parámetros ratificados por el usuario (2026-10-27)

| Tema | Decisión |
|---|---|
| Costo de una salida en déficit | **Último CPP válido** del almacén. Si nunca lo hubo, se rechaza. La entrada siguiente liquida el déficit y deja la diferencia como ajuste de valor |
| Quién autoriza el negativo | Permiso **extraordinario** `inventory.negative_stock.override`, fuera de toda plantilla. Exige también la política de la empresa, la del producto y un límite. Solo aplica a salidas de ajuste |
| Vencimiento de reservas | **Opcional** por reserva, sin valor por defecto. Se liberan una sola vez, con `reservation expire` o antes de usar el saldo |
| Aprobación de conteos | La hace **otro usuario** con `inventory.count.approve`. Flujo borrador → en revisión → aplicado o rechazado |

## Decisión

### 1. Saldos

`available = on_hand − reserved − blocked`, con signo; no se normaliza a
cero.

- Reservar y bloquear cambian `reserved` y `blocked` sin avanzar la
  versión del saldo, que sigue siendo la secuencia del kardex.
- La base exige que, si la versión no cambia, tampoco cambien existencia ni
  valor.
- `inventory.Commit` impide comprometer más que lo libre y dejar lo
  reservado o bloqueado bajo cero.

### 2. Existencia negativa

Una salida puede dejar déficit solo si se cumplen todas estas condiciones:

- la pide con `--allow-negative`;
- el usuario tiene el permiso extraordinario en el almacén;
- la empresa la habilitó con un límite;
- el producto no la prohíbe (`inherit` sigue a la empresa, `deny`, o `allow`
  con límite propio; un producto nunca salta a la empresa);
- el déficit resultante no supera el límite;
- no hay unidades reservadas ni bloqueadas en el saldo (24.3 y BL-02).

Las transferencias, las reservas y los bloqueos nunca entran en negativo.

La política es un registro de solo inserción (`inventory_policy_changes`)
y la vigente es la más reciente. El movimiento en déficit guarda el ID de
la versión que lo autorizó (24.1, punto 5).

### 3. Costo del déficit (24.7)

Lo positivo sale con todo su valor y las unidades en déficit salen al último
CPP válido. El saldo queda con valor negativo: el costo provisional
adeudado.

La entrada siguiente cubre primero el déficit. Su costo real reemplaza el
provisional y la diferencia queda en el kardex como `value_adjustment`. El
resto entra a su costo.

No hay división entre existencia cero o negativa (NG-09). El último CPP
válido no se guarda: el repositorio lo deriva del kardex, del último
movimiento que dejó existencia positiva valorada. `Replay` verifica tanto
el costo provisional como la liquidación.

### 4. Reservas

Estados: activa → despachada, liberada o vencida.

- **Crear:** solo sobre lo libre.
- **Despachar:** baja la existencia y lo reservado en la misma transacción,
  con una salida valorada (`reservation_out`) y sin descontar dos veces el
  disponible (ST-04).
- **Liberar o vencer:** devuelve lo pendiente al disponible sin tocar el
  kardex.
- **Vencimiento:** se guarda como instante UTC. Al bloquear un saldo para
  cualquier operación o consulta se liberan sus reservas vencidas, y
  `reservation expire` lo hace en lote. La reserva se actualiza con
  `WHERE status = 'active'` y el saldo está bloqueado, así que vencer y
  liberar a la vez liberan una sola vez (RS-03).

### 5. Bloqueos

Motivos: damage, quarantine, quality, administrative, investigation.

- Bloquear baja el disponible sin salida (ST-07).
- Liberar lo devuelve sin compra artificial (ST-08 y BL-01).
- Dar de baja es una merma: una salida separada que exige además
  `inventory.stock.adjust` y queda vinculada al bloqueo.

### 6. Conteos

Al capturar, cada línea guarda lo contado, la existencia registrada y la
versión del saldo.

- **Aprobar** aplica en una transacción un ajuste por producto
  (`count_in` o `count_out`) con la diferencia sobre el saldo actual, así
  que lo movido después de contar se respeta (CT-02). Tiene que aprobar un
  usuario distinto de quien contó o envió el conteo; la base también exige
  `decided_by ≠ submitted_by`.
- **Sobrantes:** entran al CPP vigente; si no lo hay, al último costo
  válido; y si tampoco, al costo capturado en la línea.
- **Faltantes:** un conteo puede dejar sin respaldo lo reservado o
  bloqueado ("reservas en riesgo", 24.3), pero nunca deja existencia
  negativa.
- **Déficit:** contar 0 sobre −3 es un ajuste de conteo, no una compra
  (CT-03).

### 7. `verify`

- Comprueba que lo reservado y lo bloqueado de cada saldo sumen sus
  reservas y bloqueos activos.
- Informa como avisos los déficits y las reservas en riesgo.

### 8. Base de datos (migración 0007)

**Tablas nuevas:**

- `inventory_policy_changes`;
- `inventory_reservations`;
- `inventory_blocks`;
- `inventory_counts` e `inventory_count_lines`.

**Disparadores:**

- reservas y bloqueos solo cambian mientras están activos;
- los conteos solo avanzan de estado;
- las líneas de conteo solo cambian en borrador;
- el historial de políticas es inmutable.

**Kardex:**

- tipos nuevos: `reservation_out`, `count_in` y `count_out`;
- referencias al documento de origen: reserva, bloqueo, conteo y política;
- índices únicos: una merma por bloqueo y un ajuste por producto y conteo.

**Reconstrucción en SQLite.** Los `CHECK` de saldos y kardex se
reconstruyen. Con las claves foráneas activas, renombrar una tabla
reescribe las referencias que otras tablas tienen hacia ella. Por eso se
renombran primero las dos tablas viejas y se borran al final, del kardex
hacia los saldos.

**Reconstrucción en PostgreSQL.** El `CHECK` sin nombre de la 0006 se busca
por su definición.

## Alternativas consideradas

- **Costo provisional declarado por quien autoriza.** Abre la puerta a
  costos arbitrarios.
- **Permiso de negativos en plantillas.** El riesgo contable (24.7) pide
  una concesión expresa.
- **TTL predeterminado para reservas.** El documento pide no inventarlo.
- **Bloquear el almacén durante el conteo.** Se prefirió la captura con
  existencia de referencia y la conciliación por diferencia (24.6, opción b),
  que no detiene la operación.
- **Guardar el último CPP en el saldo.** Obliga a calcularlo en la
  migración con aritmética de cada motor; derivarlo del kardex evita esa
  aproximación.

## Consecuencias

- M4 (ventas) creará reservas desde pedidos y usará `reservation_out` en el
  despacho. Las ventas sin existencia (backorder, 24.2) serán un flujo
  aparte, no una salida negativa.
- La excepción de negativos se evaluará operación por operación cuando
  existan ventas (24.1, punto pendiente).
- El vencimiento automático depende de que algo use el saldo o de una tarea
  programada (`reservation expire`). Un worker propio llegará con el
  servidor (M5).

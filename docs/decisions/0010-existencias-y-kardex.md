# ADR-0010: Existencias y kardex inmutable

- **Estado:** Aceptada
- **Fecha:** 2026-10-27
- **Decisiones relacionadas:** DEC-007.F, DEC-007.F2, secciones 23.2–23.6, 24.3, 24.8 y 25.6; BP-M2-001

## Contexto

El criterio de BP-M2-001 es "igualdad movimientos/saldo y replay
determinista". La sección 23.5 exige que dos cajas no vendan la misma
unidad y la 24.8, que la base proteja la integridad aunque la interfaz se
equivoque.

## Parámetros ratificados por el usuario (2026-10-27)

| Tema | Decisión |
|---|---|
| Ajustes manuales | En **un paso**: permiso + motivo; se corrigen solo con reverso |
| Permisos en plantillas | Ver y ajustar: `ADMIN_ORG`, `ADMIN_EMPRESA`, `ENCARGADO`, `ALMACENISTA`. Solo ver: `CAJERO`, `OPERADOR_TECNICO` |
| Motivos | **Catálogo fijo** (`initial_load`, `correction`, `shrinkage`, `internal_use`, `other`) + nota libre |

## Decisión

1. **Dominio** (`internal/domain/inventory`): `Balance` (física, reservada,
   bloqueada, versión) con `Available()` firmado; `Movement` con secuencia por
   saldo y existencia resultante; `Apply` (rechaza salidas que superen el
   disponible) y `Replay`, que reconstruye el saldo desde el kardex y detecta
   huecos, ediciones, borrados y saldos alterados.
2. **Kardex como fuente de verdad**: cada movimiento guarda `sequence` y
   `on_hand_after`. El saldo es una proyección. Los errores se corrigen con
   un **reverso** (movimiento opuesto que referencia al original; único por
   movimiento, y un reverso no se revierte).
3. **Concurrencia**: cada escritura lee el saldo con bloqueo (`SELECT … FOR
   UPDATE` en PostgreSQL; en SQLite la transacción ya es `BEGIN IMMEDIATE`),
   aplica el movimiento y actualiza con `WHERE version = N`. Si otro proceso
   creó el saldo primero, la operación se repite (hasta 8 veces). Probado con
   16 salidas simultáneas sobre 10 unidades: exactamente 10 confirmadas.
4. **Idempotencia**: `--key` opcional, única por organización, con huella
   SHA-256 de los datos. Misma clave y datos: se devuelve el movimiento
   original sin aplicar nada; datos distintos: conflicto.
5. **Base de datos** (migración 0005, ambos motores):
   - Kardex de solo inserción (triggers contra UPDATE, DELETE y TRUNCATE).
   - Un trigger exige que cada movimiento nuevo coincida con la versión y
     la existencia del saldo, y otro que el saldo avance exactamente una
     versión por cambio; los saldos no se borran.
   - FK compuestas al almacén (con su empresa y sucursal), al producto, al
     usuario y al movimiento revertido; `CHECK` de signo según el tipo.
   - Cantidades: `TEXT` decimal exacto en SQLite (Go hace la aritmética) y
     `NUMERIC(28,6)` en PostgreSQL (6 = escala máxima de una unidad).
6. **Inventario negativo**: DEC-007.F2 aprobó una política por empresa y
   producto, con negación por defecto. Hasta BP-M2-003 rige ese valor por
   defecto: ninguna salida deja el disponible bajo cero. No hay `CHECK`
   `on_hand >= 0` en la base para no impedir la excepción futura (24.8).
7. **Permisos**: `inventory.stock.read` e `inventory.stock.adjust`, evaluados
   en el ámbito del almacén. Un almacén que el usuario no ve responde "no
   encontrado"; uno que ve sin el permiso, "denegado". `inventory verify`
   exige lectura en toda la organización.
8. **CLI**: `inventory in|out|reverse|stock|kardex|verify|reasons`; código de
   salida nuevo **10** para existencia insuficiente.

## Alternativas consideradas

- **Solo control optimista por versión**: con 16 escrituras simultáneas en
  PostgreSQL se agotaban los reintentos; el bloqueo de fila las ordena.
- **Saldo calculado al vuelo sin tabla de saldos**: más simple, pero lento
  con kardex largos y sin punto de bloqueo para la concurrencia (23.2).
- **Editar movimientos con auditoría**: contradice "nunca reescribir un
  kardex anterior" (23.6).

## Consecuencias

- BP-M2-002 agregó costo a los movimientos (CPP por almacén) y
  transferencias (ADR-0011); BP-M2-003 agregará reservas, bloqueos, conteos
  y la política de negativos, usando las columnas `reserved` y `blocked` ya
  existentes.
- `doctor` todavía no ejecuta `inventory verify`; pendiente para cuando el
  diagnóstico tenga sesión o un modo técnico sin datos de negocio.

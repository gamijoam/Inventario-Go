# ADR-0003: Identificadores globales UUIDv7 y ámbito de negocio

- **Estado:** Aceptada
- **Fecha:** 2026-10-26
- **Decisiones relacionadas:** DEC-008.A, DEC-006, sección 25.5, sección 28.2

## Contexto

Las sucursales operan sin Internet y sincronizan con la nube; los IDs no
pueden depender de autoincrementales de SQLite/PostgreSQL. Toda operación
sensible debe llevar su ámbito organización → empresa → sucursal → almacén.

## Decisión

1. **Formato:** UUIDv7 (RFC 9562), generado en Go por `internal/domain/id`,
   sin dependencias externas (bastan `crypto/rand` y `time`).
2. **Orden:** el generador es estrictamente creciente dentro de un proceso:
   contador de 12 bits en el mismo milisegundo y avance del milisegundo
   lógico si el reloj retrocede o el contador se agota.
3. **Texto:** forma canónica en minúsculas `8-4-4-4-12`; `Parse` acepta
   mayúsculas y otras versiones RFC 9562 (p. ej. v4 de terceros) y rechaza
   el UUID nulo, que significa "sin asignar".
4. **Ámbito:** `scope.BusinessScope` contiene la ruta de IDs sin huecos ni
   repeticiones. `Contains` decide si un permiso concedido en un nivel cubre
   una operación en un descendiente; `Require` exige un nivel mínimo (p. ej.
   almacén para un movimiento de stock).
5. Los IDs técnicos no sustituyen identificadores comerciales (SKU, código de
   barras, RIF, número de factura).

## Consecuencias

- El orden entre IDs de distintos procesos o nodos solo es aproximado (por
  milisegundo); el orden de eventos de sincronización se definirá con cursor
  y versión propios (DEC-006), no con el UUID.
- `BusinessScope` no prueba la pertenencia real (sucursal ∈ empresa): la
  garantizan las claves compuestas y RLS en los adaptadores (M1).
- El usuario y la caja forman parte del contexto de operación de la capa de
  aplicación; se modelan con identidad y permisos en M1.
- Persistencia: `UUID` en PostgreSQL y `TEXT` canónico en SQLite (BP-M0-004).

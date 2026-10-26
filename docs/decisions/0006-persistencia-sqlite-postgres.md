# ADR-0006: Drivers, conexión y migraciones para SQLite y PostgreSQL

- **Estado:** Aceptada
- **Fecha:** 2026-10-26
- **Decisiones relacionadas:** DEC-004, DEC-004.DB, DEC-013.P1, secciones 10, 25.6, 25.9 y 28.5 (pasos 7 y 9)

## Contexto

DEC-004 exige SQLite en una PC y PostgreSQL en multicaja y nube, con dos
conjuntos de migraciones y pruebas contra ambos motores. Los binarios se
compilan para Linux y Windows desde Linux con `CGO_ENABLED=0` (ADR-0001).

## Decisión

1. **Driver SQLite:** `modernc.org/sqlite` v1.60.1, en Go puro. Permite la
   compilación cruzada sin CGO ni compilador C. Se descartó
   `github.com/mattn/go-sqlite3` (CGO) por complicar la compilación para
   Windows; no se midió su rendimiento.
2. **Driver PostgreSQL:** `github.com/jackc/pgx/v5` v5.11.0 vía `database/sql`
   (`stdlib`), para compartir el migrador con SQLite. Su dependencia
   `golang.org/x/text` se fija en v0.42.0: la v0.29.0 que trae por defecto
   tiene avisos alcanzables (GO-2026-6629, GO-2026-5970).
3. **SQLite, cada conexión:** `busy_timeout=5000`, `foreign_keys=ON`,
   `synchronous=FULL` y `BEGIN IMMEDIATE` en transacciones
   (`_txlock=immediate`). `journal_mode=WAL` es persistente en el archivo y
   se activa una vez al abrir, reintentando ante `SQLITE_BUSY`: el cambio de
   modo no respeta `busy_timeout`, y varias aperturas simultáneas de una
   base nueva fallaban (regresión detectada en CI y cubierta por prueba).
   Se verifica al abrir que todo quedó activo. Archivo con permisos 0600 y
   directorio con 0700.
4. **PostgreSQL, cada sesión:** `timezone=UTC`; tiempo de conexión de 5 s por
   defecto. Los errores no incluyen la contraseña y se reducen a una línea.
5. **Migraciones propias** (`internal/platform/migration`), sin bibliotecas:
   SQL nativo por motor en `migrations/sqlite` y `migrations/postgres` con la
   misma numeración (`NNNN_nombre.up.sql` y `.down.sql` opcional), embebidas
   en el ejecutable. Cada migración se aplica en su propia transacción con su
   registro en `schema_migrations` (versión, nombre, checksum SHA-256, fecha).
   Se serializa con `BEGIN IMMEDIATE` (SQLite) o `pg_advisory_xact_lock`
   (PostgreSQL) y se vuelve a comprobar dentro de la transacción. Se rechaza
   una base con migraciones modificadas o más nueva que el ejecutable.
6. **Equivalencia:** una prueba compara tablas, columnas, nulabilidad, PK,
   FK (con regla `ON DELETE`) e índices de ambos motores tras migrar. Los
   tipos físicos difieren a propósito (sección 25.6): `UUID`/`TEXT`
   canónico, `TIMESTAMPTZ`/`TEXT` RFC 3339 UTC con `CHECK`, `BOOLEAN`/`INTEGER`.
   Las tablas SQLite son `STRICT`.
7. **Migración 0001:** `installation` (identidad única del nodo, UUIDv7
   generado en Go) e `installation_settings` (ajustes locales no secretos).
   Sin entidades de negocio todavía.
8. **Respaldo previo:** `db migrate` copia la base SQLite con `VACUUM INTO`
   antes de aplicar migraciones sobre una base que ya tenía alguna. En
   PostgreSQL el respaldo previo corresponde a las herramientas del servidor.
9. **CLI:** `db status` (no crea ni modifica) y `db migrate`. `doctor` agrega
   la verificación `database`. Código de salida `5` si no hay conexión.
   La reversión (`Down`) existe y se prueba, pero no se expone en la CLI.
10. **Pruebas contra motores reales:** las suites de comportamiento corren en
    SQLite y PostgreSQL (`BALANZAPRO_TEST_POSTGRES_URL`); en la CI de Linux
    PostgreSQL 16 es obligatorio (`BALANZAPRO_REQUIRE_POSTGRES=1`).

## Mediciones de referencia

`go test -run '^$' -bench . ./internal/adapters/persistence/sqlite/`, en un
contenedor de 4 vCPU (Intel Xeon 2.1 GHz). El disco del contenedor puede
sincronizar más rápido que el hardware económico de una tienda: repetir en
el equipo objetivo antes del piloto.

| Prueba | Resultado |
|---|---|
| 1000 inserciones en una transacción | 6.0 ms (≈ 6 µs por fila) |
| Una transacción durable por operación (`synchronous=FULL`) | 0.20 ms |
| Búsqueda por índice único en 50 000 filas | 11 µs |

## Consecuencias

- Mantener dos directorios de migraciones; la prueba de equivalencia y la
  de consistencia de numeración detectan divergencias.
- En Windows la CI no tiene PostgreSQL (los servicios de contenedor de
  GitHub no funcionan en Windows): ahí se prueba SQLite.
- Pendiente: medir en hardware objetivo; respaldo/restauración completos y
  política de retención (DEC-009.BACKUP, M5).

# ADR-0001: Repositorio, módulo Go, licencia y estructura

- **Estado:** Aceptada
- **Fecha:** 2026-10-26
- **Decisiones relacionadas:** DEC-001, DEC-005.F, sección 28 del documento maestro

## Contexto

V1.CLI está aprobada (M0–M6). Hace falta un repositorio único que contenga
dominio, adaptadores SQLite/PostgreSQL, CLI/TUI, servicios y migraciones.

## Decisión

1. **Repositorio:** `gamijoam/Inventario-Go`, monorepo.
2. **Módulo Go:** `github.com/gamijoam/inventario-go` (en minúsculas; GitHub
   resuelve el nombre sin distinguir mayúsculas).
3. **Licencia:** privada, todos los derechos reservados (`LICENSE`).
4. **Estructura:** el mapa de la sección 28.4 (`cmd/`, `internal/domain`,
   `internal/application`, `internal/adapters`, `internal/platform`,
   `migrations/`, `contracts/`, `tests/`, `docs/`). Cada paquete se crea cuando
   un ticket lo necesita.
5. **Regla de dependencias:** `internal/domain` solo importa la biblioteca
   estándar, la biblioteca decimal aprobada (ADR-0002) y paquetes del propio
   dominio. Se verifica en CI con `scripts/check-domain-deps.sh`.
6. **Versión de Go:** fijada en `go.mod` (1.24.7 al iniciar; 1.27.3 desde
   BP-M0-002, requerida por Charm v2). CI usa la misma
   mediante `go-version-file`. Actualizar la versión es un cambio explícito por PR.
7. **Calidad:** CI con gofmt, `go mod tidy`, vet (Linux y Windows), pruebas en
   Linux y Windows, `-race` en Linux, compilación cruzada con `CGO_ENABLED=0`
   y `govulncheck` en versión fijada.

## Consecuencias

- La compilación con `CGO_ENABLED=0` condiciona la elección del driver SQLite
  (ADR pendiente en BP-M0-004): favorece un driver en Go puro.
- Con Go 1.24.7, `govulncheck` reportaba avisos de la biblioteca estándar;
  con Go 1.27.3 y `govulncheck` v1.9.0 no hay hallazgos.

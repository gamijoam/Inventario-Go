# BalanzaPro

Sistema de inventario y operación comercial para Venezuela: backend modular en
Go, operado primero por CLI y TUI, con SQLite para una PC y PostgreSQL para
multicaja y nube. El alcance aprobado es **V1.CLI**, entregado por hitos M0–M6
(ver `BalanzaPro_Documento_Maestro_Go_v2.2`, sección 28).

> **Estado:** M0 en curso — tipos base del dominio (`BP-M0-001`). No apto para producción.

## Qué contiene hoy

| Paquete | Responsabilidad |
|---|---|
| `internal/domain/decimal` | Decimal exacto; parseo canónico; redondeo y división con política explícita |
| `internal/domain/money` | `Money` y `Currency` (USD, VES); prohíbe mezclar monedas; `ExchangeRate` y conversión con instantánea auditable |
| `internal/domain/quantity` | `Quantity` y `Unit`; precisión por unidad, sin mezclar unidades |
| `internal/domain/id` | Identificadores globales UUIDv7 ordenables, generados en Go |
| `internal/domain/scope` | `BusinessScope`: organización → empresa → sucursal → almacén |
| `internal/domain/timezone` | Presentación en `America/Caracas`, persistencia en UTC |

El dominio no importa CLI, UI, drivers SQL ni HTTP; `scripts/check-domain-deps.sh`
lo verifica en CI.

## Requisitos

- Go (versión fijada en `go.mod`).
- Linux para desarrollo; los binarios se compilan también para Windows.

## Uso

```sh
go test ./...                  # pruebas
go test -race ./...            # detector de carreras (Linux)
go vet ./...
gofmt -l .                     # debe no listar nada
./scripts/check-domain-deps.sh # aislamiento del dominio
GOOS=windows go build ./...    # compilación cruzada
```

En PowerShell:

```powershell
go test ./...
$env:GOOS="windows"; go build ./...
```

## Estructura

Se sigue el mapa de la sección 28.4 del documento maestro, **creando cada
paquete cuando el hito lo necesita**; no hay carpetas vacías por anticipado.

```text
internal/domain/   reglas puras: dinero, tasas, cantidades, IDs, ámbitos, zona horaria
docs/decisions/    registro de decisiones técnicas (ADR)
scripts/           verificaciones de CI
.github/workflows/ CI
```

## Documentación

- [Contribuir](CONTRIBUTING.md): ramas, commits, TDD y puerta de calidad.
- [Decisiones técnicas (ADR)](docs/decisions/README.md).

## Licencia

Software privado. Todos los derechos reservados; ver [LICENSE](LICENSE).

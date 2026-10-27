# BalanzaPro

Sistema de inventario y operación comercial para Venezuela: backend modular en
Go, operado primero por CLI y TUI, con SQLite para una PC y PostgreSQL para
multicaja y nube. El alcance aprobado es **V1.CLI**, entregado por hitos M0–M6
(ver `BalanzaPro_Documento_Maestro_Go_v2.2`, sección 28).

> **Estado:** M0 completo (salvo la prerelease), M1 y M2 completos; M3 en curso — compras, recepciones y gastos (`BP-M3-001`); tasas de cambio versionadas y compras en otra moneda (`BP-M3-002`). M1: organización, empresas, sucursales y almacenes (`BP-M1-001`); usuarios y permisos por ámbito (`BP-M1-002`); catálogo de productos y unidades (`BP-M1-003`). M2: existencias y kardex (`BP-M2-001`); costo promedio por almacén y transferencias (`BP-M2-002`); reservas, bloqueos, conteos y existencia negativa por política (`BP-M2-003`). No apto para producción.

## Qué contiene hoy

| Paquete | Responsabilidad |
|---|---|
| `internal/domain/decimal` | Decimal exacto; parseo canónico; redondeo y división con política explícita |
| `internal/domain/money` | `Money` y `Currency` (USD, VES); prohíbe mezclar monedas; `ExchangeRate` y conversión con instantánea auditable |
| `internal/domain/quantity` | `Quantity` y `Unit`; precisión por unidad, sin mezclar unidades |
| `internal/domain/id` | Identificadores globales UUIDv7 ordenables, generados en Go |
| `internal/domain/scope` | `BusinessScope`: organización → empresa → sucursal → almacén |
| `internal/domain/timezone` | Presentación en `America/Caracas`, persistencia en UTC |
| `internal/domain/org` | Organización, empresa legal (RIF), sucursal y almacén |
| `internal/application/admin` | Caso de uso `doctor`: verificaciones del entorno |
| `internal/application/org` | Alta inicial y gestión de la jerarquía con control de ámbito |
| `internal/adapters/cli` | Comandos Cobra, `--json`, códigos de salida |
| `internal/adapters/tui` | Interfaz interactiva Bubble Tea v2 / Lip Gloss v2 |
| `internal/platform/buildinfo` | Versión, commit y fecha de compilación |
| `internal/platform/config` | Configuración TOML + `BALANZAPRO_*`, secretos redactados |
| `internal/platform/logging` | Logs `slog` con redacción de secretos |
| `internal/platform/migration` | Migrador SQL versionado, transaccional y concurrente |
| `internal/adapters/persistence` | Conexión SQLite (modernc) y PostgreSQL (pgx), identidad de la instalación |
| `migrations/` | SQL nativo por motor (`sqlite/`, `postgres/`), embebido en el ejecutable |
| `cmd/balanzapro` | Ejecutable: conecta la CLI/TUI con el proceso |

El dominio no importa CLI, UI, drivers SQL ni HTTP, y `application` no importa
adaptadores; `scripts/check-domain-deps.sh` lo verifica en CI.

## Requisitos

- Go (versión fijada en `go.mod`).
- Linux para desarrollo; los binarios se compilan también para Windows.

## Ejecutable `balanzapro`

```sh
go build -o bin/balanzapro ./cmd/balanzapro   # en Windows: bin/balanzapro.exe

balanzapro                  # TUI interactiva (requiere terminal)
balanzapro version [--json]
balanzapro doctor [--json]  # diagnóstico sin credenciales
balanzapro config show [--json]
balanzapro config path
balanzapro db status [--json]     # no crea ni modifica la base
balanzapro db migrate [--json]    # crea la base y aplica migraciones
balanzapro setup --name "Bodega Ana" [--legal-name ...] [--rif J-12345678-9] [--currency USD|VES]   # pide la contraseña del administrador
balanzapro auth login|logout|whoami|passwd
balanzapro user list|create|assign|unassign|disable|enable|unlock|reset-password
balanzapro role list|permissions|create|edit
balanzapro unit list|create
balanzapro product create|list|show|update
balanzapro inventory in|out|reverse|transfer|stock|kardex|verify|reasons
balanzapro inventory reservation|block|count|policy ...
balanzapro purchase supplier|order|receipt|cost ...
balanzapro rate set|show|list
balanzapro org list
balanzapro company list|create
balanzapro branch list|create
balanzapro warehouse list|create
balanzapro completion bash|powershell
balanzapro --help
```

Flags globales: `--json` (salida estable para scripts, errores JSON en stderr),
`--no-color` (también con la variable `NO_COLOR`), `--config <archivo>` y
`--log-level <nivel>`. Ver [configuración](docs/user-guide/configuracion.md).

| Código de salida | Significado |
|---|---|
| 0 | Correcto (doctor puede tener advertencias) |
| 1 | Fallo no clasificado |
| 2 | Uso inválido: comando, argumento o flag; no se hizo nada |
| 3 | `doctor` encontró al menos un fallo |
| 4 | Configuración inválida |
| 5 | Base de datos no disponible |
| 6 | Conflicto: el recurso ya existe |
| 7 | No encontrado, o instalación sin configurar (`balanzapro setup`) |
| 8 | Permiso denegado para esa operación en ese ámbito |
| 9 | Sin sesión válida (`balanzapro auth login`) o credenciales incorrectas |
| 10 | Existencia disponible insuficiente para la salida |

Para fijar la versión al compilar:

```sh
go build -ldflags "-X github.com/gamijoam/inventario-go/internal/platform/buildinfo.Version=v0.1.0" \
  -o bin/balanzapro ./cmd/balanzapro
```

## Desarrollo

```sh
go test ./...                  # pruebas
go test -race ./...            # detector de carreras (Linux)
go vet ./...
gofmt -l .                     # debe no listar nada
./scripts/check-domain-deps.sh # reglas de dependencias entre capas
./scripts/smoke.sh             # prueba de humo del ejecutable real
GOOS=windows go build ./...    # compilación cruzada
```

Pruebas contra PostgreSQL (se omiten si no hay servidor):

```sh
export BALANZAPRO_TEST_POSTGRES_URL='postgres://usuario:clave@127.0.0.1:5432/postgres?sslmode=disable'
go test ./...   # crea y elimina bases bp_test_* (el usuario necesita CREATEDB)
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
cmd/balanzapro/       ejecutable CLI/TUI
migrations/           SQL versionado por motor
internal/domain/      reglas puras: dinero, tasas, cantidades, IDs, ámbitos, zona horaria
internal/application/ casos de uso
internal/adapters/    CLI, TUI y persistencia
internal/platform/    infraestructura transversal
docs/decisions/       registro de decisiones técnicas (ADR)
scripts/              verificaciones de CI
.github/workflows/    CI
```

## Documentación

- [Primeros pasos](docs/user-guide/primeros-pasos.md): instalación, `setup` y estructura del negocio.
- [Usuarios y permisos](docs/user-guide/usuarios-y-permisos.md): sesión, usuarios, roles y ámbitos.
- [Catálogo](docs/user-guide/catalogo.md): productos, SKU, códigos de barras y unidades.
- [Inventario](docs/user-guide/inventario.md): existencias, ajustes, reversos, kardex, costo promedio, transferencias, reservas, bloqueos, conteos y existencia negativa.
- [Compras](docs/user-guide/compras.md): proveedores, órdenes, recepciones, gastos atribuibles y costos.
- [Tasas de cambio](docs/user-guide/tasas.md): registro, vigencia, correcciones y compras en otra moneda.
- [Configuración](docs/user-guide/configuracion.md): archivo, variables y logs.
- [Contribuir](CONTRIBUTING.md): ramas, commits, TDD y puerta de calidad.
- [Decisiones técnicas (ADR)](docs/decisions/README.md).

## Licencia

Software privado. Todos los derechos reservados; ver [LICENSE](LICENSE).

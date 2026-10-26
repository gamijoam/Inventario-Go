#!/usr/bin/env bash
# Prueba de humo del ejecutable real (Linux y Windows con Git Bash).
# Opcional: BALANZAPRO_SMOKE_POSTGRES_URL para probar también PostgreSQL.
set -euo pipefail

work="$(mktemp -d)"
bin="$work/balanzapro$(go env GOEXE)"
go build -o "$bin" ./cmd/balanzapro

# Aislar del entorno del usuario: configuración y base en un directorio temporal.
export BALANZAPRO_CONFIG="$work/config.toml"
export BALANZAPRO_SQLITE_PATH="$work/datos/bp.db"
: > "$BALANZAPRO_CONFIG"

"$bin" version --json
"$bin" config show --json
"$bin" doctor --json
"$bin" db status --json
"$bin" db migrate --json
"$bin" db migrate --json   # idempotente
"$bin" db status --json
"$bin" setup --name "Panadería Ñandú" --rif J-12345678-9 --json
"$bin" company create --code norte --legal-name "Ñandú Norte, C.A." --json
"$bin" branch create --company norte --code centro --name Centro --json
"$bin" warehouse create --company norte --branch centro --code principal --name Principal --json
"$bin" org list --json
"$bin" warehouse list
# Un segundo setup debe rechazarse con el código 6 (conflicto).
set +e; "$bin" setup --name Otra >/dev/null 2>&1; code=$?; set -e
test "$code" -eq 6
"$bin" doctor

# Sin terminal, el comando sin subcomando no abre la TUI: código 2.
set +e; "$bin" < /dev/null; code=$?; set -e
test "$code" -eq 2

if [ -n "${BALANZAPRO_SMOKE_POSTGRES_URL:-}" ]; then
  export BALANZAPRO_DB_DRIVER=postgres BALANZAPRO_POSTGRES_URL="$BALANZAPRO_SMOKE_POSTGRES_URL"
  "$bin" db migrate --json
  # La base de humo puede venir de una ejecución anterior ya configurada.
  set +e; "$bin" setup --name "Bodega PG" >/dev/null 2>&1; code=$?; set -e
  test "$code" -eq 0 -o "$code" -eq 6
  "$bin" branch list --json
  "$bin" doctor
fi
echo "prueba de humo correcta"

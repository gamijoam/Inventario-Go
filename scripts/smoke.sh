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
export BALANZAPRO_SESSION_FILE="$work/estado/session"
pw="cafe con leche"
: > "$BALANZAPRO_CONFIG"

"$bin" version --json
"$bin" config show --json
"$bin" doctor --json
"$bin" db status --json
"$bin" db migrate --json
"$bin" db migrate --json   # idempotente
"$bin" db status --json
printf '%s\n' "$pw" | "$bin" setup --name "Panadería Ñandú" --rif J-12345678-9 --password-stdin --json
"$bin" auth whoami --json
"$bin" company create --code norte --legal-name "Ñandú Norte, C.A." --json
"$bin" branch create --company norte --code centro --name Centro --json
"$bin" warehouse create --company norte --branch centro --code principal --name Principal --json
"$bin" org list --json
"$bin" warehouse list
printf '%s\n' "sol y luna 2026" | "$bin" user create --username cajero --name "Cajero Uno" --password-stdin --json
"$bin" user assign --username cajero --role CAJERO --company PRINCIPAL --branch PRINCIPAL --json
"$bin" role list
"$bin" unit list
"$bin" product create --name "Harina de maíz 1 kg" --barcode 7591234567894 --json
"$bin" product create --name "Queso blanco" --sku QUE-01 --unit KG --json
"$bin" product update P-000001 --sku HAR-1 --revision 1 --json
"$bin" product show 07591234567894
"$bin" product list --search harina
"$bin" inventory in --product HAR-1 --qty 40 --unit-cost 1.25 --reason initial_load --key carga-1 --json
"$bin" inventory in --product HAR-1 --qty 40 --unit-cost 1.25 --reason initial_load --key carga-1 --json   # idempotente
"$bin" inventory in --product HAR-1 --qty 10 --reason correction   # al costo promedio vigente
"$bin" inventory out --product QUE-01 --qty 1 --reason correction >/dev/null 2>&1 && exit 1 || true
"$bin" inventory in --product QUE-01 --qty 2.5 --unit-cost 6.4 --reason initial_load
"$bin" inventory out --product QUE-01 --qty 0.125 --reason shrinkage --note "rotura"
"$bin" inventory stock
"$bin" inventory kardex --product QUE-01 --json
"$bin" inventory verify
# Transferencia entre almacenes de la sucursal, idempotente y con valor conservado.
"$bin" warehouse create --code deposito --name "Depósito" --json
"$bin" inventory transfer --from principal --to deposito --product HAR-1 --qty 10 --key tr-1 --json
"$bin" inventory transfer --from principal --to deposito --product HAR-1 --qty 10 --key tr-1 --json   # idempotente
"$bin" inventory stock --product HAR-1
"$bin" inventory kardex --warehouse deposito --product HAR-1
"$bin" inventory verify
# Reservas, bloqueos, conteo y política de existencia negativa (BP-M2-003).
res=$("$bin" inventory reservation create --warehouse principal --product HAR-1 --qty 5 --reference PED-1 --key res-1 --json)
res_id=$(printf '%s' "$res" | sed -n 's/.*"id": "\([^"]*\)".*/\1/p' | head -n1)
"$bin" inventory reservation consume "$res_id" --qty 2
"$bin" inventory reservation release "$res_id"
"$bin" inventory reservation expire
"$bin" inventory block create --warehouse principal --product HAR-1 --qty 1 --reason damage --json
"$bin" inventory block list
count=$("$bin" inventory count create --warehouse deposito --json)
count_id=$(printf '%s' "$count" | sed -n 's/.*"id": "\([^"]*\)".*/\1/p' | head -n1)
"$bin" inventory count record "$count_id" --product HAR-1 --qty 9
"$bin" inventory count submit "$count_id"
# Quien contó no aprueba (código 2).
set +e; "$bin" inventory count approve "$count_id" >/dev/null 2>&1; code=$?; set -e
test "$code" -eq 2
"$bin" inventory policy set --negative on --limit 5
"$bin" inventory policy show --json
# Sin el permiso extraordinario, la salida en negativo se deniega (código 8).
set +e; "$bin" inventory out --warehouse principal --product HAR-1 --qty 100 --reason correction --allow-negative >/dev/null 2>&1; code=$?; set -e
test "$code" -eq 8
"$bin" inventory verify
# Un segundo setup debe rechazarse con el código 6 (conflicto).
set +e; printf '%s\n' "$pw" | "$bin" setup --name Otra --password-stdin >/dev/null 2>&1; code=$?; set -e
test "$code" -eq 6
# Sesión de cajero: lee la estructura, pero no crea almacenes (código 8).
"$bin" auth logout
set +e; "$bin" company list >/dev/null 2>&1; code=$?; set -e
test "$code" -eq 9
printf '%s\n' "sol y luna 2026" | "$bin" auth login --user cajero --password-stdin
"$bin" company list
set +e; "$bin" warehouse create --code x --name X >/dev/null 2>&1; code=$?; set -e
test "$code" -eq 8
"$bin" product show QUE-01
"$bin" inventory stock --product QUE-01
set +e; "$bin" inventory out --warehouse principal --product QUE-01 --qty 1 --reason correction >/dev/null 2>&1; code=$?; set -e
test "$code" -eq 8
set +e; "$bin" inventory transfer --from principal --to deposito --product QUE-01 --qty 1 >/dev/null 2>&1; code=$?; set -e
test "$code" -eq 8
set +e; "$bin" product create --name X >/dev/null 2>&1; code=$?; set -e
test "$code" -eq 8
"$bin" doctor

# Sin terminal, el comando sin subcomando no abre la TUI: código 2.
set +e; "$bin" < /dev/null; code=$?; set -e
test "$code" -eq 2

if [ -n "${BALANZAPRO_SMOKE_POSTGRES_URL:-}" ]; then
  export BALANZAPRO_DB_DRIVER=postgres BALANZAPRO_POSTGRES_URL="$BALANZAPRO_SMOKE_POSTGRES_URL"
  "$bin" db migrate --json
  # La base de humo puede venir de una ejecución anterior ya configurada.
  export BALANZAPRO_SESSION_FILE="$work/estado/session-pg"
  set +e; printf '%s\n' "$pw" | "$bin" setup --name "Bodega PG" --password-stdin >/dev/null 2>&1; code=$?; set -e
  test "$code" -eq 0 -o "$code" -eq 6
  printf '%s\n' "$pw" | "$bin" auth login --user admin --password-stdin
  "$bin" branch list --json
  "$bin" doctor
fi
echo "prueba de humo correcta"

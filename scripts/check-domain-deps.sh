#!/usr/bin/env bash
# Verifica que internal/domain solo importe la biblioteca estándar, la
# biblioteca decimal aprobada (ADR-0002) y otros paquetes del dominio.
set -euo pipefail

module="$(go list -m)"
allowed_external="github.com/shopspring/decimal"

bad=0
while read -r pkg imports; do
  for imp in $imports; do
    case "$imp" in
      "$module"/internal/domain/*) ;;
      "$allowed_external") ;;
      "$module"/*)
        echo "ERROR: $pkg importa $imp (el dominio no depende de otras capas)"; bad=1 ;;
      *.*/*)
        echo "ERROR: $pkg importa dependencia externa no aprobada $imp"; bad=1 ;;
    esac
  done
done < <(go list -f '{{.ImportPath}} {{join .Imports " "}}' ./internal/domain/...)

exit "$bad"

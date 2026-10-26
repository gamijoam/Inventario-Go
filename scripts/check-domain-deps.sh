#!/usr/bin/env bash
# Verifica las reglas de dependencias entre capas (ADR-0001, sección 28.4):
#   - internal/domain solo importa la biblioteca estándar, la biblioteca
#     decimal aprobada (ADR-0002) y otros paquetes del dominio.
#   - internal/application no importa adaptadores ni bibliotecas externas.
set -euo pipefail

module="$(go list -m)"
bad=0

while read -r pkg imports; do
  for imp in $imports; do
    case "$imp" in
      "$module"/internal/domain/*) ;;
      github.com/shopspring/decimal) ;;
      "$module"/*)
        echo "ERROR: $pkg importa $imp (el dominio no depende de otras capas)"; bad=1 ;;
      *.*/*)
        echo "ERROR: $pkg importa dependencia externa no aprobada $imp"; bad=1 ;;
    esac
  done
done < <(go list -f '{{.ImportPath}} {{join .Imports " "}}' ./internal/domain/...)

while read -r pkg imports; do
  for imp in $imports; do
    case "$imp" in
      "$module"/internal/domain/* | "$module"/internal/application/* | "$module"/internal/platform/*) ;;
      "$module"/*)
        echo "ERROR: $pkg importa $imp (application no depende de adaptadores)"; bad=1 ;;
      *.*/*)
        echo "ERROR: $pkg importa dependencia externa $imp (application usa puertos propios)"; bad=1 ;;
    esac
  done
done < <(go list -f '{{.ImportPath}} {{join .Imports " "}}' ./internal/application/...)

exit "$bad"

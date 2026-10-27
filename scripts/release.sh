#!/usr/bin/env bash
# Empaqueta los ejecutables de un release (ADR-0016):
#   scripts/release.sh v0.3.0-alpha.1 [dist]
# Compila sin CGO para Windows y Linux (amd64), con versión, commit y fecha
# embebidos, y deja en el directorio de salida un .zip (Windows) o .tar.gz
# (Linux) por plataforma, más SHA256SUMS.
set -euo pipefail

version="${1:?uso: scripts/release.sh vX.Y.Z[-pre] [dir]}"
out="${2:-dist}"
case "$version" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "versión inválida: $version (use vX.Y.Z o vX.Y.Z-pre)" >&2; exit 2 ;;
esac

pkg="github.com/gamijoam/inventario-go/internal/platform/buildinfo"
commit="$(git rev-parse HEAD)"
date="$(git log -1 --format=%cI HEAD)" # reproducible: la fecha del commit
ldflags="-s -w -X $pkg.Version=$version -X $pkg.Commit=$commit -X $pkg.Date=$date"

rm -rf "$out"
mkdir -p "$out"
for target in windows/amd64 linux/amd64; do
  goos="${target%/*}" goarch="${target#*/}"
  name="balanzapro_${version#v}_${goos}_${goarch}"
  stage="$out/$name"
  mkdir -p "$stage"
  exe="balanzapro"
  [ "$goos" = windows ] && exe="balanzapro.exe"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "$ldflags" -o "$stage/$exe" ./cmd/balanzapro
  cp README.md LICENSE "$stage/"
  if [ "$goos" = windows ]; then
    (cd "$out" && zip -qr "$name.zip" "$name")
  else
    tar -C "$out" -czf "$out/$name.tar.gz" "$name"
  fi
  rm -rf "$stage"
done
(cd "$out" && sha256sum ./*.zip ./*.tar.gz | sed 's| \./| |' > SHA256SUMS)
cat "$out/SHA256SUMS"

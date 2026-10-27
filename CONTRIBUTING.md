# Contribuir a BalanzaPro

## Ramas

- `main` está protegida: solo recibe cambios por pull request con CI en verde.
- Ramas de trabajo cortas desde `main`: `feat/<ticket>-<tema>`,
  `fix/<ticket>-<tema>`, `docs/...`, `chore/...`. Ejemplo: `feat/BP-M0-002-cli-cobra`.
- PRs pequeños: un ticket o una parte coherente de él. No crear todas las
  entidades de un hito en un solo PR.

## Commits

Se usa [Conventional Commits](https://www.conventionalcommits.org/es/):

```text
<tipo>(<ámbito>): <resumen en imperativo>

<cuerpo opcional: qué y por qué>

Refs: BP-M0-001
```

Tipos: `feat`, `fix`, `test`, `refactor`, `docs`, `build`, `ci`, `chore`.
Ámbitos frecuentes: `money`, `quantity`, `inventory`, `cli`, `tui`, `sqlite`,
`postgres`, `ci`.

## TDD en el dominio

1. Escribir la prueba que falla y verla fallar.
2. Implementar lo mínimo para pasarla.
3. Refactorizar con las pruebas en verde.

Reglas que las pruebas deben proteger: nada de `float32`/`float64` para
dinero, cantidades, tasas o CPP; todo redondeo o división declara escala y
modo; ninguna operación mezcla monedas o unidades sin conversión explícita.

## Puerta de calidad antes de merge

- `gofmt`, `go vet`, `go test ./...` y `go test -race ./...` en verde.
- `go mod tidy` sin cambios y `govulncheck` sin hallazgos alcanzables.
- Compila para Linux y Windows.
- El dominio no depende de otras capas (`scripts/check-domain-deps.sh`).
- Migraciones reproducibles en SQLite y PostgreSQL cuando el cambio las toque.
- Sin secretos en código, flags, logs ni historial.
- Documentación y ADR actualizados si cambia una decisión técnica.

## Releases

Un release se publica empujando una etiqueta sobre un commit de `main`
(ADR-0016); el workflow `release.yml` prueba, empaqueta y publica:

```sh
git tag -a v0.3.0-alpha.1 -m "BalanzaPro v0.3.0-alpha.1"
git push origin v0.3.0-alpha.1
./scripts/release.sh v0.3.0-alpha.1 dist   # el mismo empaquetado, en local
```

Las etiquetas con sufijo (`-alpha.1`, `-rc.1`) quedan como prerelease.

## Decisiones técnicas

Toda decisión de arquitectura, biblioteca o política se registra como ADR en
`docs/decisions/` usando la [plantilla](docs/decisions/0000-plantilla.md).
Un parámetro **propuesto** no se trata como aprobado hasta que su ADR lo diga.

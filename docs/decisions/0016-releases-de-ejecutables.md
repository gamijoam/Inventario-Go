# ADR-0016: Releases de ejecutables en GitHub

- **Estado:** Aceptada
- **Fecha:** 2026-10-27
- **Decisiones relacionadas:** DEC-005.F, sección 28 (M0: "ejecutables reproducibles"); pendiente de prerelease de M0

## Contexto

El usuario prueba en Windows y pidió descargar el ejecutable directamente
desde el repositorio, sin instalar Go. La CI ya compila para Windows y Linux,
pero no publica nada. El documento pide ejecutables reproducibles con su
versión.

## Decisión

1. **Etiquetas semánticas.** Un release se publica al empujar una etiqueta
   `vX.Y.Z` sobre un commit de `main`. Las que llevan sufijo (`-alpha.1`,
   `-rc.1`) se publican como *prerelease*. Mientras V1 no esté lista, la
   versión menor sigue el hito: `v0.3.x` es M3, `v0.4.x` será M4.
2. **Workflow `release.yml`**, en dos etapas:
   - verifica que la etiqueta apunte a un commit de `main` y corre las
     pruebas y la prueba de humo;
   - empaqueta y publica con `gh release create`, usando solo el token del
     propio workflow.
3. **`scripts/release.sh`**, el mismo script para CI y para uso local:
   - compila sin CGO para `windows/amd64` y `linux/amd64`, con `-trimpath`;
   - embebe la versión, el commit y la fecha del commit, no la del reloj,
     para que la compilación sea reproducible;
   - entrega un `.zip` (Windows) y un `.tar.gz` (Linux), cada uno con el
     ejecutable, el README y la licencia, más `SHA256SUMS`.
4. **Sin firma de código por ahora.** Windows SmartScreen puede advertir; la
   guía [Instalar y probar](../user-guide/instalar.md) lo explica y enseña a
   comprobar la suma.

## Alternativas consideradas

- **GoReleaser.** Es más completo, pero agrega otra herramienta y otra
  configuración para dos plataformas que un script corto ya cubre.
- **Artefactos de cada ejecución de CI.** Caducan y exigen iniciar sesión en
  GitHub para descargarlos; un release es permanente y público dentro del
  repositorio.

## Consecuencias

- Cada PR de TUI se acompaña de un release nuevo para probarlo en Windows.
- Quedan pendientes la firma de código (Authenticode), ARM64 y un
  instalador; se evaluarán para el piloto (M6).

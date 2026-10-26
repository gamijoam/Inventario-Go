# ADR-0004: CLI con Cobra y TUI con Bubble Tea v2

- **Estado:** Aceptada
- **Fecha:** 2026-10-26
- **Decisiones relacionadas:** DEC-005.F, DEC-005.F1, secciones 28.3 y 28.7

## Contexto

V1.CLI se opera por terminal en Linux y Windows. La CLI es también la
herramienta de soporte y automatización a largo plazo, por lo que su salida
y sus códigos de salida son un contrato.

## Decisión

1. **Bibliotecas fijadas:** `github.com/spf13/cobra` v1.10.2,
   `charm.land/bubbletea/v2` v2.1.0 y `charm.land/lipgloss/v2` v2.0.6, con
   Go 1.27.3 (Charm v2 exige Go ≥ 1.26). `charm.land/bubbles/v2` se agregará
   cuando una pantalla necesite sus componentes.
2. **Capas:** `cmd/balanzapro` solo conecta el proceso (stdin/stdout,
   detección de terminal, señales). `internal/adapters/cli` y
   `internal/adapters/tui` llaman casos de uso de `internal/application`;
   ninguno contiene reglas de negocio.
3. **Sin subcomando:** abre la TUI solo si stdin y stdout son una terminal.
   Sin terminal, o con `--json`, falla con código 2 en lugar de colgarse o
   escribir ANSI en un pipe.
4. **Códigos de salida estables:** `0` correcto, `1` fallo no clasificado,
   `2` uso inválido (comando, argumento o flag), `3` `doctor` con fallos
   (`4` configuración inválida, agregado en ADR-0005).
   Solo se agregan códigos nuevos; los existentes no cambian de significado.
5. **JSON:** `--json` escribe en stdout un único documento con campo
   `schema` versionado (`balanzapro.version/v1`, `balanzapro.doctor/v1`). Los
   errores van a stderr; con `--json`, como `balanzapro.error/v1`
   (`kind`, `message`). La salida JSON nunca contiene ANSI.
6. **Color:** se desactiva con `--no-color`, con `NO_COLOR` no vacío
   (https://no-color.org) o sin terminal. La TUI conserva negrita e
   inversión, y los estados se distinguen por texto (`[ OK ]`, `[WARN]`,
   `[FAIL]`), no solo por color.
7. **TUI:** pantalla alternativa; teclado (↑/↓ o j/k, enter, esc, q,
   Ctrl+C); aviso si la terminal mide menos de 40×12.
8. **`doctor`:** verificaciones con nombre estable y detalle sin
   credenciales. La de configuración se agregó con BP-M0-003; la de base de
   datos se agrega con BP-M0-004.
9. **Autocompletado:** el comando `completion` de Cobra (bash, zsh, fish,
   powershell).

## Consecuencias

- Los textos propios de Cobra (`Usage:`, `Available Commands`, `help`,
  `completion`) siguen en inglés; traducir la plantilla de ayuda queda
  pendiente.
- La compatibilidad real en PowerShell/Windows Terminal debe verificarse a
  mano; la CI prueba el ejecutable en Windows sin terminal interactiva.

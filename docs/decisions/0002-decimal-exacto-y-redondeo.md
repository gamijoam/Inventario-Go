# ADR-0002: Decimal exacto y política provisional de redondeo

- **Estado:** Aceptada (parámetros numéricos provisionales)
- **Fecha:** 2026-10-26
- **Decisiones relacionadas:** DEC-013.P1, DEC-006.TZ, secciones 25.1–25.3

## Contexto

DEC-013.P1 exige cálculo decimal exacto en Go, sin `float32`/`float64` para
importes, tasas, cantidades fraccionarias ni CPP, y prohíbe divisiones sin
política de redondeo. La biblioteca decimal no debe filtrarse a los módulos.

## Decisión

1. **Biblioteca:** `github.com/shopspring/decimal` v1.4.0, envuelta en
   `internal/domain/decimal`. Ningún otro paquete la importa.
2. **Entrada:** solo `decimal.Parse` con forma canónica
   (`-?(0|[1-9][0-9]*)(\.[0-9]+)?`): sin exponentes, signo `+`, separador de
   miles, coma decimal ni espacios. No existe constructor desde `float`.
   Las CLI/TUI que acepten formatos locales los normalizan antes de llamar.
3. **Escala máxima de entrada:** 18 decimales (provisional).
4. **Modos de redondeo:** `HalfAwayFromZero`, `HalfEven`, `TowardZero`. No hay
   modo por defecto: `Round` y `Div` exigen escala y modo.
5. **División:** el cociente se trunca y luego se decide el redondeo sobre el
   resto exacto, así ningún modo depende de aproximaciones.
6. **Money:** moneda obligatoria; `Add`, `Sub` y `Cmp` fallan con
   `ErrCurrencyMismatch` entre monedas distintas. `Mul` no redondea; solo
   `Round` liquida a la escala de la moneda.
7. **Monedas iniciales:** USD y VES con escala de liquidación 2 (provisional).
8. **Quantity:** unidad obligatoria con su propia escala (0–6); se rechaza una
   cantidad con más decimales que su unidad y la mezcla de unidades.
9. **Zona horaria:** instantes en UTC; presentación en `America/Caracas` con
   `time/tzdata` incrustado para no depender del sistema operativo.

## Consecuencias

- `0.10 + 0.20 = 0.30` exacto; las pruebas lo protegen.
- Siguen **pendientes de ratificar**: escalas por campo, modo de redondeo
  fiscal, moneda de valoración por empresa y la política de tasas.
  `ExchangeRate` y `BusinessScope` se implementan en tickets posteriores de M0.
- La serialización a JSON y a cada motor SQL (texto canónico, enteros escalados
  o `NUMERIC`) se define con los adaptadores (BP-M0-004).
- Cambiar de biblioteca decimal solo afecta a `internal/domain/decimal`.

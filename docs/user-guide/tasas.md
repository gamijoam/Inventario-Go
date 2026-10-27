# Tasas de cambio

BalanzaPro no inventa tasas ni las descarga: cada tasa la registra una
persona e indica de dónde la tomó. Rige desde una fecha hasta que otra la
reemplace. Los documentos guardan la tasa que aplicaron, así que una tasa
nueva nunca cambia lo ya confirmado.

> Las tasas de estos ejemplos son **ficticias**.

## Registrar una tasa

```sh
balanzapro rate set --base USD --quote VES --rate 45.725 --source official --label BCV
balanzapro rate set --base USD --quote VES --rate 46.10 --source official --label BCV --date 2026-10-28 --reference "publicación del 27/10"
```

- `--base USD --quote VES --rate 45.725` significa **1 USD = 45.725 VES**.
  Se admiten hasta 12 decimales.
- `--source` es `official` (p. ej. BCV), `manual` o `provider`; `--label`
  nombra la fuente.
- `--date` es la fecha de Caracas desde la que rige; por defecto, hoy. Una
  fecha futura deja la tasa programada.
- Una tasa **no se edita**. Para corregirla, registre otra con la misma
  fecha y, si quiere, `--note "corrección"`: queda como una versión nueva y
  gana a la anterior.
- USD/VES y VES/USD son el mismo par: la que se registró por último para la
  fecha vigente es la que rige.

## Consultar

```sh
balanzapro rate show                          # la que rige hoy entre USD y VES
balanzapro rate show --date 2026-10-20        # la que regía ese día
balanzapro rate list [--pair USD/VES] [--json]
```

Si no hay tasa vigente para una fecha, `rate show` y las operaciones que
la necesitan terminan con el código 6 y explican qué falta.

## Compras en otra moneda

Cada documento de compra va en su moneda: la de valoración de la empresa
(por defecto) u otra.

```sh
balanzapro purchase order create --supplier DIST --currency VES --line HARINA:100:58
balanzapro purchase receipt create --supplier DIST --currency VES --date 2026-10-22 --document FAC-778
balanzapro purchase receipt expense RC-000001 --concept Flete --amount 500        # en VES, a la tasa del 22
balanzapro purchase receipt expense RC-000001 --concept Seguro --amount 2 --currency USD
balanzapro purchase receipt confirm RC-000001
```

- La recepción toma la moneda de su orden. Su `--date` es la fecha de la
  factura (no puede ser futura) y decide la tasa.
- **Al confirmar**, cada línea se convierte a la moneda de valoración con la
  tasa vigente a la fecha de la factura, aunque hoy rija otra. Si no hay
  tasa para esa fecha, no se confirma y nada se mueve.
- Un **gasto** se convierte al registrarlo, con la tasa vigente a su
  `--date`. Por defecto esa fecha es la de la factura si la recepción está
  en borrador, y hoy si el gasto es tardío.
- `receipt show` muestra la tasa aplicada, el costo en las dos monedas y el
  importe original de cada gasto. `--json` da `currency`,
  `valuation_currency`, `rate`, `valued_cost` y `valued_amount`.

## Permisos

| Rol | Ver tasas | Registrar tasas |
|---|---|---|
| `ADMIN_ORG`, `ADMIN_EMPRESA` | sí | sí |
| `ENCARGADO` | sí | solo si está asignado a la empresa, no a una sucursal |
| `ALMACENISTA`, `CAJERO` | sí | no |
| `OPERADOR_TECNICO` | no | no |

La tasa rige para todas las sucursales de la empresa; por eso, para
registrarla hay que tener el permiso con ámbito de empresa.

## Qué no hace todavía

- Diferencias cambiarias: llegarán con los pagos a proveedores.
- Tasas distintas para precio, cobro y reportes.
- Caducidad de una tasa: hoy rige hasta que otra la reemplace.

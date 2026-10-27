# Listas y precios de venta

Recibir mercancía **no cambia precios**: deja propuestas. Un precio cambia
solo cuando un usuario autorizado aprueba una propuesta. Desde ese momento,
o desde la hora programada, rige para las ventas futuras de esa lista y
para todas las existencias, sin importar cuándo entraron. Las ventas ya
hechas no cambian.

## Listas

```sh
balanzapro price-list create --code general --name General --margin 30
balanzapro price-list create --code bs --name "Precios en bolívares" --currency VES --margin 30
balanzapro price-list list
```

- Cada lista es de la empresa y va en **una sola moneda** (por defecto, la
  de valoración).
- El **margen objetivo** es sobre la venta: con margen 20 %, un costo de 100
  sugiere 125. Una lista sin margen no recibe sugerencias, pero sus precios
  se pueden proponer a mano.

```sh
balanzapro price-list margin --list general --margin 25                    # margen de la lista
balanzapro price-list margin --list general --product HARINA --margin 15   # excepción de un producto
balanzapro price-list margin --list general --product HARINA --clear       # vuelve al de la lista
```

Los cambios de margen quedan en el historial.

## Sugerencias al recibir

Al confirmar una recepción, cada lista con margen recibe una propuesta por
producto recibido, siempre que el precio sugerido sea distinto del vigente.

```text
precio sugerido PP-000002 en GENERAL para HARINA: 10 → 12 USD; pendiente de aprobación
```

- El costo de referencia es el **costo de reposición** registrado o, si no
  hay, el **último costo de compra con gastos**.
- Si la lista está en otra moneda, el costo se convierte con la tasa
  vigente hoy, y la tasa queda en la propuesta.
- Si no hay tasa, esa sugerencia se omite y la confirmación lo dice. La
  recepción se confirma igual.
- Una propuesta nueva sustituye a la pendiente anterior del mismo producto
  en la lista.

## Proponer, aprobar y rechazar

```sh
balanzapro price show [--list general] [--product HARINA]
balanzapro price proposals [--status pending]
balanzapro price proposal PP-000002            # costo de referencia y margen resultante
balanzapro price propose --list general --product HARINA --price 12.50 --note "competencia"
balanzapro price approve PP-000002                              # rige ya
balanzapro price approve PP-000002 --price 12.40                # ajustando el precio
balanzapro price approve PP-000002 --at "2026-10-28 08:00"      # programado, hora de Caracas
balanzapro price reject PP-000002 --note "esperar a fin de mes"
balanzapro price cancel <ID-de-revisión>                        # anula un programado que aún no rige
```

- Si el precio cambió después de hacer la propuesta, la aprobación se
  rechaza (código 6). Hay que proponer de nuevo; nunca se pisa un cambio en
  silencio.
- Aprobar un precio **por debajo del costo de referencia** exige el permiso
  extraordinario `pricing.override.approve_below_cost`.
- Cada aprobación publica una revisión numerada. `price show --json` da el
  precio vigente, los programados y la propuesta pendiente.

## Permisos

| Rol | Ver | Proponer | Aprobar | Listas y márgenes |
|---|---|---|---|---|
| `ADMIN_ORG`, `ADMIN_EMPRESA` | sí | sí | sí | sí |
| `ENCARGADO` | sí | sí | solo si está asignado a la empresa | no |
| `ALMACENISTA` | sí | sí | no | no |
| `CAJERO` | sí | no | no | no |

Quien puede proponer y aprobar puede aprobar su propia propuesta; queda
registrado quién propuso y quién aprobó.

## Qué no hace todavía

- Precio mínimo y redondeo comercial (p. ej. a 0.05).
- Impuestos en la base del margen.
- Listas por cliente o canal, y reprecio automático sin aprobación.

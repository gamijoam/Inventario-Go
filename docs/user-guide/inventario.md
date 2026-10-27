# Inventario: existencias y kardex

Cada producto tiene un saldo por almacén y un **kardex**: la lista ordenada
de todos sus movimientos. El kardex no se edita ni se borra; el saldo
siempre es la suma exacta de sus movimientos.

## Registrar entradas y salidas

```sh
balanzapro inventory in  --product HARINA --qty 40 --reason initial_load
balanzapro inventory out --product QUESO --qty 0.125 --reason shrinkage --note "se partió"
```

- Sin `--company` ni `--branch` se usa la sucursal de esta instalación; sin
  `--warehouse`, su único almacén activo (si tiene varios, hay que indicarlo).
- `--product` acepta SKU, código de barras o ID.
- La cantidad respeta los decimales de la unidad del producto (`KG` admite
  3, `UND` ninguno).
- Una salida **nunca** deja el disponible en negativo: la CLI responde con
  el código 10. Las excepciones por empresa y producto (DEC-007.F2) llegan
  en BP-M2-003.

Motivos (`balanzapro inventory reasons`):

| Motivo | Uso | Entrada | Salida |
|---|---|---|---|
| `initial_load` | Carga inicial de existencia | sí | no |
| `correction` | Corrección de un error de registro | sí | sí |
| `shrinkage` | Merma, daño o pérdida | no | sí |
| `internal_use` | Consumo interno | no | sí |
| `other` | Otro; exige `--note` | sí | sí |

### Reintentos seguros

Si un comando se corta y no sabe si se registró, repítalo con la misma
`--key`: la segunda vez responde "ya registrado antes" sin sumar de nuevo.
La misma clave con otros datos se rechaza (código 6).

```sh
balanzapro inventory in --product HARINA --qty 40 --reason initial_load --key carga-harina-2026-10
```

## Corregir un error

Los movimientos no se editan. Para anular uno, se revierte: se crea un
movimiento de signo contrario que referencia al original.

```sh
balanzapro inventory kardex --product HARINA        # ver el ID del movimiento
balanzapro inventory reverse 01a17b58-... --note "eran 4 cajas, no 40"
```

Cada movimiento se revierte una sola vez, y un reverso no se revierte
(registre un ajuste nuevo). Revertir una entrada cuyas unidades ya salieron
se rechaza igual que una salida sin existencia.

## Consultar

```sh
balanzapro inventory stock [--company C --branch B --warehouse W] [--product P]
balanzapro inventory kardex --product P [--warehouse W] [--limit 100 --offset 0]
balanzapro inventory verify
```

`stock` muestra la existencia **física**, la **reservada** y la
**bloqueada** (ambas en cero hasta BP-M2-003) y la **disponible** = física −
reservada − bloqueada. `verify` reconstruye cada saldo a partir de su kardex
y avisa si alguno no coincide (código 3); requiere ver existencias en toda
la organización.

## Permisos

| Rol | Ver existencias y kardex | Ajustar y revertir |
|---|---|---|
| `ADMIN_ORG`, `ADMIN_EMPRESA`, `ENCARGADO`, `ALMACENISTA` | sí | sí |
| `CAJERO`, `OPERADOR_TECNICO` | sí | no |

Los permisos valen en el ámbito asignado: un almacenista de un almacén no
ve ni ajusta otro almacén de la misma sucursal.

## Qué no se puede hacer todavía

- Costo promedio ponderado y valoración (BP-M2-002).
- Transferencias entre almacenes (BP-M2-002).
- Reservas, bloqueos, conteos físicos e inventario negativo autorizado
  (BP-M2-003).

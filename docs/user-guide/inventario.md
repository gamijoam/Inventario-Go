# Inventario: existencias, kardex y costo promedio

Cada producto tiene un saldo por almacén y un **kardex**: la lista ordenada
de todos sus movimientos. El kardex no se edita ni se borra; el saldo
siempre es la suma exacta de sus movimientos, en cantidad y en valor.

## Registrar entradas y salidas

```sh
balanzapro inventory in  --product HARINA --qty 40 --unit-cost 1.25 --reason initial_load
balanzapro inventory in  --product HARINA --qty 5 --reason correction      # al costo promedio vigente
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
- Una entrada lleva `--unit-cost` en la moneda de la empresa. Sin él entra
  al costo promedio vigente del almacén; si no hay (carga inicial o
  existencia en cero), la CLI pide el costo (código 2). Una salida no lleva
  costo: siempre se valora al costo promedio.

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

El reverso deshace el **valor original** del movimiento, no el costo
promedio actual: si cargó 10 unidades a 100 en vez de 10, revertirlas
devuelve el costo promedio a lo que era antes del error.

## Costo promedio ponderado (CPP)

Cada almacén lleva el **valor** de la existencia de cada producto, en la
moneda de valoración de su empresa (USD o VES, fijada al crear la empresa
con `--currency`; USD por defecto, y no se puede cambiar). El costo
promedio es valor ÷ existencia.

| Operación | Efecto en el valor |
|---|---|
| Entrada con `--unit-cost` | suma cantidad × costo; el CPP se recalcula |
| Entrada sin costo | suma cantidad × CPP vigente; el CPP no cambia |
| Salida | resta cantidad × CPP; el CPP no cambia |
| Salida que deja la existencia en cero | resta todo el valor restante: nunca queda valor sin mercancía |

Ejemplo: 10 unidades a 80 y 5 a 95 dan 15 unidades por 1275, CPP 85. Una
merma de 3 cuesta 255 y quedan 12 por 1020.

Los valores se guardan con 6 decimales y el costo promedio con 12. La tabla
de `stock` los muestra redondeados (2 y 6 decimales); `--json` da los
importes exactos.

### Existencia cargada antes del costo promedio

La existencia registrada antes de esta versión no tiene costo: `stock` la
muestra "sin valor" e `inventory verify` la lista como aviso. Para valorarla
basta una entrada con costo; ese costo se aplica también a las unidades que
ya había:

```sh
balanzapro inventory in --product HARINA --qty 1 --unit-cost 1.30 --reason correction
```

Mientras no esté valorada, las salidas funcionan con normalidad (sin
costo), pero no se puede transferir esa existencia.

## Transferir entre almacenes de la sucursal

```sh
balanzapro inventory transfer --from PRINCIPAL --to DEPOSITO --product HARINA --qty 4 [--note ...] [--key ...]
```

La mercancía sale del origen a su costo promedio y entra al destino con
**exactamente ese valor**, en una sola operación: no se gana ni se pierde
valor, y el destino recalcula su costo promedio. Ejemplo: A tiene 15 a 85 y
B tiene 6 a 100; al pasar 4 de A a B, A queda con 11 por 935 (CPP 85) y B
con 10 por 940 (CPP 94). El total sigue siendo 1875.

- Ambos almacenes deben ser de la misma sucursal. Los traslados entre
  sucursales tendrán despacho y recepción propios.
- Requiere el permiso `inventory.transfer.create` en los dos almacenes.
- Con `--key`, repetir el comando no duplica la transferencia.
- Una transferencia no se revierte por partes: si fue un error, transfiera
  la misma cantidad en sentido contrario.

## Consultar

```sh
balanzapro inventory stock [--company C --branch B --warehouse W] [--product P]
balanzapro inventory kardex --product P [--warehouse W] [--limit 100 --offset 0]
balanzapro inventory verify
```

`stock` muestra la existencia **física**, la **reservada** y la
**bloqueada** (ambas en cero hasta BP-M2-003), la **disponible** = física −
reservada − bloqueada, el costo promedio, el valor y la moneda. `kardex`
agrega el costo de cada movimiento y el valor resultante. `verify`
reconstruye cada saldo, en cantidad y valor, a partir de su kardex,
comprueba que cada transferencia conserve cantidad y valor, y avisa si algo
no coincide (código 3); requiere ver existencias en toda la organización.

## Permisos

| Rol | Ver existencias y kardex | Ajustar y revertir | Transferir |
|---|---|---|---|
| `ADMIN_ORG`, `ADMIN_EMPRESA`, `ENCARGADO`, `ALMACENISTA` | sí | sí | sí |
| `CAJERO`, `OPERADOR_TECNICO` | sí | no | no |

Los permisos valen en el ámbito asignado: un almacenista de un almacén no
ve ni ajusta otro almacén de la misma sucursal, y para transferir necesita
el permiso en ambos almacenes.

## Qué no se puede hacer todavía

- Reservas, bloqueos, conteos físicos e inventario negativo autorizado
  (BP-M2-003).
- Traslados entre sucursales (despacho, tránsito y recepción).
- Compras con gastos atribuibles (M3).

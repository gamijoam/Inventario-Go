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
- Una salida no deja el disponible en negativo (código 10), salvo la
  excepción autorizada que se explica en
  [existencia negativa](#existencia-negativa-por-excepción).
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

| Rol | Ver | Ajustar, revertir y transferir | Reservas | Bloqueos y conteos | Aprobar conteos | Política de negativos |
|---|---|---|---|---|---|---|
| `ADMIN_ORG`, `ADMIN_EMPRESA` | sí | sí | sí | sí | sí | sí |
| `ENCARGADO` | sí | sí | sí | sí | sí | no |
| `ALMACENISTA` | sí | sí | sí | sí | no | no |
| `CAJERO` | sí | no | sí | no | no | no |
| `OPERADOR_TECNICO` | sí | no | no | no | no | no |

La salida en negativo exige además el permiso extraordinario
`inventory.negative_stock.override`, que ninguna plantilla incluye.

Los permisos valen en el ámbito asignado: un almacenista de un almacén no
ve ni ajusta otro almacén de la misma sucursal, y para transferir necesita
el permiso en ambos almacenes.

## Reservas

Una reserva compromete unidades **libres** de un almacén para un documento
(un pedido, un apartado) sin sacarlas: la existencia y el costo no cambian,
pero el disponible baja.

```sh
balanzapro inventory reservation create --product HARINA --qty 20 --reference PED-1001 [--expires "2026-11-05 18:00"] [--key ...]
balanzapro inventory reservation consume <ID> [--qty 8]     # despacho: baja existencia y reservado a la vez
balanzapro inventory reservation release <ID> --note "el cliente desistió"
balanzapro inventory reservation list [--status active]
balanzapro inventory reservation expire                     # para una tarea programada
```

- Nunca se reserva más que el disponible, ni en existencia negativa.
- `--expires` es opcional (hora de Caracas). Sin vencimiento, la reserva
  dura hasta despacharla o liberarla. Las vencidas se liberan una sola vez:
  con `reservation expire` o solas antes de cualquier operación o consulta
  sobre ese saldo.
- Despachar sin `--qty` despacha todo lo pendiente; la salida se valora al
  costo promedio.

## Bloqueos (mercancía no vendible)

```sh
balanzapro inventory block create --product HARINA --qty 4 --reason damage --note "sacos rotos"
balanzapro inventory block release <ID> --note "revisado, en buen estado"
balanzapro inventory block write-off <ID> --note "sin recuperación"    # merma; exige además ajustar
balanzapro inventory block list
```

Motivos: `damage`, `quarantine`, `quality`, `administrative`,
`investigation`. Bloquear baja el disponible sin sacar nada: la existencia
y el costo no cambian. Dar de baja (`write-off`) es la merma: una salida
valorada al costo promedio, separada del bloqueo.

## Conteos físicos

```sh
balanzapro inventory count create [--warehouse W] --note "cierre de octubre"
balanzapro inventory count record <ID> --product HARINA --qty 96     # repetible mientras sea borrador
balanzapro inventory count submit <ID>
balanzapro inventory count approve <ID> --note "conforme"            # otro usuario
balanzapro inventory count reject <ID> --note "recontar"
balanzapro inventory count show <ID>
```

Al registrar lo contado se guarda también la existencia que el sistema
tenía en ese momento. Al aprobar, cada producto recibe un ajuste
(`conteo +` o `conteo −`) por la **diferencia** entre lo contado y esa
existencia, aplicado sobre el saldo actual. Lo vendido o recibido después de
contar no se descuenta dos veces, y el kardex anterior no se toca.

- Aprueba un usuario **distinto** de quien contó o envió el conteo, con el
  permiso `inventory.count.approve` (plantillas `ADMIN_EMPRESA` y
  `ENCARGADO`).
- Lo que sobra entra al costo promedio vigente. Si no lo hay, entra al
  último costo válido del almacén. Si el producto nunca tuvo costo ahí,
  capture el conteo con `--unit-cost`.
- Un conteo que encuentra menos unidades que las reservadas o bloqueadas se
  aplica igual (refleja la realidad), y `verify` avisa de las reservas sin
  respaldo.

## Existencia negativa por excepción

Está **deshabilitada por defecto** (DEC-007.F2). Para que una salida deje
existencia negativa hacen falta cuatro cosas:

1. Que la empresa la habilite, con un déficit máximo por producto y almacén:
   `balanzapro inventory policy set --negative on --limit 5`.
2. Que el producto no la prohíba: `--mode inherit` (sigue a la empresa,
   valor por defecto), `deny`, o `allow --limit N` con límite propio.
   `balanzapro inventory policy set --product HARINA --mode deny`.
3. Que quien registra tenga el permiso extraordinario
   `inventory.negative_stock.override` en ese almacén.
4. Que lo pida expresamente:
   `balanzapro inventory out --product HARINA --qty 5 --reason correction --allow-negative`.

La excepción nunca toca unidades reservadas ni bloqueadas, y no aplica a
transferencias, reservas ni bloqueos. Cada cambio de política queda
registrado (`inventory policy show`) y el movimiento guarda la versión que
lo autorizó.

**Costo del déficit:** las unidades que faltan salen al último costo
promedio válido del almacén, como costo provisional. Si el producto nunca
tuvo costo ahí, la salida se rechaza. La entrada siguiente cubre primero el
déficit: su costo real reemplaza el provisional y la diferencia queda en el
kardex como ajuste de valor. Ejemplo: déficit de 2 a 8; llegan 5 a 10. Dos
cubren el déficit (diferencia de 4) y quedan 3 a 10.

`balanzapro inventory stock --negative` lista los saldos en déficit, y
`verify` los informa como aviso hasta regularizarlos.

## Qué no se puede hacer todavía

- Ventas y pedidos que creen reservas solos (M4).
- Traslados entre sucursales (despacho, tránsito y recepción).
- Compras con gastos atribuibles (M3).

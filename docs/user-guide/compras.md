# Compras: proveedores, órdenes, recepciones y costos

La **recepción confirmada** es lo único que mueve existencia: cada producto
entra al almacén con su costo de compra más los gastos que le corresponden,
y el costo promedio (CPP) se recalcula. El precio de venta **no cambia**.

Los importes van en la moneda de valoración de la empresa. Las compras en
otra moneda llegan con las tasas de cambio (BP-M3-002). Los costos se
registran **sin impuestos**.

## Proveedores

```sh
balanzapro purchase supplier create --code DIST --name "Distribuidora, C.A." --rif J-12345678-9
balanzapro purchase supplier list
```

Los proveedores son de toda la organización: crearlos exige gestionar
compras con ámbito de organización.

## Órdenes de compra (opcionales)

```sh
balanzapro purchase order create --supplier DIST --line HARINA:100:1.25 --line ARROZ:50:0.90
balanzapro purchase order show OC-000001
balanzapro purchase order list [--status open]
balanzapro purchase order cancel OC-000001
```

La orden no mueve existencia. Registra lo pedido, el costo pactado y cuánto
se ha recibido. Queda **recibida** cuando se recibe todo, o se cancela lo
pendiente.

## Recepciones

```sh
balanzapro purchase receipt create --order OC-000001 --document FAC-778     # precarga lo pendiente
balanzapro purchase receipt create --supplier DIST [--warehouse W]          # directa, sin orden
balanzapro purchase receipt line RC-000001 --product HARINA --qty 60 --unit-cost 1.27
balanzapro purchase receipt remove-line RC-000001 --product ARROZ
balanzapro purchase receipt expense RC-000001 --concept Flete --amount 30 [--by quantity]
balanzapro purchase receipt confirm RC-000001
balanzapro purchase receipt show RC-000001
```

- `line` agrega o reemplaza un producto. El costo es el **real** de la
  factura, que puede diferir del pactado en la orden.
- No se recibe más que lo pendiente de una orden. Un producto que no estaba
  en la orden se puede recibir igual.
- `confirm` puede repetirse sin efecto: la segunda vez responde "ya lo
  estaba".
- Un borrador se descarta con `cancel`. Una recepción confirmada no se
  cancela ni se edita.

## Gastos atribuibles

Flete, seguro y otros gastos de la compra se reparten entre las líneas:

- **por valor** (predeterminado): en proporción al costo de compra de cada
  línea;
- **por cantidad** (`--by quantity`): en proporción a las unidades.

El reparto se redondea a 6 decimales y el residuo va a la línea mayor, así
que la suma es exacta. `receipt show --json` muestra la base, el importe y lo
capitalizado de cada línea.

**Gasto después de confirmar.** Si el gasto llega tarde y parte de la
mercancía ya salió, lo que sigue en el almacén sube de valor con un
movimiento de **revaloración** en el kardex (sin cantidad). La parte de las
unidades que ya salieron queda registrada como **diferencia**, sin tocar
ventas cerradas. Registrar un gasto tardío exige el permiso de confirmar
recepciones.

## Costos de un producto

```sh
balanzapro purchase cost show --product HARINA
balanzapro purchase cost set-replacement --product HARINA --unit-cost 1.35 --note "cotización 27/10"
```

`cost show` muestra tres costos que nunca se mezclan:

| Costo | Qué es | De dónde sale |
|---|---|---|
| Costo promedio | Valoración del inventario, por almacén | Kardex |
| Último costo de compra | Lo pagado en la última recepción, con y sin gastos | Recepciones confirmadas |
| Costo de reposición | Estimación de lo que costaría volver a comprar | Registro manual, con historial |

El costo de reposición no cambia el costo promedio ni los precios; servirá
para las sugerencias de precio (BP-M3-003).

## Permisos

| Rol | Ver | Preparar órdenes, recepciones y gastos | Confirmar y gastos tardíos | Costo de reposición |
|---|---|---|---|---|
| `ADMIN_ORG`, `ADMIN_EMPRESA`, `ENCARGADO` | sí | sí | sí | sí |
| `ALMACENISTA` | sí | sí | no | no |
| `CAJERO`, `OPERADOR_TECNICO` | no | no | no | no |

Crear proveedores exige además ámbito de organización (`ADMIN_ORG`).

## Qué no se puede hacer todavía

- Compras en otra moneda con tasa (BP-M3-002).
- Devoluciones a proveedor y reverso de una recepción confirmada.
- Impuestos de compra (M4).

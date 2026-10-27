# Catálogo

El catálogo es de la organización: todas sus empresas y sucursales venden
los mismos productos. Lo consulta cualquier usuario con
`catalog.product.read` (todas las plantillas lo incluyen) y lo modifica solo
quien administra toda la organización (`ADMIN_ORG`).

## Productos

```sh
balanzapro product create --name "Harina de maíz 1 kg" --barcode 7591234567894
balanzapro product create --name "Queso blanco" --sku QUE-01 --unit KG
balanzapro product list [--search texto] [--all] [--limit 50 --offset 0]
balanzapro product show QUE-01            # por SKU, código de barras o ID
balanzapro product update QUE-01 --name "Queso blanco duro" --add-barcode 2000000000015
balanzapro product update QUE-01 --inactive
```

- Solo el **nombre** es obligatorio. Sin `--sku` se asigna `P-000001`,
  `P-000002`…; sin `--unit`, `UND`.
- El **SKU** es la referencia que usan las personas: único en la
  organización y se puede cambiar. El **ID** interno nunca cambia. Los
  **códigos de barras** son lo que lee el escáner; un producto puede tener
  varios (hasta 20).
- Los códigos EAN-8, UPC-A, EAN-13 y GTIN-14 se validan con su dígito
  verificador. Un UPC-A y el mismo código leído como EAN-13 (con un `0`
  delante) se consideran el mismo. Los códigos internos alfanuméricos se
  aceptan tal cual.
- Los productos **no se borran**: `--inactive` los saca de la venta y de
  los listados (salvo con `--all`), y `--active` los recupera. Sus códigos
  de barras siguen reservados.
- La búsqueda ignora mayúsculas y acentos (`nandu` encuentra "Ñandú") y
  también encuentra por prefijo de SKU o por código de barras exacto.
- Con `--revision N`, `update` se rechaza (código 6) si alguien modificó el
  producto desde la revisión N: útil cuando dos personas editan a la vez.

## Unidades de medida

| Código | Nombre | Decimales |
|---|---|---|
| UND | Unidad | 0 |
| KG | Kilogramo | 3 |
| G | Gramo | 0 |
| L | Litro | 3 |
| ML | Mililitro | 0 |
| M | Metro | 3 |
| CAJA | Caja | 0 |
| PAQ | Paquete | 0 |

```sh
balanzapro unit list
balanzapro unit create --code LB --name Libra --decimals 3
```

Los decimales de una unidad y la unidad de un producto **no cambian**
después de creados, porque las cantidades registradas dependen de ellos.

## Qué no se puede hacer todavía

- Empaques y conversiones (CAJA = 12 UND), variantes, lotes, seriales,
  categorías, marcas e imágenes (capacidades avanzadas de DEC-007.A/C/D).
- Activar productos por empresa: hoy toda la organización comparte el
  catálogo completo.
- Precios: llegan en M3.

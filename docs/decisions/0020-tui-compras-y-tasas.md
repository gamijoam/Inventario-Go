# ADR-0020: Compras y tasas en la TUI

- **Estado:** Aceptada
- **Fecha:** 2026-10-28
- **Decisiones relacionadas:** ADR-0013 (compras), ADR-0014 (tasas), ADR-0018 y ADR-0019 (TUI)

## Contexto

Tras TUI-3 el inventario se opera sin comandos; compras (BP-M3-001) y tasas
(BP-M3-002) seguían en la CLI. El usuario pidió sumar este ticket al mismo
PR de TUI-3 para publicar una sola versión.

## Parámetros ratificados por el usuario (2026-10-28)

| Tema | Decisión |
|---|---|
| Entrega | En el mismo PR que TUI-3, una sola release |
| Alcance | Proveedores, órdenes, recepciones con líneas y gastos, confirmación, costos de reposición y tasas |
| Recepción | **Pantalla de documento**: encabezado arriba, líneas abajo, teclas para línea, gasto, confirmar y anular |
| Tasa faltante | **Aviso con atajo**: el encabezado muestra la tasa vigente a la fecha de la factura; si falta, F6 la registra sin salir |

## Decisión

1. **Menú Compras**: Recepciones, Órdenes de compra, Proveedores, Costos de
   un producto y Tasas de cambio, según permisos.

2. **Documento de recepción.** Una pantalla con el encabezado (proveedor,
   almacén, orden, factura, moneda), la tasa, la tabla de líneas, el total
   en la moneda del documento (y su equivalente con la tasa) y los gastos.
   F2 línea, Enter edita, F8 quita, F3 gasto, F9 confirma, F4 anula; ya
   confirmada, F3 registra un gasto tardío. Las teclas aparecen solo con el
   estado y el permiso que las permiten (confirmar exige su permiso propio).

3. **Tasa.** La pantalla consulta la tasa que regirá a la fecha de la
   factura; confirmada, muestra la aplicada. Si falta y el usuario puede
   registrar tasas, F6 abre el formulario con el par y la fecha puestos; al
   volver, el documento se recarga con la tasa nueva. La conversión la
   sigue haciendo el servicio al confirmar.

4. **Orden de compra en dos pasos**: encabezado (proveedor, moneda, nota) y
   captura de líneas en memoria, con el mismo patrón que el conteo físico;
   F10 crea la orden con todas sus líneas en una sola operación, porque el
   servicio no admite órdenes sin líneas ni editarlas después. Desde una
   orden abierta, F5 abre la recepción contra ella.

5. **Formularios con datos previos encadenados**: la carga de almacenes se
   compone con la propia del formulario (proveedores, órdenes abiertas).

6. La confirmación genérica admite un mensaje armado con el resultado (p.
   ej. cuántas propuestas de precio dejó la compra).

## Consecuencias

- Inventario, compras y tasas se operan sin comandos.
- Quedan en la CLI las listas de precios, márgenes y propuestas manuales, y
  la administración (TUI-5).

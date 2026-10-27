# ADR-0009: Catálogo básico — productos, SKU, códigos de barras y unidades

- **Estado:** Aceptada
- **Fecha:** 2026-10-27
- **Decisiones relacionadas:** DEC-007.A (aprobada), DEC-007.C, DEC-008.B, DEC-006, secciones 13.4 y 22.4; BP-M1-003

## Contexto

DEC-007.A aprobó productos básicos por defecto con capacidades avanzadas
activables. La sección 13.4 exige que SKU, código de barras e ID interno no
sean lo mismo, que los productos usados no se borren y que la búsqueda
escale. DEC-008.B (catálogo entre empresas) y DEC-007.C (unidades) estaban
pendientes.

## Parámetros ratificados por el usuario (2026-10-27)

| Tema | Decisión |
|---|---|
| DEC-008.B | Catálogo **compartido por la organización**; SKU y códigos únicos en ella. La activación por empresa se podrá agregar sin migrar productos |
| DEC-007.C | **Unidades base sembradas** (UND, KG, G, L, ML, M, CAJA, PAQ) y **unidades nuevas** creadas por el administrador; sin conversiones por ahora |
| Códigos de barras | **GTIN con dígito verificador** para numéricos de 8, 12, 13 o 14 dígitos; otros códigos, solo formato |

## Decisión

1. **Dominio** (`internal/domain/catalog`): `Unit` (escala de 0 a 6,
   inmutable), `SKU` (1–40 caracteres, mayúsculas), `Barcode` con clave de
   búsqueda (GTIN normalizado a 14 dígitos, así UPC-A y su EAN-13 coinciden),
   `Product` con `Revision` y estado activo/inactivo, y `SearchKey` para
   buscar sin acentos ni mayúsculas igual en ambos motores.
2. **Identidad separada**: el ID UUIDv7 es estable e interno; el SKU es la
   referencia humana, editable y única; los códigos de barras tienen su
   propia tabla y unicidad por organización, aun en productos inactivos.
   Sin SKU se asigna `P-000001…` con un contador transaccional que no
   reutiliza números.
3. **Integridad en la base** (migración 0004): FK compuestas impiden usar
   una unidad de otra organización; triggers impiden cambiar la escala de
   una unidad o la unidad de un producto; `ON DELETE RESTRICT` impide
   borrar productos con códigos. Ediciones concurrentes: `UPDATE … WHERE
   revision = N-1` y error de conflicto si no coincide.
4. **Permisos**: `catalog.product.read` (todas las plantillas),
   `catalog.product.manage` y `catalog.unit.manage` (solo en toda la
   organización; `ADMIN_ORG`). La migración agrega estos permisos a las
   plantillas y las unidades base a las organizaciones existentes, con el
   mismo resultado que `setup` en una nueva (prueba de contrato que revierte
   y reaplica la 0004 y compara).
5. **Autoridad (DEC-006)**: en V1.CLI la instalación es la autoridad de su
   catálogo. Cuando exista la nube (M5), estas mismas operaciones pasarán a
   la administración central y la sucursal recibirá revisiones; `Revision`
   ya existe para eso.
6. **CLI**: `product create|list|show|update` y `unit list|create`. `show` y
   `update` aceptan SKU, código de barras o ID; si la referencia coincide
   con productos distintos (el SKU de uno y el código de otro) se pide el ID.

## Alternativas consideradas

- **SKU obligatorio**: más simple, pero contradice el alta rápida de
  DEC-007.A.
- **Búsqueda con `lower()` del motor**: SQLite solo pliega ASCII, así que
  "Ñandú" no coincidiría igual que en PostgreSQL; se guarda `name_key`
  normalizado en Go.
- **UUID v7 también en la siembra SQL**: los UUID de las unidades sembradas
  por la migración son v4 (`randomblob` / `gen_random_uuid`), suficientes
  como identificadores; las creadas desde Go son v7.

## Consecuencias

- Pendientes: empaques y conversiones, variantes, lotes y seriales,
  categorías, marcas, imágenes, activación por empresa y auditoría de
  cambios de catálogo (`audit_events`, M4).
- El inventario (M2) usará `Unit.Quantity()` para que las cantidades
  respeten los decimales de la unidad del producto.

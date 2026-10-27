-- Catálogo básico (BP-M1-003, DEC-007.A): unidades de medida y productos
-- simples con SKU y códigos de barras, compartidos por la organización
-- (DEC-008.B). El ID interno, el SKU y el código de barras son columnas
-- distintas, cada una con su propia unicidad.

CREATE TABLE units (
    organization_id TEXT    NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    id              TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    code            TEXT    NOT NULL CHECK (length(code) BETWEEN 1 AND 16 AND code = upper(code)),
    name            TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    scale           INTEGER NOT NULL CHECK (scale BETWEEN 0 AND 6),
    created_at_utc  TEXT    NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, code)
) STRICT;

-- La escala de una unidad no cambia: las cantidades guardadas dependen de ella.
CREATE TRIGGER units_scale_immutable BEFORE UPDATE OF scale ON units
WHEN NEW.scale <> OLD.scale
BEGIN
    SELECT RAISE(ABORT, 'la escala de una unidad no se puede cambiar');
END;

CREATE TABLE products (
    organization_id TEXT    NOT NULL,
    id              TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    sku             TEXT    NOT NULL CHECK (length(sku) BETWEEN 1 AND 40 AND sku = upper(sku)),
    name            TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    -- Nombre normalizado (minúsculas, sin acentos) para buscar igual en
    -- ambos motores; lo calcula Go.
    name_key        TEXT    NOT NULL CHECK (length(name_key) BETWEEN 1 AND 120),
    unit_id         TEXT    NOT NULL,
    status          TEXT    NOT NULL CHECK (status IN ('active', 'inactive')),
    revision        INTEGER NOT NULL CHECK (revision >= 1),
    created_at_utc  TEXT    NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    updated_at_utc  TEXT    NOT NULL CHECK (updated_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, sku),
    FOREIGN KEY (organization_id, unit_id) REFERENCES units (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX products_name_idx ON products (organization_id, name_key);

-- La unidad de un producto no cambia (las cantidades registradas dependen de ella).
CREATE TRIGGER products_unit_immutable BEFORE UPDATE OF unit_id ON products
WHEN NEW.unit_id <> OLD.unit_id
BEGIN
    SELECT RAISE(ABORT, 'la unidad de un producto no se puede cambiar');
END;

-- lookup_key es el GTIN normalizado a 14 dígitos (o el código tal cual si no
-- es GTIN): único en la organización aunque el producto esté inactivo.
CREATE TABLE product_barcodes (
    organization_id TEXT NOT NULL,
    product_id      TEXT NOT NULL,
    code            TEXT NOT NULL CHECK (length(code) BETWEEN 1 AND 48),
    lookup_key      TEXT NOT NULL CHECK (length(lookup_key) BETWEEN 1 AND 48),
    PRIMARY KEY (organization_id, lookup_key),
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX product_barcodes_product_idx ON product_barcodes (product_id);

-- Contadores por organización (p. ej. el SKU automático P-000001).
CREATE TABLE catalog_counters (
    organization_id TEXT    NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    name            TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 32),
    last_value      INTEGER NOT NULL CHECK (last_value >= 0),
    PRIMARY KEY (organization_id, name)
) STRICT;

-- Datos para organizaciones existentes (las nuevas los reciben de 'setup'):
-- unidades base de catalog.DefaultUnits() con UUID v4 generados aquí...
INSERT INTO units (organization_id, id, code, name, scale, created_at_utc)
SELECT o.id,
       lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' ||
       substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', 1 + (abs(random()) % 4), 1) ||
       substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6))),
       u.code, u.name, u.scale, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM organizations o
CROSS JOIN (
    SELECT 'UND' AS code, 'Unidad' AS name, 0 AS scale
    UNION ALL SELECT 'KG', 'Kilogramo', 3
    UNION ALL SELECT 'G', 'Gramo', 0
    UNION ALL SELECT 'L', 'Litro', 3
    UNION ALL SELECT 'ML', 'Mililitro', 0
    UNION ALL SELECT 'M', 'Metro', 3
    UNION ALL SELECT 'CAJA', 'Caja', 0
    UNION ALL SELECT 'PAQ', 'Paquete', 0
) u;

-- ...y los permisos nuevos en las plantillas, como en identity.Templates().
INSERT INTO role_permissions (organization_id, role_id, permission)
SELECT r.organization_id, r.id, p.permission
FROM roles r
JOIN (
    SELECT 'ADMIN_ORG' AS code, 'catalog.product.read' AS permission
    UNION ALL SELECT 'ADMIN_ORG', 'catalog.product.manage'
    UNION ALL SELECT 'ADMIN_ORG', 'catalog.unit.manage'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'catalog.product.read'
    UNION ALL SELECT 'ENCARGADO', 'catalog.product.read'
    UNION ALL SELECT 'CAJERO', 'catalog.product.read'
    UNION ALL SELECT 'ALMACENISTA', 'catalog.product.read'
    UNION ALL SELECT 'OPERADOR_TECNICO', 'catalog.product.read'
) p ON p.code = r.code;

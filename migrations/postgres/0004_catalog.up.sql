-- Catálogo básico (BP-M1-003, DEC-007.A): unidades de medida y productos
-- simples con SKU y códigos de barras, compartidos por la organización
-- (DEC-008.B). El ID interno, el SKU y el código de barras son columnas
-- distintas, cada una con su propia unicidad.

CREATE TABLE units (
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    id              UUID        NOT NULL PRIMARY KEY,
    code            TEXT        NOT NULL CHECK (length(code) BETWEEN 1 AND 16 AND code = upper(code)),
    name            TEXT        NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    scale           INTEGER     NOT NULL CHECK (scale BETWEEN 0 AND 6),
    created_at_utc  TIMESTAMPTZ NOT NULL,
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, code)
);

CREATE TABLE products (
    organization_id UUID        NOT NULL,
    id              UUID        NOT NULL PRIMARY KEY,
    sku             TEXT        NOT NULL CHECK (length(sku) BETWEEN 1 AND 40 AND sku = upper(sku)),
    name            TEXT        NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    -- Nombre normalizado (minúsculas, sin acentos) para buscar igual en
    -- ambos motores; lo calcula Go.
    name_key        TEXT        NOT NULL CHECK (length(name_key) BETWEEN 1 AND 120),
    unit_id         UUID        NOT NULL,
    status          TEXT        NOT NULL CHECK (status IN ('active', 'inactive')),
    revision        BIGINT      NOT NULL CHECK (revision >= 1),
    created_at_utc  TIMESTAMPTZ NOT NULL,
    updated_at_utc  TIMESTAMPTZ NOT NULL,
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, sku),
    FOREIGN KEY (organization_id, unit_id) REFERENCES units (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX products_name_idx ON products (organization_id, name_key);

-- La escala de una unidad y la unidad de un producto no cambian: las
-- cantidades guardadas dependen de ellas.
CREATE FUNCTION catalog_immutable_columns() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'units' AND NEW.scale <> OLD.scale THEN
        RAISE EXCEPTION 'la escala de una unidad no se puede cambiar';
    END IF;
    IF TG_TABLE_NAME = 'products' AND NEW.unit_id <> OLD.unit_id THEN
        RAISE EXCEPTION 'la unidad de un producto no se puede cambiar';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER units_scale_immutable BEFORE UPDATE OF scale ON units
    FOR EACH ROW EXECUTE FUNCTION catalog_immutable_columns();
CREATE TRIGGER products_unit_immutable BEFORE UPDATE OF unit_id ON products
    FOR EACH ROW EXECUTE FUNCTION catalog_immutable_columns();

-- lookup_key es el GTIN normalizado a 14 dígitos (o el código tal cual si no
-- es GTIN): único en la organización aunque el producto esté inactivo.
CREATE TABLE product_barcodes (
    organization_id UUID NOT NULL,
    product_id      UUID NOT NULL,
    code            TEXT NOT NULL CHECK (length(code) BETWEEN 1 AND 48),
    lookup_key      TEXT NOT NULL CHECK (length(lookup_key) BETWEEN 1 AND 48),
    PRIMARY KEY (organization_id, lookup_key),
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX product_barcodes_product_idx ON product_barcodes (product_id);

-- Contadores por organización (p. ej. el SKU automático P-000001).
CREATE TABLE catalog_counters (
    organization_id UUID    NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    name            TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 32),
    last_value      BIGINT  NOT NULL CHECK (last_value >= 0),
    PRIMARY KEY (organization_id, name)
);

-- Datos para organizaciones existentes (las nuevas los reciben de 'setup'):
-- unidades base de catalog.DefaultUnits()...
INSERT INTO units (organization_id, id, code, name, scale, created_at_utc)
SELECT o.id, gen_random_uuid(), u.code, u.name, u.scale, now()
FROM organizations o
CROSS JOIN (VALUES
    ('UND', 'Unidad', 0), ('KG', 'Kilogramo', 3), ('G', 'Gramo', 0), ('L', 'Litro', 3),
    ('ML', 'Mililitro', 0), ('M', 'Metro', 3), ('CAJA', 'Caja', 0), ('PAQ', 'Paquete', 0)
) AS u (code, name, scale);

-- ...y los permisos nuevos en las plantillas, como en identity.Templates().
INSERT INTO role_permissions (organization_id, role_id, permission)
SELECT r.organization_id, r.id, p.permission
FROM roles r
JOIN (VALUES
    ('ADMIN_ORG', 'catalog.product.read'), ('ADMIN_ORG', 'catalog.product.manage'), ('ADMIN_ORG', 'catalog.unit.manage'),
    ('ADMIN_EMPRESA', 'catalog.product.read'), ('ENCARGADO', 'catalog.product.read'), ('CAJERO', 'catalog.product.read'),
    ('ALMACENISTA', 'catalog.product.read'), ('OPERADOR_TECNICO', 'catalog.product.read')
) AS p (code, permission) ON p.code = r.code;

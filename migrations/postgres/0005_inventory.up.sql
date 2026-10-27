-- Existencias y kardex (BP-M2-001, DEC-007.F, secciones 23.2 y 24.3).
-- El kardex es inmutable y la fuente de verdad; el saldo es su proyección.
-- Las cantidades son NUMERIC exactos con hasta 6 decimales (escala máxima
-- de una unidad).

CREATE TABLE inventory_balances (
    organization_id UUID          NOT NULL,
    company_id      UUID          NOT NULL,
    branch_id       UUID          NOT NULL,
    warehouse_id    UUID          NOT NULL,
    product_id      UUID          NOT NULL,
    on_hand         NUMERIC(28,6) NOT NULL,
    reserved        NUMERIC(28,6) NOT NULL CHECK (reserved >= 0),
    blocked         NUMERIC(28,6) NOT NULL CHECK (blocked >= 0),
    version         BIGINT        NOT NULL CHECK (version >= 1),
    updated_at_utc  TIMESTAMPTZ   NOT NULL,
    PRIMARY KEY (organization_id, warehouse_id, product_id),
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX inventory_balances_product_idx ON inventory_balances (organization_id, product_id);

CREATE TABLE inventory_movements (
    organization_id UUID          NOT NULL,
    id              UUID          NOT NULL PRIMARY KEY,
    company_id      UUID          NOT NULL,
    branch_id       UUID          NOT NULL,
    warehouse_id    UUID          NOT NULL,
    product_id      UUID          NOT NULL,
    sequence        BIGINT        NOT NULL CHECK (sequence >= 1),
    kind            TEXT          NOT NULL CHECK (kind IN ('adjustment_in', 'adjustment_out', 'reversal')),
    reason          TEXT          NOT NULL CHECK (reason IN ('initial_load', 'correction', 'shrinkage', 'internal_use', 'other', 'reversal')),
    quantity        NUMERIC(28,6) NOT NULL CHECK (quantity <> 0),
    on_hand_after   NUMERIC(28,6) NOT NULL,
    note            TEXT          NOT NULL CHECK (length(note) <= 500),
    actor_id        UUID          NOT NULL,
    occurred_at_utc TIMESTAMPTZ   NOT NULL,
    reverses_id     UUID,
    idempotency_key TEXT          CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 100),
    request_hash    TEXT          NOT NULL CHECK (length(request_hash) = 64),
    CHECK ((kind = 'reversal') = (reason = 'reversal')),
    CHECK ((kind = 'reversal') = (reverses_id IS NOT NULL)),
    CHECK (kind <> 'adjustment_in' OR quantity > 0),
    CHECK (kind <> 'adjustment_out' OR quantity < 0),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, warehouse_id, product_id, sequence),
    FOREIGN KEY (organization_id, warehouse_id, product_id) REFERENCES inventory_balances (organization_id, warehouse_id, product_id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, actor_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, reverses_id) REFERENCES inventory_movements (organization_id, id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX inventory_movements_key_uq ON inventory_movements (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX inventory_movements_reverses_uq ON inventory_movements (organization_id, reverses_id) WHERE reverses_id IS NOT NULL;

CREATE FUNCTION inventory_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'inventory_balances' THEN
        IF TG_OP = 'DELETE' THEN
            RAISE EXCEPTION 'los saldos de inventario no se borran';
        END IF;
        IF NEW.version <> OLD.version + 1 THEN
            RAISE EXCEPTION 'el saldo debe avanzar una versión por movimiento';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'el kardex es de solo inserción';
    END IF;
    -- Un movimiento nuevo debe corresponder a la versión y existencia que el
    -- saldo ya registra (se actualiza antes en la misma transacción).
    IF NOT EXISTS (
        SELECT 1 FROM inventory_balances b
        WHERE b.organization_id = NEW.organization_id AND b.warehouse_id = NEW.warehouse_id AND b.product_id = NEW.product_id
          AND b.version = NEW.sequence AND b.on_hand = NEW.on_hand_after
    ) THEN
        RAISE EXCEPTION 'el movimiento no coincide con el saldo';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER inventory_balances_guard BEFORE UPDATE OR DELETE ON inventory_balances
    FOR EACH ROW EXECUTE FUNCTION inventory_guard();
CREATE TRIGGER inventory_movements_guard BEFORE INSERT OR UPDATE OR DELETE ON inventory_movements
    FOR EACH ROW EXECUTE FUNCTION inventory_guard();
CREATE TRIGGER inventory_movements_no_truncate BEFORE TRUNCATE ON inventory_movements
    FOR EACH STATEMENT EXECUTE FUNCTION inventory_guard();

-- Permisos nuevos en las plantillas, como en identity.Templates().
INSERT INTO role_permissions (organization_id, role_id, permission)
SELECT r.organization_id, r.id, p.permission
FROM roles r
JOIN (VALUES
    ('ADMIN_ORG', 'inventory.stock.read'), ('ADMIN_ORG', 'inventory.stock.adjust'),
    ('ADMIN_EMPRESA', 'inventory.stock.read'), ('ADMIN_EMPRESA', 'inventory.stock.adjust'),
    ('ENCARGADO', 'inventory.stock.read'), ('ENCARGADO', 'inventory.stock.adjust'),
    ('ALMACENISTA', 'inventory.stock.read'), ('ALMACENISTA', 'inventory.stock.adjust'),
    ('CAJERO', 'inventory.stock.read'), ('OPERADOR_TECNICO', 'inventory.stock.read')
) AS p (code, permission) ON p.code = r.code;

-- Existencias y kardex (BP-M2-001, DEC-007.F, secciones 23.2 y 24.3).
-- El kardex es inmutable y la fuente de verdad; el saldo es su proyección.
-- Las cantidades se guardan como texto decimal exacto (sección 25.6); Go las
-- calcula, nunca SQLite.

CREATE TABLE inventory_balances (
    organization_id TEXT    NOT NULL,
    company_id      TEXT    NOT NULL,
    branch_id       TEXT    NOT NULL,
    warehouse_id    TEXT    NOT NULL,
    product_id      TEXT    NOT NULL,
    on_hand         TEXT    NOT NULL CHECK (length(on_hand) BETWEEN 1 AND 40 AND on_hand NOT GLOB '*[^0-9.-]*'),
    reserved        TEXT    NOT NULL CHECK (length(reserved) BETWEEN 1 AND 40 AND reserved NOT GLOB '*[^0-9.]*'),
    blocked         TEXT    NOT NULL CHECK (length(blocked) BETWEEN 1 AND 40 AND blocked NOT GLOB '*[^0-9.]*'),
    version         INTEGER NOT NULL CHECK (version >= 1),
    updated_at_utc  TEXT    NOT NULL CHECK (updated_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    PRIMARY KEY (organization_id, warehouse_id, product_id),
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX inventory_balances_product_idx ON inventory_balances (organization_id, product_id);

-- Cada cambio de saldo avanza exactamente una versión, y los saldos no se borran.
CREATE TRIGGER inventory_balances_version BEFORE UPDATE ON inventory_balances
WHEN NEW.version <> OLD.version + 1
BEGIN
    SELECT RAISE(ABORT, 'el saldo debe avanzar una versión por movimiento');
END;

CREATE TRIGGER inventory_balances_no_delete BEFORE DELETE ON inventory_balances
BEGIN
    SELECT RAISE(ABORT, 'los saldos de inventario no se borran');
END;

CREATE TABLE inventory_movements (
    organization_id TEXT    NOT NULL,
    id              TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    company_id      TEXT    NOT NULL,
    branch_id       TEXT    NOT NULL,
    warehouse_id    TEXT    NOT NULL,
    product_id      TEXT    NOT NULL,
    sequence        INTEGER NOT NULL CHECK (sequence >= 1),
    kind            TEXT    NOT NULL CHECK (kind IN ('adjustment_in', 'adjustment_out', 'reversal')),
    reason          TEXT    NOT NULL CHECK (reason IN ('initial_load', 'correction', 'shrinkage', 'internal_use', 'other', 'reversal')),
    quantity        TEXT    NOT NULL CHECK (length(quantity) BETWEEN 1 AND 40 AND quantity NOT GLOB '*[^0-9.-]*'),
    on_hand_after   TEXT    NOT NULL CHECK (length(on_hand_after) BETWEEN 1 AND 40 AND on_hand_after NOT GLOB '*[^0-9.-]*'),
    note            TEXT    NOT NULL CHECK (length(note) <= 500),
    actor_id        TEXT    NOT NULL,
    occurred_at_utc TEXT    NOT NULL CHECK (occurred_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    reverses_id     TEXT,
    idempotency_key TEXT    CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 100),
    request_hash    TEXT    NOT NULL CHECK (length(request_hash) = 64),
    CHECK ((kind = 'reversal') = (reason = 'reversal')),
    CHECK ((kind = 'reversal') = (reverses_id IS NOT NULL)),
    CHECK (kind <> 'adjustment_in' OR quantity NOT GLOB '-*'),
    CHECK (kind <> 'adjustment_out' OR quantity GLOB '-*'),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, warehouse_id, product_id, sequence),
    FOREIGN KEY (organization_id, warehouse_id, product_id) REFERENCES inventory_balances (organization_id, warehouse_id, product_id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, actor_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, reverses_id) REFERENCES inventory_movements (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE UNIQUE INDEX inventory_movements_key_uq ON inventory_movements (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX inventory_movements_reverses_uq ON inventory_movements (organization_id, reverses_id) WHERE reverses_id IS NOT NULL;

-- Un movimiento nuevo debe corresponder a la versión y existencia que el
-- saldo ya registra (se actualiza antes en la misma transacción).
CREATE TRIGGER inventory_movements_match_balance BEFORE INSERT ON inventory_movements
WHEN NOT EXISTS (
    SELECT 1 FROM inventory_balances b
    WHERE b.organization_id = NEW.organization_id AND b.warehouse_id = NEW.warehouse_id AND b.product_id = NEW.product_id
      AND b.version = NEW.sequence AND b.on_hand = NEW.on_hand_after
)
BEGIN
    SELECT RAISE(ABORT, 'el movimiento no coincide con el saldo');
END;

-- Kardex de solo inserción.
CREATE TRIGGER inventory_movements_no_update BEFORE UPDATE ON inventory_movements
BEGIN
    SELECT RAISE(ABORT, 'el kardex es de solo inserción');
END;

CREATE TRIGGER inventory_movements_no_delete BEFORE DELETE ON inventory_movements
BEGIN
    SELECT RAISE(ABORT, 'el kardex es de solo inserción');
END;

-- Permisos nuevos en las plantillas, como en identity.Templates().
INSERT INTO role_permissions (organization_id, role_id, permission)
SELECT r.organization_id, r.id, p.permission
FROM roles r
JOIN (
    SELECT 'ADMIN_ORG' AS code, 'inventory.stock.read' AS permission
    UNION ALL SELECT 'ADMIN_ORG', 'inventory.stock.adjust'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'inventory.stock.read'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'inventory.stock.adjust'
    UNION ALL SELECT 'ENCARGADO', 'inventory.stock.read'
    UNION ALL SELECT 'ENCARGADO', 'inventory.stock.adjust'
    UNION ALL SELECT 'ALMACENISTA', 'inventory.stock.read'
    UNION ALL SELECT 'ALMACENISTA', 'inventory.stock.adjust'
    UNION ALL SELECT 'CAJERO', 'inventory.stock.read'
    UNION ALL SELECT 'OPERADOR_TECNICO', 'inventory.stock.read'
) p ON p.code = r.code;

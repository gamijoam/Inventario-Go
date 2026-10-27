-- Revierte 0007. Si hay movimientos o saldos que solo admite la 0007
-- (despachos de reservas, conteos, déficit), los CHECK anteriores los
-- rechazan y la reversión falla sin cambios.
DELETE FROM role_permissions WHERE permission IN ('inventory.reservation.manage', 'inventory.block.manage', 'inventory.count.manage',
    'inventory.count.approve', 'inventory.policy.manage', 'inventory.negative_stock.override');

PRAGMA legacy_alter_table = ON;

-- Como en la subida: renombrar ambas primero y borrar las viejas al final.
ALTER TABLE inventory_movements RENAME TO inventory_movements_v7;
ALTER TABLE inventory_balances RENAME TO inventory_balances_v7;

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
    value           TEXT    CHECK (value IS NULL OR (length(value) BETWEEN 1 AND 40 AND value NOT GLOB '*[^0-9.]*' AND (on_hand <> '0' OR value = '0'))),
    PRIMARY KEY (organization_id, warehouse_id, product_id),
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT
) STRICT;

INSERT INTO inventory_balances SELECT * FROM inventory_balances_v7;

CREATE TABLE inventory_movements (
    organization_id  TEXT    NOT NULL,
    id               TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    company_id       TEXT    NOT NULL,
    branch_id        TEXT    NOT NULL,
    warehouse_id     TEXT    NOT NULL,
    product_id       TEXT    NOT NULL,
    sequence         INTEGER NOT NULL CHECK (sequence >= 1),
    kind             TEXT    NOT NULL CHECK (kind IN ('adjustment_in', 'adjustment_out', 'reversal', 'transfer_out', 'transfer_in')),
    reason           TEXT    NOT NULL CHECK (reason IN ('initial_load', 'correction', 'shrinkage', 'internal_use', 'other', 'reversal', 'transfer')),
    quantity         TEXT    NOT NULL CHECK (length(quantity) BETWEEN 1 AND 40 AND quantity NOT GLOB '*[^0-9.-]*'),
    on_hand_after    TEXT    NOT NULL CHECK (length(on_hand_after) BETWEEN 1 AND 40 AND on_hand_after NOT GLOB '*[^0-9.-]*'),
    note             TEXT    NOT NULL CHECK (length(note) <= 500),
    actor_id         TEXT    NOT NULL,
    occurred_at_utc  TEXT    NOT NULL CHECK (occurred_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    reverses_id      TEXT,
    idempotency_key  TEXT    CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 100),
    request_hash     TEXT    NOT NULL CHECK (length(request_hash) = 64),
    unit_cost        TEXT    CHECK (unit_cost IS NULL OR (length(unit_cost) BETWEEN 1 AND 60 AND unit_cost NOT GLOB '*[^0-9.]*')),
    cost             TEXT    CHECK (cost IS NULL OR (length(cost) BETWEEN 1 AND 40 AND cost NOT GLOB '*[^0-9.-]*')),
    value_adjustment TEXT    NOT NULL DEFAULT '0' CHECK (length(value_adjustment) BETWEEN 1 AND 40 AND value_adjustment NOT GLOB '*[^0-9.-]*'),
    value_after      TEXT    CHECK (value_after IS NULL OR (length(value_after) BETWEEN 1 AND 40 AND value_after NOT GLOB '*[^0-9.]*')),
    transfer_id      TEXT,
    CHECK ((kind = 'reversal') = (reason = 'reversal')),
    CHECK ((kind = 'reversal') = (reverses_id IS NOT NULL)),
    CHECK ((kind IN ('transfer_out', 'transfer_in')) = (reason = 'transfer')),
    CHECK ((kind IN ('transfer_out', 'transfer_in')) = (transfer_id IS NOT NULL)),
    CHECK (kind NOT IN ('adjustment_in', 'transfer_in') OR quantity NOT GLOB '-*'),
    CHECK (kind NOT IN ('adjustment_out', 'transfer_out') OR quantity GLOB '-*'),
    CHECK ((unit_cost IS NULL) = (cost IS NULL)),
    -- El costo lleva el signo de la cantidad.
    CHECK (cost IS NULL OR cost = '0' OR (cost GLOB '-*') = (quantity GLOB '-*')),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, warehouse_id, product_id, sequence),
    FOREIGN KEY (organization_id, warehouse_id, product_id) REFERENCES inventory_balances (organization_id, warehouse_id, product_id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, actor_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, reverses_id) REFERENCES inventory_movements (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, transfer_id) REFERENCES inventory_transfers (organization_id, id) ON DELETE RESTRICT
) STRICT;

INSERT INTO inventory_movements
SELECT organization_id, id, company_id, branch_id, warehouse_id, product_id, sequence, kind, reason, quantity,
    on_hand_after, note, actor_id, occurred_at_utc, reverses_id, idempotency_key, request_hash, unit_cost, cost, value_adjustment, value_after, transfer_id
FROM inventory_movements_v7 ORDER BY rowid;

DROP TABLE inventory_movements_v7;
DROP TABLE inventory_count_lines;
DROP TABLE inventory_counts;
DROP TABLE inventory_blocks;
DROP TABLE inventory_reservations;
DROP TABLE inventory_policy_changes;
DROP TABLE inventory_balances_v7;

PRAGMA legacy_alter_table = OFF;

CREATE INDEX inventory_balances_product_idx ON inventory_balances (organization_id, product_id);

CREATE TRIGGER inventory_balances_version BEFORE UPDATE ON inventory_balances
WHEN NEW.version <> OLD.version + 1
BEGIN
    SELECT RAISE(ABORT, 'el saldo debe avanzar una versión por movimiento');
END;

CREATE TRIGGER inventory_balances_no_delete BEFORE DELETE ON inventory_balances
BEGIN
    SELECT RAISE(ABORT, 'los saldos de inventario no se borran');
END;

CREATE UNIQUE INDEX inventory_movements_key_uq ON inventory_movements (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX inventory_movements_reverses_uq ON inventory_movements (organization_id, reverses_id) WHERE reverses_id IS NOT NULL;
-- Una salida y una entrada por transferencia.
CREATE UNIQUE INDEX inventory_movements_transfer_uq ON inventory_movements (organization_id, transfer_id, kind) WHERE transfer_id IS NOT NULL;

-- Un movimiento nuevo debe corresponder a la versión, existencia y valor
-- que el saldo ya registra (se actualiza antes en la misma transacción).
CREATE TRIGGER inventory_movements_match_balance BEFORE INSERT ON inventory_movements
WHEN NOT EXISTS (
    SELECT 1 FROM inventory_balances b
    WHERE b.organization_id = NEW.organization_id AND b.warehouse_id = NEW.warehouse_id AND b.product_id = NEW.product_id
      AND b.version = NEW.sequence AND b.on_hand = NEW.on_hand_after AND b.value IS NEW.value_after
)
BEGIN
    SELECT RAISE(ABORT, 'el movimiento no coincide con el saldo');
END;

CREATE TRIGGER inventory_movements_no_update BEFORE UPDATE ON inventory_movements
BEGIN
    SELECT RAISE(ABORT, 'el kardex es de solo inserción');
END;

CREATE TRIGGER inventory_movements_no_delete BEFORE DELETE ON inventory_movements
BEGIN
    SELECT RAISE(ABORT, 'el kardex es de solo inserción');
END;

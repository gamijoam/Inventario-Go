-- Revierte 0008. Si ya hay entradas de compra o revaloraciones en el
-- kardex, el CHECK anterior las rechaza y la reversión falla sin cambios.
DELETE FROM role_permissions WHERE permission IN ('purchase.read', 'purchase.manage', 'purchase.receipt.confirm', 'purchase.replacement_cost.manage');

-- Primero las tablas de compras, que referencian el kardex.
DROP TABLE replacement_costs;
DROP TABLE purchase_expense_allocations;
DROP TABLE purchase_expenses;
DROP TABLE purchase_receipt_lines;
DROP TABLE purchase_receipts;
DROP TABLE purchase_order_lines;
DROP TABLE purchase_orders;
DROP TABLE suppliers;

PRAGMA legacy_alter_table = ON;

ALTER TABLE inventory_movements RENAME TO inventory_movements_v8;

CREATE TABLE inventory_movements (
    organization_id  TEXT    NOT NULL,
    id               TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    company_id       TEXT    NOT NULL,
    branch_id        TEXT    NOT NULL,
    warehouse_id     TEXT    NOT NULL,
    product_id       TEXT    NOT NULL,
    sequence         INTEGER NOT NULL CHECK (sequence >= 1),
    kind             TEXT    NOT NULL CHECK (kind IN ('adjustment_in', 'adjustment_out', 'reversal', 'transfer_out', 'transfer_in', 'reservation_out', 'count_in', 'count_out')),
    reason           TEXT    NOT NULL CHECK (reason IN ('initial_load', 'correction', 'shrinkage', 'internal_use', 'other', 'reversal', 'transfer', 'reservation', 'count')),
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
    -- Negativo solo en déficit.
    value_after      TEXT    CHECK (value_after IS NULL OR (length(value_after) BETWEEN 1 AND 40 AND value_after NOT GLOB '*[^0-9.-]*' AND
                         (value_after NOT GLOB '-*' OR on_hand_after GLOB '-*'))),
    transfer_id      TEXT,
    reservation_id   TEXT,
    block_id         TEXT,
    count_id         TEXT,
    policy_change_id TEXT,
    CHECK ((kind = 'reversal') = (reason = 'reversal')),
    CHECK ((kind = 'reversal') = (reverses_id IS NOT NULL)),
    CHECK ((kind IN ('transfer_out', 'transfer_in')) = (reason = 'transfer')),
    CHECK ((kind IN ('transfer_out', 'transfer_in')) = (transfer_id IS NOT NULL)),
    CHECK ((kind = 'reservation_out') = (reason = 'reservation')),
    CHECK ((kind = 'reservation_out') = (reservation_id IS NOT NULL)),
    CHECK ((kind IN ('count_in', 'count_out')) = (reason = 'count')),
    CHECK ((kind IN ('count_in', 'count_out')) = (count_id IS NOT NULL)),
    -- Merma de mercancía bloqueada y salida en déficit: salidas de ajuste.
    CHECK (block_id IS NULL OR kind = 'adjustment_out'),
    CHECK (policy_change_id IS NULL OR (kind = 'adjustment_out' AND on_hand_after GLOB '-*')),
    CHECK (kind NOT IN ('adjustment_in', 'transfer_in', 'count_in') OR quantity NOT GLOB '-*'),
    CHECK (kind NOT IN ('adjustment_out', 'transfer_out', 'reservation_out', 'count_out') OR quantity GLOB '-*'),
    CHECK ((unit_cost IS NULL) = (cost IS NULL)),
    -- El costo lleva el signo de la cantidad.
    CHECK (cost IS NULL OR cost = '0' OR (cost GLOB '-*') = (quantity GLOB '-*')),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, warehouse_id, product_id, sequence),
    FOREIGN KEY (organization_id, warehouse_id, product_id) REFERENCES inventory_balances (organization_id, warehouse_id, product_id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, actor_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, reverses_id) REFERENCES inventory_movements (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, transfer_id) REFERENCES inventory_transfers (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, reservation_id) REFERENCES inventory_reservations (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, block_id) REFERENCES inventory_blocks (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, count_id) REFERENCES inventory_counts (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, policy_change_id) REFERENCES inventory_policy_changes (organization_id, id) ON DELETE RESTRICT
) STRICT;

INSERT INTO inventory_movements
SELECT organization_id, id, company_id, branch_id, warehouse_id, product_id, sequence, kind, reason, quantity, on_hand_after, note, actor_id, occurred_at_utc, reverses_id, idempotency_key, request_hash, unit_cost, cost, value_adjustment, value_after, transfer_id, reservation_id, block_id, count_id, policy_change_id
FROM inventory_movements_v8 ORDER BY rowid;

DROP TABLE inventory_movements_v8;

PRAGMA legacy_alter_table = OFF;

CREATE UNIQUE INDEX inventory_movements_key_uq ON inventory_movements (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX inventory_movements_reverses_uq ON inventory_movements (organization_id, reverses_id) WHERE reverses_id IS NOT NULL;
CREATE UNIQUE INDEX inventory_movements_transfer_uq ON inventory_movements (organization_id, transfer_id, kind) WHERE transfer_id IS NOT NULL;
-- Una merma por bloqueo y un ajuste por producto y conteo.
CREATE UNIQUE INDEX inventory_movements_block_uq ON inventory_movements (organization_id, block_id) WHERE block_id IS NOT NULL;
CREATE UNIQUE INDEX inventory_movements_count_uq ON inventory_movements (organization_id, count_id, product_id) WHERE count_id IS NOT NULL;
CREATE INDEX inventory_movements_reservation_idx ON inventory_movements (organization_id, reservation_id) WHERE reservation_id IS NOT NULL;

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

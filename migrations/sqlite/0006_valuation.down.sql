-- Revierte 0006. Si ya hay transferencias, el CHECK del kardex anterior
-- rechaza sus movimientos y la reversión falla sin cambios: no se pierden
-- datos en silencio.
DELETE FROM role_permissions WHERE permission = 'inventory.transfer.create';

PRAGMA legacy_alter_table = ON;

ALTER TABLE inventory_movements RENAME TO inventory_movements_v6;

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

INSERT INTO inventory_movements
SELECT organization_id, id, company_id, branch_id, warehouse_id, product_id, sequence, kind, reason, quantity,
    on_hand_after, note, actor_id, occurred_at_utc, reverses_id, idempotency_key, request_hash
FROM inventory_movements_v6 ORDER BY rowid;

DROP TABLE inventory_movements_v6;

PRAGMA legacy_alter_table = OFF;

CREATE UNIQUE INDEX inventory_movements_key_uq ON inventory_movements (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX inventory_movements_reverses_uq ON inventory_movements (organization_id, reverses_id) WHERE reverses_id IS NOT NULL;

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

DROP TABLE inventory_transfers;

ALTER TABLE inventory_balances DROP COLUMN value;

DROP TRIGGER companies_valuation_currency_fixed;
ALTER TABLE companies DROP COLUMN valuation_currency;

-- Costo promedio ponderado por producto y almacén y transferencias entre
-- almacenes de una sucursal (BP-M2-002, DEC-007.E2, secciones 21.1 y 25.8).
-- Los importes se guardan como texto decimal exacto; Go los calcula.

-- Moneda de valoración del inventario de cada empresa; no cambia.
ALTER TABLE companies ADD COLUMN valuation_currency TEXT NOT NULL DEFAULT 'USD' CHECK (valuation_currency IN ('USD', 'VES'));

CREATE TRIGGER companies_valuation_currency_fixed BEFORE UPDATE OF valuation_currency ON companies
WHEN NEW.valuation_currency IS NOT OLD.valuation_currency
BEGIN
    SELECT RAISE(ABORT, 'la moneda de valoración de una empresa no cambia');
END;

-- Valor total de la existencia. NULL: existencia cargada antes de esta
-- versión que ninguna entrada con costo ha valorado. Sin existencia no hay
-- valor.
ALTER TABLE inventory_balances ADD COLUMN value TEXT
    CHECK (value IS NULL OR (length(value) BETWEEN 1 AND 40 AND value NOT GLOB '*[^0-9.]*' AND (on_hand <> '0' OR value = '0')));

CREATE TABLE inventory_transfers (
    organization_id   TEXT NOT NULL,
    id                TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    company_id        TEXT NOT NULL,
    branch_id         TEXT NOT NULL,
    from_warehouse_id TEXT NOT NULL,
    to_warehouse_id   TEXT NOT NULL,
    product_id        TEXT NOT NULL,
    quantity          TEXT NOT NULL CHECK (length(quantity) BETWEEN 1 AND 40 AND quantity NOT GLOB '*[^0-9.]*' AND quantity <> '0'),
    value             TEXT NOT NULL CHECK (length(value) BETWEEN 1 AND 40 AND value NOT GLOB '*[^0-9.]*'),
    note              TEXT NOT NULL CHECK (length(note) <= 500),
    actor_id          TEXT NOT NULL,
    occurred_at_utc   TEXT NOT NULL CHECK (occurred_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    idempotency_key   TEXT CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 100),
    request_hash      TEXT NOT NULL CHECK (length(request_hash) = 64),
    CHECK (from_warehouse_id <> to_warehouse_id),
    UNIQUE (organization_id, id),
    -- Ambos almacenes cuelgan de la misma sucursal.
    FOREIGN KEY (organization_id, company_id, branch_id, from_warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id, branch_id, to_warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, actor_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE UNIQUE INDEX inventory_transfers_key_uq ON inventory_transfers (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TRIGGER inventory_transfers_no_update BEFORE UPDATE ON inventory_transfers
BEGIN
    SELECT RAISE(ABORT, 'las transferencias no se modifican');
END;

CREATE TRIGGER inventory_transfers_no_delete BEFORE DELETE ON inventory_transfers
BEGIN
    SELECT RAISE(ABORT, 'las transferencias no se modifican');
END;

-- El kardex admite nuevos tipos y columnas de valoración. SQLite no altera
-- un CHECK: se reconstruye la tabla. Con legacy_alter_table el renombre no
-- reescribe las referencias, y la tabla nueva nace con el nombre definitivo
-- para que su clave foránea a sí misma quede bien.
PRAGMA legacy_alter_table = ON;

ALTER TABLE inventory_movements RENAME TO inventory_movements_v5;

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

-- Los movimientos anteriores no tienen costo: quedan sin valorar.
INSERT INTO inventory_movements (organization_id, id, company_id, branch_id, warehouse_id, product_id, sequence, kind, reason, quantity,
    on_hand_after, note, actor_id, occurred_at_utc, reverses_id, idempotency_key, request_hash)
SELECT organization_id, id, company_id, branch_id, warehouse_id, product_id, sequence, kind, reason, quantity,
    on_hand_after, note, actor_id, occurred_at_utc, reverses_id, idempotency_key, request_hash
FROM inventory_movements_v5 ORDER BY rowid;

-- Borra también sus índices y disparadores.
DROP TABLE inventory_movements_v5;

PRAGMA legacy_alter_table = OFF;

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

-- Permiso nuevo en las plantillas, como en identity.Templates().
INSERT INTO role_permissions (organization_id, role_id, permission)
SELECT r.organization_id, r.id, 'inventory.transfer.create'
FROM roles r
WHERE r.code IN ('ADMIN_ORG', 'ADMIN_EMPRESA', 'ENCARGADO', 'ALMACENISTA');

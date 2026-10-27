-- Compras: proveedores, órdenes, recepciones, gastos atribuibles y costo de
-- reposición (BP-M3-001, secciones 19.2–19.4). Importes en la moneda de
-- valoración de la empresa, como texto decimal exacto.

CREATE TABLE suppliers (
    organization_id TEXT NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    id              TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    code            TEXT NOT NULL CHECK (length(code) BETWEEN 1 AND 32),
    name            TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    rif             TEXT CHECK (rif IS NULL OR length(rif) = 12),
    status          TEXT NOT NULL CHECK (status IN ('active', 'inactive')),
    created_at_utc  TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, code)
) STRICT;

CREATE UNIQUE INDEX suppliers_rif_uq ON suppliers (organization_id, rif) WHERE rif IS NOT NULL;

CREATE TABLE purchase_orders (
    organization_id TEXT NOT NULL,
    id              TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    company_id      TEXT NOT NULL,
    branch_id       TEXT NOT NULL,
    number          TEXT NOT NULL CHECK (length(number) BETWEEN 1 AND 32),
    supplier_id     TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('open', 'received', 'cancelled')),
    note            TEXT NOT NULL CHECK (length(note) <= 800),
    created_by      TEXT NOT NULL,
    created_at_utc  TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    closed_by       TEXT,
    closed_at_utc   TEXT CHECK (closed_at_utc IS NULL OR closed_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    CHECK ((status = 'cancelled') = (closed_by IS NOT NULL)),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, number),
    FOREIGN KEY (organization_id, company_id, branch_id) REFERENCES branches (organization_id, company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, supplier_id) REFERENCES suppliers (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, closed_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE TABLE purchase_order_lines (
    organization_id TEXT    NOT NULL,
    order_id        TEXT    NOT NULL,
    line_no         INTEGER NOT NULL CHECK (line_no >= 1),
    product_id      TEXT    NOT NULL,
    quantity        TEXT    NOT NULL CHECK (length(quantity) BETWEEN 1 AND 40 AND quantity NOT GLOB '*[^0-9.]*' AND quantity <> '0'),
    unit_cost       TEXT    NOT NULL CHECK (length(unit_cost) BETWEEN 1 AND 60 AND unit_cost NOT GLOB '*[^0-9.]*'),
    received        TEXT    NOT NULL CHECK (length(received) BETWEEN 1 AND 40 AND received NOT GLOB '*[^0-9.]*'),
    PRIMARY KEY (organization_id, order_id, line_no),
    UNIQUE (organization_id, order_id, product_id),
    FOREIGN KEY (organization_id, order_id) REFERENCES purchase_orders (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE TABLE purchase_receipts (
    organization_id   TEXT NOT NULL,
    id                TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    company_id        TEXT NOT NULL,
    branch_id         TEXT NOT NULL,
    warehouse_id      TEXT NOT NULL,
    number            TEXT NOT NULL CHECK (length(number) BETWEEN 1 AND 32),
    supplier_id       TEXT NOT NULL,
    order_id          TEXT,
    status            TEXT NOT NULL CHECK (status IN ('draft', 'confirmed', 'cancelled')),
    supplier_document TEXT NOT NULL CHECK (length(supplier_document) <= 800),
    note              TEXT NOT NULL CHECK (length(note) <= 800),
    created_by        TEXT NOT NULL,
    created_at_utc    TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    closed_by         TEXT,
    closed_at_utc     TEXT CHECK (closed_at_utc IS NULL OR closed_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    CHECK ((status = 'draft') = (closed_by IS NULL)),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, number),
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, supplier_id) REFERENCES suppliers (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, order_id) REFERENCES purchase_orders (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, closed_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX purchase_receipts_order_idx ON purchase_receipts (organization_id, order_id) WHERE order_id IS NOT NULL;

-- El kardex admite entradas de compra y revaloraciones por gastos tardíos.
-- Como en la 0006 y la 0007, SQLite reconstruye la tabla: después de crear
-- las recepciones, a las que apunta, y antes de las tablas que apuntan al
-- kardex, porque renombrarlo reescribiría sus referencias.
PRAGMA legacy_alter_table = ON;

ALTER TABLE inventory_movements RENAME TO inventory_movements_v7;

CREATE TABLE inventory_movements (
    organization_id  TEXT    NOT NULL,
    id               TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    company_id       TEXT    NOT NULL,
    branch_id        TEXT    NOT NULL,
    warehouse_id     TEXT    NOT NULL,
    product_id       TEXT    NOT NULL,
    sequence         INTEGER NOT NULL CHECK (sequence >= 1),
    kind             TEXT    NOT NULL CHECK (kind IN ('adjustment_in', 'adjustment_out', 'reversal', 'transfer_out', 'transfer_in', 'reservation_out', 'count_in', 'count_out', 'purchase_in', 'revaluation')),
    reason           TEXT    NOT NULL CHECK (reason IN ('initial_load', 'correction', 'shrinkage', 'internal_use', 'other', 'reversal', 'transfer', 'reservation', 'count', 'purchase', 'purchase_expense')),
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
    receipt_id       TEXT,
    CHECK ((kind = 'reversal') = (reason = 'reversal')),
    CHECK ((kind = 'reversal') = (reverses_id IS NOT NULL)),
    CHECK ((kind IN ('transfer_out', 'transfer_in')) = (reason = 'transfer')),
    CHECK ((kind IN ('transfer_out', 'transfer_in')) = (transfer_id IS NOT NULL)),
    CHECK ((kind = 'reservation_out') = (reason = 'reservation')),
    CHECK ((kind = 'reservation_out') = (reservation_id IS NOT NULL)),
    CHECK ((kind IN ('count_in', 'count_out')) = (reason = 'count')),
    CHECK ((kind IN ('count_in', 'count_out')) = (count_id IS NOT NULL)),
    -- Merma de mercancía bloqueada y salida en déficit: salidas de ajuste.
    CHECK ((kind = 'purchase_in') = (reason = 'purchase')),
    CHECK ((kind = 'revaluation') = (reason = 'purchase_expense')),
    CHECK ((kind IN ('purchase_in', 'revaluation')) = (receipt_id IS NOT NULL)),
    -- Una revaloración no mueve cantidad ni tiene costo propio: solo ajuste
    -- de valor positivo sobre existencia positiva.
    CHECK ((kind = 'revaluation') = (quantity = '0')),
    CHECK (kind <> 'revaluation' OR (cost = '0' AND value_adjustment NOT GLOB '-*' AND value_adjustment <> '0' AND on_hand_after NOT GLOB '-*' AND on_hand_after <> '0')),
    CHECK (block_id IS NULL OR kind = 'adjustment_out'),
    CHECK (policy_change_id IS NULL OR (kind = 'adjustment_out' AND on_hand_after GLOB '-*')),
    CHECK (kind NOT IN ('adjustment_in', 'transfer_in', 'count_in', 'purchase_in') OR quantity NOT GLOB '-*'),
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
    FOREIGN KEY (organization_id, policy_change_id) REFERENCES inventory_policy_changes (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, receipt_id) REFERENCES purchase_receipts (organization_id, id) ON DELETE RESTRICT
) STRICT;

INSERT INTO inventory_movements (organization_id, id, company_id, branch_id, warehouse_id, product_id, sequence, kind, reason, quantity, on_hand_after, note, actor_id, occurred_at_utc, reverses_id, idempotency_key, request_hash, unit_cost, cost, value_adjustment, value_after, transfer_id, reservation_id, block_id, count_id, policy_change_id)
SELECT organization_id, id, company_id, branch_id, warehouse_id, product_id, sequence, kind, reason, quantity, on_hand_after, note, actor_id, occurred_at_utc, reverses_id, idempotency_key, request_hash, unit_cost, cost, value_adjustment, value_after, transfer_id, reservation_id, block_id, count_id, policy_change_id
FROM inventory_movements_v7 ORDER BY rowid;

DROP TABLE inventory_movements_v7;

PRAGMA legacy_alter_table = OFF;

CREATE UNIQUE INDEX inventory_movements_key_uq ON inventory_movements (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX inventory_movements_reverses_uq ON inventory_movements (organization_id, reverses_id) WHERE reverses_id IS NOT NULL;
CREATE UNIQUE INDEX inventory_movements_transfer_uq ON inventory_movements (organization_id, transfer_id, kind) WHERE transfer_id IS NOT NULL;
-- Una merma por bloqueo y un ajuste por producto y conteo.
CREATE UNIQUE INDEX inventory_movements_block_uq ON inventory_movements (organization_id, block_id) WHERE block_id IS NOT NULL;
CREATE UNIQUE INDEX inventory_movements_count_uq ON inventory_movements (organization_id, count_id, product_id) WHERE count_id IS NOT NULL;
CREATE INDEX inventory_movements_receipt_idx ON inventory_movements (organization_id, receipt_id) WHERE receipt_id IS NOT NULL;
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

CREATE TABLE purchase_receipt_lines (
    organization_id TEXT    NOT NULL,
    receipt_id      TEXT    NOT NULL,
    line_no         INTEGER NOT NULL CHECK (line_no >= 1),
    product_id      TEXT    NOT NULL,
    order_line_no   INTEGER CHECK (order_line_no IS NULL OR order_line_no >= 1),
    quantity        TEXT    NOT NULL CHECK (length(quantity) BETWEEN 1 AND 40 AND quantity NOT GLOB '*[^0-9.]*' AND quantity <> '0'),
    unit_cost       TEXT    NOT NULL CHECK (length(unit_cost) BETWEEN 1 AND 60 AND unit_cost NOT GLOB '*[^0-9.]*'),
    expenses        TEXT    NOT NULL CHECK (length(expenses) BETWEEN 1 AND 40 AND expenses NOT GLOB '*[^0-9.]*'),
    movement_id     TEXT,
    PRIMARY KEY (organization_id, receipt_id, line_no),
    UNIQUE (organization_id, receipt_id, product_id),
    FOREIGN KEY (organization_id, receipt_id) REFERENCES purchase_receipts (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, movement_id) REFERENCES inventory_movements (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE TABLE purchase_expenses (
    organization_id TEXT    NOT NULL,
    id              TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    receipt_id      TEXT    NOT NULL,
    concept         TEXT    NOT NULL CHECK (length(concept) BETWEEN 1 AND 800),
    amount          TEXT    NOT NULL CHECK (length(amount) BETWEEN 1 AND 40 AND amount NOT GLOB '*[^0-9.]*' AND amount <> '0'),
    criterion       TEXT    NOT NULL CHECK (criterion IN ('value', 'quantity')),
    late            INTEGER NOT NULL CHECK (late IN (0, 1)),
    created_by      TEXT    NOT NULL,
    created_at_utc  TEXT    NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, receipt_id) REFERENCES purchase_receipts (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX purchase_expenses_receipt_idx ON purchase_expenses (organization_id, receipt_id);

CREATE TABLE purchase_expense_allocations (
    organization_id TEXT    NOT NULL,
    expense_id      TEXT    NOT NULL,
    line_no         INTEGER NOT NULL CHECK (line_no >= 1),
    base            TEXT    NOT NULL CHECK (length(base) BETWEEN 1 AND 40 AND base NOT GLOB '*[^0-9.]*'),
    amount          TEXT    NOT NULL CHECK (length(amount) BETWEEN 1 AND 40 AND amount NOT GLOB '*[^0-9.]*'),
    capitalized     TEXT    NOT NULL CHECK (length(capitalized) BETWEEN 1 AND 40 AND capitalized NOT GLOB '*[^0-9.]*'),
    variance        TEXT    NOT NULL CHECK (length(variance) BETWEEN 1 AND 40 AND variance NOT GLOB '*[^0-9.]*'),
    movement_id     TEXT,
    PRIMARY KEY (organization_id, expense_id, line_no),
    FOREIGN KEY (organization_id, expense_id) REFERENCES purchase_expenses (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, movement_id) REFERENCES inventory_movements (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE TABLE replacement_costs (
    organization_id TEXT NOT NULL,
    id              TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    company_id      TEXT NOT NULL,
    product_id      TEXT NOT NULL,
    unit_cost       TEXT NOT NULL CHECK (length(unit_cost) BETWEEN 1 AND 60 AND unit_cost NOT GLOB '*[^0-9.]*'),
    note            TEXT NOT NULL CHECK (length(note) <= 800),
    actor_id        TEXT NOT NULL,
    occurred_at_utc TEXT NOT NULL CHECK (occurred_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, company_id) REFERENCES companies (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, actor_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX replacement_costs_product_idx ON replacement_costs (organization_id, company_id, product_id, occurred_at_utc);

-- Órdenes: abierta → recibida o cancelada; lo recibido solo crece y no
-- supera lo pedido; nada se borra.
CREATE TRIGGER purchase_orders_guard BEFORE UPDATE ON purchase_orders
WHEN OLD.status <> 'open' OR NEW.id IS NOT OLD.id OR NEW.organization_id IS NOT OLD.organization_id OR NEW.number IS NOT OLD.number
  OR NEW.supplier_id IS NOT OLD.supplier_id OR NEW.branch_id IS NOT OLD.branch_id OR NEW.created_by IS NOT OLD.created_by
BEGIN
    SELECT RAISE(ABORT, 'una orden cerrada no cambia');
END;

CREATE TRIGGER purchase_order_lines_guard BEFORE UPDATE ON purchase_order_lines
WHEN (SELECT status FROM purchase_orders WHERE organization_id = OLD.organization_id AND id = OLD.order_id) IS NOT 'open'
  OR NEW.product_id IS NOT OLD.product_id OR NEW.quantity IS NOT OLD.quantity OR NEW.unit_cost IS NOT OLD.unit_cost
  OR NEW.order_id IS NOT OLD.order_id OR NEW.line_no IS NOT OLD.line_no
  OR CAST(NEW.received AS REAL) < CAST(OLD.received AS REAL) OR CAST(NEW.received AS REAL) > CAST(NEW.quantity AS REAL)
BEGIN
    SELECT RAISE(ABORT, 'lo recibido de una orden solo crece hasta lo pedido');
END;

-- Recepciones: borrador → confirmada o cancelada; sus líneas solo cambian
-- en borrador.
CREATE TRIGGER purchase_receipts_guard BEFORE UPDATE ON purchase_receipts
WHEN OLD.status <> 'draft' OR NEW.id IS NOT OLD.id OR NEW.organization_id IS NOT OLD.organization_id OR NEW.number IS NOT OLD.number
  OR NEW.warehouse_id IS NOT OLD.warehouse_id OR NEW.supplier_id IS NOT OLD.supplier_id OR NEW.order_id IS NOT OLD.order_id
  OR NEW.created_by IS NOT OLD.created_by
BEGIN
    SELECT RAISE(ABORT, 'una recepción confirmada o cancelada no cambia');
END;

CREATE TRIGGER purchase_receipt_lines_insert BEFORE INSERT ON purchase_receipt_lines
WHEN (SELECT status FROM purchase_receipts WHERE organization_id = NEW.organization_id AND id = NEW.receipt_id) IS NOT 'draft'
BEGIN
    SELECT RAISE(ABORT, 'la recepción ya no está en borrador');
END;

CREATE TRIGGER purchase_receipt_lines_update BEFORE UPDATE ON purchase_receipt_lines
WHEN (SELECT status FROM purchase_receipts WHERE organization_id = OLD.organization_id AND id = OLD.receipt_id) IS NOT 'draft'
  OR NEW.receipt_id IS NOT OLD.receipt_id OR NEW.line_no IS NOT OLD.line_no OR NEW.organization_id IS NOT OLD.organization_id
BEGIN
    SELECT RAISE(ABORT, 'la recepción ya no está en borrador');
END;

CREATE TRIGGER purchase_receipt_lines_delete BEFORE DELETE ON purchase_receipt_lines
WHEN (SELECT status FROM purchase_receipts WHERE organization_id = OLD.organization_id AND id = OLD.receipt_id) IS NOT 'draft'
BEGIN
    SELECT RAISE(ABORT, 'la recepción ya no está en borrador');
END;

CREATE TRIGGER suppliers_no_delete BEFORE DELETE ON suppliers
BEGIN
    SELECT RAISE(ABORT, 'los documentos de compra no se borran');
END;

CREATE TRIGGER purchase_orders_no_delete BEFORE DELETE ON purchase_orders
BEGIN
    SELECT RAISE(ABORT, 'los documentos de compra no se borran');
END;

CREATE TRIGGER purchase_receipts_no_delete BEFORE DELETE ON purchase_receipts
BEGIN
    SELECT RAISE(ABORT, 'los documentos de compra no se borran');
END;

CREATE TRIGGER purchase_order_lines_no_delete BEFORE DELETE ON purchase_order_lines
BEGIN
    SELECT RAISE(ABORT, 'los documentos de compra no se borran');
END;

CREATE TRIGGER purchase_expenses_no_update BEFORE UPDATE ON purchase_expenses
BEGIN
    SELECT RAISE(ABORT, 'el registro purchase_expenses es de solo inserción');
END;

CREATE TRIGGER purchase_expenses_no_delete BEFORE DELETE ON purchase_expenses
BEGIN
    SELECT RAISE(ABORT, 'el registro purchase_expenses es de solo inserción');
END;

CREATE TRIGGER purchase_expense_allocations_no_update BEFORE UPDATE ON purchase_expense_allocations
BEGIN
    SELECT RAISE(ABORT, 'el registro purchase_expense_allocations es de solo inserción');
END;

CREATE TRIGGER purchase_expense_allocations_no_delete BEFORE DELETE ON purchase_expense_allocations
BEGIN
    SELECT RAISE(ABORT, 'el registro purchase_expense_allocations es de solo inserción');
END;

CREATE TRIGGER replacement_costs_no_update BEFORE UPDATE ON replacement_costs
BEGIN
    SELECT RAISE(ABORT, 'el registro replacement_costs es de solo inserción');
END;

CREATE TRIGGER replacement_costs_no_delete BEFORE DELETE ON replacement_costs
BEGIN
    SELECT RAISE(ABORT, 'el registro replacement_costs es de solo inserción');
END;

-- Permisos nuevos en las plantillas, como en identity.Templates().
INSERT INTO role_permissions (organization_id, role_id, permission)
SELECT r.organization_id, r.id, p.permission
FROM roles r
JOIN (
    SELECT 'ADMIN_ORG' AS code, 'purchase.read' AS permission
    UNION ALL SELECT 'ADMIN_ORG', 'purchase.manage'
    UNION ALL SELECT 'ADMIN_ORG', 'purchase.receipt.confirm'
    UNION ALL SELECT 'ADMIN_ORG', 'purchase.replacement_cost.manage'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'purchase.read'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'purchase.manage'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'purchase.receipt.confirm'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'purchase.replacement_cost.manage'
    UNION ALL SELECT 'ENCARGADO', 'purchase.read'
    UNION ALL SELECT 'ENCARGADO', 'purchase.manage'
    UNION ALL SELECT 'ENCARGADO', 'purchase.receipt.confirm'
    UNION ALL SELECT 'ENCARGADO', 'purchase.replacement_cost.manage'
    UNION ALL SELECT 'ALMACENISTA', 'purchase.read'
    UNION ALL SELECT 'ALMACENISTA', 'purchase.manage'
) p ON p.code = r.code;

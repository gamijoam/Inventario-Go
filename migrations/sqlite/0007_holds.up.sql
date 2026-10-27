-- Existencia negativa por política, reservas, bloqueos y conteos físicos
-- (BP-M2-003, DEC-007.F2, secciones 24.1–24.6).

-- Saldos: el valor puede ser negativo solo con existencia negativa (costo
-- provisional del déficit), y reservar o bloquear cambia el saldo sin
-- avanzar su versión, que sigue siendo la secuencia del kardex. SQLite no
-- altera un CHECK: se reconstruyen saldos y kardex como en la 0006.
PRAGMA legacy_alter_table = ON;

-- Con claves foráneas activas, renombrar una tabla reescribe las
-- referencias a ella: se renombran primero ambas y las viejas se borran al
-- final, del kardex hacia los saldos.
ALTER TABLE inventory_movements RENAME TO inventory_movements_v6;
ALTER TABLE inventory_balances RENAME TO inventory_balances_v6;



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
    -- Valor total; NULL: existencia anterior a la valoración. Con signo de
    -- la existencia: cero sin existencia, negativo solo en déficit.
    value           TEXT    CHECK (value IS NULL OR (length(value) BETWEEN 1 AND 40 AND value NOT GLOB '*[^0-9.-]*' AND
                        CASE WHEN on_hand = '0' THEN value = '0' WHEN on_hand GLOB '-*' THEN value = '0' OR value GLOB '-*' ELSE value NOT GLOB '-*' END)),
    PRIMARY KEY (organization_id, warehouse_id, product_id),
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT
) STRICT;

INSERT INTO inventory_balances SELECT * FROM inventory_balances_v6;


-- Política de existencia negativa: registro de solo inserción; la vigente
-- es la más reciente por empresa (product_id NULL) o por producto.
CREATE TABLE inventory_policy_changes (
    organization_id  TEXT NOT NULL,
    id               TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    company_id       TEXT NOT NULL,
    product_id       TEXT,
    negative_enabled INTEGER CHECK (negative_enabled IN (0, 1)),
    mode             TEXT CHECK (mode IN ('inherit', 'deny', 'allow')),
    negative_limit   TEXT CHECK (negative_limit IS NULL OR (length(negative_limit) BETWEEN 1 AND 40 AND negative_limit NOT GLOB '*[^0-9.]*' AND negative_limit <> '0')),
    actor_id         TEXT NOT NULL,
    occurred_at_utc  TEXT NOT NULL CHECK (occurred_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    CHECK ((product_id IS NULL) = (negative_enabled IS NOT NULL)),
    CHECK ((product_id IS NULL) = (mode IS NULL)),
    CHECK ((negative_limit IS NOT NULL) = (negative_enabled = 1 OR mode = 'allow')),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, company_id) REFERENCES companies (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, actor_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX inventory_policy_changes_idx ON inventory_policy_changes (organization_id, company_id, product_id, occurred_at_utc);

CREATE TRIGGER inventory_policy_changes_no_update BEFORE UPDATE ON inventory_policy_changes
BEGIN
    SELECT RAISE(ABORT, 'el historial de políticas no se modifica');
END;

CREATE TRIGGER inventory_policy_changes_no_delete BEFORE DELETE ON inventory_policy_changes
BEGIN
    SELECT RAISE(ABORT, 'el historial de políticas no se modifica');
END;

CREATE TABLE inventory_reservations (
    organization_id TEXT NOT NULL,
    id              TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    company_id      TEXT NOT NULL,
    branch_id       TEXT NOT NULL,
    warehouse_id    TEXT NOT NULL,
    product_id      TEXT NOT NULL,
    quantity        TEXT NOT NULL CHECK (length(quantity) BETWEEN 1 AND 40 AND quantity NOT GLOB '*[^0-9.]*' AND quantity <> '0'),
    consumed        TEXT NOT NULL CHECK (length(consumed) BETWEEN 1 AND 40 AND consumed NOT GLOB '*[^0-9.]*'),
    status          TEXT NOT NULL CHECK (status IN ('active', 'consumed', 'released', 'expired')),
    reference       TEXT NOT NULL CHECK (length(reference) BETWEEN 1 AND 100),
    note            TEXT NOT NULL CHECK (length(note) <= 500),
    expires_at_utc  TEXT CHECK (expires_at_utc IS NULL OR expires_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    created_by      TEXT NOT NULL,
    created_at_utc  TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    closed_by       TEXT,
    closed_at_utc   TEXT CHECK (closed_at_utc IS NULL OR closed_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    close_note      TEXT NOT NULL CHECK (length(close_note) <= 500),
    idempotency_key TEXT CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 100),
    request_hash    TEXT NOT NULL CHECK (length(request_hash) = 64),
    CHECK ((status = 'active') = (closed_at_utc IS NULL)),
    -- Solo el vencimiento cierra sin usuario.
    CHECK ((closed_by IS NULL) = (status IN ('active', 'expired'))),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, warehouse_id, product_id) REFERENCES inventory_balances (organization_id, warehouse_id, product_id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, closed_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX inventory_reservations_balance_idx ON inventory_reservations (organization_id, warehouse_id, product_id, status);
CREATE INDEX inventory_reservations_due_idx ON inventory_reservations (organization_id, expires_at_utc) WHERE status = 'active' AND expires_at_utc IS NOT NULL;
CREATE UNIQUE INDEX inventory_reservations_key_uq ON inventory_reservations (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

-- Una reserva solo cambia mientras está activa, y solo su avance y cierre.
CREATE TRIGGER inventory_reservations_guard BEFORE UPDATE ON inventory_reservations
WHEN OLD.status <> 'active' OR NEW.id IS NOT OLD.id OR NEW.organization_id IS NOT OLD.organization_id OR NEW.warehouse_id IS NOT OLD.warehouse_id
  OR NEW.product_id IS NOT OLD.product_id OR NEW.quantity IS NOT OLD.quantity OR NEW.created_by IS NOT OLD.created_by
  OR NEW.created_at_utc IS NOT OLD.created_at_utc OR NEW.reference IS NOT OLD.reference OR NEW.request_hash IS NOT OLD.request_hash
BEGIN
    SELECT RAISE(ABORT, 'una reserva cerrada no cambia');
END;

CREATE TRIGGER inventory_reservations_no_delete BEFORE DELETE ON inventory_reservations
BEGIN
    SELECT RAISE(ABORT, 'las reservas no se borran');
END;

CREATE TABLE inventory_blocks (
    organization_id TEXT NOT NULL,
    id              TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    company_id      TEXT NOT NULL,
    branch_id       TEXT NOT NULL,
    warehouse_id    TEXT NOT NULL,
    product_id      TEXT NOT NULL,
    quantity        TEXT NOT NULL CHECK (length(quantity) BETWEEN 1 AND 40 AND quantity NOT GLOB '*[^0-9.]*' AND quantity <> '0'),
    reason          TEXT NOT NULL CHECK (reason IN ('damage', 'quarantine', 'quality', 'administrative', 'investigation')),
    note            TEXT NOT NULL CHECK (length(note) <= 500),
    status          TEXT NOT NULL CHECK (status IN ('active', 'released', 'written_off')),
    created_by      TEXT NOT NULL,
    created_at_utc  TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    closed_by       TEXT,
    closed_at_utc   TEXT CHECK (closed_at_utc IS NULL OR closed_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    close_note      TEXT NOT NULL CHECK (length(close_note) <= 500),
    idempotency_key TEXT CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 100),
    request_hash    TEXT NOT NULL CHECK (length(request_hash) = 64),
    CHECK ((status = 'active') = (closed_at_utc IS NULL)),
    CHECK ((status = 'active') = (closed_by IS NULL)),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, warehouse_id, product_id) REFERENCES inventory_balances (organization_id, warehouse_id, product_id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, closed_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX inventory_blocks_balance_idx ON inventory_blocks (organization_id, warehouse_id, product_id, status);
CREATE UNIQUE INDEX inventory_blocks_key_uq ON inventory_blocks (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TRIGGER inventory_blocks_guard BEFORE UPDATE ON inventory_blocks
WHEN OLD.status <> 'active' OR NEW.id IS NOT OLD.id OR NEW.organization_id IS NOT OLD.organization_id OR NEW.warehouse_id IS NOT OLD.warehouse_id
  OR NEW.product_id IS NOT OLD.product_id OR NEW.quantity IS NOT OLD.quantity OR NEW.reason IS NOT OLD.reason
  OR NEW.created_by IS NOT OLD.created_by OR NEW.created_at_utc IS NOT OLD.created_at_utc OR NEW.request_hash IS NOT OLD.request_hash
BEGIN
    SELECT RAISE(ABORT, 'un bloqueo cerrado no cambia');
END;

CREATE TRIGGER inventory_blocks_no_delete BEFORE DELETE ON inventory_blocks
BEGIN
    SELECT RAISE(ABORT, 'los bloqueos no se borran');
END;

CREATE TABLE inventory_counts (
    organization_id TEXT NOT NULL,
    id              TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    company_id      TEXT NOT NULL,
    branch_id       TEXT NOT NULL,
    warehouse_id    TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('draft', 'in_review', 'applied', 'rejected')),
    note            TEXT NOT NULL CHECK (length(note) <= 500),
    created_by      TEXT NOT NULL,
    created_at_utc  TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    submitted_by    TEXT,
    submitted_at_utc TEXT CHECK (submitted_at_utc IS NULL OR submitted_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    decided_by      TEXT,
    decided_at_utc  TEXT CHECK (decided_at_utc IS NULL OR decided_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    decision_note   TEXT NOT NULL CHECK (length(decision_note) <= 500),
    CHECK ((status = 'draft') = (submitted_by IS NULL)),
    CHECK ((status IN ('applied', 'rejected')) = (decided_by IS NOT NULL)),
    -- Quien aprueba no es quien envió.
    CHECK (decided_by IS NULL OR decided_by <> submitted_by),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, submitted_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, decided_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX inventory_counts_warehouse_idx ON inventory_counts (organization_id, warehouse_id, status);

-- Borrador → en revisión → aplicado o rechazado; nunca hacia atrás.
CREATE TRIGGER inventory_counts_guard BEFORE UPDATE ON inventory_counts
WHEN NOT ((OLD.status = 'draft' AND NEW.status = 'in_review') OR (OLD.status = 'in_review' AND NEW.status IN ('applied', 'rejected')))
  OR NEW.id IS NOT OLD.id OR NEW.organization_id IS NOT OLD.organization_id OR NEW.warehouse_id IS NOT OLD.warehouse_id
  OR NEW.created_by IS NOT OLD.created_by OR NEW.created_at_utc IS NOT OLD.created_at_utc
BEGIN
    SELECT RAISE(ABORT, 'transición de conteo no permitida');
END;

CREATE TRIGGER inventory_counts_no_delete BEFORE DELETE ON inventory_counts
BEGIN
    SELECT RAISE(ABORT, 'los conteos no se borran');
END;

CREATE TABLE inventory_count_lines (
    organization_id  TEXT    NOT NULL,
    count_id         TEXT    NOT NULL,
    product_id       TEXT    NOT NULL,
    counted          TEXT    NOT NULL CHECK (length(counted) BETWEEN 1 AND 40 AND counted NOT GLOB '*[^0-9.]*'),
    expected         TEXT    NOT NULL CHECK (length(expected) BETWEEN 1 AND 40 AND expected NOT GLOB '*[^0-9.-]*'),
    expected_version INTEGER NOT NULL CHECK (expected_version >= 0),
    unit_cost        TEXT    CHECK (unit_cost IS NULL OR (length(unit_cost) BETWEEN 1 AND 60 AND unit_cost NOT GLOB '*[^0-9.]*')),
    captured_by      TEXT    NOT NULL,
    captured_at_utc  TEXT    NOT NULL CHECK (captured_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    PRIMARY KEY (organization_id, count_id, product_id),
    FOREIGN KEY (organization_id, count_id) REFERENCES inventory_counts (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, captured_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

-- Las líneas solo cambian mientras el conteo está en borrador.
CREATE TRIGGER inventory_count_lines_insert BEFORE INSERT ON inventory_count_lines
WHEN (SELECT status FROM inventory_counts WHERE organization_id = NEW.organization_id AND id = NEW.count_id) IS NOT 'draft'
BEGIN
    SELECT RAISE(ABORT, 'el conteo ya no está en borrador');
END;

CREATE TRIGGER inventory_count_lines_update BEFORE UPDATE ON inventory_count_lines
WHEN (SELECT status FROM inventory_counts WHERE organization_id = OLD.organization_id AND id = OLD.count_id) IS NOT 'draft'
  OR NEW.count_id IS NOT OLD.count_id OR NEW.product_id IS NOT OLD.product_id OR NEW.organization_id IS NOT OLD.organization_id
BEGIN
    SELECT RAISE(ABORT, 'el conteo ya no está en borrador');
END;

CREATE TRIGGER inventory_count_lines_delete BEFORE DELETE ON inventory_count_lines
BEGIN
    SELECT RAISE(ABORT, 'las líneas de conteo no se borran');
END;

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

INSERT INTO inventory_movements (organization_id, id, company_id, branch_id, warehouse_id, product_id, sequence, kind, reason, quantity,
    on_hand_after, note, actor_id, occurred_at_utc, reverses_id, idempotency_key, request_hash, unit_cost, cost, value_adjustment, value_after, transfer_id)
SELECT organization_id, id, company_id, branch_id, warehouse_id, product_id, sequence, kind, reason, quantity,
    on_hand_after, note, actor_id, occurred_at_utc, reverses_id, idempotency_key, request_hash, unit_cost, cost, value_adjustment, value_after, transfer_id
FROM inventory_movements_v6 ORDER BY rowid;

DROP TABLE inventory_movements_v6;
DROP TABLE inventory_balances_v6;

PRAGMA legacy_alter_table = OFF;

CREATE INDEX inventory_balances_product_idx ON inventory_balances (organization_id, product_id);

-- Un movimiento avanza exactamente una versión; reservar o bloquear no la
-- cambia ni toca existencia ni valor. Los saldos no se borran.
CREATE TRIGGER inventory_balances_version BEFORE UPDATE ON inventory_balances
WHEN NOT (NEW.version = OLD.version + 1 OR (NEW.version = OLD.version AND NEW.on_hand IS OLD.on_hand AND NEW.value IS OLD.value))
BEGIN
    SELECT RAISE(ABORT, 'el saldo debe avanzar una versión por movimiento');
END;

CREATE TRIGGER inventory_balances_no_delete BEFORE DELETE ON inventory_balances
BEGIN
    SELECT RAISE(ABORT, 'los saldos de inventario no se borran');
END;

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

-- Permisos nuevos en las plantillas, como en identity.Templates(). El de
-- existencia negativa es extraordinario: no va en ninguna plantilla.
INSERT INTO role_permissions (organization_id, role_id, permission)
SELECT r.organization_id, r.id, p.permission
FROM roles r
JOIN (
    SELECT 'ADMIN_ORG' AS code, 'inventory.reservation.manage' AS permission
    UNION ALL SELECT 'ADMIN_ORG', 'inventory.block.manage'
    UNION ALL SELECT 'ADMIN_ORG', 'inventory.count.manage'
    UNION ALL SELECT 'ADMIN_ORG', 'inventory.count.approve'
    UNION ALL SELECT 'ADMIN_ORG', 'inventory.policy.manage'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'inventory.reservation.manage'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'inventory.block.manage'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'inventory.count.manage'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'inventory.count.approve'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'inventory.policy.manage'
    UNION ALL SELECT 'ENCARGADO', 'inventory.reservation.manage'
    UNION ALL SELECT 'ENCARGADO', 'inventory.block.manage'
    UNION ALL SELECT 'ENCARGADO', 'inventory.count.manage'
    UNION ALL SELECT 'ENCARGADO', 'inventory.count.approve'
    UNION ALL SELECT 'ALMACENISTA', 'inventory.reservation.manage'
    UNION ALL SELECT 'ALMACENISTA', 'inventory.block.manage'
    UNION ALL SELECT 'ALMACENISTA', 'inventory.count.manage'
    UNION ALL SELECT 'CAJERO', 'inventory.reservation.manage'
) p ON p.code = r.code;

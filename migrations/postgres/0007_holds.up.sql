-- Existencia negativa por política, reservas, bloqueos y conteos físicos
-- (BP-M2-003, DEC-007.F2, secciones 24.1–24.6).

-- El valor puede ser negativo solo con existencia negativa (costo
-- provisional del déficit).
-- La 0006 lo creó sin nombre (PostgreSQL lo llama inventory_balances_check):
-- se busca por su definición.
DO $$
DECLARE
    c TEXT;
BEGIN
    SELECT conname INTO STRICT c FROM pg_constraint
    WHERE conrelid = 'inventory_balances'::regclass AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%value%';
    EXECUTE format('ALTER TABLE inventory_balances DROP CONSTRAINT %I', c);
END;
$$;

ALTER TABLE inventory_balances
    ADD CONSTRAINT inventory_balances_value_check CHECK (
        (on_hand = 0 AND value = 0) OR (on_hand > 0 AND value >= 0) OR (on_hand < 0 AND value <= 0));

-- Política de existencia negativa: registro de solo inserción; la vigente
-- es la más reciente por empresa (product_id NULL) o por producto.
CREATE TABLE inventory_policy_changes (
    organization_id  UUID          NOT NULL,
    id               UUID          NOT NULL PRIMARY KEY,
    company_id       UUID          NOT NULL,
    product_id       UUID,
    negative_enabled BOOLEAN,
    mode             TEXT          CHECK (mode IN ('inherit', 'deny', 'allow')),
    negative_limit   NUMERIC(28,6) CHECK (negative_limit > 0),
    actor_id         UUID          NOT NULL,
    occurred_at_utc  TIMESTAMPTZ   NOT NULL,
    CHECK ((product_id IS NULL) = (negative_enabled IS NOT NULL)),
    CHECK ((product_id IS NULL) = (mode IS NULL)),
    CHECK ((negative_limit IS NOT NULL) = (negative_enabled IS TRUE OR mode = 'allow')),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, company_id) REFERENCES companies (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, actor_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX inventory_policy_changes_idx ON inventory_policy_changes (organization_id, company_id, product_id, occurred_at_utc);

CREATE TABLE inventory_reservations (
    organization_id UUID          NOT NULL,
    id              UUID          NOT NULL PRIMARY KEY,
    company_id      UUID          NOT NULL,
    branch_id       UUID          NOT NULL,
    warehouse_id    UUID          NOT NULL,
    product_id      UUID          NOT NULL,
    quantity        NUMERIC(28,6) NOT NULL CHECK (quantity > 0),
    consumed        NUMERIC(28,6) NOT NULL CHECK (consumed >= 0),
    status          TEXT          NOT NULL CHECK (status IN ('active', 'consumed', 'released', 'expired')),
    reference       TEXT          NOT NULL CHECK (length(reference) BETWEEN 1 AND 100),
    note            TEXT          NOT NULL CHECK (length(note) <= 500),
    expires_at_utc  TIMESTAMPTZ,
    created_by      UUID          NOT NULL,
    created_at_utc  TIMESTAMPTZ   NOT NULL,
    closed_by       UUID,
    closed_at_utc   TIMESTAMPTZ,
    close_note      TEXT          NOT NULL CHECK (length(close_note) <= 500),
    idempotency_key TEXT          CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 100),
    request_hash    TEXT          NOT NULL CHECK (length(request_hash) = 64),
    CHECK ((status = 'active') = (closed_at_utc IS NULL)),
    -- Solo el vencimiento cierra sin usuario.
    CHECK ((closed_by IS NULL) = (status IN ('active', 'expired'))),
    CHECK (consumed <= quantity AND (status <> 'consumed' OR consumed = quantity)),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, warehouse_id, product_id) REFERENCES inventory_balances (organization_id, warehouse_id, product_id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, closed_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX inventory_reservations_balance_idx ON inventory_reservations (organization_id, warehouse_id, product_id, status);
CREATE INDEX inventory_reservations_due_idx ON inventory_reservations (organization_id, expires_at_utc) WHERE status = 'active' AND expires_at_utc IS NOT NULL;
CREATE UNIQUE INDEX inventory_reservations_key_uq ON inventory_reservations (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TABLE inventory_blocks (
    organization_id UUID          NOT NULL,
    id              UUID          NOT NULL PRIMARY KEY,
    company_id      UUID          NOT NULL,
    branch_id       UUID          NOT NULL,
    warehouse_id    UUID          NOT NULL,
    product_id      UUID          NOT NULL,
    quantity        NUMERIC(28,6) NOT NULL CHECK (quantity > 0),
    reason          TEXT          NOT NULL CHECK (reason IN ('damage', 'quarantine', 'quality', 'administrative', 'investigation')),
    note            TEXT          NOT NULL CHECK (length(note) <= 500),
    status          TEXT          NOT NULL CHECK (status IN ('active', 'released', 'written_off')),
    created_by      UUID          NOT NULL,
    created_at_utc  TIMESTAMPTZ   NOT NULL,
    closed_by       UUID,
    closed_at_utc   TIMESTAMPTZ,
    close_note      TEXT          NOT NULL CHECK (length(close_note) <= 500),
    idempotency_key TEXT          CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 100),
    request_hash    TEXT          NOT NULL CHECK (length(request_hash) = 64),
    CHECK ((status = 'active') = (closed_at_utc IS NULL)),
    CHECK ((status = 'active') = (closed_by IS NULL)),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, warehouse_id, product_id) REFERENCES inventory_balances (organization_id, warehouse_id, product_id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, closed_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX inventory_blocks_balance_idx ON inventory_blocks (organization_id, warehouse_id, product_id, status);
CREATE UNIQUE INDEX inventory_blocks_key_uq ON inventory_blocks (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TABLE inventory_counts (
    organization_id  UUID        NOT NULL,
    id               UUID        NOT NULL PRIMARY KEY,
    company_id       UUID        NOT NULL,
    branch_id        UUID        NOT NULL,
    warehouse_id     UUID        NOT NULL,
    status           TEXT        NOT NULL CHECK (status IN ('draft', 'in_review', 'applied', 'rejected')),
    note             TEXT        NOT NULL CHECK (length(note) <= 500),
    created_by       UUID        NOT NULL,
    created_at_utc   TIMESTAMPTZ NOT NULL,
    submitted_by     UUID,
    submitted_at_utc TIMESTAMPTZ,
    decided_by       UUID,
    decided_at_utc   TIMESTAMPTZ,
    decision_note    TEXT        NOT NULL CHECK (length(decision_note) <= 500),
    CHECK ((status = 'draft') = (submitted_by IS NULL)),
    CHECK ((status IN ('applied', 'rejected')) = (decided_by IS NOT NULL)),
    -- Quien aprueba no es quien envió.
    CHECK (decided_by IS NULL OR decided_by <> submitted_by),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, submitted_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, decided_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX inventory_counts_warehouse_idx ON inventory_counts (organization_id, warehouse_id, status);

CREATE TABLE inventory_count_lines (
    organization_id  UUID           NOT NULL,
    count_id         UUID           NOT NULL,
    product_id       UUID           NOT NULL,
    counted          NUMERIC(28,6)  NOT NULL CHECK (counted >= 0),
    expected         NUMERIC(28,6)  NOT NULL,
    expected_version BIGINT         NOT NULL CHECK (expected_version >= 0),
    unit_cost        NUMERIC(40,12) CHECK (unit_cost >= 0),
    captured_by      UUID           NOT NULL,
    captured_at_utc  TIMESTAMPTZ    NOT NULL,
    PRIMARY KEY (organization_id, count_id, product_id),
    FOREIGN KEY (organization_id, count_id) REFERENCES inventory_counts (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, captured_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

ALTER TABLE inventory_movements
    DROP CONSTRAINT inventory_movements_kind_check,
    DROP CONSTRAINT inventory_movements_reason_check,
    DROP CONSTRAINT inventory_movements_value_after_check,
    ADD COLUMN reservation_id UUID,
    ADD COLUMN block_id UUID,
    ADD COLUMN count_id UUID,
    ADD COLUMN policy_change_id UUID,
    ADD CONSTRAINT inventory_movements_kind_check CHECK (kind IN ('adjustment_in', 'adjustment_out', 'reversal', 'transfer_out', 'transfer_in', 'reservation_out', 'count_in', 'count_out')),
    ADD CONSTRAINT inventory_movements_reason_check CHECK (reason IN ('initial_load', 'correction', 'shrinkage', 'internal_use', 'other', 'reversal', 'transfer', 'reservation', 'count')),
    -- Negativo solo en déficit.
    ADD CONSTRAINT inventory_movements_value_after_check CHECK (value_after >= 0 OR on_hand_after < 0),
    ADD CHECK ((kind = 'reservation_out') = (reason = 'reservation')),
    ADD CHECK ((kind = 'reservation_out') = (reservation_id IS NOT NULL)),
    ADD CHECK ((kind IN ('count_in', 'count_out')) = (reason = 'count')),
    ADD CHECK ((kind IN ('count_in', 'count_out')) = (count_id IS NOT NULL)),
    -- Merma de mercancía bloqueada y salida en déficit: salidas de ajuste.
    ADD CHECK (block_id IS NULL OR kind = 'adjustment_out'),
    ADD CHECK (policy_change_id IS NULL OR (kind = 'adjustment_out' AND on_hand_after < 0)),
    ADD CHECK (kind NOT IN ('count_in') OR quantity > 0),
    ADD CHECK (kind NOT IN ('reservation_out', 'count_out') OR quantity < 0),
    ADD FOREIGN KEY (organization_id, reservation_id) REFERENCES inventory_reservations (organization_id, id) ON DELETE RESTRICT,
    ADD FOREIGN KEY (organization_id, block_id) REFERENCES inventory_blocks (organization_id, id) ON DELETE RESTRICT,
    ADD FOREIGN KEY (organization_id, count_id) REFERENCES inventory_counts (organization_id, id) ON DELETE RESTRICT,
    ADD FOREIGN KEY (organization_id, policy_change_id) REFERENCES inventory_policy_changes (organization_id, id) ON DELETE RESTRICT;

-- Una merma por bloqueo y un ajuste por producto y conteo.
CREATE UNIQUE INDEX inventory_movements_block_uq ON inventory_movements (organization_id, block_id) WHERE block_id IS NOT NULL;
CREATE UNIQUE INDEX inventory_movements_count_uq ON inventory_movements (organization_id, count_id, product_id) WHERE count_id IS NOT NULL;
CREATE INDEX inventory_movements_reservation_idx ON inventory_movements (organization_id, reservation_id) WHERE reservation_id IS NOT NULL;

CREATE OR REPLACE FUNCTION inventory_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'inventory_transfers' THEN
        RAISE EXCEPTION 'las transferencias no se modifican';
    END IF;
    IF TG_TABLE_NAME = 'inventory_balances' THEN
        IF TG_OP = 'DELETE' THEN
            RAISE EXCEPTION 'los saldos de inventario no se borran';
        END IF;
        -- Un movimiento avanza exactamente una versión; reservar o bloquear
        -- no la cambia ni toca existencia ni valor.
        IF NOT (NEW.version = OLD.version + 1 OR (NEW.version = OLD.version AND NEW.on_hand = OLD.on_hand
                AND NEW.value IS NOT DISTINCT FROM OLD.value)) THEN
            RAISE EXCEPTION 'el saldo debe avanzar una versión por movimiento';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'el kardex es de solo inserción';
    END IF;
    -- Un movimiento nuevo debe corresponder a la versión, existencia y valor
    -- que el saldo ya registra (se actualiza antes en la misma transacción).
    IF NOT EXISTS (
        SELECT 1 FROM inventory_balances b
        WHERE b.organization_id = NEW.organization_id AND b.warehouse_id = NEW.warehouse_id AND b.product_id = NEW.product_id
          AND b.version = NEW.sequence AND b.on_hand = NEW.on_hand_after AND b.value IS NOT DISTINCT FROM NEW.value_after
    ) THEN
        RAISE EXCEPTION 'el movimiento no coincide con el saldo';
    END IF;
    RETURN NEW;
END;
$$;

-- Reservas, bloqueos y conteos: cambian solo mientras siguen abiertos, y el
-- historial de políticas y las líneas de conteos cerrados no cambian.
CREATE FUNCTION inventory_hold_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    st TEXT;
BEGIN
    IF TG_OP = 'DELETE' OR TG_OP = 'TRUNCATE' OR TG_TABLE_NAME = 'inventory_policy_changes' THEN
        RAISE EXCEPTION 'el registro % no se borra ni se modifica', TG_TABLE_NAME;
    END IF;
    IF TG_TABLE_NAME = 'inventory_reservations' THEN
        IF OLD.status <> 'active' OR (NEW.id, NEW.organization_id, NEW.warehouse_id, NEW.product_id, NEW.quantity, NEW.created_by, NEW.created_at_utc, NEW.reference, NEW.request_hash)
            IS DISTINCT FROM (OLD.id, OLD.organization_id, OLD.warehouse_id, OLD.product_id, OLD.quantity, OLD.created_by, OLD.created_at_utc, OLD.reference, OLD.request_hash) THEN
            RAISE EXCEPTION 'una reserva cerrada no cambia';
        END IF;
    ELSIF TG_TABLE_NAME = 'inventory_blocks' THEN
        IF OLD.status <> 'active' OR (NEW.id, NEW.organization_id, NEW.warehouse_id, NEW.product_id, NEW.quantity, NEW.reason, NEW.created_by, NEW.created_at_utc, NEW.request_hash)
            IS DISTINCT FROM (OLD.id, OLD.organization_id, OLD.warehouse_id, OLD.product_id, OLD.quantity, OLD.reason, OLD.created_by, OLD.created_at_utc, OLD.request_hash) THEN
            RAISE EXCEPTION 'un bloqueo cerrado no cambia';
        END IF;
    ELSIF TG_TABLE_NAME = 'inventory_counts' THEN
        IF NOT ((OLD.status = 'draft' AND NEW.status = 'in_review') OR (OLD.status = 'in_review' AND NEW.status IN ('applied', 'rejected')))
            OR (NEW.id, NEW.organization_id, NEW.warehouse_id, NEW.created_by, NEW.created_at_utc)
            IS DISTINCT FROM (OLD.id, OLD.organization_id, OLD.warehouse_id, OLD.created_by, OLD.created_at_utc) THEN
            RAISE EXCEPTION 'transición de conteo no permitida';
        END IF;
    ELSE
        SELECT c.status INTO st FROM inventory_counts c WHERE c.organization_id = NEW.organization_id AND c.id = NEW.count_id;
        IF st IS DISTINCT FROM 'draft' OR (TG_OP = 'UPDATE' AND (NEW.organization_id, NEW.count_id, NEW.product_id)
            IS DISTINCT FROM (OLD.organization_id, OLD.count_id, OLD.product_id)) THEN
            RAISE EXCEPTION 'el conteo ya no está en borrador';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER inventory_policy_changes_guard BEFORE UPDATE OR DELETE ON inventory_policy_changes
    FOR EACH ROW EXECUTE FUNCTION inventory_hold_guard();
CREATE TRIGGER inventory_reservations_guard BEFORE UPDATE OR DELETE ON inventory_reservations
    FOR EACH ROW EXECUTE FUNCTION inventory_hold_guard();
CREATE TRIGGER inventory_blocks_guard BEFORE UPDATE OR DELETE ON inventory_blocks
    FOR EACH ROW EXECUTE FUNCTION inventory_hold_guard();
CREATE TRIGGER inventory_counts_guard BEFORE UPDATE OR DELETE ON inventory_counts
    FOR EACH ROW EXECUTE FUNCTION inventory_hold_guard();
CREATE TRIGGER inventory_count_lines_guard BEFORE INSERT OR UPDATE OR DELETE ON inventory_count_lines
    FOR EACH ROW EXECUTE FUNCTION inventory_hold_guard();

-- Permisos nuevos en las plantillas, como en identity.Templates(). El de
-- existencia negativa es extraordinario: no va en ninguna plantilla.
INSERT INTO role_permissions (organization_id, role_id, permission)
SELECT r.organization_id, r.id, p.permission
FROM roles r
JOIN (VALUES
    ('ADMIN_ORG', 'inventory.reservation.manage'), ('ADMIN_ORG', 'inventory.block.manage'), ('ADMIN_ORG', 'inventory.count.manage'),
    ('ADMIN_ORG', 'inventory.count.approve'), ('ADMIN_ORG', 'inventory.policy.manage'),
    ('ADMIN_EMPRESA', 'inventory.reservation.manage'), ('ADMIN_EMPRESA', 'inventory.block.manage'), ('ADMIN_EMPRESA', 'inventory.count.manage'),
    ('ADMIN_EMPRESA', 'inventory.count.approve'), ('ADMIN_EMPRESA', 'inventory.policy.manage'),
    ('ENCARGADO', 'inventory.reservation.manage'), ('ENCARGADO', 'inventory.block.manage'), ('ENCARGADO', 'inventory.count.manage'),
    ('ENCARGADO', 'inventory.count.approve'),
    ('ALMACENISTA', 'inventory.reservation.manage'), ('ALMACENISTA', 'inventory.block.manage'), ('ALMACENISTA', 'inventory.count.manage'),
    ('CAJERO', 'inventory.reservation.manage')
) AS p (code, permission) ON p.code = r.code;

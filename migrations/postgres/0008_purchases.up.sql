-- Compras: proveedores, órdenes, recepciones, gastos atribuibles y costo de
-- reposición (BP-M3-001, secciones 19.2–19.4). Importes en la moneda de
-- valoración de la empresa.

CREATE TABLE suppliers (
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    id              UUID        NOT NULL PRIMARY KEY,
    code            TEXT        NOT NULL CHECK (length(code) BETWEEN 1 AND 32),
    name            TEXT        NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    rif             TEXT        CHECK (rif IS NULL OR length(rif) = 12),
    status          TEXT        NOT NULL CHECK (status IN ('active', 'inactive')),
    created_at_utc  TIMESTAMPTZ NOT NULL,
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, code)
);

CREATE UNIQUE INDEX suppliers_rif_uq ON suppliers (organization_id, rif) WHERE rif IS NOT NULL;

CREATE TABLE purchase_orders (
    organization_id UUID        NOT NULL,
    id              UUID        NOT NULL PRIMARY KEY,
    company_id      UUID        NOT NULL,
    branch_id       UUID        NOT NULL,
    number          TEXT        NOT NULL CHECK (length(number) BETWEEN 1 AND 32),
    supplier_id     UUID        NOT NULL,
    status          TEXT        NOT NULL CHECK (status IN ('open', 'received', 'cancelled')),
    note            TEXT        NOT NULL CHECK (length(note) <= 800),
    created_by      UUID        NOT NULL,
    created_at_utc  TIMESTAMPTZ NOT NULL,
    closed_by       UUID,
    closed_at_utc   TIMESTAMPTZ,
    CHECK ((status = 'cancelled') = (closed_by IS NOT NULL)),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, number),
    FOREIGN KEY (organization_id, company_id, branch_id) REFERENCES branches (organization_id, company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, supplier_id) REFERENCES suppliers (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, closed_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE TABLE purchase_order_lines (
    organization_id UUID           NOT NULL,
    order_id        UUID           NOT NULL,
    line_no         INTEGER        NOT NULL CHECK (line_no >= 1),
    product_id      UUID           NOT NULL,
    quantity        NUMERIC(28,6)  NOT NULL CHECK (quantity > 0),
    unit_cost       NUMERIC(40,12) NOT NULL CHECK (unit_cost >= 0),
    received        NUMERIC(28,6)  NOT NULL CHECK (received >= 0),
    CHECK (received <= quantity),
    PRIMARY KEY (organization_id, order_id, line_no),
    UNIQUE (organization_id, order_id, product_id),
    FOREIGN KEY (organization_id, order_id) REFERENCES purchase_orders (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT
);

CREATE TABLE purchase_receipts (
    organization_id   UUID        NOT NULL,
    id                UUID        NOT NULL PRIMARY KEY,
    company_id        UUID        NOT NULL,
    branch_id         UUID        NOT NULL,
    warehouse_id      UUID        NOT NULL,
    number            TEXT        NOT NULL CHECK (length(number) BETWEEN 1 AND 32),
    supplier_id       UUID        NOT NULL,
    order_id          UUID,
    status            TEXT        NOT NULL CHECK (status IN ('draft', 'confirmed', 'cancelled')),
    supplier_document TEXT        NOT NULL CHECK (length(supplier_document) <= 800),
    note              TEXT        NOT NULL CHECK (length(note) <= 800),
    created_by        UUID        NOT NULL,
    created_at_utc    TIMESTAMPTZ NOT NULL,
    closed_by         UUID,
    closed_at_utc     TIMESTAMPTZ,
    CHECK ((status = 'draft') = (closed_by IS NULL)),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, number),
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, supplier_id) REFERENCES suppliers (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, order_id) REFERENCES purchase_orders (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, closed_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX purchase_receipts_order_idx ON purchase_receipts (organization_id, order_id) WHERE order_id IS NOT NULL;

-- El kardex admite entradas de compra y revaloraciones por gastos tardíos.
ALTER TABLE inventory_movements
    DROP CONSTRAINT inventory_movements_kind_check,
    DROP CONSTRAINT inventory_movements_reason_check,
    DROP CONSTRAINT inventory_movements_quantity_check,
    ADD COLUMN receipt_id UUID,
    ADD CONSTRAINT inventory_movements_kind_check CHECK (kind IN ('adjustment_in', 'adjustment_out', 'reversal', 'transfer_out', 'transfer_in',
        'reservation_out', 'count_in', 'count_out', 'purchase_in', 'revaluation')),
    ADD CONSTRAINT inventory_movements_reason_check CHECK (reason IN ('initial_load', 'correction', 'shrinkage', 'internal_use', 'other', 'reversal',
        'transfer', 'reservation', 'count', 'purchase', 'purchase_expense')),
    ADD CHECK ((kind = 'purchase_in') = (reason = 'purchase')),
    ADD CHECK ((kind = 'revaluation') = (reason = 'purchase_expense')),
    ADD CHECK ((kind IN ('purchase_in', 'revaluation')) = (receipt_id IS NOT NULL)),
    -- Una revaloración no mueve cantidad ni tiene costo propio: solo ajuste
    -- de valor positivo sobre existencia positiva.
    ADD CONSTRAINT inventory_movements_quantity_check CHECK ((kind = 'revaluation') = (quantity = 0)),
    ADD CHECK (kind <> 'revaluation' OR (cost = 0 AND value_adjustment > 0 AND on_hand_after > 0)),
    ADD CHECK (kind <> 'purchase_in' OR quantity > 0),
    ADD FOREIGN KEY (organization_id, receipt_id) REFERENCES purchase_receipts (organization_id, id) ON DELETE RESTRICT;

CREATE INDEX inventory_movements_receipt_idx ON inventory_movements (organization_id, receipt_id) WHERE receipt_id IS NOT NULL;

CREATE TABLE purchase_receipt_lines (
    organization_id UUID           NOT NULL,
    receipt_id      UUID           NOT NULL,
    line_no         INTEGER        NOT NULL CHECK (line_no >= 1),
    product_id      UUID           NOT NULL,
    order_line_no   INTEGER        CHECK (order_line_no >= 1),
    quantity        NUMERIC(28,6)  NOT NULL CHECK (quantity > 0),
    unit_cost       NUMERIC(40,12) NOT NULL CHECK (unit_cost >= 0),
    expenses        NUMERIC(28,6)  NOT NULL CHECK (expenses >= 0),
    movement_id     UUID,
    PRIMARY KEY (organization_id, receipt_id, line_no),
    UNIQUE (organization_id, receipt_id, product_id),
    FOREIGN KEY (organization_id, receipt_id) REFERENCES purchase_receipts (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, movement_id) REFERENCES inventory_movements (organization_id, id) ON DELETE RESTRICT
);

CREATE TABLE purchase_expenses (
    organization_id UUID          NOT NULL,
    id              UUID          NOT NULL PRIMARY KEY,
    receipt_id      UUID          NOT NULL,
    concept         TEXT          NOT NULL CHECK (length(concept) BETWEEN 1 AND 800),
    amount          NUMERIC(28,6) NOT NULL CHECK (amount > 0),
    criterion       TEXT          NOT NULL CHECK (criterion IN ('value', 'quantity')),
    late            BOOLEAN       NOT NULL,
    created_by      UUID          NOT NULL,
    created_at_utc  TIMESTAMPTZ   NOT NULL,
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, receipt_id) REFERENCES purchase_receipts (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX purchase_expenses_receipt_idx ON purchase_expenses (organization_id, receipt_id);

CREATE TABLE purchase_expense_allocations (
    organization_id UUID          NOT NULL,
    expense_id      UUID          NOT NULL,
    line_no         INTEGER       NOT NULL CHECK (line_no >= 1),
    base            NUMERIC(28,6) NOT NULL CHECK (base >= 0),
    amount          NUMERIC(28,6) NOT NULL CHECK (amount >= 0),
    capitalized     NUMERIC(28,6) NOT NULL CHECK (capitalized >= 0),
    variance        NUMERIC(28,6) NOT NULL CHECK (variance >= 0),
    movement_id     UUID,
    PRIMARY KEY (organization_id, expense_id, line_no),
    FOREIGN KEY (organization_id, expense_id) REFERENCES purchase_expenses (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, movement_id) REFERENCES inventory_movements (organization_id, id) ON DELETE RESTRICT
);

CREATE TABLE replacement_costs (
    organization_id UUID           NOT NULL,
    id              UUID           NOT NULL PRIMARY KEY,
    company_id      UUID           NOT NULL,
    product_id      UUID           NOT NULL,
    unit_cost       NUMERIC(40,12) NOT NULL CHECK (unit_cost >= 0),
    note            TEXT           NOT NULL CHECK (length(note) <= 800),
    actor_id        UUID           NOT NULL,
    occurred_at_utc TIMESTAMPTZ    NOT NULL,
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, company_id) REFERENCES companies (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, actor_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX replacement_costs_product_idx ON replacement_costs (organization_id, company_id, product_id, occurred_at_utc);

CREATE FUNCTION purchase_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    st TEXT;
BEGIN
    IF TG_OP = 'TRUNCATE' THEN
        RAISE EXCEPTION 'los documentos de compra no se borran';
    END IF;
    IF TG_TABLE_NAME IN ('purchase_expenses', 'purchase_expense_allocations', 'replacement_costs') THEN
        RAISE EXCEPTION 'el registro % es de solo inserción', TG_TABLE_NAME;
    END IF;
    IF TG_TABLE_NAME = 'purchase_receipt_lines' THEN
        IF TG_OP = 'DELETE' THEN
            SELECT r.status INTO st FROM purchase_receipts r WHERE r.organization_id = OLD.organization_id AND r.id = OLD.receipt_id;
            IF st IS DISTINCT FROM 'draft' THEN
                RAISE EXCEPTION 'la recepción ya no está en borrador';
            END IF;
            RETURN OLD;
        END IF;
        SELECT r.status INTO st FROM purchase_receipts r WHERE r.organization_id = NEW.organization_id AND r.id = NEW.receipt_id;
        IF st IS DISTINCT FROM 'draft' OR (TG_OP = 'UPDATE' AND (NEW.organization_id, NEW.receipt_id, NEW.line_no)
            IS DISTINCT FROM (OLD.organization_id, OLD.receipt_id, OLD.line_no)) THEN
            RAISE EXCEPTION 'la recepción ya no está en borrador';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'los documentos de compra no se borran';
    END IF;
    IF TG_TABLE_NAME = 'purchase_orders' THEN
        IF OLD.status <> 'open' OR (NEW.id, NEW.organization_id, NEW.number, NEW.supplier_id, NEW.branch_id, NEW.created_by)
            IS DISTINCT FROM (OLD.id, OLD.organization_id, OLD.number, OLD.supplier_id, OLD.branch_id, OLD.created_by) THEN
            RAISE EXCEPTION 'una orden cerrada no cambia';
        END IF;
    ELSIF TG_TABLE_NAME = 'purchase_order_lines' THEN
        SELECT o.status INTO st FROM purchase_orders o WHERE o.organization_id = OLD.organization_id AND o.id = OLD.order_id;
        IF st IS DISTINCT FROM 'open' OR NEW.received < OLD.received OR (NEW.product_id, NEW.quantity, NEW.unit_cost, NEW.order_id, NEW.line_no)
            IS DISTINCT FROM (OLD.product_id, OLD.quantity, OLD.unit_cost, OLD.order_id, OLD.line_no) THEN
            RAISE EXCEPTION 'lo recibido de una orden solo crece hasta lo pedido';
        END IF;
    ELSIF TG_TABLE_NAME = 'purchase_receipts' THEN
        IF OLD.status <> 'draft' OR (NEW.id, NEW.organization_id, NEW.number, NEW.warehouse_id, NEW.supplier_id, NEW.order_id, NEW.created_by)
            IS DISTINCT FROM (OLD.id, OLD.organization_id, OLD.number, OLD.warehouse_id, OLD.supplier_id, OLD.order_id, OLD.created_by) THEN
            RAISE EXCEPTION 'una recepción confirmada o cancelada no cambia';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER suppliers_guard BEFORE DELETE ON suppliers FOR EACH ROW EXECUTE FUNCTION purchase_guard();
CREATE TRIGGER purchase_orders_guard BEFORE UPDATE OR DELETE ON purchase_orders FOR EACH ROW EXECUTE FUNCTION purchase_guard();
CREATE TRIGGER purchase_order_lines_guard BEFORE UPDATE OR DELETE ON purchase_order_lines FOR EACH ROW EXECUTE FUNCTION purchase_guard();
CREATE TRIGGER purchase_receipts_guard BEFORE UPDATE OR DELETE ON purchase_receipts FOR EACH ROW EXECUTE FUNCTION purchase_guard();
CREATE TRIGGER purchase_receipt_lines_guard BEFORE INSERT OR UPDATE OR DELETE ON purchase_receipt_lines FOR EACH ROW EXECUTE FUNCTION purchase_guard();
CREATE TRIGGER purchase_expenses_guard BEFORE UPDATE OR DELETE ON purchase_expenses FOR EACH ROW EXECUTE FUNCTION purchase_guard();
CREATE TRIGGER purchase_expense_allocations_guard BEFORE UPDATE OR DELETE ON purchase_expense_allocations FOR EACH ROW EXECUTE FUNCTION purchase_guard();
CREATE TRIGGER replacement_costs_guard BEFORE UPDATE OR DELETE ON replacement_costs FOR EACH ROW EXECUTE FUNCTION purchase_guard();

-- Permisos nuevos en las plantillas, como en identity.Templates().
INSERT INTO role_permissions (organization_id, role_id, permission)
SELECT r.organization_id, r.id, p.permission
FROM roles r
JOIN (VALUES
    ('ADMIN_ORG', 'purchase.read'), ('ADMIN_ORG', 'purchase.manage'), ('ADMIN_ORG', 'purchase.receipt.confirm'), ('ADMIN_ORG', 'purchase.replacement_cost.manage'),
    ('ADMIN_EMPRESA', 'purchase.read'), ('ADMIN_EMPRESA', 'purchase.manage'), ('ADMIN_EMPRESA', 'purchase.receipt.confirm'), ('ADMIN_EMPRESA', 'purchase.replacement_cost.manage'),
    ('ENCARGADO', 'purchase.read'), ('ENCARGADO', 'purchase.manage'), ('ENCARGADO', 'purchase.receipt.confirm'), ('ENCARGADO', 'purchase.replacement_cost.manage'),
    ('ALMACENISTA', 'purchase.read'), ('ALMACENISTA', 'purchase.manage')
) AS p (code, permission) ON p.code = r.code;

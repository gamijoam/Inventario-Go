-- Listas de precios, márgenes objetivo, propuestas y revisiones de precio
-- (BP-M3-003, DEC-007.P2). Recibir mercancía crea propuestas; solo una
-- aprobación publica una revisión nueva. Márgenes y revisiones no se
-- editan: tienen historial.

CREATE TABLE price_lists (
    organization_id UUID        NOT NULL,
    id              UUID        NOT NULL PRIMARY KEY,
    company_id      UUID        NOT NULL,
    code            TEXT        NOT NULL CHECK (length(code) BETWEEN 1 AND 32),
    name            TEXT        NOT NULL CHECK (length(name) BETWEEN 1 AND 800),
    currency        TEXT        NOT NULL CHECK (currency IN ('USD', 'VES')),
    status          TEXT        NOT NULL CHECK (status IN ('active', 'inactive')),
    created_by      UUID        NOT NULL,
    created_at_utc  TIMESTAMPTZ NOT NULL,
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, company_id, code),
    FOREIGN KEY (organization_id, company_id) REFERENCES companies (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

-- Margen objetivo de la lista (product_id NULL) o excepción de un producto;
-- margin NULL lo quita. Rige el de mayor seq.
CREATE TABLE price_margins (
    organization_id UUID         NOT NULL,
    id              UUID         NOT NULL PRIMARY KEY,
    list_id         UUID         NOT NULL,
    seq             INTEGER      NOT NULL CHECK (seq >= 1),
    product_id      UUID,
    margin          NUMERIC(8,4) CHECK (margin >= 0 AND margin < 100),
    actor_id        UUID         NOT NULL,
    occurred_at_utc TIMESTAMPTZ  NOT NULL,
    UNIQUE (organization_id, list_id, seq),
    FOREIGN KEY (organization_id, list_id) REFERENCES price_lists (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, actor_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE TABLE price_revisions (
    organization_id  UUID          NOT NULL,
    id               UUID          NOT NULL PRIMARY KEY,
    list_id          UUID          NOT NULL,
    product_id       UUID          NOT NULL,
    version          INTEGER       NOT NULL CHECK (version >= 1),
    price            NUMERIC(28,8) NOT NULL CHECK (price >= 0),
    effective_at_utc TIMESTAMPTZ   NOT NULL,
    status           TEXT          NOT NULL CHECK (status IN ('published', 'cancelled')),
    created_by       UUID          NOT NULL,
    created_at_utc   TIMESTAMPTZ   NOT NULL,
    cancelled_by     UUID,
    cancelled_at_utc TIMESTAMPTZ,
    CHECK ((status = 'cancelled') = (cancelled_by IS NOT NULL)),
    CHECK ((cancelled_by IS NULL) = (cancelled_at_utc IS NULL)),
    -- Solo se anula una revisión programada.
    CHECK (cancelled_at_utc IS NULL OR cancelled_at_utc < effective_at_utc),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, list_id, product_id, version),
    FOREIGN KEY (organization_id, list_id) REFERENCES price_lists (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, cancelled_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX price_revisions_effective_idx ON price_revisions (organization_id, list_id, product_id, effective_at_utc);

CREATE TABLE price_proposals (
    organization_id UUID           NOT NULL,
    id              UUID           NOT NULL PRIMARY KEY,
    number          TEXT           NOT NULL CHECK (length(number) BETWEEN 1 AND 32),
    list_id         UUID           NOT NULL,
    product_id      UUID           NOT NULL,
    kind            TEXT           NOT NULL CHECK (kind IN ('suggested', 'manual')),
    status          TEXT           NOT NULL CHECK (status IN ('pending', 'approved', 'rejected', 'superseded')),
    base_version    INTEGER        NOT NULL CHECK (base_version >= 0),
    current_price   NUMERIC(28,8)  CHECK (current_price >= 0),
    price           NUMERIC(28,8)  NOT NULL CHECK (price >= 0),
    cost_basis      TEXT           CHECK (cost_basis IN ('replacement', 'last_purchase')),
    cost            NUMERIC(40,12) CHECK (cost >= 0),
    cost_currency   TEXT           CHECK (cost_currency IN ('USD', 'VES')),
    rate_id         UUID,
    list_cost       NUMERIC(40,12) CHECK (list_cost >= 0),
    margin          NUMERIC(8,4)   CHECK (margin >= 0 AND margin < 100),
    receipt_id      UUID,
    note            TEXT           NOT NULL CHECK (length(note) <= 800),
    proposed_by     UUID           NOT NULL,
    created_at_utc  TIMESTAMPTZ    NOT NULL,
    decided_by      UUID,
    decided_at_utc  TIMESTAMPTZ,
    decision_note   TEXT           NOT NULL CHECK (length(decision_note) <= 800),
    revision_id     UUID,
    -- El costo de referencia va completo o no va.
    CHECK ((cost_basis IS NULL) = (cost IS NULL) AND (cost IS NULL) = (cost_currency IS NULL) AND (cost IS NULL) = (list_cost IS NULL)),
    CHECK (rate_id IS NULL OR cost IS NOT NULL),
    -- Una sugerencia sale de una recepción y de un margen objetivo.
    CHECK ((kind = 'suggested') = (receipt_id IS NOT NULL)),
    CHECK (kind = 'manual' OR (margin IS NOT NULL AND cost IS NOT NULL)),
    CHECK ((status = 'pending') = (decided_by IS NULL)),
    CHECK ((decided_by IS NULL) = (decided_at_utc IS NULL)),
    CHECK ((status = 'approved') = (revision_id IS NOT NULL)),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, number),
    FOREIGN KEY (organization_id, list_id) REFERENCES price_lists (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, rate_id) REFERENCES exchange_rates (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, receipt_id) REFERENCES purchase_receipts (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, revision_id) REFERENCES price_revisions (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, proposed_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, decided_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

-- Una sola propuesta pendiente por producto y lista: la nueva sustituye.
CREATE UNIQUE INDEX price_proposals_pending_uq ON price_proposals (organization_id, list_id, product_id) WHERE status = 'pending';
CREATE INDEX price_proposals_receipt_idx ON price_proposals (organization_id, receipt_id) WHERE receipt_id IS NOT NULL;

CREATE FUNCTION pricing_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP IN ('DELETE', 'TRUNCATE') THEN
        RAISE EXCEPTION 'los registros de precios no se borran';
    END IF;
    IF TG_TABLE_NAME = 'price_margins' THEN
        RAISE EXCEPTION 'el registro price_margins es de solo inserción';
    ELSIF TG_TABLE_NAME = 'price_lists' THEN
        IF (NEW.id, NEW.organization_id, NEW.company_id, NEW.code, NEW.currency, NEW.created_by)
            IS DISTINCT FROM (OLD.id, OLD.organization_id, OLD.company_id, OLD.code, OLD.currency, OLD.created_by) THEN
            RAISE EXCEPTION 'una lista conserva su empresa, código y moneda';
        END IF;
    ELSIF TG_TABLE_NAME = 'price_revisions' THEN
        IF OLD.status <> 'published' OR NEW.status <> 'cancelled'
            OR (NEW.id, NEW.organization_id, NEW.list_id, NEW.product_id, NEW.version, NEW.price, NEW.effective_at_utc, NEW.created_by, NEW.created_at_utc)
            IS DISTINCT FROM (OLD.id, OLD.organization_id, OLD.list_id, OLD.product_id, OLD.version, OLD.price, OLD.effective_at_utc, OLD.created_by, OLD.created_at_utc) THEN
            RAISE EXCEPTION 'una revisión de precio solo se anula';
        END IF;
    ELSIF TG_TABLE_NAME = 'price_proposals' THEN
        IF OLD.status <> 'pending'
            OR (NEW.id, NEW.organization_id, NEW.number, NEW.list_id, NEW.product_id, NEW.kind, NEW.base_version, NEW.current_price, NEW.cost_basis,
                NEW.cost, NEW.cost_currency, NEW.rate_id, NEW.list_cost, NEW.margin, NEW.receipt_id, NEW.note, NEW.proposed_by, NEW.created_at_utc)
            IS DISTINCT FROM (OLD.id, OLD.organization_id, OLD.number, OLD.list_id, OLD.product_id, OLD.kind, OLD.base_version, OLD.current_price, OLD.cost_basis,
                OLD.cost, OLD.cost_currency, OLD.rate_id, OLD.list_cost, OLD.margin, OLD.receipt_id, OLD.note, OLD.proposed_by, OLD.created_at_utc) THEN
            RAISE EXCEPTION 'una propuesta decidida no cambia';
        END IF;
        IF NEW.revision_id IS NOT NULL AND NOT EXISTS (
            SELECT 1 FROM price_revisions r WHERE r.organization_id = NEW.organization_id AND r.id = NEW.revision_id
              AND r.list_id = NEW.list_id AND r.product_id = NEW.product_id) THEN
            RAISE EXCEPTION 'la revisión no corresponde a la propuesta';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER price_lists_guard BEFORE UPDATE OR DELETE ON price_lists FOR EACH ROW EXECUTE FUNCTION pricing_guard();
CREATE TRIGGER price_margins_guard BEFORE UPDATE OR DELETE ON price_margins FOR EACH ROW EXECUTE FUNCTION pricing_guard();
CREATE TRIGGER price_revisions_guard BEFORE UPDATE OR DELETE ON price_revisions FOR EACH ROW EXECUTE FUNCTION pricing_guard();
CREATE TRIGGER price_proposals_guard BEFORE UPDATE OR DELETE ON price_proposals FOR EACH ROW EXECUTE FUNCTION pricing_guard();

-- Permisos nuevos en las plantillas, como en identity.Templates().
INSERT INTO role_permissions (organization_id, role_id, permission)
SELECT r.organization_id, r.id, p.permission
FROM roles r
JOIN (VALUES
    ('ADMIN_ORG', 'pricing.price.read'), ('ADMIN_ORG', 'pricing.list.manage'), ('ADMIN_ORG', 'pricing.price.propose'), ('ADMIN_ORG', 'pricing.price.approve'),
    ('ADMIN_EMPRESA', 'pricing.price.read'), ('ADMIN_EMPRESA', 'pricing.list.manage'), ('ADMIN_EMPRESA', 'pricing.price.propose'), ('ADMIN_EMPRESA', 'pricing.price.approve'),
    ('ENCARGADO', 'pricing.price.read'), ('ENCARGADO', 'pricing.price.propose'), ('ENCARGADO', 'pricing.price.approve'),
    ('ALMACENISTA', 'pricing.price.read'), ('ALMACENISTA', 'pricing.price.propose'),
    ('CAJERO', 'pricing.price.read')
) AS p (code, permission) ON p.code = r.code;

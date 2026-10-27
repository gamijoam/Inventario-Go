-- Listas de precios, márgenes objetivo, propuestas y revisiones de precio
-- (BP-M3-003, DEC-007.P2). Recibir mercancía crea propuestas; solo una
-- aprobación publica una revisión nueva. Márgenes y revisiones no se
-- editan: tienen historial.

CREATE TABLE price_lists (
    organization_id TEXT NOT NULL,
    id              TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    company_id      TEXT NOT NULL,
    code            TEXT NOT NULL CHECK (length(code) BETWEEN 1 AND 32),
    name            TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 800),
    currency        TEXT NOT NULL CHECK (currency IN ('USD', 'VES')),
    status          TEXT NOT NULL CHECK (status IN ('active', 'inactive')),
    created_by      TEXT NOT NULL,
    created_at_utc  TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, company_id, code),
    FOREIGN KEY (organization_id, company_id) REFERENCES companies (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

-- Margen objetivo de la lista (product_id NULL) o excepción de un producto;
-- margin NULL lo quita. Rige el de mayor seq.
CREATE TABLE price_margins (
    organization_id TEXT    NOT NULL,
    id              TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    list_id         TEXT    NOT NULL,
    seq             INTEGER NOT NULL CHECK (seq >= 1),
    product_id      TEXT,
    margin          TEXT    CHECK (margin IS NULL OR (length(margin) BETWEEN 1 AND 12 AND margin NOT GLOB '*[^0-9.]*')),
    actor_id        TEXT    NOT NULL,
    occurred_at_utc TEXT    NOT NULL CHECK (occurred_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    UNIQUE (organization_id, list_id, seq),
    FOREIGN KEY (organization_id, list_id) REFERENCES price_lists (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, actor_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE TABLE price_revisions (
    organization_id  TEXT    NOT NULL,
    id               TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    list_id          TEXT    NOT NULL,
    product_id       TEXT    NOT NULL,
    version          INTEGER NOT NULL CHECK (version >= 1),
    price            TEXT    NOT NULL CHECK (length(price) BETWEEN 1 AND 40 AND price NOT GLOB '*[^0-9.]*'),
    effective_at_utc TEXT    NOT NULL CHECK (effective_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    status           TEXT    NOT NULL CHECK (status IN ('published', 'cancelled')),
    created_by       TEXT    NOT NULL,
    created_at_utc   TEXT    NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    cancelled_by     TEXT,
    cancelled_at_utc TEXT    CHECK (cancelled_at_utc IS NULL OR cancelled_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
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
) STRICT;

CREATE INDEX price_revisions_effective_idx ON price_revisions (organization_id, list_id, product_id, effective_at_utc);

CREATE TABLE price_proposals (
    organization_id TEXT    NOT NULL,
    id              TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    number          TEXT    NOT NULL CHECK (length(number) BETWEEN 1 AND 32),
    list_id         TEXT    NOT NULL,
    product_id      TEXT    NOT NULL,
    kind            TEXT    NOT NULL CHECK (kind IN ('suggested', 'manual')),
    status          TEXT    NOT NULL CHECK (status IN ('pending', 'approved', 'rejected', 'superseded')),
    base_version    INTEGER NOT NULL CHECK (base_version >= 0),
    current_price   TEXT    CHECK (current_price IS NULL OR (length(current_price) BETWEEN 1 AND 40 AND current_price NOT GLOB '*[^0-9.]*')),
    price           TEXT    NOT NULL CHECK (length(price) BETWEEN 1 AND 40 AND price NOT GLOB '*[^0-9.]*'),
    cost_basis      TEXT    CHECK (cost_basis IS NULL OR cost_basis IN ('replacement', 'last_purchase')),
    cost            TEXT    CHECK (cost IS NULL OR (length(cost) BETWEEN 1 AND 60 AND cost NOT GLOB '*[^0-9.]*')),
    cost_currency   TEXT    CHECK (cost_currency IS NULL OR cost_currency IN ('USD', 'VES')),
    rate_id         TEXT,
    list_cost       TEXT    CHECK (list_cost IS NULL OR (length(list_cost) BETWEEN 1 AND 60 AND list_cost NOT GLOB '*[^0-9.]*')),
    margin          TEXT    CHECK (margin IS NULL OR (length(margin) BETWEEN 1 AND 12 AND margin NOT GLOB '*[^0-9.]*')),
    receipt_id      TEXT,
    note            TEXT    NOT NULL CHECK (length(note) <= 800),
    proposed_by     TEXT    NOT NULL,
    created_at_utc  TEXT    NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    decided_by      TEXT,
    decided_at_utc  TEXT    CHECK (decided_at_utc IS NULL OR decided_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    decision_note   TEXT    NOT NULL CHECK (length(decision_note) <= 800),
    revision_id     TEXT,
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
) STRICT;

-- Una sola propuesta pendiente por producto y lista: la nueva sustituye.
CREATE UNIQUE INDEX price_proposals_pending_uq ON price_proposals (organization_id, list_id, product_id) WHERE status = 'pending';
CREATE INDEX price_proposals_receipt_idx ON price_proposals (organization_id, receipt_id) WHERE receipt_id IS NOT NULL;

-- Una lista conserva su empresa, código y moneda.
CREATE TRIGGER price_lists_guard BEFORE UPDATE ON price_lists
WHEN NEW.id IS NOT OLD.id OR NEW.organization_id IS NOT OLD.organization_id OR NEW.company_id IS NOT OLD.company_id
  OR NEW.code IS NOT OLD.code OR NEW.currency IS NOT OLD.currency OR NEW.created_by IS NOT OLD.created_by
BEGIN
    SELECT RAISE(ABORT, 'una lista conserva su empresa, código y moneda');
END;

CREATE TRIGGER price_margins_no_update BEFORE UPDATE ON price_margins
BEGIN
    SELECT RAISE(ABORT, 'el registro price_margins es de solo inserción');
END;

-- Una revisión solo pasa de publicada a anulada.
CREATE TRIGGER price_revisions_guard BEFORE UPDATE ON price_revisions
WHEN OLD.status <> 'published' OR NEW.status <> 'cancelled' OR NEW.id IS NOT OLD.id OR NEW.organization_id IS NOT OLD.organization_id
  OR NEW.list_id IS NOT OLD.list_id OR NEW.product_id IS NOT OLD.product_id OR NEW.version IS NOT OLD.version OR NEW.price IS NOT OLD.price
  OR NEW.effective_at_utc IS NOT OLD.effective_at_utc OR NEW.created_by IS NOT OLD.created_by OR NEW.created_at_utc IS NOT OLD.created_at_utc
BEGIN
    SELECT RAISE(ABORT, 'una revisión de precio solo se anula');
END;

-- Una propuesta solo se decide una vez y no cambia lo propuesto.
CREATE TRIGGER price_proposals_guard BEFORE UPDATE ON price_proposals
WHEN OLD.status <> 'pending' OR NEW.id IS NOT OLD.id OR NEW.organization_id IS NOT OLD.organization_id OR NEW.number IS NOT OLD.number
  OR NEW.list_id IS NOT OLD.list_id OR NEW.product_id IS NOT OLD.product_id OR NEW.kind IS NOT OLD.kind OR NEW.base_version IS NOT OLD.base_version
  OR NEW.current_price IS NOT OLD.current_price OR NEW.cost_basis IS NOT OLD.cost_basis OR NEW.cost IS NOT OLD.cost
  OR NEW.cost_currency IS NOT OLD.cost_currency OR NEW.rate_id IS NOT OLD.rate_id OR NEW.list_cost IS NOT OLD.list_cost
  OR NEW.margin IS NOT OLD.margin OR NEW.receipt_id IS NOT OLD.receipt_id OR NEW.note IS NOT OLD.note OR NEW.proposed_by IS NOT OLD.proposed_by
  OR NEW.created_at_utc IS NOT OLD.created_at_utc
BEGIN
    SELECT RAISE(ABORT, 'una propuesta decidida no cambia');
END;

-- La revisión de una propuesta aprobada es de su lista y producto.
CREATE TRIGGER price_proposals_revision BEFORE UPDATE ON price_proposals
WHEN NEW.revision_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM price_revisions r WHERE r.organization_id = NEW.organization_id AND r.id = NEW.revision_id
      AND r.list_id = NEW.list_id AND r.product_id = NEW.product_id)
BEGIN
    SELECT RAISE(ABORT, 'la revisión no corresponde a la propuesta');
END;

CREATE TRIGGER price_lists_no_delete BEFORE DELETE ON price_lists
BEGIN
    SELECT RAISE(ABORT, 'las listas de precios no se borran');
END;

CREATE TRIGGER price_margins_no_delete BEFORE DELETE ON price_margins
BEGIN
    SELECT RAISE(ABORT, 'el registro price_margins es de solo inserción');
END;

CREATE TRIGGER price_revisions_no_delete BEFORE DELETE ON price_revisions
BEGIN
    SELECT RAISE(ABORT, 'las revisiones de precio no se borran');
END;

CREATE TRIGGER price_proposals_no_delete BEFORE DELETE ON price_proposals
BEGIN
    SELECT RAISE(ABORT, 'las propuestas de precio no se borran');
END;

-- Permisos nuevos en las plantillas, como en identity.Templates().
INSERT INTO role_permissions (organization_id, role_id, permission)
SELECT r.organization_id, r.id, p.permission
FROM roles r
JOIN (
    SELECT 'ADMIN_ORG' AS code, 'pricing.price.read' AS permission
    UNION ALL SELECT 'ADMIN_ORG', 'pricing.list.manage'
    UNION ALL SELECT 'ADMIN_ORG', 'pricing.price.propose'
    UNION ALL SELECT 'ADMIN_ORG', 'pricing.price.approve'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'pricing.price.read'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'pricing.list.manage'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'pricing.price.propose'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'pricing.price.approve'
    UNION ALL SELECT 'ENCARGADO', 'pricing.price.read'
    UNION ALL SELECT 'ENCARGADO', 'pricing.price.propose'
    UNION ALL SELECT 'ENCARGADO', 'pricing.price.approve'
    UNION ALL SELECT 'ALMACENISTA', 'pricing.price.read'
    UNION ALL SELECT 'ALMACENISTA', 'pricing.price.propose'
    UNION ALL SELECT 'CAJERO', 'pricing.price.read'
) p ON p.code = r.code;

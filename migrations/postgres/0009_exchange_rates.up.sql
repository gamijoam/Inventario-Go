-- Tasas de cambio versionadas y compras en moneda original (BP-M3-002,
-- sección 25.7). Una tasa rige desde una fecha comercial de Caracas hasta
-- que otra la reemplace; nunca se edita ni se borra. Los documentos de
-- compra guardan su moneda, su fecha y la tasa que aplicaron.

CREATE TABLE exchange_rates (
    organization_id  UUID           NOT NULL,
    id               UUID           NOT NULL PRIMARY KEY,
    company_id       UUID           NOT NULL,
    base_currency    TEXT           NOT NULL CHECK (base_currency IN ('USD', 'VES')),
    quote_currency   TEXT           NOT NULL CHECK (quote_currency IN ('USD', 'VES')),
    -- El par sin dirección: USD/VES y VES/USD compiten por la vigencia.
    pair             TEXT           NOT NULL,
    rate             NUMERIC(40,12) NOT NULL CHECK (rate > 0),
    source_type      TEXT           NOT NULL CHECK (source_type IN ('official', 'manually_entered', 'provider')),
    source_label     TEXT           NOT NULL CHECK (length(source_label) BETWEEN 1 AND 800),
    source_reference TEXT           NOT NULL CHECK (length(source_reference) <= 800),
    valid_on         DATE           NOT NULL,
    version          INTEGER        NOT NULL CHECK (version >= 1),
    note             TEXT           NOT NULL CHECK (length(note) <= 800),
    created_by       UUID           NOT NULL,
    created_at_utc   TIMESTAMPTZ    NOT NULL,
    CHECK (base_currency <> quote_currency),
    CHECK (pair = CASE WHEN base_currency < quote_currency THEN base_currency || '/' || quote_currency ELSE quote_currency || '/' || base_currency END),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, company_id, pair, version),
    FOREIGN KEY (organization_id, company_id) REFERENCES companies (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX exchange_rates_valid_idx ON exchange_rates (organization_id, company_id, pair, valid_on);

CREATE FUNCTION exchange_rates_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'una tasa registrada no cambia ni se borra: registre una versión nueva';
END;
$$;

CREATE TRIGGER exchange_rates_guard BEFORE UPDATE OR DELETE ON exchange_rates FOR EACH ROW EXECUTE FUNCTION exchange_rates_guard();
CREATE TRIGGER exchange_rates_truncate BEFORE TRUNCATE ON exchange_rates FOR EACH STATEMENT EXECUTE FUNCTION exchange_rates_guard();

-- Los documentos anteriores están en la moneda de valoración de su empresa,
-- con la fecha de Caracas en que se crearon. La tasa se valida con
-- disparadores y no con clave foránea, como en SQLite; las tasas no se
-- borran.
ALTER TABLE purchase_orders DISABLE TRIGGER purchase_orders_guard;
ALTER TABLE purchase_receipts DISABLE TRIGGER purchase_receipts_guard;
ALTER TABLE purchase_expenses DISABLE TRIGGER purchase_expenses_guard;
ALTER TABLE replacement_costs DISABLE TRIGGER replacement_costs_guard;

ALTER TABLE purchase_orders ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD' CHECK (currency IN ('USD', 'VES'));
UPDATE purchase_orders o SET currency = c.valuation_currency FROM companies c WHERE c.organization_id = o.organization_id AND c.id = o.company_id;
ALTER TABLE purchase_orders ALTER COLUMN currency DROP DEFAULT;

ALTER TABLE purchase_receipts
    ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD' CHECK (currency IN ('USD', 'VES')),
    ADD COLUMN document_date DATE NOT NULL DEFAULT DATE '2000-01-01',
    ADD COLUMN rate_id UUID;
UPDATE purchase_receipts r SET currency = c.valuation_currency, document_date = (r.created_at_utc AT TIME ZONE 'America/Caracas')::date
FROM companies c WHERE c.organization_id = r.organization_id AND c.id = r.company_id;
ALTER TABLE purchase_receipts ALTER COLUMN currency DROP DEFAULT, ALTER COLUMN document_date DROP DEFAULT;

-- Costo de compra en la moneda de valoración, fijado al confirmar. NULL en
-- las líneas anteriores: equivale a cantidad × costo.
ALTER TABLE purchase_receipt_lines ADD COLUMN valued NUMERIC(28,6) CHECK (valued >= 0);

ALTER TABLE purchase_expenses
    ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD' CHECK (currency IN ('USD', 'VES')),
    ADD COLUMN expense_date DATE NOT NULL DEFAULT DATE '2000-01-01',
    ADD COLUMN rate_id UUID,
    -- Importe en la moneda de valoración. NULL en los gastos anteriores:
    -- equivale al importe.
    ADD COLUMN valued NUMERIC(28,6) CHECK (valued > 0);
UPDATE purchase_expenses e SET currency = c.valuation_currency, expense_date = (e.created_at_utc AT TIME ZONE 'America/Caracas')::date
FROM purchase_receipts r JOIN companies c ON c.organization_id = r.organization_id AND c.id = r.company_id
WHERE r.organization_id = e.organization_id AND r.id = e.receipt_id;
ALTER TABLE purchase_expenses ALTER COLUMN currency DROP DEFAULT, ALTER COLUMN expense_date DROP DEFAULT;

ALTER TABLE replacement_costs ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD' CHECK (currency IN ('USD', 'VES'));
UPDATE replacement_costs x SET currency = c.valuation_currency FROM companies c WHERE c.organization_id = x.organization_id AND c.id = x.company_id;
ALTER TABLE replacement_costs ALTER COLUMN currency DROP DEFAULT;

ALTER TABLE purchase_orders ENABLE TRIGGER purchase_orders_guard;
ALTER TABLE purchase_receipts ENABLE TRIGGER purchase_receipts_guard;
ALTER TABLE purchase_expenses ENABLE TRIGGER purchase_expenses_guard;
ALTER TABLE replacement_costs ENABLE TRIGGER replacement_costs_guard;

-- purchase_rate_ok indica si la tasa es de la empresa, cubre el par entre
-- la moneda del documento y la de valoración y ya regía el día del
-- documento.
CREATE FUNCTION purchase_rate_ok(p_org UUID, p_company UUID, p_rate UUID, p_currency TEXT, p_valuation TEXT, p_on DATE) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM exchange_rates x WHERE x.organization_id = p_org AND x.id = p_rate AND x.company_id = p_company
        AND x.valid_on <= p_on AND x.pair IN (p_currency || '/' || p_valuation, p_valuation || '/' || p_currency));
$$;

-- Moneda y fecha no cambian. Una recepción nace en borrador y sin tasa; al
-- confirmarse tiene todas sus líneas valoradas y una tasa de su empresa,
-- vigente a la fecha del documento, si y solo si su moneda no es la de
-- valoración. Un gasto nuevo cumple lo mismo con su propia fecha.
CREATE FUNCTION purchase_currency_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    valuation TEXT;
    company UUID;
BEGIN
    IF TG_TABLE_NAME = 'purchase_orders' THEN
        IF NEW.currency IS DISTINCT FROM OLD.currency THEN
            RAISE EXCEPTION 'una orden cerrada no cambia';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_TABLE_NAME = 'purchase_expenses' THEN
        SELECT r.company_id, c.valuation_currency INTO company, valuation FROM purchase_receipts r
        JOIN companies c ON c.organization_id = r.organization_id AND c.id = r.company_id
        WHERE r.organization_id = NEW.organization_id AND r.id = NEW.receipt_id;
        IF NEW.valued IS NULL OR (NEW.rate_id IS NULL) <> (NEW.currency = valuation)
            OR (NEW.rate_id IS NOT NULL AND NOT purchase_rate_ok(NEW.organization_id, company, NEW.rate_id, NEW.currency, valuation, NEW.expense_date)) THEN
            RAISE EXCEPTION 'el gasto no tiene su valoración o su tasa';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'INSERT' THEN
        IF NEW.rate_id IS NOT NULL OR NEW.status <> 'draft' THEN
            RAISE EXCEPTION 'una recepción nace en borrador y sin tasa';
        END IF;
        RETURN NEW;
    END IF;
    IF (NEW.currency, NEW.document_date) IS DISTINCT FROM (OLD.currency, OLD.document_date) THEN
        RAISE EXCEPTION 'una recepción confirmada o cancelada no cambia';
    END IF;
    IF NEW.status <> 'confirmed' THEN
        IF NEW.rate_id IS NOT NULL THEN
            RAISE EXCEPTION 'solo una recepción confirmada tiene tasa aplicada';
        END IF;
        RETURN NEW;
    END IF;
    SELECT c.valuation_currency INTO valuation FROM companies c WHERE c.organization_id = NEW.organization_id AND c.id = NEW.company_id;
    IF EXISTS (SELECT 1 FROM purchase_receipt_lines l WHERE l.organization_id = NEW.organization_id AND l.receipt_id = NEW.id AND l.valued IS NULL)
        OR (NEW.rate_id IS NULL) <> (NEW.currency = valuation)
        OR (NEW.rate_id IS NOT NULL AND NOT purchase_rate_ok(NEW.organization_id, NEW.company_id, NEW.rate_id, NEW.currency, valuation, NEW.document_date)) THEN
        RAISE EXCEPTION 'la recepción confirmada no tiene su valoración o su tasa';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER purchase_orders_currency BEFORE UPDATE ON purchase_orders FOR EACH ROW EXECUTE FUNCTION purchase_currency_guard();
CREATE TRIGGER purchase_receipts_currency BEFORE INSERT OR UPDATE ON purchase_receipts FOR EACH ROW EXECUTE FUNCTION purchase_currency_guard();
CREATE TRIGGER purchase_expenses_currency BEFORE INSERT ON purchase_expenses FOR EACH ROW EXECUTE FUNCTION purchase_currency_guard();

-- Permisos nuevos en las plantillas, como en identity.Templates().
INSERT INTO role_permissions (organization_id, role_id, permission)
SELECT r.organization_id, r.id, p.permission
FROM roles r
JOIN (VALUES
    ('ADMIN_ORG', 'currency.rate.read'), ('ADMIN_ORG', 'currency.rate.manage'),
    ('ADMIN_EMPRESA', 'currency.rate.read'), ('ADMIN_EMPRESA', 'currency.rate.manage'),
    ('ENCARGADO', 'currency.rate.read'), ('ENCARGADO', 'currency.rate.manage'),
    ('CAJERO', 'currency.rate.read'), ('ALMACENISTA', 'currency.rate.read')
) AS p (code, permission) ON p.code = r.code;

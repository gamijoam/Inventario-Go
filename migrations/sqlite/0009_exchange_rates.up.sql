-- Tasas de cambio versionadas y compras en moneda original (BP-M3-002,
-- sección 25.7). Una tasa rige desde una fecha comercial de Caracas hasta
-- que otra la reemplace; nunca se edita ni se borra. Los documentos de
-- compra guardan su moneda, su fecha y la tasa que aplicaron.

CREATE TABLE exchange_rates (
    organization_id  TEXT    NOT NULL,
    id               TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    company_id       TEXT    NOT NULL,
    base_currency    TEXT    NOT NULL CHECK (base_currency IN ('USD', 'VES')),
    quote_currency   TEXT    NOT NULL CHECK (quote_currency IN ('USD', 'VES')),
    -- El par sin dirección: USD/VES y VES/USD compiten por la vigencia.
    pair             TEXT    NOT NULL,
    rate             TEXT    NOT NULL CHECK (length(rate) BETWEEN 1 AND 40 AND rate NOT GLOB '*[^0-9.]*' AND rate GLOB '*[1-9]*'),
    source_type      TEXT    NOT NULL CHECK (source_type IN ('official', 'manually_entered', 'provider')),
    source_label     TEXT    NOT NULL CHECK (length(source_label) BETWEEN 1 AND 800),
    source_reference TEXT    NOT NULL CHECK (length(source_reference) <= 800),
    valid_on         TEXT    NOT NULL CHECK (valid_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    version          INTEGER NOT NULL CHECK (version >= 1),
    note             TEXT    NOT NULL CHECK (length(note) <= 800),
    created_by       TEXT    NOT NULL,
    created_at_utc   TEXT    NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    CHECK (base_currency <> quote_currency),
    CHECK (pair = CASE WHEN base_currency < quote_currency THEN base_currency || '/' || quote_currency ELSE quote_currency || '/' || base_currency END),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, company_id, pair, version),
    FOREIGN KEY (organization_id, company_id) REFERENCES companies (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX exchange_rates_valid_idx ON exchange_rates (organization_id, company_id, pair, valid_on);

CREATE TRIGGER exchange_rates_no_update BEFORE UPDATE ON exchange_rates
BEGIN
    SELECT RAISE(ABORT, 'una tasa registrada no cambia: registre una versión nueva');
END;

CREATE TRIGGER exchange_rates_no_delete BEFORE DELETE ON exchange_rates
BEGIN
    SELECT RAISE(ABORT, 'las tasas no se borran');
END;

-- Las guardas se recrean más abajo con las columnas nuevas; quitarlas
-- primero permite completar los documentos existentes.
DROP TRIGGER purchase_orders_guard;
DROP TRIGGER purchase_receipts_guard;
DROP TRIGGER purchase_expenses_no_update;
DROP TRIGGER replacement_costs_no_update;

-- Los documentos anteriores están en la moneda de valoración de su empresa,
-- con la fecha de Caracas en que se crearon. La tasa se valida con
-- disparadores y no con clave foránea, para que la reversión pueda quitar
-- la columna; las tasas no se borran.
ALTER TABLE purchase_orders ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD' CHECK (currency IN ('USD', 'VES'));
UPDATE purchase_orders SET currency = (SELECT c.valuation_currency FROM companies c WHERE c.organization_id = purchase_orders.organization_id AND c.id = purchase_orders.company_id);

ALTER TABLE purchase_receipts ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD' CHECK (currency IN ('USD', 'VES'));
ALTER TABLE purchase_receipts ADD COLUMN document_date TEXT NOT NULL DEFAULT '2000-01-01' CHECK (document_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]');
ALTER TABLE purchase_receipts ADD COLUMN rate_id TEXT CHECK (rate_id IS NULL OR length(rate_id) = 36);
UPDATE purchase_receipts SET
    currency = (SELECT c.valuation_currency FROM companies c WHERE c.organization_id = purchase_receipts.organization_id AND c.id = purchase_receipts.company_id),
    document_date = date(created_at_utc, '-4 hours');

-- Costo de compra en la moneda de valoración, fijado al confirmar. NULL en
-- las líneas anteriores: equivale a cantidad × costo.
ALTER TABLE purchase_receipt_lines ADD COLUMN valued TEXT CHECK (valued IS NULL OR (length(valued) BETWEEN 1 AND 40 AND valued NOT GLOB '*[^0-9.]*'));

ALTER TABLE purchase_expenses ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD' CHECK (currency IN ('USD', 'VES'));
ALTER TABLE purchase_expenses ADD COLUMN expense_date TEXT NOT NULL DEFAULT '2000-01-01' CHECK (expense_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]');
ALTER TABLE purchase_expenses ADD COLUMN rate_id TEXT CHECK (rate_id IS NULL OR length(rate_id) = 36);
-- Importe en la moneda de valoración. NULL en los gastos anteriores:
-- equivale al importe.
ALTER TABLE purchase_expenses ADD COLUMN valued TEXT CHECK (valued IS NULL OR (length(valued) BETWEEN 1 AND 40 AND valued NOT GLOB '*[^0-9.]*' AND valued GLOB '*[1-9]*'));
UPDATE purchase_expenses SET
    currency = (SELECT c.valuation_currency FROM purchase_receipts r JOIN companies c ON c.organization_id = r.organization_id AND c.id = r.company_id
                WHERE r.organization_id = purchase_expenses.organization_id AND r.id = purchase_expenses.receipt_id),
    expense_date = date(created_at_utc, '-4 hours');

ALTER TABLE replacement_costs ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD' CHECK (currency IN ('USD', 'VES'));
UPDATE replacement_costs SET currency = (SELECT c.valuation_currency FROM companies c WHERE c.organization_id = replacement_costs.organization_id AND c.id = replacement_costs.company_id);

CREATE TRIGGER purchase_orders_guard BEFORE UPDATE ON purchase_orders
WHEN OLD.status <> 'open' OR NEW.id IS NOT OLD.id OR NEW.organization_id IS NOT OLD.organization_id OR NEW.number IS NOT OLD.number
  OR NEW.supplier_id IS NOT OLD.supplier_id OR NEW.branch_id IS NOT OLD.branch_id OR NEW.created_by IS NOT OLD.created_by
  OR NEW.currency IS NOT OLD.currency
BEGIN
    SELECT RAISE(ABORT, 'una orden cerrada no cambia');
END;

CREATE TRIGGER purchase_receipts_guard BEFORE UPDATE ON purchase_receipts
WHEN OLD.status <> 'draft' OR NEW.id IS NOT OLD.id OR NEW.organization_id IS NOT OLD.organization_id OR NEW.number IS NOT OLD.number
  OR NEW.warehouse_id IS NOT OLD.warehouse_id OR NEW.supplier_id IS NOT OLD.supplier_id OR NEW.order_id IS NOT OLD.order_id
  OR NEW.created_by IS NOT OLD.created_by OR NEW.currency IS NOT OLD.currency OR NEW.document_date IS NOT OLD.document_date
BEGIN
    SELECT RAISE(ABORT, 'una recepción confirmada o cancelada no cambia');
END;

-- Una recepción confirmada tiene todas sus líneas valoradas y una tasa de
-- su empresa, vigente a la fecha del documento, si y solo si su moneda no
-- es la de valoración.
CREATE TRIGGER purchase_receipts_rate BEFORE UPDATE ON purchase_receipts
WHEN NEW.status = 'confirmed' AND (
    EXISTS (SELECT 1 FROM purchase_receipt_lines l WHERE l.organization_id = NEW.organization_id AND l.receipt_id = NEW.id AND l.valued IS NULL)
    OR (NEW.rate_id IS NULL) <> (NEW.currency = (SELECT c.valuation_currency FROM companies c WHERE c.organization_id = NEW.organization_id AND c.id = NEW.company_id))
    OR (NEW.rate_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM exchange_rates x JOIN companies c ON c.organization_id = x.organization_id AND c.id = x.company_id
        WHERE x.organization_id = NEW.organization_id AND x.id = NEW.rate_id AND x.company_id = NEW.company_id AND x.valid_on <= NEW.document_date
          AND x.pair IN (NEW.currency || '/' || c.valuation_currency, c.valuation_currency || '/' || NEW.currency))))
BEGIN
    SELECT RAISE(ABORT, 'la recepción confirmada no tiene su valoración o su tasa');
END;

CREATE TRIGGER purchase_receipts_rate_draft BEFORE UPDATE ON purchase_receipts
WHEN NEW.status <> 'confirmed' AND NEW.rate_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'solo una recepción confirmada tiene tasa aplicada');
END;

CREATE TRIGGER purchase_receipts_rate_insert BEFORE INSERT ON purchase_receipts
WHEN NEW.rate_id IS NOT NULL OR NEW.status <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'una recepción nace en borrador y sin tasa');
END;

CREATE TRIGGER purchase_expenses_no_update BEFORE UPDATE ON purchase_expenses
BEGIN
    SELECT RAISE(ABORT, 'el registro purchase_expenses es de solo inserción');
END;

-- Un gasto nuevo está valorado y lleva una tasa de su empresa, vigente a su
-- fecha, si y solo si su moneda no es la de valoración.
CREATE TRIGGER purchase_expenses_rate BEFORE INSERT ON purchase_expenses
WHEN NEW.valued IS NULL
  OR (NEW.rate_id IS NULL) <> (NEW.currency = (SELECT c.valuation_currency FROM purchase_receipts r JOIN companies c ON c.organization_id = r.organization_id AND c.id = r.company_id
                                              WHERE r.organization_id = NEW.organization_id AND r.id = NEW.receipt_id))
  OR (NEW.rate_id IS NOT NULL AND NOT EXISTS (
      SELECT 1 FROM exchange_rates x JOIN purchase_receipts r ON r.organization_id = x.organization_id AND r.company_id = x.company_id
      JOIN companies c ON c.organization_id = r.organization_id AND c.id = r.company_id
      WHERE x.organization_id = NEW.organization_id AND x.id = NEW.rate_id AND r.id = NEW.receipt_id AND x.valid_on <= NEW.expense_date
        AND x.pair IN (NEW.currency || '/' || c.valuation_currency, c.valuation_currency || '/' || NEW.currency)))
BEGIN
    SELECT RAISE(ABORT, 'el gasto no tiene su valoración o su tasa');
END;

CREATE TRIGGER replacement_costs_no_update BEFORE UPDATE ON replacement_costs
BEGIN
    SELECT RAISE(ABORT, 'el registro replacement_costs es de solo inserción');
END;

-- Permisos nuevos en las plantillas, como en identity.Templates().
INSERT INTO role_permissions (organization_id, role_id, permission)
SELECT r.organization_id, r.id, p.permission
FROM roles r
JOIN (
    SELECT 'ADMIN_ORG' AS code, 'currency.rate.read' AS permission
    UNION ALL SELECT 'ADMIN_ORG', 'currency.rate.manage'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'currency.rate.read'
    UNION ALL SELECT 'ADMIN_EMPRESA', 'currency.rate.manage'
    UNION ALL SELECT 'ENCARGADO', 'currency.rate.read'
    UNION ALL SELECT 'ENCARGADO', 'currency.rate.manage'
    UNION ALL SELECT 'CAJERO', 'currency.rate.read'
    UNION ALL SELECT 'ALMACENISTA', 'currency.rate.read'
) p ON p.code = r.code;

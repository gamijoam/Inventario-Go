-- Revierte 0009. Sin las columnas de moneda, un documento en otra moneda se
-- leería como si estuviera en la de valoración; si hay alguno, la reversión
-- falla sin cambios.
CREATE TEMP TABLE revert_0009 (foreign_documents INTEGER NOT NULL CHECK (foreign_documents = 0));
INSERT INTO revert_0009
SELECT (SELECT COUNT(*) FROM purchase_orders o JOIN companies c ON c.organization_id = o.organization_id AND c.id = o.company_id WHERE o.currency <> c.valuation_currency)
     + (SELECT COUNT(*) FROM purchase_receipts r JOIN companies c ON c.organization_id = r.organization_id AND c.id = r.company_id WHERE r.currency <> c.valuation_currency)
     + (SELECT COUNT(*) FROM purchase_expenses WHERE rate_id IS NOT NULL)
     + (SELECT COUNT(*) FROM replacement_costs x JOIN companies c ON c.organization_id = x.organization_id AND c.id = x.company_id WHERE x.currency <> c.valuation_currency);
DROP TABLE revert_0009;

DELETE FROM role_permissions WHERE permission IN ('currency.rate.read', 'currency.rate.manage');

DROP TRIGGER purchase_orders_guard;
DROP TRIGGER purchase_receipts_guard;
DROP TRIGGER purchase_receipts_rate;
DROP TRIGGER purchase_receipts_rate_draft;
DROP TRIGGER purchase_receipts_rate_insert;
DROP TRIGGER purchase_expenses_rate;

ALTER TABLE purchase_orders DROP COLUMN currency;
ALTER TABLE purchase_receipts DROP COLUMN currency;
ALTER TABLE purchase_receipts DROP COLUMN document_date;
ALTER TABLE purchase_receipts DROP COLUMN rate_id;
ALTER TABLE purchase_receipt_lines DROP COLUMN valued;
ALTER TABLE purchase_expenses DROP COLUMN currency;
ALTER TABLE purchase_expenses DROP COLUMN expense_date;
ALTER TABLE purchase_expenses DROP COLUMN rate_id;
ALTER TABLE purchase_expenses DROP COLUMN valued;
ALTER TABLE replacement_costs DROP COLUMN currency;

CREATE TRIGGER purchase_orders_guard BEFORE UPDATE ON purchase_orders
WHEN OLD.status <> 'open' OR NEW.id IS NOT OLD.id OR NEW.organization_id IS NOT OLD.organization_id OR NEW.number IS NOT OLD.number
  OR NEW.supplier_id IS NOT OLD.supplier_id OR NEW.branch_id IS NOT OLD.branch_id OR NEW.created_by IS NOT OLD.created_by
BEGIN
    SELECT RAISE(ABORT, 'una orden cerrada no cambia');
END;

CREATE TRIGGER purchase_receipts_guard BEFORE UPDATE ON purchase_receipts
WHEN OLD.status <> 'draft' OR NEW.id IS NOT OLD.id OR NEW.organization_id IS NOT OLD.organization_id OR NEW.number IS NOT OLD.number
  OR NEW.warehouse_id IS NOT OLD.warehouse_id OR NEW.supplier_id IS NOT OLD.supplier_id OR NEW.order_id IS NOT OLD.order_id
  OR NEW.created_by IS NOT OLD.created_by
BEGIN
    SELECT RAISE(ABORT, 'una recepción confirmada o cancelada no cambia');
END;

DROP TABLE exchange_rates;

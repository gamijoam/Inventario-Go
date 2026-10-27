-- Revierte 0009. Sin las columnas de moneda, un documento en otra moneda se
-- leería como si estuviera en la de valoración; si hay alguno, la reversión
-- falla sin cambios.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM purchase_orders o JOIN companies c ON c.organization_id = o.organization_id AND c.id = o.company_id WHERE o.currency <> c.valuation_currency)
        OR EXISTS (SELECT 1 FROM purchase_receipts r JOIN companies c ON c.organization_id = r.organization_id AND c.id = r.company_id WHERE r.currency <> c.valuation_currency)
        OR EXISTS (SELECT 1 FROM purchase_expenses WHERE rate_id IS NOT NULL)
        OR EXISTS (SELECT 1 FROM replacement_costs x JOIN companies c ON c.organization_id = x.organization_id AND c.id = x.company_id WHERE x.currency <> c.valuation_currency) THEN
        RAISE EXCEPTION 'hay documentos en otra moneda; la reversión los confundiría';
    END IF;
END;
$$;

DELETE FROM role_permissions WHERE permission IN ('currency.rate.read', 'currency.rate.manage');

DROP TRIGGER purchase_orders_currency ON purchase_orders;
DROP TRIGGER purchase_receipts_currency ON purchase_receipts;
DROP TRIGGER purchase_expenses_currency ON purchase_expenses;
DROP FUNCTION purchase_currency_guard();
DROP FUNCTION purchase_rate_ok(UUID, UUID, UUID, TEXT, TEXT, DATE);

ALTER TABLE purchase_orders DROP COLUMN currency;
ALTER TABLE purchase_receipts DROP COLUMN currency, DROP COLUMN document_date, DROP COLUMN rate_id;
ALTER TABLE purchase_receipt_lines DROP COLUMN valued;
ALTER TABLE purchase_expenses DROP COLUMN currency, DROP COLUMN expense_date, DROP COLUMN rate_id, DROP COLUMN valued;
ALTER TABLE replacement_costs DROP COLUMN currency;

DROP TABLE exchange_rates;
DROP FUNCTION exchange_rates_guard();

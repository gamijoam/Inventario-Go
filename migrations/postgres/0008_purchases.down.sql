-- Revierte 0008. Si ya hay entradas de compra o revaloraciones en el
-- kardex, el CHECK anterior las rechaza y la reversión falla sin cambios.
DELETE FROM role_permissions WHERE permission IN ('purchase.read', 'purchase.manage', 'purchase.receipt.confirm', 'purchase.replacement_cost.manage');

DROP TABLE replacement_costs;
DROP TABLE purchase_expense_allocations;
DROP TABLE purchase_expenses;
DROP TABLE purchase_receipt_lines;

ALTER TABLE inventory_movements
    DROP COLUMN receipt_id,
    DROP CONSTRAINT inventory_movements_kind_check,
    DROP CONSTRAINT inventory_movements_reason_check,
    DROP CONSTRAINT inventory_movements_quantity_check,
    ADD CONSTRAINT inventory_movements_quantity_check CHECK (quantity <> 0),
    ADD CONSTRAINT inventory_movements_kind_check CHECK (kind IN ('adjustment_in', 'adjustment_out', 'reversal', 'transfer_out', 'transfer_in',
        'reservation_out', 'count_in', 'count_out')),
    ADD CONSTRAINT inventory_movements_reason_check CHECK (reason IN ('initial_load', 'correction', 'shrinkage', 'internal_use', 'other', 'reversal',
        'transfer', 'reservation', 'count'));

DROP TABLE purchase_receipts;
DROP TABLE purchase_order_lines;
DROP TABLE purchase_orders;
DROP TABLE suppliers;
DROP FUNCTION purchase_guard();

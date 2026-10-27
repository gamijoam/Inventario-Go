-- Revierte 0006. Si ya hay transferencias, el CHECK del kardex anterior
-- rechaza sus movimientos y la reversión falla sin cambios: no se pierden
-- datos en silencio.
DELETE FROM role_permissions WHERE permission = 'inventory.transfer.create';

ALTER TABLE inventory_movements
    DROP COLUMN transfer_id,
    DROP COLUMN value_after,
    DROP COLUMN value_adjustment,
    DROP COLUMN cost,
    DROP COLUMN unit_cost,
    DROP CONSTRAINT inventory_movements_kind_check,
    DROP CONSTRAINT inventory_movements_reason_check,
    ADD CONSTRAINT inventory_movements_kind_check CHECK (kind IN ('adjustment_in', 'adjustment_out', 'reversal')),
    ADD CONSTRAINT inventory_movements_reason_check CHECK (reason IN ('initial_load', 'correction', 'shrinkage', 'internal_use', 'other', 'reversal'));

DROP TABLE inventory_transfers;

ALTER TABLE inventory_balances DROP COLUMN value;

CREATE OR REPLACE FUNCTION inventory_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'inventory_balances' THEN
        IF TG_OP = 'DELETE' THEN
            RAISE EXCEPTION 'los saldos de inventario no se borran';
        END IF;
        IF NEW.version <> OLD.version + 1 THEN
            RAISE EXCEPTION 'el saldo debe avanzar una versión por movimiento';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'el kardex es de solo inserción';
    END IF;
    -- Un movimiento nuevo debe corresponder a la versión y existencia que el
    -- saldo ya registra (se actualiza antes en la misma transacción).
    IF NOT EXISTS (
        SELECT 1 FROM inventory_balances b
        WHERE b.organization_id = NEW.organization_id AND b.warehouse_id = NEW.warehouse_id AND b.product_id = NEW.product_id
          AND b.version = NEW.sequence AND b.on_hand = NEW.on_hand_after
    ) THEN
        RAISE EXCEPTION 'el movimiento no coincide con el saldo';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER companies_valuation_currency_fixed ON companies;
DROP FUNCTION companies_valuation_currency_fixed();
ALTER TABLE companies DROP COLUMN valuation_currency;

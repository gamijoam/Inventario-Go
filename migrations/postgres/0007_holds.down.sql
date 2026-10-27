-- Revierte 0007. Si hay movimientos o saldos que solo admite la 0007
-- (despachos de reservas, conteos, déficit), los CHECK anteriores los
-- rechazan y la reversión falla sin cambios.
DELETE FROM role_permissions WHERE permission IN ('inventory.reservation.manage', 'inventory.block.manage', 'inventory.count.manage',
    'inventory.count.approve', 'inventory.policy.manage', 'inventory.negative_stock.override');

ALTER TABLE inventory_movements
    DROP COLUMN policy_change_id,
    DROP COLUMN count_id,
    DROP COLUMN block_id,
    DROP COLUMN reservation_id,
    DROP CONSTRAINT inventory_movements_kind_check,
    DROP CONSTRAINT inventory_movements_reason_check,
    DROP CONSTRAINT inventory_movements_value_after_check,
    ADD CONSTRAINT inventory_movements_kind_check CHECK (kind IN ('adjustment_in', 'adjustment_out', 'reversal', 'transfer_out', 'transfer_in')),
    ADD CONSTRAINT inventory_movements_reason_check CHECK (reason IN ('initial_load', 'correction', 'shrinkage', 'internal_use', 'other', 'reversal', 'transfer')),
    ADD CONSTRAINT inventory_movements_value_after_check CHECK (value_after >= 0);

DROP TABLE inventory_count_lines;
DROP TABLE inventory_counts;
DROP TABLE inventory_blocks;
DROP TABLE inventory_reservations;
DROP TABLE inventory_policy_changes;
DROP FUNCTION inventory_hold_guard();

ALTER TABLE inventory_balances
    DROP CONSTRAINT inventory_balances_value_check,
    ADD CONSTRAINT inventory_balances_value_check CHECK (value >= 0 AND (on_hand <> 0 OR value = 0));

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
        IF NEW.version <> OLD.version + 1 THEN
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

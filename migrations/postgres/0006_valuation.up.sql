-- Costo promedio ponderado por producto y almacén y transferencias entre
-- almacenes de una sucursal (BP-M2-002, DEC-007.E2, secciones 21.1 y 25.8).
-- Valores con 6 decimales; costo unitario con 12 (sección 25.3).

-- Moneda de valoración del inventario de cada empresa; no cambia.
ALTER TABLE companies ADD COLUMN valuation_currency TEXT NOT NULL DEFAULT 'USD' CHECK (valuation_currency IN ('USD', 'VES'));

CREATE FUNCTION companies_valuation_currency_fixed() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.valuation_currency IS DISTINCT FROM OLD.valuation_currency THEN
        RAISE EXCEPTION 'la moneda de valoración de una empresa no cambia';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER companies_valuation_currency_fixed BEFORE UPDATE OF valuation_currency ON companies
    FOR EACH ROW EXECUTE FUNCTION companies_valuation_currency_fixed();

-- Valor total de la existencia. NULL: existencia cargada antes de esta
-- versión que ninguna entrada con costo ha valorado. Sin existencia no hay
-- valor.
ALTER TABLE inventory_balances ADD COLUMN value NUMERIC(28,6) CHECK (value >= 0 AND (on_hand <> 0 OR value = 0));

CREATE TABLE inventory_transfers (
    organization_id   UUID          NOT NULL,
    id                UUID          NOT NULL PRIMARY KEY,
    company_id        UUID          NOT NULL,
    branch_id         UUID          NOT NULL,
    from_warehouse_id UUID          NOT NULL,
    to_warehouse_id   UUID          NOT NULL,
    product_id        UUID          NOT NULL,
    quantity          NUMERIC(28,6) NOT NULL CHECK (quantity > 0),
    value             NUMERIC(28,6) NOT NULL CHECK (value >= 0),
    note              TEXT          NOT NULL CHECK (length(note) <= 500),
    actor_id          UUID          NOT NULL,
    occurred_at_utc   TIMESTAMPTZ   NOT NULL,
    idempotency_key   TEXT          CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 100),
    request_hash      TEXT          NOT NULL CHECK (length(request_hash) = 64),
    CHECK (from_warehouse_id <> to_warehouse_id),
    UNIQUE (organization_id, id),
    -- Ambos almacenes cuelgan de la misma sucursal.
    FOREIGN KEY (organization_id, company_id, branch_id, from_warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id, branch_id, to_warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, actor_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX inventory_transfers_key_uq ON inventory_transfers (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

ALTER TABLE inventory_movements
    DROP CONSTRAINT inventory_movements_kind_check,
    DROP CONSTRAINT inventory_movements_reason_check,
    ADD COLUMN unit_cost NUMERIC(40,12) CHECK (unit_cost >= 0),
    ADD COLUMN cost NUMERIC(28,6),
    ADD COLUMN value_adjustment NUMERIC(28,6) NOT NULL DEFAULT 0,
    ADD COLUMN value_after NUMERIC(28,6) CHECK (value_after >= 0),
    ADD COLUMN transfer_id UUID,
    ADD CONSTRAINT inventory_movements_kind_check CHECK (kind IN ('adjustment_in', 'adjustment_out', 'reversal', 'transfer_out', 'transfer_in')),
    ADD CONSTRAINT inventory_movements_reason_check CHECK (reason IN ('initial_load', 'correction', 'shrinkage', 'internal_use', 'other', 'reversal', 'transfer')),
    ADD CHECK ((kind IN ('transfer_out', 'transfer_in')) = (reason = 'transfer')),
    ADD CHECK ((kind IN ('transfer_out', 'transfer_in')) = (transfer_id IS NOT NULL)),
    ADD CHECK (kind <> 'transfer_in' OR quantity > 0),
    ADD CHECK (kind <> 'transfer_out' OR quantity < 0),
    ADD CHECK ((unit_cost IS NULL) = (cost IS NULL)),
    -- El costo lleva el signo de la cantidad.
    ADD CHECK (cost IS NULL OR cost = 0 OR sign(cost) = sign(quantity)),
    ADD FOREIGN KEY (organization_id, transfer_id) REFERENCES inventory_transfers (organization_id, id) ON DELETE RESTRICT;

-- Una salida y una entrada por transferencia.
CREATE UNIQUE INDEX inventory_movements_transfer_uq ON inventory_movements (organization_id, transfer_id, kind) WHERE transfer_id IS NOT NULL;

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

CREATE TRIGGER inventory_transfers_guard BEFORE UPDATE OR DELETE ON inventory_transfers
    FOR EACH ROW EXECUTE FUNCTION inventory_guard();
CREATE TRIGGER inventory_transfers_no_truncate BEFORE TRUNCATE ON inventory_transfers
    FOR EACH STATEMENT EXECUTE FUNCTION inventory_guard();

-- Permiso nuevo en las plantillas, como en identity.Templates().
INSERT INTO role_permissions (organization_id, role_id, permission)
SELECT r.organization_id, r.id, 'inventory.transfer.create'
FROM roles r
WHERE r.code IN ('ADMIN_ORG', 'ADMIN_EMPRESA', 'ENCARGADO', 'ALMACENISTA');

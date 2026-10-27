package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
	"github.com/gamijoam/inventario-go/internal/platform/config"
)

// StockStore implementa stock.Store.
func (s *Store) StockStore() stock.Store { return stockStore{s} }

type stockStore struct{ s *Store }

func (c stockStore) InTx(ctx context.Context, fn func(stock.Repository) error) error {
	return orgStore(c).InTx(ctx, func(r apporg.Repository) error { return fn(r.(*orgRepo)) })
}

// dec convierte una cantidad a su forma guardada: texto decimal exacto en
// SQLite; PostgreSQL recibe el mismo texto para NUMERIC.
func dec(d decimal.Decimal) string { return d.String() }

// parseDec lee una cantidad; PostgreSQL devuelve NUMERIC con ceros a la
// derecha ("10.000000"), que Parse acepta.
func parseDec(raw string) (decimal.Decimal, error) {
	d, err := decimal.Parse(raw)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("cantidad guardada inválida %q: %w", raw, err)
	}
	return d, nil
}

func (r *orgRepo) InsertBalance(ctx context.Context, b inventory.Balance) error {
	sc := b.Scope
	_, err := r.tx.ExecContext(ctx, r.q(
		"INSERT INTO inventory_balances (organization_id, company_id, branch_id, warehouse_id, product_id, on_hand, reserved, blocked, version, updated_at_utc) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"),
		sc.Organization().String(), sc.Company().String(), sc.Branch().String(), sc.Warehouse().String(), b.ProductID.String(),
		dec(b.OnHand), dec(b.Reserved), dec(b.Blocked), b.Version, r.ts(b.UpdatedAt))
	if r.s.isUniqueViolation(err) {
		return stock.ErrConcurrent
	}
	if err != nil {
		return fmt.Errorf("guardar saldo: %w", err)
	}
	return nil
}

func (r *orgRepo) UpdateBalance(ctx context.Context, b inventory.Balance, previousVersion int64) error {
	sc := b.Scope
	res, err := r.tx.ExecContext(ctx, r.q(
		"UPDATE inventory_balances SET on_hand = ?, reserved = ?, blocked = ?, version = ?, updated_at_utc = ? WHERE organization_id = ? AND warehouse_id = ? AND product_id = ? AND version = ?"),
		dec(b.OnHand), dec(b.Reserved), dec(b.Blocked), b.Version, r.ts(b.UpdatedAt),
		sc.Organization().String(), sc.Warehouse().String(), b.ProductID.String(), previousVersion)
	if err != nil {
		return fmt.Errorf("guardar saldo: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return stock.ErrConcurrent
	}
	return nil
}

const balanceCols = "CAST(organization_id AS TEXT), CAST(company_id AS TEXT), CAST(branch_id AS TEXT), CAST(warehouse_id AS TEXT), CAST(product_id AS TEXT), " +
	"CAST(on_hand AS TEXT), CAST(reserved AS TEXT), CAST(blocked AS TEXT), version, updated_at_utc"

func scanBalance(sc scanner) (inventory.Balance, error) {
	var o, c, br, w, p, onHand, reserved, blocked string
	var updated any
	var b inventory.Balance
	if err := sc.Scan(&o, &c, &br, &w, &p, &onHand, &reserved, &blocked, &b.Version, &updated); err != nil {
		return b, err
	}
	ids, err := parseIDs(o, c, br, w, p)
	if err != nil {
		return b, err
	}
	if b.Scope, err = scope.New(ids[0], ids[1], ids[2], ids[3]); err != nil {
		return b, err
	}
	b.ProductID = ids[4]
	if b.OnHand, err = parseDec(onHand); err != nil {
		return b, err
	}
	if b.Reserved, err = parseDec(reserved); err != nil {
		return b, err
	}
	if b.Blocked, err = parseDec(blocked); err != nil {
		return b, err
	}
	b.UpdatedAt, err = parseTime(updated)
	return b, err
}

func (r *orgRepo) Balance(ctx context.Context, orgID, warehouseID, productID id.ID) (inventory.Balance, error) {
	row := r.tx.QueryRowContext(ctx, r.q("SELECT "+balanceCols+" FROM inventory_balances WHERE organization_id = ? AND warehouse_id = ? AND product_id = ?"),
		orgID.String(), warehouseID.String(), productID.String())
	b, err := scanBalance(row)
	return b, notFound(err, "saldo")
}

// BalanceForUpdate bloquea la fila en PostgreSQL. En SQLite la transacción
// de escritura ya es exclusiva (BEGIN IMMEDIATE).
func (r *orgRepo) BalanceForUpdate(ctx context.Context, orgID, warehouseID, productID id.ID) (inventory.Balance, error) {
	q := "SELECT " + balanceCols + " FROM inventory_balances WHERE organization_id = ? AND warehouse_id = ? AND product_id = ?"
	if r.s.Engine != config.DriverSQLite {
		q += " FOR UPDATE"
	}
	b, err := scanBalance(r.tx.QueryRowContext(ctx, r.q(q), orgID.String(), warehouseID.String(), productID.String()))
	return b, notFound(err, "saldo")
}

func (r *orgRepo) Balances(ctx context.Context, orgID, warehouseID, productID id.ID) ([]inventory.Balance, error) {
	q, args := "SELECT "+balanceCols+" FROM inventory_balances WHERE organization_id = ?", []any{orgID.String()}
	if !warehouseID.IsZero() {
		q, args = q+" AND warehouse_id = ?", append(args, warehouseID.String())
	}
	if !productID.IsZero() {
		q, args = q+" AND product_id = ?", append(args, productID.String())
	}
	return queryAll(ctx, r, scanBalance, q+" ORDER BY company_id, branch_id, warehouse_id, product_id", args...)
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (r *orgRepo) InsertMovement(ctx context.Context, m inventory.Movement) error {
	sc := m.Scope
	_, err := r.tx.ExecContext(ctx, r.q(
		"INSERT INTO inventory_movements (organization_id, id, company_id, branch_id, warehouse_id, product_id, sequence, kind, reason, quantity, on_hand_after, note, actor_id, occurred_at_utc, reverses_id, idempotency_key, request_hash) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"),
		sc.Organization().String(), m.ID.String(), sc.Company().String(), sc.Branch().String(), sc.Warehouse().String(), m.ProductID.String(),
		m.Sequence, string(m.Kind), string(m.Reason), dec(m.Quantity), dec(m.OnHandAfter), m.Note, m.ActorID.String(), r.ts(m.OccurredAt),
		nullID(m.ReversesID), nullString(m.IdempotencyKey), m.RequestHash)
	if r.s.isUniqueViolation(err) {
		// Otro proceso usó la misma clave o revirtió el mismo movimiento:
		// al reintentar se encontrará su resultado.
		return stock.ErrConcurrent
	}
	if err != nil {
		return fmt.Errorf("guardar movimiento: %w", err)
	}
	return nil
}

const movementCols = "CAST(organization_id AS TEXT), CAST(id AS TEXT), CAST(company_id AS TEXT), CAST(branch_id AS TEXT), CAST(warehouse_id AS TEXT), CAST(product_id AS TEXT), " +
	"sequence, kind, reason, CAST(quantity AS TEXT), CAST(on_hand_after AS TEXT), note, CAST(actor_id AS TEXT), occurred_at_utc, CAST(reverses_id AS TEXT), idempotency_key, request_hash"

func scanMovement(sc scanner) (inventory.Movement, error) {
	var o, mid, c, br, w, p, kind, reason, qty, after, actor string
	var reverses, key sql.NullString
	var occurred any
	var m inventory.Movement
	if err := sc.Scan(&o, &mid, &c, &br, &w, &p, &m.Sequence, &kind, &reason, &qty, &after, &m.Note, &actor, &occurred, &reverses, &key, &m.RequestHash); err != nil {
		return m, err
	}
	ids, err := parseIDs(o, mid, c, br, w, p, actor, reverses.String)
	if err != nil {
		return m, err
	}
	if m.Scope, err = scope.New(ids[0], ids[2], ids[3], ids[4]); err != nil {
		return m, err
	}
	m.ID, m.ProductID, m.ActorID, m.ReversesID = ids[1], ids[5], ids[6], ids[7]
	m.Kind, m.Reason, m.IdempotencyKey = inventory.Kind(kind), inventory.Reason(reason), key.String
	if m.Quantity, err = parseDec(qty); err != nil {
		return m, err
	}
	if m.OnHandAfter, err = parseDec(after); err != nil {
		return m, err
	}
	m.OccurredAt, err = parseTime(occurred)
	return m, err
}

func (r *orgRepo) movementWhere(ctx context.Context, what, where string, args ...any) (inventory.Movement, error) {
	m, err := scanMovement(r.tx.QueryRowContext(ctx, r.q("SELECT "+movementCols+" FROM inventory_movements WHERE "+where), args...))
	if errors.Is(err, sql.ErrNoRows) {
		return m, fmt.Errorf("%w: %s", apporg.ErrNotFound, what)
	}
	return m, err
}

func (r *orgRepo) Movement(ctx context.Context, orgID, movementID id.ID) (inventory.Movement, error) {
	return r.movementWhere(ctx, "movimiento "+movementID.String(), "organization_id = ? AND id = ?", orgID.String(), movementID.String())
}

func (r *orgRepo) MovementByKey(ctx context.Context, orgID id.ID, key string) (inventory.Movement, error) {
	return r.movementWhere(ctx, "movimiento con esa clave", "organization_id = ? AND idempotency_key = ?", orgID.String(), key)
}

func (r *orgRepo) ReversalOf(ctx context.Context, orgID, movementID id.ID) (inventory.Movement, error) {
	return r.movementWhere(ctx, "reverso", "organization_id = ? AND reverses_id = ?", orgID.String(), movementID.String())
}

func (r *orgRepo) Movements(ctx context.Context, orgID, warehouseID, productID id.ID, limit, offset int) ([]inventory.Movement, error) {
	q := "SELECT " + movementCols + " FROM inventory_movements WHERE organization_id = ? AND warehouse_id = ? AND product_id = ? ORDER BY sequence"
	args := []any{orgID.String(), warehouseID.String(), productID.String()}
	if limit > 0 {
		q, args = q+" LIMIT ? OFFSET ?", append(args, limit, offset)
	}
	return queryAll(ctx, r, scanMovement, q, args...)
}

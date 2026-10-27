package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

// Políticas de existencia negativa, reservas, bloqueos y conteos
// (BP-M2-003).

func parseNullTime(v any) (time.Time, error) {
	if v == nil {
		return time.Time{}, nil
	}
	return parseTime(v)
}

func nullDec(known bool, d interface{ String() string }) any {
	if !known {
		return nil
	}
	return d.String()
}

func (r *orgRepo) InsertPolicyChange(ctx context.Context, c inventory.PolicyChange) error {
	var enabled, mode, limit any
	if c.ProductID.IsZero() {
		enabled = c.Company.NegativeEnabled
		limit = nullDec(c.Company.NegativeEnabled, c.Company.Limit)
	} else {
		mode = string(c.Product.Mode)
		limit = nullDec(c.Product.Mode == inventory.NegativeAllow, c.Product.Limit)
	}
	_, err := r.tx.ExecContext(ctx, r.q(
		"INSERT INTO inventory_policy_changes (organization_id, id, company_id, product_id, negative_enabled, mode, negative_limit, actor_id, occurred_at_utc) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"),
		c.OrganizationID.String(), c.ID.String(), c.CompanyID.String(), nullID(c.ProductID), enabled, mode, limit, c.ActorID.String(), r.ts(c.OccurredAt))
	if err != nil {
		return fmt.Errorf("guardar política: %w", err)
	}
	return nil
}

const policyCols = "CAST(p.organization_id AS TEXT), CAST(p.id AS TEXT), CAST(p.company_id AS TEXT), CAST(p.product_id AS TEXT), p.negative_enabled, p.mode, " +
	"CAST(p.negative_limit AS TEXT), CAST(p.actor_id AS TEXT), p.occurred_at_utc"

// latestPolicy filtra la versión vigente: no hay otra posterior para la
// misma empresa y producto.
const latestPolicy = " AND NOT EXISTS (SELECT 1 FROM inventory_policy_changes q WHERE q.organization_id = p.organization_id AND q.company_id = p.company_id " +
	"AND ((q.product_id IS NULL AND p.product_id IS NULL) OR q.product_id = p.product_id) AND (q.occurred_at_utc > p.occurred_at_utc OR (q.occurred_at_utc = p.occurred_at_utc AND q.id > p.id)))"

func scanPolicy(sc scanner) (inventory.PolicyChange, error) {
	var o, pid, c, actor string
	var product, mode, limit sql.NullString
	var enabled sql.NullBool
	var at any
	var ch inventory.PolicyChange
	if err := sc.Scan(&o, &pid, &c, &product, &enabled, &mode, &limit, &actor, &at); err != nil {
		return ch, err
	}
	ids, err := parseIDs(o, pid, c, product.String, actor)
	if err != nil {
		return ch, err
	}
	ch.OrganizationID, ch.ID, ch.CompanyID, ch.ProductID, ch.ActorID = ids[0], ids[1], ids[2], ids[3], ids[4]
	var l inventory.Amount
	if l, err = parseAmount(limit); err != nil {
		return ch, err
	}
	if ch.ProductID.IsZero() {
		ch.Company = inventory.CompanyPolicy{NegativeEnabled: enabled.Bool, Limit: l.Value, ChangeID: ch.ID}
	} else {
		ch.Product = inventory.ProductPolicy{Mode: inventory.NegativeMode(mode.String), Limit: l.Value, ChangeID: ch.ID}
	}
	ch.OccurredAt, err = parseTime(at)
	return ch, err
}

func (r *orgRepo) CurrentPolicy(ctx context.Context, orgID, companyID, productID id.ID) (inventory.PolicyChange, error) {
	q := "SELECT " + policyCols + " FROM inventory_policy_changes p WHERE p.organization_id = ? AND p.company_id = ? AND "
	args := []any{orgID.String(), companyID.String()}
	if productID.IsZero() {
		q += "p.product_id IS NULL"
	} else {
		q, args = q+"p.product_id = ?", append(args, productID.String())
	}
	ch, err := scanPolicy(r.tx.QueryRowContext(ctx, r.q(q+latestPolicy), args...))
	return ch, notFound(err, "política")
}

func (r *orgRepo) ProductPolicies(ctx context.Context, orgID, companyID id.ID) ([]inventory.PolicyChange, error) {
	return queryAll(ctx, r, scanPolicy, "SELECT "+policyCols+" FROM inventory_policy_changes p WHERE p.organization_id = ? AND p.company_id = ? AND p.product_id IS NOT NULL"+
		latestPolicy+" ORDER BY p.product_id", orgID.String(), companyID.String())
}

func (r *orgRepo) InsertReservation(ctx context.Context, v inventory.Reservation) error {
	sc := v.Scope
	_, err := r.tx.ExecContext(ctx, r.q(
		"INSERT INTO inventory_reservations (organization_id, id, company_id, branch_id, warehouse_id, product_id, quantity, consumed, status, reference, note, "+
			"expires_at_utc, created_by, created_at_utc, closed_by, closed_at_utc, close_note, idempotency_key, request_hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"),
		sc.Organization().String(), v.ID.String(), sc.Company().String(), sc.Branch().String(), sc.Warehouse().String(), v.ProductID.String(),
		dec(v.Quantity), dec(v.Consumed), string(v.Status), v.Reference, v.Note, r.nullTime(v.ExpiresAt), v.CreatedBy.String(), r.ts(v.CreatedAt),
		nullID(v.ClosedBy), r.nullTime(v.ClosedAt), v.CloseNote, nullString(v.IdempotencyKey), v.RequestHash)
	if r.s.isUniqueViolation(err) {
		return stock.ErrConcurrent
	}
	if err != nil {
		return fmt.Errorf("guardar reserva: %w", err)
	}
	return nil
}

func (r *orgRepo) UpdateReservation(ctx context.Context, v inventory.Reservation) error {
	return r.execOne(ctx, "reserva",
		"UPDATE inventory_reservations SET consumed = ?, status = ?, closed_by = ?, closed_at_utc = ?, close_note = ? WHERE organization_id = ? AND id = ? AND status = 'active'",
		dec(v.Consumed), string(v.Status), nullID(v.ClosedBy), r.nullTime(v.ClosedAt), v.CloseNote, v.Scope.Organization().String(), v.ID.String())
}

const reservationCols = "CAST(organization_id AS TEXT), CAST(id AS TEXT), CAST(company_id AS TEXT), CAST(branch_id AS TEXT), CAST(warehouse_id AS TEXT), CAST(product_id AS TEXT), " +
	"CAST(quantity AS TEXT), CAST(consumed AS TEXT), status, reference, note, expires_at_utc, CAST(created_by AS TEXT), created_at_utc, CAST(closed_by AS TEXT), closed_at_utc, " +
	"close_note, idempotency_key, request_hash"

func scanKey(o, c, br, w, p string) (inventory.Key, error) {
	ids, err := parseIDs(o, c, br, w, p)
	if err != nil {
		return inventory.Key{}, err
	}
	sc, err := scope.New(ids[0], ids[1], ids[2], ids[3])
	return inventory.Key{Scope: sc, ProductID: ids[4]}, err
}

func scanReservation(sc scanner) (inventory.Reservation, error) {
	var o, rid, c, br, w, p, qty, consumed, status, createdBy string
	var closedBy, key sql.NullString
	var expires, created, closed any
	var v inventory.Reservation
	if err := sc.Scan(&o, &rid, &c, &br, &w, &p, &qty, &consumed, &status, &v.Reference, &v.Note, &expires, &createdBy, &created, &closedBy, &closed,
		&v.CloseNote, &key, &v.RequestHash); err != nil {
		return v, err
	}
	var err error
	if v.Key, err = scanKey(o, c, br, w, p); err != nil {
		return v, err
	}
	ids, err := parseIDs(rid, createdBy, closedBy.String)
	if err != nil {
		return v, err
	}
	v.ID, v.CreatedBy, v.ClosedBy, v.Status, v.IdempotencyKey = ids[0], ids[1], ids[2], inventory.HoldStatus(status), key.String
	if v.Quantity, err = parseDec(qty); err != nil {
		return v, err
	}
	if v.Consumed, err = parseDec(consumed); err != nil {
		return v, err
	}
	if v.ExpiresAt, err = parseNullTime(expires); err != nil {
		return v, err
	}
	if v.CreatedAt, err = parseTime(created); err != nil {
		return v, err
	}
	v.ClosedAt, err = parseNullTime(closed)
	return v, err
}

func (r *orgRepo) Reservation(ctx context.Context, orgID, reservationID id.ID) (inventory.Reservation, error) {
	v, err := scanReservation(r.tx.QueryRowContext(ctx, r.q("SELECT "+reservationCols+" FROM inventory_reservations WHERE organization_id = ? AND id = ?"),
		orgID.String(), reservationID.String()))
	return v, notFound(err, "reserva "+reservationID.String())
}

func (r *orgRepo) ReservationByKey(ctx context.Context, orgID id.ID, key string) (inventory.Reservation, error) {
	v, err := scanReservation(r.tx.QueryRowContext(ctx, r.q("SELECT "+reservationCols+" FROM inventory_reservations WHERE organization_id = ? AND idempotency_key = ?"),
		orgID.String(), key))
	return v, notFound(err, "reserva con esa clave")
}

func (r *orgRepo) Reservations(ctx context.Context, f stock.HoldFilter) ([]inventory.Reservation, error) {
	q, args := holdWhere("SELECT "+reservationCols+" FROM inventory_reservations WHERE organization_id = ?", f)
	return queryAll(ctx, r, scanReservation, q+" ORDER BY created_at_utc, id", args...)
}

func (r *orgRepo) DueReservations(ctx context.Context, f stock.HoldFilter, now time.Time) ([]inventory.Reservation, error) {
	f.Status = inventory.HoldActive
	q, args := holdWhere("SELECT "+reservationCols+" FROM inventory_reservations WHERE organization_id = ?", f)
	return queryAll(ctx, r, scanReservation, q+" AND expires_at_utc IS NOT NULL AND expires_at_utc <= ? ORDER BY warehouse_id, product_id, id", append(args, r.ts(now))...)
}

// holdWhere agrega los filtros no nulos de f.
func holdWhere(base string, f stock.HoldFilter) (string, []any) {
	q, args := base, []any{f.OrganizationID.String()}
	if !f.WarehouseID.IsZero() {
		q, args = q+" AND warehouse_id = ?", append(args, f.WarehouseID.String())
	}
	if !f.ProductID.IsZero() {
		q, args = q+" AND product_id = ?", append(args, f.ProductID.String())
	}
	if f.Status != "" {
		q, args = q+" AND status = ?", append(args, string(f.Status))
	}
	return q, args
}

func (r *orgRepo) InsertBlock(ctx context.Context, v inventory.Block) error {
	sc := v.Scope
	_, err := r.tx.ExecContext(ctx, r.q(
		"INSERT INTO inventory_blocks (organization_id, id, company_id, branch_id, warehouse_id, product_id, quantity, reason, note, status, "+
			"created_by, created_at_utc, closed_by, closed_at_utc, close_note, idempotency_key, request_hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"),
		sc.Organization().String(), v.ID.String(), sc.Company().String(), sc.Branch().String(), sc.Warehouse().String(), v.ProductID.String(),
		dec(v.Quantity), string(v.Reason), v.Note, string(v.Status), v.CreatedBy.String(), r.ts(v.CreatedAt),
		nullID(v.ClosedBy), r.nullTime(v.ClosedAt), v.CloseNote, nullString(v.IdempotencyKey), v.RequestHash)
	if r.s.isUniqueViolation(err) {
		return stock.ErrConcurrent
	}
	if err != nil {
		return fmt.Errorf("guardar bloqueo: %w", err)
	}
	return nil
}

func (r *orgRepo) UpdateBlock(ctx context.Context, v inventory.Block) error {
	return r.execOne(ctx, "bloqueo",
		"UPDATE inventory_blocks SET status = ?, closed_by = ?, closed_at_utc = ?, close_note = ? WHERE organization_id = ? AND id = ? AND status = 'active'",
		string(v.Status), nullID(v.ClosedBy), r.nullTime(v.ClosedAt), v.CloseNote, v.Scope.Organization().String(), v.ID.String())
}

const blockCols = "CAST(organization_id AS TEXT), CAST(id AS TEXT), CAST(company_id AS TEXT), CAST(branch_id AS TEXT), CAST(warehouse_id AS TEXT), CAST(product_id AS TEXT), " +
	"CAST(quantity AS TEXT), reason, note, status, CAST(created_by AS TEXT), created_at_utc, CAST(closed_by AS TEXT), closed_at_utc, close_note, idempotency_key, request_hash"

func scanBlock(sc scanner) (inventory.Block, error) {
	var o, bid, c, br, w, p, qty, reason, status, createdBy string
	var closedBy, key sql.NullString
	var created, closed any
	var v inventory.Block
	if err := sc.Scan(&o, &bid, &c, &br, &w, &p, &qty, &reason, &v.Note, &status, &createdBy, &created, &closedBy, &closed, &v.CloseNote, &key, &v.RequestHash); err != nil {
		return v, err
	}
	var err error
	if v.Key, err = scanKey(o, c, br, w, p); err != nil {
		return v, err
	}
	ids, err := parseIDs(bid, createdBy, closedBy.String)
	if err != nil {
		return v, err
	}
	v.ID, v.CreatedBy, v.ClosedBy, v.IdempotencyKey = ids[0], ids[1], ids[2], key.String
	v.Reason, v.Status = inventory.BlockReason(reason), inventory.HoldStatus(status)
	if v.Quantity, err = parseDec(qty); err != nil {
		return v, err
	}
	if v.CreatedAt, err = parseTime(created); err != nil {
		return v, err
	}
	v.ClosedAt, err = parseNullTime(closed)
	return v, err
}

func (r *orgRepo) Block(ctx context.Context, orgID, blockID id.ID) (inventory.Block, error) {
	v, err := scanBlock(r.tx.QueryRowContext(ctx, r.q("SELECT "+blockCols+" FROM inventory_blocks WHERE organization_id = ? AND id = ?"), orgID.String(), blockID.String()))
	return v, notFound(err, "bloqueo "+blockID.String())
}

func (r *orgRepo) BlockByKey(ctx context.Context, orgID id.ID, key string) (inventory.Block, error) {
	v, err := scanBlock(r.tx.QueryRowContext(ctx, r.q("SELECT "+blockCols+" FROM inventory_blocks WHERE organization_id = ? AND idempotency_key = ?"), orgID.String(), key))
	return v, notFound(err, "bloqueo con esa clave")
}

func (r *orgRepo) Blocks(ctx context.Context, f stock.HoldFilter) ([]inventory.Block, error) {
	q, args := holdWhere("SELECT "+blockCols+" FROM inventory_blocks WHERE organization_id = ?", f)
	return queryAll(ctx, r, scanBlock, q+" ORDER BY created_at_utc, id", args...)
}

func (r *orgRepo) InsertCount(ctx context.Context, c inventory.Count) error {
	sc := c.Key.Scope
	_, err := r.tx.ExecContext(ctx, r.q(
		"INSERT INTO inventory_counts (organization_id, id, company_id, branch_id, warehouse_id, status, note, created_by, created_at_utc, decision_note) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '')"),
		sc.Organization().String(), c.ID.String(), sc.Company().String(), sc.Branch().String(), sc.Warehouse().String(), string(c.Status), c.Note,
		c.CreatedBy.String(), r.ts(c.CreatedAt))
	if err != nil {
		return fmt.Errorf("guardar conteo: %w", err)
	}
	return nil
}

func (r *orgRepo) UpdateCount(ctx context.Context, c inventory.Count, previous inventory.CountStatus) error {
	return r.execOne(ctx, "conteo",
		"UPDATE inventory_counts SET status = ?, submitted_by = ?, submitted_at_utc = ?, decided_by = ?, decided_at_utc = ?, decision_note = ? WHERE organization_id = ? AND id = ? AND status = ?",
		string(c.Status), nullID(c.SubmittedBy), r.nullTime(c.SubmittedAt), nullID(c.DecidedBy), r.nullTime(c.DecidedAt), c.DecisionNote,
		c.Key.Scope.Organization().String(), c.ID.String(), string(previous))
}

const countCols = "CAST(organization_id AS TEXT), CAST(id AS TEXT), CAST(company_id AS TEXT), CAST(branch_id AS TEXT), CAST(warehouse_id AS TEXT), status, note, " +
	"CAST(created_by AS TEXT), created_at_utc, CAST(submitted_by AS TEXT), submitted_at_utc, CAST(decided_by AS TEXT), decided_at_utc, decision_note"

func scanCount(sc scanner) (inventory.Count, error) {
	var o, cid, c, br, w, status, createdBy string
	var submittedBy, decidedBy sql.NullString
	var created, submitted, decided any
	var v inventory.Count
	if err := sc.Scan(&o, &cid, &c, &br, &w, &status, &v.Note, &createdBy, &created, &submittedBy, &submitted, &decidedBy, &decided, &v.DecisionNote); err != nil {
		return v, err
	}
	ids, err := parseIDs(o, cid, c, br, w, createdBy, submittedBy.String, decidedBy.String)
	if err != nil {
		return v, err
	}
	if v.Key.Scope, err = scope.New(ids[0], ids[2], ids[3], ids[4]); err != nil {
		return v, err
	}
	v.ID, v.CreatedBy, v.SubmittedBy, v.DecidedBy, v.Status = ids[1], ids[5], ids[6], ids[7], inventory.CountStatus(status)
	if v.CreatedAt, err = parseTime(created); err != nil {
		return v, err
	}
	if v.SubmittedAt, err = parseNullTime(submitted); err != nil {
		return v, err
	}
	v.DecidedAt, err = parseNullTime(decided)
	return v, err
}

func (r *orgRepo) Count(ctx context.Context, orgID, countID id.ID) (inventory.Count, error) {
	v, err := scanCount(r.tx.QueryRowContext(ctx, r.q("SELECT "+countCols+" FROM inventory_counts WHERE organization_id = ? AND id = ?"), orgID.String(), countID.String()))
	return v, notFound(err, "conteo "+countID.String())
}

func (r *orgRepo) Counts(ctx context.Context, orgID, warehouseID id.ID, status inventory.CountStatus) ([]inventory.Count, error) {
	q, args := "SELECT "+countCols+" FROM inventory_counts WHERE organization_id = ?", []any{orgID.String()}
	if !warehouseID.IsZero() {
		q, args = q+" AND warehouse_id = ?", append(args, warehouseID.String())
	}
	if status != "" {
		q, args = q+" AND status = ?", append(args, string(status))
	}
	return queryAll(ctx, r, scanCount, q+" ORDER BY created_at_utc, id", args...)
}

// SaveCountLine inserta o reemplaza lo contado de un producto; la base solo
// lo admite con el conteo en borrador.
func (r *orgRepo) SaveCountLine(ctx context.Context, orgID id.ID, l inventory.CountLine) error {
	args := []any{dec(l.Counted), dec(l.Expected), l.ExpectedVersion, amount(l.UnitCost), l.CapturedBy.String(), r.ts(l.CapturedAt),
		orgID.String(), l.CountID.String(), l.ProductID.String()}
	res, err := r.tx.ExecContext(ctx, r.q("UPDATE inventory_count_lines SET counted = ?, expected = ?, expected_version = ?, unit_cost = ?, captured_by = ?, captured_at_utc = ? "+
		"WHERE organization_id = ? AND count_id = ? AND product_id = ?"), args...)
	if err != nil {
		return fmt.Errorf("guardar línea de conteo: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return err
	}
	_, err = r.tx.ExecContext(ctx, r.q("INSERT INTO inventory_count_lines (counted, expected, expected_version, unit_cost, captured_by, captured_at_utc, organization_id, count_id, product_id) "+
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"), args...)
	if err != nil {
		return fmt.Errorf("guardar línea de conteo: %w", err)
	}
	return nil
}

func (r *orgRepo) CountLines(ctx context.Context, orgID, countID id.ID) ([]inventory.CountLine, error) {
	return queryAll(ctx, r, func(sc scanner) (inventory.CountLine, error) {
		var cid, pid, counted, expected, by string
		var unitCost sql.NullString
		var at any
		var l inventory.CountLine
		if err := sc.Scan(&cid, &pid, &counted, &expected, &l.ExpectedVersion, &unitCost, &by, &at); err != nil {
			return l, err
		}
		ids, err := parseIDs(cid, pid, by)
		if err != nil {
			return l, err
		}
		l.CountID, l.ProductID, l.CapturedBy = ids[0], ids[1], ids[2]
		if l.Counted, err = parseDec(counted); err != nil {
			return l, err
		}
		if l.Expected, err = parseDec(expected); err != nil {
			return l, err
		}
		if l.UnitCost, err = parseAmount(unitCost); err != nil {
			return l, err
		}
		l.CapturedAt, err = parseTime(at)
		return l, err
	}, "SELECT CAST(count_id AS TEXT), CAST(product_id AS TEXT), CAST(counted AS TEXT), CAST(expected AS TEXT), expected_version, CAST(unit_cost AS TEXT), "+
		"CAST(captured_by AS TEXT), captured_at_utc FROM inventory_count_lines WHERE organization_id = ? AND count_id = ? ORDER BY product_id", orgID.String(), countID.String())
}

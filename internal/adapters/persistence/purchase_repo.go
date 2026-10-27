package persistence

import (
	"context"
	"database/sql"
	"fmt"

	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/application/purchasing"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/purchase"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
	"github.com/gamijoam/inventario-go/internal/platform/config"
)

// PurchaseStore implementa purchasing.Store.
func (s *Store) PurchaseStore() purchasing.Store { return purchaseStore{s} }

type purchaseStore struct{ s *Store }

func (c purchaseStore) InTx(ctx context.Context, fn func(purchasing.Repository) error) error {
	return orgStore(c).InTx(ctx, func(r apporg.Repository) error { return fn(r.(*orgRepo)) })
}

func (r *orgRepo) forUpdate(q string, lock bool) string {
	if lock && r.s.Engine != config.DriverSQLite {
		return q + " FOR UPDATE"
	}
	return q
}

// ---------- Proveedores ----------

func (r *orgRepo) InsertSupplier(ctx context.Context, s purchase.Supplier) error {
	var rif any
	if !s.RIF.IsZero() {
		rif = s.RIF.String()
	}
	return r.exec(ctx, "proveedor "+s.Code.String()+" (código o RIF repetido)",
		"INSERT INTO suppliers (organization_id, id, code, name, rif, status, created_at_utc) VALUES (?, ?, ?, ?, ?, ?, ?)",
		s.OrganizationID.String(), s.ID.String(), s.Code.String(), s.Name.String(), rif, string(s.Status), r.ts(s.CreatedAt))
}

const supplierCols = "CAST(organization_id AS TEXT), CAST(id AS TEXT), code, name, rif, status, created_at_utc"

func scanSupplier(sc scanner) (purchase.Supplier, error) {
	var o, sid, code, name, status string
	var rif sql.NullString
	var at any
	var s purchase.Supplier
	if err := sc.Scan(&o, &sid, &code, &name, &rif, &status, &at); err != nil {
		return s, err
	}
	ids, err := parseIDs(o, sid)
	if err != nil {
		return s, err
	}
	s.OrganizationID, s.ID, s.Status = ids[0], ids[1], org.Status(status)
	if s.Code, err = org.ParseCode(code); err != nil {
		return s, err
	}
	if s.Name, err = org.ParseName(name); err != nil {
		return s, err
	}
	if rif.Valid {
		if s.RIF, err = org.ParseRIF(rif.String); err != nil {
			return s, err
		}
	}
	s.CreatedAt, err = parseTime(at)
	return s, err
}

func (r *orgRepo) Supplier(ctx context.Context, orgID, supplierID id.ID) (purchase.Supplier, error) {
	s, err := scanSupplier(r.tx.QueryRowContext(ctx, r.q("SELECT "+supplierCols+" FROM suppliers WHERE organization_id = ? AND id = ?"), orgID.String(), supplierID.String()))
	return s, notFound(err, "proveedor")
}

func (r *orgRepo) SupplierByCode(ctx context.Context, orgID id.ID, code org.Code) (purchase.Supplier, error) {
	s, err := scanSupplier(r.tx.QueryRowContext(ctx, r.q("SELECT "+supplierCols+" FROM suppliers WHERE organization_id = ? AND code = ?"), orgID.String(), code.String()))
	return s, notFound(err, "proveedor "+code.String())
}

func (r *orgRepo) Suppliers(ctx context.Context, orgID id.ID) ([]purchase.Supplier, error) {
	return queryAll(ctx, r, scanSupplier, "SELECT "+supplierCols+" FROM suppliers WHERE organization_id = ? ORDER BY code", orgID.String())
}

// ---------- Órdenes ----------

func (r *orgRepo) InsertOrder(ctx context.Context, o purchase.Order) error {
	sc := o.Scope
	if err := r.exec(ctx, "orden "+o.Number,
		"INSERT INTO purchase_orders (organization_id, id, company_id, branch_id, number, supplier_id, status, note, created_by, created_at_utc, currency) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		sc.Organization().String(), o.ID.String(), sc.Company().String(), sc.Branch().String(), o.Number, o.SupplierID.String(), string(o.Status),
		o.Note, o.CreatedBy.String(), r.ts(o.CreatedAt), o.Currency.Code()); err != nil {
		return err
	}
	for _, l := range o.Lines {
		if err := r.exec(ctx, "línea de orden",
			"INSERT INTO purchase_order_lines (organization_id, order_id, line_no, product_id, quantity, unit_cost, received) VALUES (?, ?, ?, ?, ?, ?, ?)",
			sc.Organization().String(), o.ID.String(), l.LineNo, l.ProductID.String(), dec(l.Quantity), dec(l.UnitCost), dec(l.Received)); err != nil {
			return err
		}
	}
	return nil
}

func (r *orgRepo) UpdateOrder(ctx context.Context, o purchase.Order) error {
	orgID := o.Scope.Organization().String()
	for _, l := range o.Lines {
		if err := r.execOne(ctx, "línea de orden", "UPDATE purchase_order_lines SET received = ? WHERE organization_id = ? AND order_id = ? AND line_no = ?",
			dec(l.Received), orgID, o.ID.String(), l.LineNo); err != nil {
			return err
		}
	}
	return r.execOne(ctx, "orden "+o.Number, "UPDATE purchase_orders SET status = ?, closed_by = ?, closed_at_utc = ? WHERE organization_id = ? AND id = ?",
		string(o.Status), nullID(o.ClosedBy), r.nullTime(o.ClosedAt), orgID, o.ID.String())
}

const orderCols = "CAST(organization_id AS TEXT), CAST(id AS TEXT), CAST(company_id AS TEXT), CAST(branch_id AS TEXT), number, CAST(supplier_id AS TEXT), " +
	"status, note, CAST(created_by AS TEXT), created_at_utc, CAST(closed_by AS TEXT), closed_at_utc, currency"

func scanOrder(sc scanner) (purchase.Order, error) {
	var o, oid, c, b, sup, status, by, cur string
	var closedBy sql.NullString
	var at, closed any
	var v purchase.Order
	if err := sc.Scan(&o, &oid, &c, &b, &v.Number, &sup, &status, &v.Note, &by, &at, &closedBy, &closed, &cur); err != nil {
		return v, err
	}
	var err error
	if v.Currency, err = money.Lookup(cur); err != nil {
		return v, err
	}
	ids, err := parseIDs(o, oid, c, b, sup, by, closedBy.String)
	if err != nil {
		return v, err
	}
	if v.Scope, err = scope.New(ids[0], ids[2], ids[3], id.ID{}); err != nil {
		return v, err
	}
	v.ID, v.SupplierID, v.CreatedBy, v.ClosedBy, v.Status = ids[1], ids[4], ids[5], ids[6], purchase.OrderStatus(status)
	if v.CreatedAt, err = parseTime(at); err != nil {
		return v, err
	}
	v.ClosedAt, err = parseNullTime(closed)
	return v, err
}

func (r *orgRepo) orderLines(ctx context.Context, o purchase.Order) (purchase.Order, error) {
	var err error
	o.Lines, err = queryAll(ctx, r, func(sc scanner) (purchase.OrderLine, error) {
		var pid, qty, cost, received string
		var l purchase.OrderLine
		if err := sc.Scan(&l.LineNo, &pid, &qty, &cost, &received); err != nil {
			return l, err
		}
		var err error
		if l.ProductID, err = id.Parse(pid); err != nil {
			return l, err
		}
		if l.Quantity, err = parseDec(qty); err != nil {
			return l, err
		}
		if l.UnitCost, err = parseDec(cost); err != nil {
			return l, err
		}
		l.Received, err = parseDec(received)
		return l, err
	}, "SELECT line_no, CAST(product_id AS TEXT), CAST(quantity AS TEXT), CAST(unit_cost AS TEXT), CAST(received AS TEXT) FROM purchase_order_lines "+
		"WHERE organization_id = ? AND order_id = ? ORDER BY line_no", o.Scope.Organization().String(), o.ID.String())
	return o, err
}

func (r *orgRepo) Order(ctx context.Context, orgID, orderID id.ID, lock bool) (purchase.Order, error) {
	o, err := scanOrder(r.tx.QueryRowContext(ctx, r.q(r.forUpdate("SELECT "+orderCols+" FROM purchase_orders WHERE organization_id = ? AND id = ?", lock)),
		orgID.String(), orderID.String()))
	if err != nil {
		return o, notFound(err, "orden de compra")
	}
	return r.orderLines(ctx, o)
}

func (r *orgRepo) OrderByNumber(ctx context.Context, orgID id.ID, number string) (purchase.Order, error) {
	o, err := scanOrder(r.tx.QueryRowContext(ctx, r.q("SELECT "+orderCols+" FROM purchase_orders WHERE organization_id = ? AND number = ?"), orgID.String(), number))
	if err != nil {
		return o, notFound(err, "orden "+number)
	}
	return r.orderLines(ctx, o)
}

func (r *orgRepo) Orders(ctx context.Context, orgID id.ID, status purchase.OrderStatus) ([]purchase.Order, error) {
	q, args := "SELECT "+orderCols+" FROM purchase_orders WHERE organization_id = ?", []any{orgID.String()}
	if status != "" {
		q, args = q+" AND status = ?", append(args, string(status))
	}
	list, err := queryAll(ctx, r, scanOrder, q+" ORDER BY number", args...)
	for i := range list {
		if err != nil {
			break
		}
		list[i], err = r.orderLines(ctx, list[i])
	}
	return list, err
}

// ---------- Recepciones ----------

func (r *orgRepo) InsertReceipt(ctx context.Context, rc purchase.Receipt) error {
	sc := rc.Scope
	return r.exec(ctx, "recepción "+rc.Number,
		"INSERT INTO purchase_receipts (organization_id, id, company_id, branch_id, warehouse_id, number, supplier_id, order_id, status, supplier_document, note, created_by, created_at_utc, "+
			"currency, document_date) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		sc.Organization().String(), rc.ID.String(), sc.Company().String(), sc.Branch().String(), sc.Warehouse().String(), rc.Number,
		rc.SupplierID.String(), nullID(rc.OrderID), string(rc.Status), rc.SupplierDocument, rc.Note, rc.CreatedBy.String(), r.ts(rc.CreatedAt),
		rc.Currency.Code(), date(rc.DocumentDate))
}

func (r *orgRepo) UpdateReceipt(ctx context.Context, rc purchase.Receipt) error {
	res, err := r.tx.ExecContext(ctx, r.q("UPDATE purchase_receipts SET status = ?, closed_by = ?, closed_at_utc = ?, rate_id = ? WHERE organization_id = ? AND id = ? AND status = 'draft'"),
		string(rc.Status), nullID(rc.ClosedBy), r.nullTime(rc.ClosedAt), nullID(rc.RateID), rc.Scope.Organization().String(), rc.ID.String())
	if err != nil {
		return fmt.Errorf("guardar recepción: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return stock.ErrConcurrent
	}
	return nil
}

// SaveReceiptLines reemplaza las líneas: la base solo lo admite en borrador.
func (r *orgRepo) SaveReceiptLines(ctx context.Context, rc purchase.Receipt) error {
	orgID := rc.Scope.Organization().String()
	if _, err := r.tx.ExecContext(ctx, r.q("DELETE FROM purchase_receipt_lines WHERE organization_id = ? AND receipt_id = ?"), orgID, rc.ID.String()); err != nil {
		return fmt.Errorf("guardar líneas de recepción: %w", err)
	}
	for _, l := range rc.Lines {
		var orderLine, valued any
		if l.OrderLineNo > 0 {
			orderLine = l.OrderLineNo
		}
		if rc.Status != purchase.ReceiptDraft {
			valued = dec(l.Valued)
		}
		if err := r.exec(ctx, "línea de recepción",
			"INSERT INTO purchase_receipt_lines (organization_id, receipt_id, line_no, product_id, order_line_no, quantity, unit_cost, expenses, movement_id, valued) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			orgID, rc.ID.String(), l.LineNo, l.ProductID.String(), orderLine, dec(l.Quantity), dec(l.UnitCost), dec(l.Expenses), nullID(l.MovementID), valued); err != nil {
			return err
		}
	}
	return nil
}

const receiptCols = "CAST(organization_id AS TEXT), CAST(id AS TEXT), CAST(company_id AS TEXT), CAST(branch_id AS TEXT), CAST(warehouse_id AS TEXT), number, " +
	"CAST(supplier_id AS TEXT), CAST(order_id AS TEXT), status, supplier_document, note, CAST(created_by AS TEXT), created_at_utc, CAST(closed_by AS TEXT), closed_at_utc, " +
	"currency, CAST(document_date AS TEXT), CAST(rate_id AS TEXT)"

func scanReceipt(sc scanner) (purchase.Receipt, error) {
	var o, rid, c, b, w, sup, status, by, cur, on string
	var order, closedBy, rate sql.NullString
	var at, closed any
	var v purchase.Receipt
	if err := sc.Scan(&o, &rid, &c, &b, &w, &v.Number, &sup, &order, &status, &v.SupplierDocument, &v.Note, &by, &at, &closedBy, &closed, &cur, &on, &rate); err != nil {
		return v, err
	}
	ids, err := parseIDs(o, rid, c, b, w, sup, order.String, by, closedBy.String, rate.String)
	if err != nil {
		return v, err
	}
	v.RateID = ids[9]
	if v.Currency, err = money.Lookup(cur); err != nil {
		return v, err
	}
	if v.DocumentDate, err = parseDate(on); err != nil {
		return v, err
	}
	if v.Scope, err = scope.New(ids[0], ids[2], ids[3], ids[4]); err != nil {
		return v, err
	}
	v.ID, v.SupplierID, v.OrderID, v.CreatedBy, v.ClosedBy, v.Status = ids[1], ids[5], ids[6], ids[7], ids[8], purchase.ReceiptStatus(status)
	if v.CreatedAt, err = parseTime(at); err != nil {
		return v, err
	}
	v.ClosedAt, err = parseNullTime(closed)
	return v, err
}

func (r *orgRepo) receiptLines(ctx context.Context, rc purchase.Receipt) (purchase.Receipt, error) {
	var err error
	rc.Lines, err = queryAll(ctx, r, func(sc scanner) (purchase.ReceiptLine, error) {
		var pid, qty, cost, expenses string
		var orderLine sql.NullInt64
		var movement, valued sql.NullString
		var l purchase.ReceiptLine
		if err := sc.Scan(&l.LineNo, &pid, &orderLine, &qty, &cost, &expenses, &movement, &valued); err != nil {
			return l, err
		}
		ids, err := parseIDs(pid, movement.String)
		if err != nil {
			return l, err
		}
		l.ProductID, l.MovementID, l.OrderLineNo = ids[0], ids[1], int(orderLine.Int64)
		if l.Quantity, err = parseDec(qty); err != nil {
			return l, err
		}
		if l.UnitCost, err = parseDec(cost); err != nil {
			return l, err
		}
		if l.Expenses, err = parseDec(expenses); err != nil {
			return l, err
		}
		// Sin valorar: borrador, o confirmada antes de BP-M3-002 en la
		// moneda de valoración.
		if !valued.Valid {
			l.Valued = l.BaseCost()
			return l, nil
		}
		l.Valued, err = parseDec(valued.String)
		return l, err
	}, "SELECT line_no, CAST(product_id AS TEXT), order_line_no, CAST(quantity AS TEXT), CAST(unit_cost AS TEXT), CAST(expenses AS TEXT), CAST(movement_id AS TEXT), CAST(valued AS TEXT) "+
		"FROM purchase_receipt_lines WHERE organization_id = ? AND receipt_id = ? ORDER BY line_no", rc.Scope.Organization().String(), rc.ID.String())
	return rc, err
}

func (r *orgRepo) Receipt(ctx context.Context, orgID, receiptID id.ID, lock bool) (purchase.Receipt, error) {
	rc, err := scanReceipt(r.tx.QueryRowContext(ctx, r.q(r.forUpdate("SELECT "+receiptCols+" FROM purchase_receipts WHERE organization_id = ? AND id = ?", lock)),
		orgID.String(), receiptID.String()))
	if err != nil {
		return rc, notFound(err, "recepción")
	}
	return r.receiptLines(ctx, rc)
}

func (r *orgRepo) ReceiptByNumber(ctx context.Context, orgID id.ID, number string) (purchase.Receipt, error) {
	rc, err := scanReceipt(r.tx.QueryRowContext(ctx, r.q("SELECT "+receiptCols+" FROM purchase_receipts WHERE organization_id = ? AND number = ?"), orgID.String(), number))
	if err != nil {
		return rc, notFound(err, "recepción "+number)
	}
	return r.receiptLines(ctx, rc)
}

func (r *orgRepo) Receipts(ctx context.Context, orgID id.ID, status purchase.ReceiptStatus) ([]purchase.Receipt, error) {
	q, args := "SELECT "+receiptCols+" FROM purchase_receipts WHERE organization_id = ?", []any{orgID.String()}
	if status != "" {
		q, args = q+" AND status = ?", append(args, string(status))
	}
	list, err := queryAll(ctx, r, scanReceipt, q+" ORDER BY number", args...)
	for i := range list {
		if err != nil {
			break
		}
		list[i], err = r.receiptLines(ctx, list[i])
	}
	return list, err
}

// ---------- Gastos ----------

func (r *orgRepo) InsertExpense(ctx context.Context, orgID id.ID, e purchase.Expense) error {
	return r.exec(ctx, "gasto",
		"INSERT INTO purchase_expenses (organization_id, id, receipt_id, concept, amount, criterion, late, created_by, created_at_utc, currency, expense_date, rate_id, valued) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		orgID.String(), e.ID.String(), e.ReceiptID.String(), e.Concept, dec(e.Amount), string(e.Criterion), e.Late, e.CreatedBy.String(), r.ts(e.CreatedAt),
		e.Currency.Code(), date(e.Date), nullID(e.RateID), dec(e.Valued))
}

func (r *orgRepo) InsertAllocations(ctx context.Context, orgID id.ID, e purchase.Expense) error {
	for _, a := range e.Allocations {
		if err := r.exec(ctx, "prorrateo",
			"INSERT INTO purchase_expense_allocations (organization_id, expense_id, line_no, base, amount, capitalized, variance, movement_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			orgID.String(), e.ID.String(), a.LineNo, dec(a.Base), dec(a.Amount), dec(a.Capitalized), dec(a.Variance), nullID(a.MovementID)); err != nil {
			return err
		}
	}
	return nil
}

func (r *orgRepo) Expenses(ctx context.Context, orgID, receiptID id.ID) ([]purchase.Expense, error) {
	list, err := queryAll(ctx, r, func(sc scanner) (purchase.Expense, error) {
		var eid, rid, amount, criterion, by, cur, on string
		var rate, valued sql.NullString
		var late bool
		var at any
		var e purchase.Expense
		if err := sc.Scan(&eid, &rid, &e.Concept, &amount, &criterion, &late, &by, &at, &cur, &on, &rate, &valued); err != nil {
			return e, err
		}
		ids, err := parseIDs(eid, rid, by, rate.String)
		if err != nil {
			return e, err
		}
		e.ID, e.ReceiptID, e.CreatedBy, e.RateID, e.Criterion, e.Late = ids[0], ids[1], ids[2], ids[3], purchase.Criterion(criterion), late
		if e.Amount, err = parseDec(amount); err != nil {
			return e, err
		}
		if e.Currency, err = money.Lookup(cur); err != nil {
			return e, err
		}
		if e.Date, err = parseDate(on); err != nil {
			return e, err
		}
		// Sin valorar: gasto anterior a BP-M3-002, en la moneda de valoración.
		e.Valued = e.Amount
		if valued.Valid {
			if e.Valued, err = parseDec(valued.String); err != nil {
				return e, err
			}
		}
		e.CreatedAt, err = parseTime(at)
		return e, err
	}, "SELECT CAST(id AS TEXT), CAST(receipt_id AS TEXT), concept, CAST(amount AS TEXT), criterion, late, CAST(created_by AS TEXT), created_at_utc, "+
		"currency, CAST(expense_date AS TEXT), CAST(rate_id AS TEXT), CAST(valued AS TEXT) "+
		"FROM purchase_expenses WHERE organization_id = ? AND receipt_id = ? ORDER BY created_at_utc, id", orgID.String(), receiptID.String())
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Allocations, err = queryAll(ctx, r, func(sc scanner) (purchase.Allocation, error) {
			var base, amount, capitalized, variance string
			var movement sql.NullString
			var a purchase.Allocation
			if err := sc.Scan(&a.LineNo, &base, &amount, &capitalized, &variance, &movement); err != nil {
				return a, err
			}
			ds := []*decimal.Decimal{&a.Base, &a.Amount, &a.Capitalized, &a.Variance}
			for j, raw := range []string{base, amount, capitalized, variance} {
				v, err := parseDec(raw)
				if err != nil {
					return a, err
				}
				*ds[j] = v
			}
			ids, err := parseIDs(movement.String)
			if err != nil {
				return a, err
			}
			a.MovementID = ids[0]
			return a, nil
		}, "SELECT line_no, CAST(base AS TEXT), CAST(amount AS TEXT), CAST(capitalized AS TEXT), CAST(variance AS TEXT), CAST(movement_id AS TEXT) "+
			"FROM purchase_expense_allocations WHERE organization_id = ? AND expense_id = ? ORDER BY line_no", orgID.String(), list[i].ID.String())
		if err != nil {
			return nil, err
		}
	}
	return list, nil
}

// ---------- Costos ----------

func (r *orgRepo) InsertReplacementCost(ctx context.Context, c purchase.ReplacementCost) error {
	return r.exec(ctx, "costo de reposición",
		"INSERT INTO replacement_costs (organization_id, id, company_id, product_id, unit_cost, note, actor_id, occurred_at_utc, currency) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		c.OrganizationID.String(), c.ID.String(), c.CompanyID.String(), c.ProductID.String(), dec(c.UnitCost), c.Note, c.ActorID.String(), r.ts(c.OccurredAt), c.Currency.Code())
}

func (r *orgRepo) ReplacementCosts(ctx context.Context, orgID, companyID, productID id.ID) ([]purchase.ReplacementCost, error) {
	return queryAll(ctx, r, func(sc scanner) (purchase.ReplacementCost, error) {
		var o, cid, c, p, cost, actor, cur string
		var at any
		var v purchase.ReplacementCost
		if err := sc.Scan(&o, &cid, &c, &p, &cost, &v.Note, &actor, &at, &cur); err != nil {
			return v, err
		}
		var err error
		if v.Currency, err = money.Lookup(cur); err != nil {
			return v, err
		}
		ids, err := parseIDs(o, cid, c, p, actor)
		if err != nil {
			return v, err
		}
		v.OrganizationID, v.ID, v.CompanyID, v.ProductID, v.ActorID = ids[0], ids[1], ids[2], ids[3], ids[4]
		if v.UnitCost, err = parseDec(cost); err != nil {
			return v, err
		}
		v.OccurredAt, err = parseTime(at)
		return v, err
	}, "SELECT CAST(organization_id AS TEXT), CAST(id AS TEXT), CAST(company_id AS TEXT), CAST(product_id AS TEXT), CAST(unit_cost AS TEXT), note, "+
		"CAST(actor_id AS TEXT), occurred_at_utc, currency FROM replacement_costs WHERE organization_id = ? AND company_id = ? AND product_id = ? ORDER BY occurred_at_utc, id",
		orgID.String(), companyID.String(), productID.String())
}

func (r *orgRepo) LastPurchase(ctx context.Context, orgID, companyID, productID id.ID) (purchasing.LastPurchase, error) {
	var qty, cost, expenses, cur string
	var valued sql.NullString
	var at any
	var v purchasing.LastPurchase
	err := r.tx.QueryRowContext(ctx, r.q("SELECT CAST(l.quantity AS TEXT), CAST(l.unit_cost AS TEXT), CAST(l.expenses AS TEXT), CAST(l.valued AS TEXT), rc.currency, rc.number, rc.closed_at_utc "+
		"FROM purchase_receipt_lines l JOIN purchase_receipts rc ON rc.organization_id = l.organization_id AND rc.id = l.receipt_id "+
		"WHERE l.organization_id = ? AND rc.company_id = ? AND l.product_id = ? AND rc.status = 'confirmed' ORDER BY rc.closed_at_utc DESC, rc.number DESC LIMIT 1"),
		orgID.String(), companyID.String(), productID.String()).Scan(&qty, &cost, &expenses, &valued, &cur, &v.Receipt, &at)
	if err != nil {
		return v, notFound(err, "compra")
	}
	q, err := parseDec(qty)
	if err != nil {
		return v, err
	}
	if v.UnitCost, err = parseDec(cost); err != nil {
		return v, err
	}
	if v.Currency, err = money.Lookup(cur); err != nil {
		return v, err
	}
	e, err := parseDec(expenses)
	if err != nil {
		return v, err
	}
	l := purchase.ReceiptLine{Quantity: q, UnitCost: v.UnitCost}
	base := l.BaseCost() // confirmada antes de BP-M3-002
	if valued.Valid {
		if base, err = parseDec(valued.String); err != nil {
			return v, err
		}
	}
	if v.ValuedUnit, err = base.Div(q, inventory.UnitCostScale, decimal.HalfAwayFromZero); err != nil {
		return v, err
	}
	if v.TotalUnit, err = base.Add(e).Div(q, inventory.UnitCostScale, decimal.HalfAwayFromZero); err != nil {
		return v, err
	}
	v.ReceivedAt, err = parseTime(at)
	return v, err
}

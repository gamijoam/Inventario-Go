package persistence

import (
	"context"
	"database/sql"
	"fmt"

	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/pricing"
)

// PricingStore implementa apppricing.Store.
func (s *Store) PricingStore() apppricing.Store { return pricingStore{s} }

type pricingStore struct{ s *Store }

func (c pricingStore) InTx(ctx context.Context, fn func(apppricing.Repository) error) error {
	return orgStore(c).InTx(ctx, func(r apporg.Repository) error { return fn(r.(*orgRepo)) })
}

// insertOnce inserta y traduce la violación de unicidad a
// stock.ErrConcurrent: otra transacción tomó la misma versión o secuencia.
func (r *orgRepo) insertOnce(ctx context.Context, what, query string, args ...any) error {
	if _, err := r.tx.ExecContext(ctx, r.q(query), args...); err != nil {
		if r.s.isUniqueViolation(err) {
			return stock.ErrConcurrent
		}
		return fmt.Errorf("guardar %s: %w", what, err)
	}
	return nil
}

func optDec(d *decimal.Decimal) any {
	if d == nil {
		return nil
	}
	return dec(*d)
}

func parseNullDec(raw sql.NullString) (*decimal.Decimal, error) {
	if !raw.Valid {
		return nil, nil
	}
	d, err := parseDec(raw.String)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ---------- Listas ----------

func (r *orgRepo) InsertPriceList(ctx context.Context, l pricing.List) error {
	return r.exec(ctx, "lista "+l.Code.String(),
		"INSERT INTO price_lists (organization_id, id, company_id, code, name, currency, status, created_by, created_at_utc) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		l.OrganizationID.String(), l.ID.String(), l.CompanyID.String(), l.Code.String(), l.Name, l.Currency.Code(), string(l.Status), l.CreatedBy.String(), r.ts(l.CreatedAt))
}

const priceListCols = "CAST(organization_id AS TEXT), CAST(id AS TEXT), CAST(company_id AS TEXT), code, name, currency, status, CAST(created_by AS TEXT), created_at_utc"

func scanPriceList(sc scanner) (pricing.List, error) {
	var o, lid, c, code, cur, status, by string
	var at any
	var l pricing.List
	if err := sc.Scan(&o, &lid, &c, &code, &l.Name, &cur, &status, &by, &at); err != nil {
		return l, err
	}
	ids, err := parseIDs(o, lid, c, by)
	if err != nil {
		return l, err
	}
	l.OrganizationID, l.ID, l.CompanyID, l.CreatedBy, l.Status = ids[0], ids[1], ids[2], ids[3], org.Status(status)
	if l.Code, err = org.ParseCode(code); err != nil {
		return l, err
	}
	if l.Currency, err = money.Lookup(cur); err != nil {
		return l, err
	}
	l.CreatedAt, err = parseTime(at)
	return l, err
}

func (r *orgRepo) PriceList(ctx context.Context, orgID, listID id.ID) (pricing.List, error) {
	l, err := scanPriceList(r.tx.QueryRowContext(ctx, r.q("SELECT "+priceListCols+" FROM price_lists WHERE organization_id = ? AND id = ?"), orgID.String(), listID.String()))
	return l, notFound(err, "lista de precios")
}

func (r *orgRepo) PriceListByCode(ctx context.Context, orgID, companyID id.ID, code org.Code) (pricing.List, error) {
	l, err := scanPriceList(r.tx.QueryRowContext(ctx, r.q("SELECT "+priceListCols+" FROM price_lists WHERE organization_id = ? AND company_id = ? AND code = ?"),
		orgID.String(), companyID.String(), code.String()))
	return l, notFound(err, "lista de precios "+code.String())
}

func (r *orgRepo) PriceLists(ctx context.Context, orgID, companyID id.ID) ([]pricing.List, error) {
	return queryAll(ctx, r, scanPriceList, "SELECT "+priceListCols+" FROM price_lists WHERE organization_id = ? AND company_id = ? ORDER BY code",
		orgID.String(), companyID.String())
}

// ---------- Márgenes ----------

func (r *orgRepo) InsertMargin(ctx context.Context, orgID id.ID, m pricing.MarginChange) error {
	var seq int
	if err := r.tx.QueryRowContext(ctx, r.q("SELECT COALESCE(MAX(seq), 0) + 1 FROM price_margins WHERE organization_id = ? AND list_id = ?"),
		orgID.String(), m.ListID.String()).Scan(&seq); err != nil {
		return err
	}
	return r.insertOnce(ctx, "margen",
		"INSERT INTO price_margins (organization_id, id, list_id, seq, product_id, margin, actor_id, occurred_at_utc) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		orgID.String(), m.ID.String(), m.ListID.String(), seq, nullID(m.ProductID), optDec(m.Margin), m.ActorID.String(), r.ts(m.OccurredAt))
}

func (r *orgRepo) Margins(ctx context.Context, orgID, listID id.ID) ([]pricing.MarginChange, error) {
	return queryAll(ctx, r, func(sc scanner) (pricing.MarginChange, error) {
		var mid, lid, actor string
		var product, margin sql.NullString
		var at any
		var m pricing.MarginChange
		if err := sc.Scan(&mid, &lid, &product, &margin, &actor, &at); err != nil {
			return m, err
		}
		ids, err := parseIDs(mid, lid, product.String, actor)
		if err != nil {
			return m, err
		}
		m.ID, m.ListID, m.ProductID, m.ActorID = ids[0], ids[1], ids[2], ids[3]
		if m.Margin, err = parseNullDec(margin); err != nil {
			return m, err
		}
		m.OccurredAt, err = parseTime(at)
		return m, err
	}, "SELECT CAST(id AS TEXT), CAST(list_id AS TEXT), CAST(product_id AS TEXT), CAST(margin AS TEXT), CAST(actor_id AS TEXT), occurred_at_utc "+
		"FROM price_margins WHERE organization_id = ? AND list_id = ? ORDER BY seq", orgID.String(), listID.String())
}

// ---------- Revisiones ----------

func (r *orgRepo) InsertRevision(ctx context.Context, orgID id.ID, x pricing.Revision) error {
	return r.insertOnce(ctx, "revisión de precio",
		"INSERT INTO price_revisions (organization_id, id, list_id, product_id, version, price, effective_at_utc, status, created_by, created_at_utc) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		orgID.String(), x.ID.String(), x.ListID.String(), x.ProductID.String(), x.Version, dec(x.Price), r.ts(x.EffectiveAt), string(x.Status),
		x.CreatedBy.String(), r.ts(x.CreatedAt))
}

func (r *orgRepo) CancelRevision(ctx context.Context, orgID id.ID, x pricing.Revision) error {
	res, err := r.tx.ExecContext(ctx, r.q("UPDATE price_revisions SET status = ?, cancelled_by = ?, cancelled_at_utc = ? WHERE organization_id = ? AND id = ? AND status = 'published'"),
		string(x.Status), x.CancelledBy.String(), r.ts(x.CancelledAt), orgID.String(), x.ID.String())
	if err != nil {
		return fmt.Errorf("anular revisión: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return stock.ErrConcurrent
	}
	return nil
}

const revisionCols = "CAST(id AS TEXT), CAST(list_id AS TEXT), CAST(product_id AS TEXT), version, CAST(price AS TEXT), effective_at_utc, status, " +
	"CAST(created_by AS TEXT), created_at_utc, CAST(cancelled_by AS TEXT), cancelled_at_utc"

func scanRevision(sc scanner) (pricing.Revision, error) {
	var rid, lid, pid, price, status, by string
	var cancelledBy sql.NullString
	var eff, at, cancelled any
	var x pricing.Revision
	if err := sc.Scan(&rid, &lid, &pid, &x.Version, &price, &eff, &status, &by, &at, &cancelledBy, &cancelled); err != nil {
		return x, err
	}
	ids, err := parseIDs(rid, lid, pid, by, cancelledBy.String)
	if err != nil {
		return x, err
	}
	x.ID, x.ListID, x.ProductID, x.CreatedBy, x.CancelledBy, x.Status = ids[0], ids[1], ids[2], ids[3], ids[4], pricing.RevisionStatus(status)
	if x.Price, err = parseDec(price); err != nil {
		return x, err
	}
	if x.EffectiveAt, err = parseTime(eff); err != nil {
		return x, err
	}
	if x.CreatedAt, err = parseTime(at); err != nil {
		return x, err
	}
	x.CancelledAt, err = parseNullTime(cancelled)
	return x, err
}

func (r *orgRepo) Revision(ctx context.Context, orgID, revisionID id.ID) (pricing.Revision, error) {
	x, err := scanRevision(r.tx.QueryRowContext(ctx, r.q("SELECT "+revisionCols+" FROM price_revisions WHERE organization_id = ? AND id = ?"), orgID.String(), revisionID.String()))
	return x, notFound(err, "revisión de precio")
}

func (r *orgRepo) Revisions(ctx context.Context, orgID, listID, productID id.ID) ([]pricing.Revision, error) {
	q, args := "SELECT "+revisionCols+" FROM price_revisions WHERE organization_id = ? AND list_id = ?", []any{orgID.String(), listID.String()}
	if !productID.IsZero() {
		q, args = q+" AND product_id = ?", append(args, productID.String())
	}
	return queryAll(ctx, r, scanRevision, q+" ORDER BY product_id, version", args...)
}

// ---------- Propuestas ----------

func (r *orgRepo) InsertProposal(ctx context.Context, orgID id.ID, x pricing.Proposal) error {
	var basis, cost, costCur, rate, listCost any
	if ref := x.Reference; ref != nil {
		basis, cost, costCur, rate, listCost = string(ref.Basis), dec(ref.Cost), ref.Currency.Code(), nullID(ref.RateID), dec(ref.ListCost)
	}
	return r.insertOnce(ctx, "propuesta "+x.Number,
		"INSERT INTO price_proposals (organization_id, id, number, list_id, product_id, kind, status, base_version, current_price, price, cost_basis, cost, "+
			"cost_currency, rate_id, list_cost, margin, receipt_id, note, proposed_by, created_at_utc, decision_note) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		orgID.String(), x.ID.String(), x.Number, x.ListID.String(), x.ProductID.String(), string(x.Kind), string(x.Status), x.BaseVersion,
		optDec(x.Current), dec(x.Price), basis, cost, costCur, rate, listCost, optDec(x.Margin), nullID(x.ReceiptID), x.Note,
		x.ProposedBy.String(), r.ts(x.CreatedAt), x.DecisionNote)
}

func (r *orgRepo) UpdateProposal(ctx context.Context, orgID id.ID, x pricing.Proposal) error {
	res, err := r.tx.ExecContext(ctx, r.q("UPDATE price_proposals SET status = ?, price = ?, decided_by = ?, decided_at_utc = ?, decision_note = ?, revision_id = ? "+
		"WHERE organization_id = ? AND id = ? AND status = 'pending'"),
		string(x.Status), dec(x.Price), nullID(x.DecidedBy), r.nullTime(x.DecidedAt), x.DecisionNote, nullID(x.RevisionID), orgID.String(), x.ID.String())
	if err != nil {
		return fmt.Errorf("guardar propuesta: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return stock.ErrConcurrent
	}
	return nil
}

const proposalCols = "CAST(id AS TEXT), number, CAST(list_id AS TEXT), CAST(product_id AS TEXT), kind, status, base_version, CAST(current_price AS TEXT), " +
	"CAST(price AS TEXT), cost_basis, CAST(cost AS TEXT), cost_currency, CAST(rate_id AS TEXT), CAST(list_cost AS TEXT), CAST(margin AS TEXT), " +
	"CAST(receipt_id AS TEXT), note, CAST(proposed_by AS TEXT), created_at_utc, CAST(decided_by AS TEXT), decided_at_utc, decision_note, CAST(revision_id AS TEXT)"

func scanProposal(sc scanner) (pricing.Proposal, error) {
	var pid, lid, prod, kind, status, price, by string
	var current, basis, cost, costCur, rate, listCost, margin, receipt, decidedBy, revision sql.NullString
	var at, decided any
	var x pricing.Proposal
	if err := sc.Scan(&pid, &x.Number, &lid, &prod, &kind, &status, &x.BaseVersion, &current, &price, &basis, &cost, &costCur, &rate, &listCost, &margin,
		&receipt, &x.Note, &by, &at, &decidedBy, &decided, &x.DecisionNote, &revision); err != nil {
		return x, err
	}
	ids, err := parseIDs(pid, lid, prod, receipt.String, by, decidedBy.String, revision.String, rate.String)
	if err != nil {
		return x, err
	}
	x.ID, x.ListID, x.ProductID, x.ReceiptID, x.ProposedBy, x.DecidedBy, x.RevisionID = ids[0], ids[1], ids[2], ids[3], ids[4], ids[5], ids[6]
	x.Kind, x.Status = pricing.ProposalKind(kind), pricing.ProposalStatus(status)
	if x.Current, err = parseNullDec(current); err != nil {
		return x, err
	}
	if x.Price, err = parseDec(price); err != nil {
		return x, err
	}
	if x.Margin, err = parseNullDec(margin); err != nil {
		return x, err
	}
	if basis.Valid {
		ref := pricing.Reference{Basis: pricing.CostBasis(basis.String), RateID: ids[7]}
		if ref.Cost, err = parseDec(cost.String); err != nil {
			return x, err
		}
		if ref.Currency, err = money.Lookup(costCur.String); err != nil {
			return x, err
		}
		if ref.ListCost, err = parseDec(listCost.String); err != nil {
			return x, err
		}
		x.Reference = &ref
	}
	if x.CreatedAt, err = parseTime(at); err != nil {
		return x, err
	}
	x.DecidedAt, err = parseNullTime(decided)
	return x, err
}

func (r *orgRepo) Proposal(ctx context.Context, orgID, proposalID id.ID, lock bool) (pricing.Proposal, error) {
	x, err := scanProposal(r.tx.QueryRowContext(ctx, r.q(r.forUpdate("SELECT "+proposalCols+" FROM price_proposals WHERE organization_id = ? AND id = ?", lock)),
		orgID.String(), proposalID.String()))
	return x, notFound(err, "propuesta de precio")
}

func (r *orgRepo) ProposalByNumber(ctx context.Context, orgID id.ID, number string) (pricing.Proposal, error) {
	x, err := scanProposal(r.tx.QueryRowContext(ctx, r.q("SELECT "+proposalCols+" FROM price_proposals WHERE organization_id = ? AND number = ?"), orgID.String(), number))
	return x, notFound(err, "propuesta "+number)
}

func (r *orgRepo) PendingProposal(ctx context.Context, orgID, listID, productID id.ID) (pricing.Proposal, error) {
	x, err := scanProposal(r.tx.QueryRowContext(ctx, r.q("SELECT "+proposalCols+" FROM price_proposals WHERE organization_id = ? AND list_id = ? AND product_id = ? AND status = 'pending'"),
		orgID.String(), listID.String(), productID.String()))
	return x, notFound(err, "propuesta pendiente")
}

func (r *orgRepo) Proposals(ctx context.Context, orgID, listID id.ID, status pricing.ProposalStatus) ([]pricing.Proposal, error) {
	q, args := "SELECT "+proposalCols+" FROM price_proposals WHERE organization_id = ?", []any{orgID.String()}
	if !listID.IsZero() {
		q, args = q+" AND list_id = ?", append(args, listID.String())
	}
	if status != "" {
		q, args = q+" AND status = ?", append(args, string(status))
	}
	return queryAll(ctx, r, scanProposal, q+" ORDER BY number", args...)
}

func (r *orgRepo) ProposalsOfReceipt(ctx context.Context, orgID, receiptID id.ID) ([]pricing.Proposal, error) {
	return queryAll(ctx, r, scanProposal, "SELECT "+proposalCols+" FROM price_proposals WHERE organization_id = ? AND receipt_id = ? ORDER BY number",
		orgID.String(), receiptID.String())
}

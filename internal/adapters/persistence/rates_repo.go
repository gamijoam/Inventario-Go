package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/application/rates"
	"github.com/gamijoam/inventario-go/internal/domain/exchange"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

// RateStore implementa rates.Store.
func (s *Store) RateStore() rates.Store { return rateStore{s} }

type rateStore struct{ s *Store }

func (c rateStore) InTx(ctx context.Context, fn func(rates.Repository) error) error {
	return orgStore(c).InTx(ctx, func(r apporg.Repository) error { return fn(r.(*orgRepo)) })
}

// date guarda una fecha comercial como AAAA-MM-DD: TEXT en SQLite, DATE en
// PostgreSQL.
func date(t time.Time) string { return t.Format(timezone.DateLayout) }

func parseDate(raw string) (time.Time, error) { return timezone.ParseDate(raw) }

func (r *orgRepo) NextRateVersion(ctx context.Context, orgID, companyID id.ID, pair string) (int, error) {
	var v int
	err := r.tx.QueryRowContext(ctx, r.q("SELECT COALESCE(MAX(version), 0) + 1 FROM exchange_rates WHERE organization_id = ? AND company_id = ? AND pair = ?"),
		orgID.String(), companyID.String(), pair).Scan(&v)
	return v, err
}

func (r *orgRepo) InsertRate(ctx context.Context, x exchange.Rate) error {
	return r.exec(ctx, "tasa "+x.Pair()+" (versión repetida)",
		"INSERT INTO exchange_rates (organization_id, id, company_id, base_currency, quote_currency, pair, rate, source_type, source_label, source_reference, "+
			"valid_on, version, note, created_by, created_at_utc) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		x.OrganizationID.String(), x.ID.String(), x.CompanyID.String(), x.Base.Code(), x.Quote.Code(), x.Pair(), dec(x.Value), string(x.Source.Type),
		x.Source.Label, x.Reference, date(x.ValidOn), x.Version, x.Note, x.CreatedBy.String(), r.ts(x.CreatedAt))
}

const rateCols = "CAST(organization_id AS TEXT), CAST(id AS TEXT), CAST(company_id AS TEXT), base_currency, quote_currency, CAST(rate AS TEXT), " +
	"source_type, source_label, source_reference, CAST(valid_on AS TEXT), version, note, CAST(created_by AS TEXT), created_at_utc"

func scanRate(sc scanner) (exchange.Rate, error) {
	var o, rid, c, base, quote, value, typ, on, by string
	var at any
	var x exchange.Rate
	if err := sc.Scan(&o, &rid, &c, &base, &quote, &value, &typ, &x.Source.Label, &x.Reference, &on, &x.Version, &x.Note, &by, &at); err != nil {
		return x, err
	}
	ids, err := parseIDs(o, rid, c, by)
	if err != nil {
		return x, err
	}
	x.OrganizationID, x.ID, x.CompanyID, x.CreatedBy, x.Source.Type = ids[0], ids[1], ids[2], ids[3], money.RateSourceType(typ)
	if x.Base, err = money.Lookup(base); err != nil {
		return x, err
	}
	if x.Quote, err = money.Lookup(quote); err != nil {
		return x, err
	}
	if x.Value, err = parseDec(value); err != nil {
		return x, err
	}
	if x.ValidOn, err = parseDate(on); err != nil {
		return x, err
	}
	x.CreatedAt, err = parseTime(at)
	return x, err
}

func (r *orgRepo) Rate(ctx context.Context, orgID, rateID id.ID) (exchange.Rate, error) {
	x, err := scanRate(r.tx.QueryRowContext(ctx, r.q("SELECT "+rateCols+" FROM exchange_rates WHERE organization_id = ? AND id = ?"), orgID.String(), rateID.String()))
	return x, notFound(err, "tasa")
}

func (r *orgRepo) Rates(ctx context.Context, orgID, companyID id.ID, pair string) ([]exchange.Rate, error) {
	q, args := "SELECT "+rateCols+" FROM exchange_rates WHERE organization_id = ? AND company_id = ?", []any{orgID.String(), companyID.String()}
	if pair != "" {
		q, args = q+" AND pair = ?", append(args, pair)
	}
	return queryAll(ctx, r, scanRate, q+" ORDER BY pair, valid_on, version", args...)
}

// EffectiveRate aplica exchange.Latest en SQL: la fecha más reciente no
// posterior a on y, en ella, la mayor versión.
func (r *orgRepo) EffectiveRate(ctx context.Context, orgID, companyID id.ID, pair string, on time.Time) (exchange.Rate, error) {
	x, err := scanRate(r.tx.QueryRowContext(ctx, r.q("SELECT "+rateCols+" FROM exchange_rates WHERE organization_id = ? AND company_id = ? AND pair = ? AND valid_on <= ? "+
		"ORDER BY valid_on DESC, version DESC LIMIT 1"), orgID.String(), companyID.String(), pair, date(on)))
	if errors.Is(err, sql.ErrNoRows) {
		return x, fmt.Errorf("%w para %s", exchange.ErrNoRate, pair)
	}
	return x, err
}

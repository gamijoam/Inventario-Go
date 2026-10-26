package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/platform/config"
)

// OrgStore implementa apporg.Store sobre el Store abierto.
func (s *Store) OrgStore() apporg.Store { return orgStore{s} }

type orgStore struct{ s *Store }

func (o orgStore) InTx(ctx context.Context, fn func(apporg.Repository) error) error {
	tx, err := o.s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&orgRepo{tx: tx, s: o.s}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

type orgRepo struct {
	tx *sql.Tx
	s  *Store
}

// q reescribe los marcadores "?" al dialecto del motor.
func (r *orgRepo) q(query string) string {
	if r.s.Engine == config.DriverSQLite {
		return query
	}
	var b strings.Builder
	n := 0
	for _, ch := range query {
		if ch == '?' {
			n++
			b.WriteString(r.s.Dialect.Placeholder(n))
			continue
		}
		b.WriteRune(ch)
	}
	return b.String()
}

func (r *orgRepo) ts(t time.Time) any { return r.s.Dialect.Timestamp(t) }

func (r *orgRepo) exec(ctx context.Context, what, query string, args ...any) error {
	if _, err := r.tx.ExecContext(ctx, r.q(query), args...); err != nil {
		if r.s.isUniqueViolation(err) {
			return fmt.Errorf("%w: %s", apporg.ErrConflict, what)
		}
		return fmt.Errorf("guardar %s: %w", what, err)
	}
	return nil
}

func (r *orgRepo) InsertOrganization(ctx context.Context, o org.Organization) error {
	return r.exec(ctx, "organización "+o.Code.String(),
		"INSERT INTO organizations (id, code, name, status, created_at_utc, updated_at_utc) VALUES (?, ?, ?, ?, ?, ?)",
		o.ID.String(), o.Code.String(), o.Name.String(), string(o.Status), r.ts(o.CreatedAt), r.ts(o.CreatedAt))
}

func (r *orgRepo) InsertCompany(ctx context.Context, c org.Company) error {
	var rif any
	if !c.RIF.IsZero() {
		rif = c.RIF.String()
	}
	return r.exec(ctx, "empresa "+c.Code.String()+" (código o RIF repetido)",
		"INSERT INTO companies (organization_id, id, code, legal_name, rif, status, created_at_utc, updated_at_utc) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		c.OrganizationID.String(), c.ID.String(), c.Code.String(), c.LegalName.String(), rif, string(c.Status), r.ts(c.CreatedAt), r.ts(c.CreatedAt))
}

func (r *orgRepo) InsertBranch(ctx context.Context, b org.Branch) error {
	return r.exec(ctx, "sucursal "+b.Code.String(),
		"INSERT INTO branches (organization_id, company_id, id, code, name, status, created_at_utc, updated_at_utc) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		b.OrganizationID.String(), b.CompanyID.String(), b.ID.String(), b.Code.String(), b.Name.String(), string(b.Status), r.ts(b.CreatedAt), r.ts(b.CreatedAt))
}

func (r *orgRepo) InsertWarehouse(ctx context.Context, w org.Warehouse) error {
	return r.exec(ctx, "almacén "+w.Code.String(),
		"INSERT INTO warehouses (organization_id, company_id, branch_id, id, code, name, status, created_at_utc, updated_at_utc) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		w.OrganizationID.String(), w.CompanyID.String(), w.BranchID.String(), w.ID.String(), w.Code.String(), w.Name.String(), string(w.Status), r.ts(w.CreatedAt), r.ts(w.CreatedAt))
}

const (
	orgCols       = "CAST(id AS TEXT), code, name, status, created_at_utc"
	companyCols   = "CAST(organization_id AS TEXT), CAST(id AS TEXT), code, legal_name, rif, status, created_at_utc"
	branchCols    = "CAST(organization_id AS TEXT), CAST(company_id AS TEXT), CAST(id AS TEXT), code, name, status, created_at_utc"
	warehouseCols = "CAST(organization_id AS TEXT), CAST(company_id AS TEXT), CAST(branch_id AS TEXT), CAST(id AS TEXT), code, name, status, created_at_utc"
)

type scanner interface{ Scan(dest ...any) error }

func (r *orgRepo) Organization(ctx context.Context, orgID id.ID) (org.Organization, error) {
	row := r.tx.QueryRowContext(ctx, r.q("SELECT "+orgCols+" FROM organizations WHERE id = ?"), orgID.String())
	o, err := scanOrganization(row)
	return o, notFound(err, "organización")
}

func (r *orgRepo) Company(ctx context.Context, orgID, companyID id.ID) (org.Company, error) {
	row := r.tx.QueryRowContext(ctx, r.q("SELECT "+companyCols+" FROM companies WHERE organization_id = ? AND id = ?"), orgID.String(), companyID.String())
	c, err := scanCompany(row)
	return c, notFound(err, "empresa")
}

func (r *orgRepo) Branch(ctx context.Context, orgID, companyID, branchID id.ID) (org.Branch, error) {
	row := r.tx.QueryRowContext(ctx, r.q("SELECT "+branchCols+" FROM branches WHERE organization_id = ? AND company_id = ? AND id = ?"),
		orgID.String(), companyID.String(), branchID.String())
	b, err := scanBranch(row)
	return b, notFound(err, "sucursal")
}

func (r *orgRepo) CompanyByCode(ctx context.Context, orgID id.ID, code org.Code) (org.Company, error) {
	row := r.tx.QueryRowContext(ctx, r.q("SELECT "+companyCols+" FROM companies WHERE organization_id = ? AND code = ?"), orgID.String(), code.String())
	c, err := scanCompany(row)
	return c, notFound(err, "empresa "+code.String())
}

func (r *orgRepo) BranchByCode(ctx context.Context, orgID, companyID id.ID, code org.Code) (org.Branch, error) {
	row := r.tx.QueryRowContext(ctx, r.q("SELECT "+branchCols+" FROM branches WHERE organization_id = ? AND company_id = ? AND code = ?"),
		orgID.String(), companyID.String(), code.String())
	b, err := scanBranch(row)
	return b, notFound(err, "sucursal "+code.String())
}

func (r *orgRepo) Companies(ctx context.Context, orgID id.ID) ([]org.Company, error) {
	return queryAll(ctx, r, scanCompany, "SELECT "+companyCols+" FROM companies WHERE organization_id = ? ORDER BY code", orgID.String())
}

func (r *orgRepo) Branches(ctx context.Context, orgID, companyID id.ID) ([]org.Branch, error) {
	q, args := "SELECT "+branchCols+" FROM branches WHERE organization_id = ?", []any{orgID.String()}
	if !companyID.IsZero() {
		q, args = q+" AND company_id = ?", append(args, companyID.String())
	}
	return queryAll(ctx, r, scanBranch, q+" ORDER BY code", args...)
}

func (r *orgRepo) Warehouses(ctx context.Context, orgID, companyID, branchID id.ID) ([]org.Warehouse, error) {
	q, args := "SELECT "+warehouseCols+" FROM warehouses WHERE organization_id = ?", []any{orgID.String()}
	if !companyID.IsZero() {
		q, args = q+" AND company_id = ?", append(args, companyID.String())
	}
	if !branchID.IsZero() {
		q, args = q+" AND branch_id = ?", append(args, branchID.String())
	}
	return queryAll(ctx, r, scanWarehouse, q+" ORDER BY code", args...)
}

func (r *orgRepo) Binding(ctx context.Context) (apporg.Binding, error) {
	var o, c, b string
	err := r.tx.QueryRowContext(ctx, "SELECT CAST(organization_id AS TEXT), CAST(company_id AS TEXT), CAST(branch_id AS TEXT) FROM installation_binding").Scan(&o, &c, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return apporg.Binding{}, apporg.ErrNotBootstrapped
	}
	if err != nil {
		return apporg.Binding{}, err
	}
	var out apporg.Binding
	if out.OrganizationID, err = id.Parse(o); err != nil {
		return apporg.Binding{}, err
	}
	if out.CompanyID, err = id.Parse(c); err != nil {
		return apporg.Binding{}, err
	}
	if out.BranchID, err = id.Parse(b); err != nil {
		return apporg.Binding{}, err
	}
	return out, nil
}

func (r *orgRepo) Bind(ctx context.Context, b apporg.Binding) error {
	var instID string
	if err := r.tx.QueryRowContext(ctx, "SELECT CAST(id AS TEXT) FROM installation").Scan(&instID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("la instalación no está registrada; ejecute 'balanzapro db migrate'")
		}
		return err
	}
	return r.exec(ctx, "asociación de la instalación",
		"INSERT INTO installation_binding (installation_id, organization_id, company_id, branch_id, bound_at_utc) VALUES (?, ?, ?, ?, ?)",
		instID, b.OrganizationID.String(), b.CompanyID.String(), b.BranchID.String(), r.ts(time.Now().UTC()))
}

func queryAll[T any](ctx context.Context, r *orgRepo, scan func(scanner) (T, error), query string, args ...any) ([]T, error) {
	rows, err := r.tx.QueryContext(ctx, r.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func scanOrganization(sc scanner) (org.Organization, error) {
	var rawID, code, name, status string
	var created any
	if err := sc.Scan(&rawID, &code, &name, &status, &created); err != nil {
		return org.Organization{}, err
	}
	var o org.Organization
	var err error
	if o.ID, err = id.Parse(rawID); err != nil {
		return o, err
	}
	o.Status = org.Status(status)
	if o.Code, o.Name, o.CreatedAt, err = decodeCommon(code, name, created); err != nil {
		return o, err
	}
	return o, nil
}

func scanCompany(sc scanner) (org.Company, error) {
	var orgID, rawID, code, name, status string
	var rif sql.NullString
	var created any
	if err := sc.Scan(&orgID, &rawID, &code, &name, &rif, &status, &created); err != nil {
		return org.Company{}, err
	}
	var c org.Company
	var err error
	if c.OrganizationID, err = id.Parse(orgID); err != nil {
		return c, err
	}
	if c.ID, err = id.Parse(rawID); err != nil {
		return c, err
	}
	if rif.Valid {
		if c.RIF, err = org.ParseRIF(rif.String); err != nil {
			return c, err
		}
	}
	c.Status = org.Status(status)
	if c.Code, c.LegalName, c.CreatedAt, err = decodeCommon(code, name, created); err != nil {
		return c, err
	}
	return c, nil
}

func scanBranch(sc scanner) (org.Branch, error) {
	var orgID, companyID, rawID, code, name, status string
	var created any
	if err := sc.Scan(&orgID, &companyID, &rawID, &code, &name, &status, &created); err != nil {
		return org.Branch{}, err
	}
	var b org.Branch
	var err error
	if b.OrganizationID, err = id.Parse(orgID); err != nil {
		return b, err
	}
	if b.CompanyID, err = id.Parse(companyID); err != nil {
		return b, err
	}
	if b.ID, err = id.Parse(rawID); err != nil {
		return b, err
	}
	b.Status = org.Status(status)
	if b.Code, b.Name, b.CreatedAt, err = decodeCommon(code, name, created); err != nil {
		return b, err
	}
	return b, nil
}

func scanWarehouse(sc scanner) (org.Warehouse, error) {
	var orgID, companyID, branchID, rawID, code, name, status string
	var created any
	if err := sc.Scan(&orgID, &companyID, &branchID, &rawID, &code, &name, &status, &created); err != nil {
		return org.Warehouse{}, err
	}
	var w org.Warehouse
	var err error
	if w.OrganizationID, err = id.Parse(orgID); err != nil {
		return w, err
	}
	if w.CompanyID, err = id.Parse(companyID); err != nil {
		return w, err
	}
	if w.BranchID, err = id.Parse(branchID); err != nil {
		return w, err
	}
	if w.ID, err = id.Parse(rawID); err != nil {
		return w, err
	}
	w.Status = org.Status(status)
	if w.Code, w.Name, w.CreatedAt, err = decodeCommon(code, name, created); err != nil {
		return w, err
	}
	return w, nil
}

func decodeCommon(code, name string, created any) (org.Code, org.Name, time.Time, error) {
	c, err := org.ParseCode(code)
	if err != nil {
		return org.Code{}, org.Name{}, time.Time{}, err
	}
	n, err := org.ParseName(name)
	if err != nil {
		return org.Code{}, org.Name{}, time.Time{}, err
	}
	t, err := parseTime(created)
	return c, n, t, err
}

func notFound(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", apporg.ErrNotFound, what)
	}
	return err
}

// isUniqueViolation reconoce la violación de UNIQUE/PRIMARY KEY de cada
// motor: SQLite 2067/1555 (códigos extendidos) y PostgreSQL SQLSTATE 23505.
func (s *Store) isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	var se interface{ Code() int }
	if errors.As(err, &se) {
		return se.Code() == 2067 || se.Code() == 1555
	}
	return false
}

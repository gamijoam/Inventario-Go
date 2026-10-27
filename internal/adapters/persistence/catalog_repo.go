package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/org"
)

// CatalogStore implementa appcatalog.Store.
func (s *Store) CatalogStore() appcatalog.Store { return catalogStore{s} }

type catalogStore struct{ s *Store }

func (c catalogStore) InTx(ctx context.Context, fn func(appcatalog.Repository) error) error {
	return orgStore(c).InTx(ctx, func(r apporg.Repository) error { return fn(r.(*orgRepo)) })
}

func (r *orgRepo) InsertUnit(ctx context.Context, u catalog.Unit) error {
	return r.exec(ctx, "unidad "+u.Code,
		"INSERT INTO units (organization_id, id, code, name, scale, created_at_utc) VALUES (?, ?, ?, ?, ?, ?)",
		u.OrganizationID.String(), u.ID.String(), u.Code, u.Name.String(), u.Scale, r.ts(u.CreatedAt))
}

const unitCols = "CAST(organization_id AS TEXT), CAST(id AS TEXT), code, name, scale, created_at_utc"

func scanUnit(sc scanner) (catalog.Unit, error) {
	var orgID, rawID, code, name string
	var created any
	var u catalog.Unit
	if err := sc.Scan(&orgID, &rawID, &code, &name, &u.Scale, &created); err != nil {
		return u, err
	}
	ids, err := parseIDs(orgID, rawID)
	if err != nil {
		return u, err
	}
	u.OrganizationID, u.ID, u.Code = ids[0], ids[1], code
	if u.Name, err = org.ParseName(name); err != nil {
		return u, err
	}
	u.CreatedAt, err = parseTime(created)
	return u, err
}

func (r *orgRepo) UnitByCode(ctx context.Context, orgID id.ID, code string) (catalog.Unit, error) {
	row := r.tx.QueryRowContext(ctx, r.q("SELECT "+unitCols+" FROM units WHERE organization_id = ? AND code = ?"), orgID.String(), code)
	u, err := scanUnit(row)
	return u, notFound(err, "unidad "+code)
}

func (r *orgRepo) Unit(ctx context.Context, orgID, unitID id.ID) (catalog.Unit, error) {
	row := r.tx.QueryRowContext(ctx, r.q("SELECT "+unitCols+" FROM units WHERE organization_id = ? AND id = ?"), orgID.String(), unitID.String())
	u, err := scanUnit(row)
	return u, notFound(err, "unidad")
}

func (r *orgRepo) Units(ctx context.Context, orgID id.ID) ([]catalog.Unit, error) {
	return queryAll(ctx, r, scanUnit, "SELECT "+unitCols+" FROM units WHERE organization_id = ? ORDER BY code", orgID.String())
}

func (r *orgRepo) InsertProduct(ctx context.Context, p catalog.Product) error {
	if err := r.exec(ctx, "producto (SKU "+p.SKU.String()+" repetido)",
		"INSERT INTO products (organization_id, id, sku, name, name_key, unit_id, status, revision, created_at_utc, updated_at_utc) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		p.OrganizationID.String(), p.ID.String(), p.SKU.String(), p.Name.String(), catalog.SearchKey(p.Name.String()), p.UnitID.String(),
		string(p.Status), p.Revision, r.ts(p.CreatedAt), r.ts(p.UpdatedAt)); err != nil {
		return err
	}
	return r.insertBarcodes(ctx, p)
}

func (r *orgRepo) insertBarcodes(ctx context.Context, p catalog.Product) error {
	for _, b := range p.Barcodes {
		if err := r.exec(ctx, "código de barras "+b.String()+" (ya está asignado a un producto)",
			"INSERT INTO product_barcodes (organization_id, product_id, code, lookup_key) VALUES (?, ?, ?, ?)",
			p.OrganizationID.String(), p.ID.String(), b.String(), b.Key()); err != nil {
			return err
		}
	}
	return nil
}

func (r *orgRepo) UpdateProduct(ctx context.Context, p catalog.Product) error {
	res, err := r.tx.ExecContext(ctx, r.q(
		"UPDATE products SET sku = ?, name = ?, name_key = ?, status = ?, revision = ?, updated_at_utc = ? WHERE organization_id = ? AND id = ? AND revision = ?"),
		p.SKU.String(), p.Name.String(), catalog.SearchKey(p.Name.String()), string(p.Status), p.Revision, r.ts(p.UpdatedAt),
		p.OrganizationID.String(), p.ID.String(), p.Revision-1)
	if err != nil {
		if r.s.isUniqueViolation(err) {
			return fmt.Errorf("%w: SKU %s repetido", apporg.ErrConflict, p.SKU)
		}
		return fmt.Errorf("guardar producto: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return appcatalog.ErrStale
	}
	if err := r.exec(ctx, "códigos de barras", "DELETE FROM product_barcodes WHERE organization_id = ? AND product_id = ?",
		p.OrganizationID.String(), p.ID.String()); err != nil {
		return err
	}
	return r.insertBarcodes(ctx, p)
}

const productCols = "CAST(p.organization_id AS TEXT), CAST(p.id AS TEXT), p.sku, p.name, CAST(p.unit_id AS TEXT), p.status, p.revision, p.created_at_utc, p.updated_at_utc"

func scanProduct(sc scanner) (catalog.Product, error) {
	var orgID, rawID, sku, name, unitID, status string
	var created, updated any
	var p catalog.Product
	if err := sc.Scan(&orgID, &rawID, &sku, &name, &unitID, &status, &p.Revision, &created, &updated); err != nil {
		return p, err
	}
	ids, err := parseIDs(orgID, rawID, unitID)
	if err != nil {
		return p, err
	}
	p.OrganizationID, p.ID, p.UnitID, p.Status = ids[0], ids[1], ids[2], catalog.Status(status)
	if p.SKU, err = catalog.ParseSKU(sku); err != nil {
		return p, err
	}
	if p.Name, err = org.ParseName(name); err != nil {
		return p, err
	}
	if p.CreatedAt, err = parseTime(created); err != nil {
		return p, err
	}
	p.UpdatedAt, err = parseTime(updated)
	return p, err
}

// withBarcodes completa los códigos de barras de los productos.
func (r *orgRepo) withBarcodes(ctx context.Context, list []catalog.Product) ([]catalog.Product, error) {
	for i := range list {
		scan := func(sc scanner) (catalog.Barcode, error) {
			var code string
			if err := sc.Scan(&code); err != nil {
				return catalog.Barcode{}, err
			}
			return catalog.ParseBarcode(code)
		}
		codes, err := queryAll(ctx, r, scan, "SELECT code FROM product_barcodes WHERE organization_id = ? AND product_id = ? ORDER BY code",
			list[i].OrganizationID.String(), list[i].ID.String())
		if err != nil {
			return nil, err
		}
		list[i].Barcodes = codes
	}
	return list, nil
}

func (r *orgRepo) productWhere(ctx context.Context, what, where string, args ...any) (catalog.Product, error) {
	p, err := scanProduct(r.tx.QueryRowContext(ctx, r.q("SELECT "+productCols+" FROM products p WHERE "+where), args...))
	if err != nil {
		return p, notFound(err, what)
	}
	list, err := r.withBarcodes(ctx, []catalog.Product{p})
	if err != nil {
		return catalog.Product{}, err
	}
	return list[0], nil
}

func (r *orgRepo) Product(ctx context.Context, orgID, productID id.ID) (catalog.Product, error) {
	return r.productWhere(ctx, "producto", "p.organization_id = ? AND p.id = ?", orgID.String(), productID.String())
}

func (r *orgRepo) ProductBySKU(ctx context.Context, orgID id.ID, sku catalog.SKU) (catalog.Product, error) {
	return r.productWhere(ctx, "producto "+sku.String(), "p.organization_id = ? AND p.sku = ?", orgID.String(), sku.String())
}

func (r *orgRepo) ProductByBarcode(ctx context.Context, orgID id.ID, key string) (catalog.Product, error) {
	return r.productWhere(ctx, "producto con código "+key,
		"p.organization_id = ? AND p.id = (SELECT b.product_id FROM product_barcodes b WHERE b.organization_id = ? AND b.lookup_key = ?)",
		orgID.String(), orgID.String(), key)
}

// likeEscape escapa los comodines de LIKE para buscar el texto literal.
var likeEscape = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func (r *orgRepo) Products(ctx context.Context, orgID id.ID, f appcatalog.Filter) ([]catalog.Product, error) {
	q := "SELECT " + productCols + " FROM products p WHERE p.organization_id = ?"
	args := []any{orgID.String()}
	if !f.IncludeInactive {
		q += " AND p.status = 'active'"
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		key := s
		if b, err := catalog.ParseBarcode(s); err == nil {
			key = b.Key()
		}
		q += ` AND (p.name_key LIKE ? ESCAPE '\' OR p.sku LIKE ? ESCAPE '\' OR p.id IN (SELECT b.product_id FROM product_barcodes b WHERE b.organization_id = ? AND b.lookup_key = ?))`
		args = append(args, "%"+likeEscape.Replace(catalog.SearchKey(s))+"%", likeEscape.Replace(strings.ToUpper(s))+"%", orgID.String(), key)
	}
	q += " ORDER BY p.name_key, p.sku LIMIT ? OFFSET ?"
	args = append(args, f.Limit, f.Offset)
	list, err := queryAll(ctx, r, scanProduct, q, args...)
	if err != nil {
		return nil, err
	}
	return r.withBarcodes(ctx, list)
}

func (r *orgRepo) NextCounter(ctx context.Context, orgID id.ID, name string) (int64, error) {
	res, err := r.tx.ExecContext(ctx, r.q("UPDATE catalog_counters SET last_value = last_value + 1 WHERE organization_id = ? AND name = ?"), orgID.String(), name)
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return 0, err
	} else if n == 0 {
		if err := r.exec(ctx, "contador "+name, "INSERT INTO catalog_counters (organization_id, name, last_value) VALUES (?, ?, 1)", orgID.String(), name); err != nil {
			return 0, err
		}
		return 1, nil
	}
	var v int64
	err = r.tx.QueryRowContext(ctx, r.q("SELECT last_value FROM catalog_counters WHERE organization_id = ? AND name = ?"), orgID.String(), name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("contador %s desaparecido", name)
	}
	return v, err
}

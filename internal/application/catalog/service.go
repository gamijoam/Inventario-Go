// Package catalog contiene los casos de uso del catálogo básico: unidades
// de medida y productos con SKU y códigos de barras (BP-M1-003).
//
// El catálogo es de la organización: lo ve quien tenga catalog.product.read
// en cualquier ámbito, y lo modifica solo quien tenga los permisos de
// gestión en toda la organización (administración central, sección 22.4).
package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/org"
)

var (
	// ErrStale indica que el producto cambió desde la revisión esperada.
	ErrStale = fmt.Errorf("%w: el producto cambió desde que lo consultó; vuelva a consultarlo", apporg.ErrConflict)
	// ErrAmbiguous: la referencia coincide con productos distintos (p. ej.
	// el SKU de uno y el código de barras de otro).
	ErrAmbiguous = fmt.Errorf("%w: la referencia coincide con varios productos; use el ID", catalog.ErrInvalid)
)

// UnitWriter es lo mínimo para sembrar unidades; lo usa también el alta de
// la instalación, en su propia transacción.
type UnitWriter interface {
	InsertUnit(ctx context.Context, u catalog.Unit) error
	UnitByCode(ctx context.Context, orgID id.ID, code string) (catalog.Unit, error)
}

// Filter limita un listado de productos.
type Filter struct {
	// Search busca en el nombre (sin acentos ni mayúsculas), el SKU y el
	// código de barras exacto.
	Search          string
	IncludeInactive bool
	Limit, Offset   int
}

// Repository es el puerto de persistencia. Los errores envuelven
// apporg.ErrNotFound o apporg.ErrConflict cuando corresponde.
type Repository interface {
	UnitWriter
	Units(ctx context.Context, orgID id.ID) ([]catalog.Unit, error)
	Unit(ctx context.Context, orgID, unitID id.ID) (catalog.Unit, error)

	InsertProduct(ctx context.Context, p catalog.Product) error
	// UpdateProduct guarda p si la revisión guardada es p.Revision-1 y
	// reemplaza sus códigos de barras; si no, devuelve ErrStale.
	UpdateProduct(ctx context.Context, p catalog.Product) error
	Product(ctx context.Context, orgID, productID id.ID) (catalog.Product, error)
	ProductBySKU(ctx context.Context, orgID id.ID, sku catalog.SKU) (catalog.Product, error)
	ProductByBarcode(ctx context.Context, orgID id.ID, key string) (catalog.Product, error)
	Products(ctx context.Context, orgID id.ID, f Filter) ([]catalog.Product, error)

	// NextCounter devuelve el siguiente valor (desde 1) del contador name.
	NextCounter(ctx context.Context, orgID id.ID, name string) (int64, error)
}

type Store interface {
	InTx(ctx context.Context, fn func(Repository) error) error
}

type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store, now func() time.Time) *Service {
	return &Service{store: store, now: now}
}

// SeedUnits crea las unidades base que falten en la organización.
func SeedUnits(ctx context.Context, r UnitWriter, o org.Organization, now time.Time) error {
	for _, tpl := range catalog.DefaultUnits() {
		if _, err := r.UnitByCode(ctx, o.ID, tpl.Code); err == nil {
			continue
		} else if !errors.Is(err, apporg.ErrNotFound) {
			return err
		}
		u, err := catalog.NewUnit(o, tpl.Code, tpl.Name, tpl.Scale, now)
		if err != nil {
			return err
		}
		if err := r.InsertUnit(ctx, u); err != nil {
			return err
		}
	}
	return nil
}

func canRead(p identity.Principal) error {
	if !p.HasAnywhere(identity.PermProductRead) {
		return fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermProductRead)
	}
	return nil
}

func (s *Service) ListUnits(ctx context.Context, p identity.Principal) ([]catalog.Unit, error) {
	if err := canRead(p); err != nil {
		return nil, err
	}
	var out []catalog.Unit
	err := s.store.InTx(ctx, func(r Repository) error {
		var err error
		out, err = r.Units(ctx, p.OrganizationID)
		return err
	})
	return out, err
}

// CreateUnit crea una unidad; su escala no se podrá cambiar.
func (s *Service) CreateUnit(ctx context.Context, p identity.Principal, o org.Organization, code, name string, scale int32) (catalog.Unit, error) {
	if err := p.Authorize(identity.PermUnitManage, o.Scope(), "toda la organización"); err != nil {
		return catalog.Unit{}, err
	}
	u, err := catalog.NewUnit(o, code, name, scale, s.now())
	if err != nil {
		return catalog.Unit{}, err
	}
	err = s.store.InTx(ctx, func(r Repository) error { return r.InsertUnit(ctx, u) })
	return u, err
}

// ProductView es un producto con su unidad.
type ProductView struct {
	catalog.Product
	Unit catalog.Unit
}

// NewProduct describe un producto simple: solo el nombre es obligatorio.
type NewProduct struct {
	SKU      string // vacío: se asigna P-000001, P-000002…
	Name     string
	UnitCode string // vacío: UND
	Barcodes []string
}

func parseBarcodes(raw []string) ([]catalog.Barcode, error) {
	out := make([]catalog.Barcode, 0, len(raw))
	for _, s := range raw {
		b, err := catalog.ParseBarcode(s)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// maxAutoSKUAttempts evita un ciclo infinito si alguien ocupó a mano muchos
// SKU de la serie automática.
const maxAutoSKUAttempts = 1000

func (s *Service) CreateProduct(ctx context.Context, p identity.Principal, o org.Organization, in NewProduct) (ProductView, error) {
	if err := p.Authorize(identity.PermProductManage, o.Scope(), "toda la organización"); err != nil {
		return ProductView{}, err
	}
	var sku catalog.SKU
	if strings.TrimSpace(in.SKU) != "" {
		var err error
		if sku, err = catalog.ParseSKU(in.SKU); err != nil {
			return ProductView{}, err
		}
	}
	barcodes, err := parseBarcodes(in.Barcodes)
	if err != nil {
		return ProductView{}, err
	}
	unitCode := strings.ToUpper(strings.TrimSpace(in.UnitCode))
	if unitCode == "" {
		unitCode = catalog.DefaultUnitCode
	}
	var out ProductView
	err = s.store.InTx(ctx, func(r Repository) error {
		u, err := r.UnitByCode(ctx, o.ID, unitCode)
		if err != nil {
			return err
		}
		if sku.IsZero() {
			if sku, err = nextSKU(ctx, r, o.ID); err != nil {
				return err
			}
		}
		prod, err := catalog.NewProduct(o, sku, in.Name, u, barcodes, s.now())
		if err != nil {
			return err
		}
		if err := r.InsertProduct(ctx, prod); err != nil {
			return err
		}
		out = ProductView{Product: prod, Unit: u}
		return nil
	})
	return out, err
}

func nextSKU(ctx context.Context, r Repository, orgID id.ID) (catalog.SKU, error) {
	for range maxAutoSKUAttempts {
		n, err := r.NextCounter(ctx, orgID, "product_sku")
		if err != nil {
			return catalog.SKU{}, err
		}
		sku := catalog.AutoSKU(n)
		if _, err := r.ProductBySKU(ctx, orgID, sku); errors.Is(err, apporg.ErrNotFound) {
			return sku, nil
		} else if err != nil {
			return catalog.SKU{}, err
		}
	}
	return catalog.SKU{}, fmt.Errorf("%w: no hay SKU automáticos libres; indique --sku", apporg.ErrConflict)
}

// resolve busca por SKU, código de barras o ID. Si la referencia coincide
// con productos distintos, devuelve ErrAmbiguous.
func resolve(ctx context.Context, r Repository, orgID id.ID, ref string) (catalog.Product, error) {
	var found []catalog.Product
	add := func(p catalog.Product, err error) error {
		if errors.Is(err, apporg.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, f := range found {
			if f.ID == p.ID {
				return nil
			}
		}
		found = append(found, p)
		return nil
	}
	if sku, err := catalog.ParseSKU(ref); err == nil {
		if err := add(r.ProductBySKU(ctx, orgID, sku)); err != nil {
			return catalog.Product{}, err
		}
	}
	if b, err := catalog.ParseBarcode(ref); err == nil {
		if err := add(r.ProductByBarcode(ctx, orgID, b.Key())); err != nil {
			return catalog.Product{}, err
		}
	}
	if pid, err := id.Parse(strings.TrimSpace(ref)); err == nil {
		if err := add(r.Product(ctx, orgID, pid)); err != nil {
			return catalog.Product{}, err
		}
	}
	switch len(found) {
	case 0:
		return catalog.Product{}, fmt.Errorf("%w: producto %q", apporg.ErrNotFound, ref)
	case 1:
		return found[0], nil
	default:
		return catalog.Product{}, ErrAmbiguous
	}
}

func (s *Service) view(ctx context.Context, r Repository, p catalog.Product) (ProductView, error) {
	u, err := r.Unit(ctx, p.OrganizationID, p.UnitID)
	return ProductView{Product: p, Unit: u}, err
}

// ShowProduct busca un producto por SKU, código de barras o ID.
func (s *Service) ShowProduct(ctx context.Context, p identity.Principal, ref string) (ProductView, error) {
	if err := canRead(p); err != nil {
		return ProductView{}, err
	}
	var out ProductView
	err := s.store.InTx(ctx, func(r Repository) error {
		prod, err := resolve(ctx, r, p.OrganizationID, ref)
		if err != nil {
			return err
		}
		out, err = s.view(ctx, r, prod)
		return err
	})
	return out, err
}

// MaxListLimit es el máximo de productos por página.
const MaxListLimit = 500

func (s *Service) ListProducts(ctx context.Context, p identity.Principal, f Filter) ([]ProductView, error) {
	if err := canRead(p); err != nil {
		return nil, err
	}
	if f.Limit <= 0 || f.Limit > MaxListLimit {
		return nil, fmt.Errorf("%w: el límite debe estar entre 1 y %d", catalog.ErrInvalid, MaxListLimit)
	}
	if f.Offset < 0 {
		return nil, fmt.Errorf("%w: desplazamiento negativo", catalog.ErrInvalid)
	}
	var out []ProductView
	err := s.store.InTx(ctx, func(r Repository) error {
		units, err := r.Units(ctx, p.OrganizationID)
		if err != nil {
			return err
		}
		byID := make(map[id.ID]catalog.Unit, len(units))
		for _, u := range units {
			byID[u.ID] = u
		}
		list, err := r.Products(ctx, p.OrganizationID, f)
		if err != nil {
			return err
		}
		for _, prod := range list {
			out = append(out, ProductView{Product: prod, Unit: byID[prod.UnitID]})
		}
		return nil
	})
	return out, err
}

// Changes describe una edición; los campos nil no cambian. La unidad no se
// edita: las cantidades registradas dependen de ella.
type Changes struct {
	Name, SKU                   *string
	Active                      *bool
	AddBarcodes, RemoveBarcodes []string
	// ExpectedRevision, si no es cero, exige que nadie haya modificado el
	// producto desde esa revisión.
	ExpectedRevision int64
}

func (s *Service) UpdateProduct(ctx context.Context, p identity.Principal, o org.Organization, ref string, c Changes) (ProductView, error) {
	if err := p.Authorize(identity.PermProductManage, o.Scope(), "toda la organización"); err != nil {
		return ProductView{}, err
	}
	if c.Name == nil && c.SKU == nil && c.Active == nil && len(c.AddBarcodes)+len(c.RemoveBarcodes) == 0 {
		return ProductView{}, fmt.Errorf("%w: no se indicó ningún cambio", catalog.ErrInvalid)
	}
	add, err := parseBarcodes(c.AddBarcodes)
	if err != nil {
		return ProductView{}, err
	}
	remove, err := parseBarcodes(c.RemoveBarcodes)
	if err != nil {
		return ProductView{}, err
	}
	var out ProductView
	err = s.store.InTx(ctx, func(r Repository) error {
		prod, err := resolve(ctx, r, o.ID, ref)
		if err != nil {
			return err
		}
		if c.ExpectedRevision != 0 && c.ExpectedRevision != prod.Revision {
			return ErrStale
		}
		next := prod
		if c.Name != nil {
			if next.Name, err = org.ParseName(*c.Name); err != nil {
				return err
			}
		}
		if c.SKU != nil {
			if next.SKU, err = catalog.ParseSKU(*c.SKU); err != nil {
				return err
			}
		}
		if c.Active != nil {
			next.Status = catalog.StatusInactive
			if *c.Active {
				next.Status = catalog.StatusActive
			}
		}
		if next, err = next.WithBarcodes(add, remove); err != nil {
			return err
		}
		next.Revision = prod.Revision + 1
		next.UpdatedAt = s.now().UTC()
		if err := r.UpdateProduct(ctx, next); err != nil {
			return err
		}
		out, err = s.view(ctx, r, next)
		return err
	})
	return out, err
}

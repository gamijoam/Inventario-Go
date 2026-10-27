// Package org contiene los casos de uso de la jerarquía organizacional:
// alta inicial de una instalación y gestión de empresas, sucursales y
// almacenes.
//
// Cada operación recibe el principal autenticado (identity.Principal) y
// comprueba el permiso de la operación en el ámbito del recurso. Las
// búsquedas fuera de lo visible devuelven ErrNotFound, sin revelar si el
// recurso existe (sección 22.8, punto 3).
package org

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

var (
	ErrNotFound            = errors.New("no encontrado")
	ErrConflict            = errors.New("ya existe")
	ErrForbidden           = errors.New("operación no permitida en este ámbito")
	ErrNotBootstrapped     = errors.New("la instalación no tiene organización; ejecute 'balanzapro setup'")
	ErrAlreadyBootstrapped = errors.New("la instalación ya tiene una organización configurada")
	ErrInactive            = errors.New("la entidad está inactiva")
)

// Binding asocia la instalación con su sucursal (DEC-003: el nodo V1
// custodia una única sucursal).
type Binding struct {
	OrganizationID id.ID
	CompanyID      id.ID
	BranchID       id.ID
}

// Repository es el puerto de persistencia. Las implementaciones devuelven
// errores que envuelven ErrNotFound o ErrConflict cuando corresponde.
type Repository interface {
	InsertOrganization(ctx context.Context, o org.Organization) error
	InsertCompany(ctx context.Context, c org.Company) error
	InsertBranch(ctx context.Context, b org.Branch) error
	InsertWarehouse(ctx context.Context, w org.Warehouse) error

	Organization(ctx context.Context, orgID id.ID) (org.Organization, error)
	Company(ctx context.Context, orgID, companyID id.ID) (org.Company, error)
	Branch(ctx context.Context, orgID, companyID, branchID id.ID) (org.Branch, error)
	CompanyByCode(ctx context.Context, orgID id.ID, code org.Code) (org.Company, error)
	BranchByCode(ctx context.Context, orgID, companyID id.ID, code org.Code) (org.Branch, error)

	// Companies, Branches y Warehouses filtran por los IDs no nulos.
	Companies(ctx context.Context, orgID id.ID) ([]org.Company, error)
	Branches(ctx context.Context, orgID, companyID id.ID) ([]org.Branch, error)
	Warehouses(ctx context.Context, orgID, companyID, branchID id.ID) ([]org.Warehouse, error)

	// Binding devuelve ErrNotBootstrapped si la instalación no está asociada.
	Binding(ctx context.Context) (Binding, error)
	Bind(ctx context.Context, b Binding) error
}

// Store ejecuta fn en una transacción: todo o nada.
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

// BootstrapInput describe la estructura mínima de una instalación nueva.
type BootstrapInput struct {
	OrganizationCode, OrganizationName string
	CompanyCode, CompanyLegalName      string
	RIF                                string // opcional
	Currency                           string // de valoración; vacío es USD
	BranchCode, BranchName             string
	WarehouseCode, WarehouseName       string
}

// Context es la estructura asociada a la instalación.
type Context struct {
	Organization org.Organization
	Company      org.Company
	Branch       org.Branch
}

// Bootstrapped es el resultado del alta inicial.
type Bootstrapped struct {
	Context
	Warehouse org.Warehouse
}

// Bootstrap crea organización, empresa, sucursal y almacén y asocia la
// instalación, en una sola transacción (sección 22.7).
func (s *Service) Bootstrap(ctx context.Context, in BootstrapInput) (Bootstrapped, error) {
	var out Bootstrapped
	err := s.store.InTx(ctx, func(r Repository) error {
		var err error
		out, err = BootstrapTx(ctx, r, in, s.now())
		return err
	})
	return out, err
}

// BootstrapTx es Bootstrap dentro de una transacción ajena, para que el alta
// de usuarios (BP-M1-002) ocurra en la misma unidad de trabajo.
func BootstrapTx(ctx context.Context, r Repository, in BootstrapInput, now time.Time) (Bootstrapped, error) {
	rif, cur, err := companyDetails(in.RIF, in.Currency)
	if err != nil {
		return Bootstrapped{}, err
	}
	o, err := org.NewOrganization(in.OrganizationCode, in.OrganizationName, now)
	if err != nil {
		return Bootstrapped{}, err
	}
	c, err := org.NewCompany(o, in.CompanyCode, in.CompanyLegalName, rif, now)
	if err != nil {
		return Bootstrapped{}, err
	}
	c.Currency = cur
	b, err := org.NewBranch(c, in.BranchCode, in.BranchName, now)
	if err != nil {
		return Bootstrapped{}, err
	}
	w, err := org.NewWarehouse(b, in.WarehouseCode, in.WarehouseName, now)
	if err != nil {
		return Bootstrapped{}, err
	}

	if _, err := r.Binding(ctx); err == nil {
		return Bootstrapped{}, ErrAlreadyBootstrapped
	} else if !errors.Is(err, ErrNotBootstrapped) {
		return Bootstrapped{}, err
	}
	for _, insert := range []func() error{
		func() error { return r.InsertOrganization(ctx, o) },
		func() error { return r.InsertCompany(ctx, c) },
		func() error { return r.InsertBranch(ctx, b) },
		func() error { return r.InsertWarehouse(ctx, w) },
	} {
		if err := insert(); err != nil {
			return Bootstrapped{}, err
		}
	}
	if err := r.Bind(ctx, Binding{OrganizationID: o.ID, CompanyID: c.ID, BranchID: b.ID}); err != nil {
		return Bootstrapped{}, err
	}
	return Bootstrapped{Context: Context{Organization: o, Company: c, Branch: b}, Warehouse: w}, nil
}

// Current devuelve la estructura asociada a la instalación.
func (s *Service) Current(ctx context.Context) (Context, error) {
	var out Context
	err := s.store.InTx(ctx, func(r Repository) error {
		var err error
		out, err = CurrentTx(ctx, r)
		return err
	})
	return out, err
}

// Overview es Current para un principal que puede ver la estructura.
func (s *Service) Overview(ctx context.Context, p identity.Principal) (Context, error) {
	var out Context
	err := s.store.InTx(ctx, func(r Repository) error {
		if _, err := s.organization(ctx, r, p); err != nil {
			return err
		}
		var err error
		out, err = CurrentTx(ctx, r)
		return err
	})
	return out, err
}

// CurrentTx es Current dentro de una transacción ajena.
func CurrentTx(ctx context.Context, r Repository) (Context, error) {
	var out Context
	bind, err := r.Binding(ctx)
	if err != nil {
		return out, err
	}
	if out.Organization, err = r.Organization(ctx, bind.OrganizationID); err != nil {
		return out, err
	}
	if out.Company, err = r.Company(ctx, bind.OrganizationID, bind.CompanyID); err != nil {
		return out, err
	}
	out.Branch, err = r.Branch(ctx, bind.OrganizationID, bind.CompanyID, bind.BranchID)
	return out, err
}

// companyDetails valida el RIF (opcional) y la moneda de valoración
// (vacía es la predeterminada).
func companyDetails(rif, currency string) (org.RIF, money.Currency, error) {
	var parsed org.RIF
	if rif != "" {
		var err error
		if parsed, err = org.ParseRIF(rif); err != nil {
			return org.RIF{}, money.Currency{}, err
		}
	}
	if currency == "" {
		return parsed, org.DefaultCurrency, nil
	}
	cur, err := org.ParseCurrency(currency)
	return parsed, cur, err
}

// CreateCompany crea una empresa legal; requiere un ámbito de organización.
// currency es la moneda de valoración del inventario; vacía es USD.
func (s *Service) CreateCompany(ctx context.Context, p identity.Principal, code, legalName, rif, currency string) (org.Company, error) {
	parsed, cur, err := companyDetails(rif, currency)
	if err != nil {
		return org.Company{}, err
	}
	var c org.Company
	err = s.store.InTx(ctx, func(r Repository) error {
		o, err := r.Organization(ctx, p.OrganizationID)
		if err != nil {
			return err
		}
		if c, err = org.NewCompany(o, code, legalName, parsed, s.now()); err != nil {
			return err
		}
		c.Currency = cur
		if err := p.Authorize(identity.PermCompanyCreate, c.Scope(), "la organización "+o.Code.String()); err != nil {
			return err
		}
		return r.InsertCompany(ctx, c)
	})
	return c, err
}

// CreateBranch crea una sucursal en la empresa companyCode.
func (s *Service) CreateBranch(ctx context.Context, p identity.Principal, companyCode, code, name string) (org.Branch, error) {
	var b org.Branch
	err := s.store.InTx(ctx, func(r Repository) error {
		c, err := s.company(ctx, r, p, companyCode)
		if err != nil {
			return err
		}
		if c.Status != org.StatusActive {
			return fmt.Errorf("%w: empresa %s", ErrInactive, c.Code)
		}
		if b, err = org.NewBranch(c, code, name, s.now()); err != nil {
			return err
		}
		if err := p.Authorize(identity.PermBranchCreate, b.Scope(), "la empresa "+c.Code.String()); err != nil {
			return err
		}
		return r.InsertBranch(ctx, b)
	})
	return b, err
}

// CreateWarehouse crea un almacén en la sucursal branchCode de companyCode.
func (s *Service) CreateWarehouse(ctx context.Context, p identity.Principal, companyCode, branchCode, code, name string) (org.Warehouse, error) {
	var w org.Warehouse
	err := s.store.InTx(ctx, func(r Repository) error {
		b, err := s.branch(ctx, r, p, companyCode, branchCode)
		if err != nil {
			return err
		}
		if b.Status != org.StatusActive {
			return fmt.Errorf("%w: sucursal %s", ErrInactive, b.Code)
		}
		if w, err = org.NewWarehouse(b, code, name, s.now()); err != nil {
			return err
		}
		if err := p.Authorize(identity.PermWarehouseCreate, w.Scope(), "la sucursal "+b.Code.String()); err != nil {
			return err
		}
		return r.InsertWarehouse(ctx, w)
	})
	return w, err
}

// ListCompanies devuelve las empresas visibles para el principal.
func (s *Service) ListCompanies(ctx context.Context, p identity.Principal) ([]org.Company, error) {
	var out []org.Company
	err := s.store.InTx(ctx, func(r Repository) error {
		if _, err := s.organization(ctx, r, p); err != nil {
			return err
		}
		all, err := r.Companies(ctx, p.OrganizationID)
		if err != nil {
			return err
		}
		out = filter(all, p, org.Company.Scope)
		return nil
	})
	return out, err
}

// ListBranches devuelve las sucursales visibles; companyCode vacío = todas.
func (s *Service) ListBranches(ctx context.Context, p identity.Principal, companyCode string) ([]org.Branch, error) {
	var out []org.Branch
	err := s.store.InTx(ctx, func(r Repository) error {
		var companyID id.ID
		if companyCode != "" {
			c, err := s.company(ctx, r, p, companyCode)
			if err != nil {
				return err
			}
			companyID = c.ID
		} else if _, err := s.organization(ctx, r, p); err != nil {
			return err
		}
		all, err := r.Branches(ctx, p.OrganizationID, companyID)
		if err != nil {
			return err
		}
		out = filter(all, p, org.Branch.Scope)
		return nil
	})
	return out, err
}

// ListWarehouses devuelve los almacenes visibles, opcionalmente filtrados
// por empresa y sucursal (branchCode requiere companyCode).
func (s *Service) ListWarehouses(ctx context.Context, p identity.Principal, companyCode, branchCode string) ([]org.Warehouse, error) {
	var out []org.Warehouse
	err := s.store.InTx(ctx, func(r Repository) error {
		var companyID, branchID id.ID
		switch {
		case branchCode != "":
			b, err := s.branch(ctx, r, p, companyCode, branchCode)
			if err != nil {
				return err
			}
			companyID, branchID = b.CompanyID, b.ID
		case companyCode != "":
			c, err := s.company(ctx, r, p, companyCode)
			if err != nil {
				return err
			}
			companyID = c.ID
		default:
			if _, err := s.organization(ctx, r, p); err != nil {
				return err
			}
		}
		all, err := r.Warehouses(ctx, p.OrganizationID, companyID, branchID)
		if err != nil {
			return err
		}
		out = filter(all, p, org.Warehouse.Scope)
		return nil
	})
	return out, err
}

// organization exige que el principal pueda ver la estructura en algún
// ámbito de su organización.
func (s *Service) organization(ctx context.Context, r Repository, p identity.Principal) (org.Organization, error) {
	if !p.HasAnywhere(identity.PermOrgRead) {
		return org.Organization{}, fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermOrgRead)
	}
	return r.Organization(ctx, p.OrganizationID)
}

// company busca por código dentro de la organización del principal; si no
// la ve, responde como si no existiera.
func (s *Service) company(ctx context.Context, r Repository, p identity.Principal, code string) (org.Company, error) {
	c, err := org.ParseCode(code)
	if err != nil {
		return org.Company{}, err
	}
	if _, err := s.organization(ctx, r, p); err != nil {
		return org.Company{}, err
	}
	company, err := r.CompanyByCode(ctx, p.OrganizationID, c)
	if err != nil {
		return org.Company{}, err
	}
	if !visible(p, company.Scope()) {
		return org.Company{}, fmt.Errorf("%w: empresa %s", ErrNotFound, c)
	}
	return company, nil
}

func (s *Service) branch(ctx context.Context, r Repository, p identity.Principal, companyCode, code string) (org.Branch, error) {
	company, err := s.company(ctx, r, p, companyCode)
	if err != nil {
		return org.Branch{}, err
	}
	c, err := org.ParseCode(code)
	if err != nil {
		return org.Branch{}, err
	}
	b, err := r.BranchByCode(ctx, p.OrganizationID, company.ID, c)
	if err != nil {
		return org.Branch{}, err
	}
	if !visible(p, b.Scope()) {
		return org.Branch{}, fmt.Errorf("%w: sucursal %s", ErrNotFound, c)
	}
	return b, nil
}

// visible: el principal ve una entidad si tiene permiso de lectura en ella,
// en un ancestro o en un descendiente (un usuario de sucursal ve su empresa).
func visible(p identity.Principal, entity scope.BusinessScope) bool {
	return p.Sees(identity.PermOrgRead, entity)
}

func filter[T any](items []T, p identity.Principal, scopeOf func(T) scope.BusinessScope) []T {
	out := make([]T, 0, len(items))
	for _, it := range items {
		if visible(p, scopeOf(it)) {
			out = append(out, it)
		}
	}
	return out
}

// Package pricing contiene los casos de uso de listas y precios de venta
// (BP-M3-003, DEC-007.P2): listas por empresa con margen objetivo,
// propuestas de precio (sugeridas al recibir mercancía o manuales), su
// aprobación y las revisiones inmediatas o programadas.
//
// Recibir mercancía nunca cambia un precio: solo deja propuestas. Aprobar
// exige pricing.price.approve con ámbito de empresa, porque una lista rige
// en todas sus sucursales; quien propone puede aprobar si tiene ese
// permiso (ratificado 2026-10-27). Aprobar por debajo del costo de
// referencia exige además el permiso extraordinario
// pricing.override.approve_below_cost.
package pricing

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/application/rates"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/exchange"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/pricing"
	"github.com/gamijoam/inventario-go/internal/domain/purchase"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

// Repository es el puerto de persistencia de precios. Las inserciones
// devuelven stock.ErrConcurrent si otra transacción tomó la misma versión,
// secuencia o propuesta pendiente.
type Repository interface {
	appcatalog.Repository
	rates.Repository
	Company(ctx context.Context, orgID, companyID id.ID) (org.Company, error)

	InsertPriceList(ctx context.Context, l pricing.List) error
	PriceList(ctx context.Context, orgID, listID id.ID) (pricing.List, error)
	PriceListByCode(ctx context.Context, orgID, companyID id.ID, code org.Code) (pricing.List, error)
	PriceLists(ctx context.Context, orgID, companyID id.ID) ([]pricing.List, error)

	// InsertMargin asigna la secuencia siguiente de la lista.
	InsertMargin(ctx context.Context, orgID id.ID, m pricing.MarginChange) error
	// Margins devuelve los cambios de margen de la lista en orden.
	Margins(ctx context.Context, orgID, listID id.ID) ([]pricing.MarginChange, error)

	InsertRevision(ctx context.Context, orgID id.ID, r pricing.Revision) error
	CancelRevision(ctx context.Context, orgID id.ID, r pricing.Revision) error
	Revision(ctx context.Context, orgID, revisionID id.ID) (pricing.Revision, error)
	// Revisions devuelve las revisiones de la lista; productID cero las de
	// todos sus productos.
	Revisions(ctx context.Context, orgID, listID, productID id.ID) ([]pricing.Revision, error)

	InsertProposal(ctx context.Context, orgID id.ID, p pricing.Proposal) error
	// UpdateProposal guarda la decisión si seguía pendiente;
	// stock.ErrConcurrent si otra transacción la decidió.
	UpdateProposal(ctx context.Context, orgID id.ID, p pricing.Proposal) error
	Proposal(ctx context.Context, orgID, proposalID id.ID, forUpdate bool) (pricing.Proposal, error)
	ProposalByNumber(ctx context.Context, orgID id.ID, number string) (pricing.Proposal, error)
	// PendingProposal es la propuesta pendiente del producto en la lista;
	// apporg.ErrNotFound si no hay.
	PendingProposal(ctx context.Context, orgID, listID, productID id.ID) (pricing.Proposal, error)
	// Proposals filtra por lista y estado si no son cero o vacíos.
	Proposals(ctx context.Context, orgID, listID id.ID, status pricing.ProposalStatus) ([]pricing.Proposal, error)
	ProposalsOfReceipt(ctx context.Context, orgID, receiptID id.ID) ([]pricing.Proposal, error)

	ReplacementCosts(ctx context.Context, orgID, companyID, productID id.ID) ([]purchase.ReplacementCost, error)
	LastPurchase(ctx context.Context, orgID, companyID, productID id.ID) (purchase.LastPurchase, error)
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

const maxAttempts = 8

func (s *Service) write(ctx context.Context, fn func(Repository) error) error {
	for range maxAttempts {
		err := s.store.InTx(ctx, fn)
		if !errors.Is(err, stock.ErrConcurrent) {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return stock.ErrBusy
}

// ---------- Vistas ----------

// ListView es una lista con su empresa y su margen objetivo vigente.
type ListView struct {
	pricing.List
	Company org.Company
	Margin  *decimal.Decimal
}

// ProposalView es una propuesta con su contexto.
type ProposalView struct {
	pricing.Proposal
	List     pricing.List
	Product  catalog.Product
	Rate     *exchange.Rate    // con que se convirtió el costo de referencia
	Revision *pricing.Revision // publicada al aprobar
}

// PriceView es el precio de un producto en una lista: el vigente, los
// programados y la propuesta pendiente.
type PriceView struct {
	List      pricing.List
	Product   catalog.Product
	Margin    *decimal.Decimal // objetivo del producto en la lista
	Current   *pricing.Revision
	Scheduled []pricing.Revision
	Pending   *pricing.Proposal
}

// ---------- Listas y márgenes ----------

// company resuelve una empresa visible.
func company(ctx context.Context, r Repository, p identity.Principal, code string) (org.Company, error) {
	cc, err := org.ParseCode(code)
	if err != nil {
		return org.Company{}, err
	}
	c, err := r.CompanyByCode(ctx, p.OrganizationID, cc)
	if err != nil {
		return c, err
	}
	if !p.Sees(identity.PermOrgRead, c.Scope()) && !p.Sees(identity.PermPriceRead, c.Scope()) {
		return c, fmt.Errorf("%w: empresa %s", apporg.ErrNotFound, cc)
	}
	return c, nil
}

func readable(p identity.Principal, c org.Company) error {
	if !p.Sees(identity.PermPriceRead, c.Scope()) {
		return fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermPriceRead)
	}
	return nil
}

func list(ctx context.Context, r Repository, orgID id.ID, c org.Company, code string) (pricing.List, error) {
	lc, err := org.ParseCode(code)
	if err != nil {
		return pricing.List{}, fmt.Errorf("%w: %w", pricing.ErrInvalid, err)
	}
	return r.PriceListByCode(ctx, orgID, c.ID, lc)
}

// margins devuelve el margen vigente de la lista y las excepciones por
// producto.
func margins(ctx context.Context, r Repository, orgID, listID id.ID) (*decimal.Decimal, map[id.ID]*decimal.Decimal, error) {
	changes, err := r.Margins(ctx, orgID, listID)
	if err != nil {
		return nil, nil, err
	}
	var listMargin *decimal.Decimal
	products := map[id.ID]*decimal.Decimal{}
	for _, m := range changes { // en orden: el último rige
		if m.ProductID.IsZero() {
			listMargin = m.Margin
		} else {
			products[m.ProductID] = m.Margin
		}
	}
	return listMargin, products, nil
}

// ListInput crea una lista de precios.
type ListInput struct {
	Company  string
	Code     string
	Name     string
	Currency string // vacía es la de valoración de la empresa
	Margin   string // margen objetivo en %, opcional
}

// CreateList crea una lista de la empresa; exige gestionar listas con
// ámbito de empresa.
func (s *Service) CreateList(ctx context.Context, p identity.Principal, in ListInput) (ListView, error) {
	var out ListView
	err := s.write(ctx, func(r Repository) error {
		c, err := company(ctx, r, p, in.Company)
		if err != nil {
			return err
		}
		if err := p.Authorize(identity.PermPriceListManage, c.Scope(), "la empresa "+c.Code.String()); err != nil {
			return err
		}
		cur := c.Currency
		if strings.TrimSpace(in.Currency) != "" {
			if cur, err = rates.ParseCurrency(in.Currency); err != nil {
				return err
			}
		}
		now := s.now()
		l, err := pricing.NewList(p.OrganizationID, c.ID, in.Code, in.Name, cur, p.UserID, now)
		if err != nil {
			return err
		}
		if err := r.InsertPriceList(ctx, l); err != nil {
			return err
		}
		out = ListView{List: l, Company: c}
		if strings.TrimSpace(in.Margin) == "" {
			return nil
		}
		m, err := pricing.ParseMargin(in.Margin)
		if err != nil {
			return err
		}
		ch, err := pricing.NewMarginChange(l.ID, id.ID{}, &m, p.UserID, now)
		if err != nil {
			return err
		}
		out.Margin = &m
		return r.InsertMargin(ctx, p.OrganizationID, ch)
	})
	return out, err
}

// Lists devuelve las listas de la empresa.
func (s *Service) Lists(ctx context.Context, p identity.Principal, companyCode string) ([]ListView, error) {
	var out []ListView
	err := s.store.InTx(ctx, func(r Repository) error {
		c, err := company(ctx, r, p, companyCode)
		if err != nil {
			return err
		}
		if err := readable(p, c); err != nil {
			return err
		}
		lists, err := r.PriceLists(ctx, p.OrganizationID, c.ID)
		if err != nil {
			return err
		}
		for _, l := range lists {
			m, _, err := margins(ctx, r, p.OrganizationID, l.ID)
			if err != nil {
				return err
			}
			out = append(out, ListView{List: l, Company: c, Margin: m})
		}
		return nil
	})
	return out, err
}

// MarginInput fija el margen objetivo de una lista o la excepción de un
// producto (Product no vacío). Margin vacío lo quita.
type MarginInput struct {
	Company string
	List    string
	Product string
	Margin  string
}

// SetMargin registra un cambio de margen; exige gestionar listas con ámbito
// de empresa.
func (s *Service) SetMargin(ctx context.Context, p identity.Principal, in MarginInput) (ListView, error) {
	var m *decimal.Decimal
	if strings.TrimSpace(in.Margin) != "" {
		v, err := pricing.ParseMargin(in.Margin)
		if err != nil {
			return ListView{}, err
		}
		m = &v
	}
	var out ListView
	err := s.write(ctx, func(r Repository) error {
		c, err := company(ctx, r, p, in.Company)
		if err != nil {
			return err
		}
		if err := p.Authorize(identity.PermPriceListManage, c.Scope(), "la empresa "+c.Code.String()); err != nil {
			return err
		}
		l, err := list(ctx, r, p.OrganizationID, c, in.List)
		if err != nil {
			return err
		}
		var pid id.ID
		if strings.TrimSpace(in.Product) != "" {
			prod, err := appcatalog.Resolve(ctx, r, p.OrganizationID, in.Product)
			if err != nil {
				return err
			}
			pid = prod.ID
		}
		ch, err := pricing.NewMarginChange(l.ID, pid, m, p.UserID, s.now())
		if err != nil {
			return err
		}
		if err := r.InsertMargin(ctx, p.OrganizationID, ch); err != nil {
			return err
		}
		lm, _, err := margins(ctx, r, p.OrganizationID, l.ID)
		out = ListView{List: l, Company: c, Margin: lm}
		return err
	})
	return out, err
}

// ---------- Costo de referencia ----------

// reference es el costo con que se sugiere o evalúa un precio: el de
// reposición vigente o, si no hay, el último de compra con gastos;
// convertido a la moneda de la lista con la tasa de hoy. nil si el
// producto no tiene costo; exchange.ErrNoRate si falta la tasa.
func reference(ctx context.Context, r Repository, c org.Company, l pricing.List, productID id.ID, now time.Time) (*pricing.Reference, *exchange.Rate, error) {
	var ref pricing.Reference
	replacements, err := r.ReplacementCosts(ctx, c.OrganizationID, c.ID, productID)
	if err != nil {
		return nil, nil, err
	}
	if n := len(replacements); n > 0 {
		last := replacements[n-1] // en orden de registro
		ref = pricing.Reference{Basis: pricing.BasisReplacement, Cost: last.UnitCost, Currency: last.Currency}
	} else {
		lp, err := r.LastPurchase(ctx, c.OrganizationID, c.ID, productID)
		if errors.Is(err, apporg.ErrNotFound) {
			return nil, nil, nil
		}
		if err != nil {
			return nil, nil, err
		}
		ref = pricing.Reference{Basis: pricing.BasisLastPurchase, Cost: lp.TotalUnit, Currency: c.Currency}
	}
	var rate *exchange.Rate
	if ref.Currency != l.Currency {
		x, err := r.EffectiveRate(ctx, c.OrganizationID, c.ID, exchange.Pair(ref.Currency, l.Currency), timezone.DateOf(now))
		if err != nil {
			return nil, nil, err
		}
		rate, ref.RateID = &x, x.ID
	}
	if ref.ListCost, err = purchase.Valuate(ref.Cost, ref.Currency, l.Currency, rate); err != nil {
		return nil, nil, err
	}
	return &ref, rate, nil
}

// ---------- Propuestas ----------

// latest es la versión más reciente y el precio vigente del producto en
// la lista.
func latest(ctx context.Context, r Repository, orgID, listID, productID id.ID, now time.Time) (int, *pricing.Revision, error) {
	revs, err := r.Revisions(ctx, orgID, listID, productID)
	if err != nil {
		return 0, nil, err
	}
	v := 0
	for _, x := range revs {
		v = max(v, x.Version)
	}
	if cur, ok := pricing.Current(revs, now); ok {
		return v, &cur, nil
	}
	return v, nil, nil
}

// record numera una propuesta, fija su versión base y precio vigente, y
// sustituye la pendiente anterior del mismo producto en la lista.
func record(ctx context.Context, r Repository, orgID id.ID, prop pricing.Proposal, now time.Time) (pricing.Proposal, error) {
	v, cur, err := latest(ctx, r, orgID, prop.ListID, prop.ProductID, now)
	if err != nil {
		return prop, err
	}
	prop.BaseVersion = v
	if cur != nil {
		price := cur.Price
		prop.Current = &price
	}
	old, err := r.PendingProposal(ctx, orgID, prop.ListID, prop.ProductID)
	switch {
	case err == nil:
		if old, err = old.Supersede(prop.ProposedBy, now); err != nil {
			return prop, err
		}
		if err := r.UpdateProposal(ctx, orgID, old); err != nil {
			return prop, err
		}
	case !errors.Is(err, apporg.ErrNotFound):
		return prop, err
	}
	n, err := r.NextCounter(ctx, orgID, "price_proposal")
	if err != nil {
		return prop, err
	}
	prop.Number = fmt.Sprintf("PP-%06d", n)
	return prop, r.InsertProposal(ctx, orgID, prop)
}

func proposalView(ctx context.Context, r Repository, orgID id.ID, prop pricing.Proposal) (ProposalView, error) {
	v := ProposalView{Proposal: prop}
	var err error
	if v.List, err = r.PriceList(ctx, orgID, prop.ListID); err != nil {
		return v, err
	}
	if v.Product, err = r.Product(ctx, orgID, prop.ProductID); err != nil {
		return v, err
	}
	if prop.Reference != nil && !prop.Reference.RateID.IsZero() {
		x, err := r.Rate(ctx, orgID, prop.Reference.RateID)
		if err != nil {
			return v, err
		}
		v.Rate = &x
	}
	if !prop.RevisionID.IsZero() {
		rev, err := r.Revision(ctx, orgID, prop.RevisionID)
		if err != nil {
			return v, err
		}
		v.Revision = &rev
	}
	return v, nil
}

// ProposeInput propone a mano el precio de un producto en una lista.
type ProposeInput struct {
	Company string
	List    string
	Product string
	Price   string
	Note    string
}

// Propose registra una propuesta manual. Exige proponer precios en la
// empresa (un almacenista de sucursal puede); no cambia el precio vigente.
func (s *Service) Propose(ctx context.Context, p identity.Principal, in ProposeInput) (ProposalView, error) {
	var out ProposalView
	err := s.write(ctx, func(r Repository) error {
		c, err := company(ctx, r, p, in.Company)
		if err != nil {
			return err
		}
		if !p.Sees(identity.PermPricePropose, c.Scope()) {
			return fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermPricePropose)
		}
		l, err := list(ctx, r, p.OrganizationID, c, in.List)
		if err != nil {
			return err
		}
		prod, err := appcatalog.Resolve(ctx, r, p.OrganizationID, in.Product)
		if err != nil {
			return err
		}
		price, err := pricing.ParsePrice(in.Price, l.Currency)
		if err != nil {
			return err
		}
		now := s.now()
		// Sin tasa para convertir el costo, la propuesta queda sin
		// referencia: no se puede evaluar si queda bajo el costo.
		ref, _, err := reference(ctx, r, c, l, prod.ID, now)
		if err != nil && !errors.Is(err, exchange.ErrNoRate) {
			return err
		}
		prop, err := pricing.NewProposal(l, prod.ID, pricing.KindManual, price, ref, nil, in.Note, p.UserID, now)
		if err != nil {
			return err
		}
		if prop, err = record(ctx, r, p.OrganizationID, prop, now); err != nil {
			return err
		}
		out, err = proposalView(ctx, r, p.OrganizationID, prop)
		return err
	})
	return out, err
}

// findProposal acepta número (PP-000001) o ID.
func findProposal(ctx context.Context, r Repository, orgID id.ID, ref string, forUpdate bool) (pricing.Proposal, error) {
	ref = strings.TrimSpace(ref)
	if pid, err := id.Parse(ref); err == nil {
		return r.Proposal(ctx, orgID, pid, forUpdate)
	}
	prop, err := r.ProposalByNumber(ctx, orgID, strings.ToUpper(ref))
	if err != nil || !forUpdate {
		return prop, err
	}
	return r.Proposal(ctx, orgID, prop.ID, true)
}

// decidable resuelve una propuesta y exige aprobar precios en su empresa.
func decidable(ctx context.Context, r Repository, p identity.Principal, ref string) (pricing.Proposal, pricing.List, org.Company, error) {
	prop, err := findProposal(ctx, r, p.OrganizationID, ref, true)
	if err != nil {
		return prop, pricing.List{}, org.Company{}, err
	}
	l, err := r.PriceList(ctx, p.OrganizationID, prop.ListID)
	if err != nil {
		return prop, l, org.Company{}, err
	}
	c, err := r.Company(ctx, p.OrganizationID, l.CompanyID)
	if err != nil {
		return prop, l, c, err
	}
	if !p.Sees(identity.PermPriceRead, c.Scope()) && !p.Sees(identity.PermPriceApprove, c.Scope()) {
		return prop, l, c, fmt.Errorf("%w: propuesta %s", apporg.ErrNotFound, ref)
	}
	return prop, l, c, p.Authorize(identity.PermPriceApprove, c.Scope(), "la empresa "+c.Code.String())
}

// ApproveInput aprueba una propuesta. Price vacío aprueba lo propuesto; At
// vacío la hace regir ya, o desde esa hora de Caracas ("AAAA-MM-DD" o
// "AAAA-MM-DD HH:MM").
type ApproveInput struct {
	Price string
	At    string
}

// ParseAt lee una vigencia en hora de Caracas.
func ParseAt(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{"2006-01-02 15:04", timezone.DateLayout} {
		if t, err := time.ParseInLocation(layout, s, timezone.Caracas()); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: vigencia %q (use AAAA-MM-DD o \"AAAA-MM-DD HH:MM\", hora de Caracas)", pricing.ErrInvalid, s)
}

// Approve publica la propuesta como una revisión nueva. Si el precio del
// producto cambió desde la propuesta, devuelve pricing.ErrStale.
func (s *Service) Approve(ctx context.Context, p identity.Principal, ref string, in ApproveInput) (ProposalView, error) {
	at, err := ParseAt(in.At)
	if err != nil {
		return ProposalView{}, err
	}
	var out ProposalView
	err = s.write(ctx, func(r Repository) error {
		prop, l, c, err := decidable(ctx, r, p, ref)
		if err != nil {
			return err
		}
		price := prop.Price
		if strings.TrimSpace(in.Price) != "" {
			if price, err = pricing.ParsePrice(in.Price, l.Currency); err != nil {
				return err
			}
		}
		if prop.BelowCost(price) {
			if err := p.Authorize(identity.PermApproveBelowCost, c.Scope(), "la empresa "+c.Code.String()+
				fmt.Sprintf(" (precio %s bajo el costo de referencia %s %s)", price, prop.Reference.ListCost, l.Currency)); err != nil {
				return err
			}
		}
		now := s.now()
		v, _, err := latest(ctx, r, p.OrganizationID, prop.ListID, prop.ProductID, now)
		if err != nil {
			return err
		}
		prop, rev, err := prop.Approve(price, l.Currency, v, at, p.UserID, now)
		if err != nil {
			return err
		}
		if err := r.InsertRevision(ctx, p.OrganizationID, rev); err != nil {
			return err
		}
		if err := r.UpdateProposal(ctx, p.OrganizationID, prop); err != nil {
			return err
		}
		out, err = proposalView(ctx, r, p.OrganizationID, prop)
		return err
	})
	return out, err
}

// Reject descarta una propuesta; el precio vigente no cambia.
func (s *Service) Reject(ctx context.Context, p identity.Principal, ref, note string) (ProposalView, error) {
	var out ProposalView
	err := s.write(ctx, func(r Repository) error {
		prop, _, _, err := decidable(ctx, r, p, ref)
		if err != nil {
			return err
		}
		if prop, err = prop.Reject(note, p.UserID, s.now()); err != nil {
			return err
		}
		if err := r.UpdateProposal(ctx, p.OrganizationID, prop); err != nil {
			return err
		}
		out, err = proposalView(ctx, r, p.OrganizationID, prop)
		return err
	})
	return out, err
}

// ShowProposal muestra una propuesta.
func (s *Service) ShowProposal(ctx context.Context, p identity.Principal, ref string) (ProposalView, error) {
	var out ProposalView
	err := s.store.InTx(ctx, func(r Repository) error {
		prop, err := findProposal(ctx, r, p.OrganizationID, ref, false)
		if err != nil {
			return err
		}
		l, err := r.PriceList(ctx, p.OrganizationID, prop.ListID)
		if err != nil {
			return err
		}
		c, err := r.Company(ctx, p.OrganizationID, l.CompanyID)
		if err != nil {
			return err
		}
		if !p.Sees(identity.PermPriceRead, c.Scope()) {
			return fmt.Errorf("%w: propuesta %s", apporg.ErrNotFound, ref)
		}
		out, err = proposalView(ctx, r, p.OrganizationID, prop)
		return err
	})
	return out, err
}

// Proposals lista las propuestas de la empresa, opcionalmente de una lista
// y en un estado.
func (s *Service) Proposals(ctx context.Context, p identity.Principal, companyCode, listCode string, status pricing.ProposalStatus) ([]ProposalView, error) {
	var out []ProposalView
	err := s.store.InTx(ctx, func(r Repository) error {
		c, err := company(ctx, r, p, companyCode)
		if err != nil {
			return err
		}
		if err := readable(p, c); err != nil {
			return err
		}
		var lists []pricing.List
		if strings.TrimSpace(listCode) != "" {
			l, err := list(ctx, r, p.OrganizationID, c, listCode)
			if err != nil {
				return err
			}
			lists = []pricing.List{l}
		} else if lists, err = r.PriceLists(ctx, p.OrganizationID, c.ID); err != nil {
			return err
		}
		for _, l := range lists {
			props, err := r.Proposals(ctx, p.OrganizationID, l.ID, status)
			if err != nil {
				return err
			}
			for _, prop := range props {
				v, err := proposalView(ctx, r, p.OrganizationID, prop)
				if err != nil {
					return err
				}
				out = append(out, v)
			}
		}
		return nil
	})
	return out, err
}

// CancelRevision anula una revisión programada que todavía no rige.
func (s *Service) CancelRevision(ctx context.Context, p identity.Principal, revisionID string) (pricing.Revision, error) {
	rid, err := id.Parse(strings.TrimSpace(revisionID))
	if err != nil {
		return pricing.Revision{}, fmt.Errorf("%w: revisión %q", pricing.ErrInvalid, revisionID)
	}
	var out pricing.Revision
	err = s.write(ctx, func(r Repository) error {
		rev, err := r.Revision(ctx, p.OrganizationID, rid)
		if err != nil {
			return err
		}
		l, err := r.PriceList(ctx, p.OrganizationID, rev.ListID)
		if err != nil {
			return err
		}
		c, err := r.Company(ctx, p.OrganizationID, l.CompanyID)
		if err != nil {
			return err
		}
		if err := p.Authorize(identity.PermPriceApprove, c.Scope(), "la empresa "+c.Code.String()); err != nil {
			return err
		}
		if out, err = rev.Cancel(p.UserID, s.now()); err != nil {
			return err
		}
		return r.CancelRevision(ctx, p.OrganizationID, out)
	})
	return out, err
}

// ---------- Precios ----------

// Prices muestra, por lista, el precio vigente, los programados y la
// propuesta pendiente de los productos con precio o propuesta. Con
// product, solo ese producto (aunque no tenga precio).
func (s *Service) Prices(ctx context.Context, p identity.Principal, companyCode, listCode, product string) ([]PriceView, error) {
	var out []PriceView
	err := s.store.InTx(ctx, func(r Repository) error {
		c, err := company(ctx, r, p, companyCode)
		if err != nil {
			return err
		}
		if err := readable(p, c); err != nil {
			return err
		}
		var lists []pricing.List
		if strings.TrimSpace(listCode) != "" {
			l, err := list(ctx, r, p.OrganizationID, c, listCode)
			if err != nil {
				return err
			}
			lists = []pricing.List{l}
		} else if lists, err = r.PriceLists(ctx, p.OrganizationID, c.ID); err != nil {
			return err
		}
		var only id.ID
		if strings.TrimSpace(product) != "" {
			prod, err := appcatalog.Resolve(ctx, r, p.OrganizationID, product)
			if err != nil {
				return err
			}
			only = prod.ID
		}
		now := s.now()
		for _, l := range lists {
			lm, pm, err := margins(ctx, r, p.OrganizationID, l.ID)
			if err != nil {
				return err
			}
			revs, err := r.Revisions(ctx, p.OrganizationID, l.ID, only)
			if err != nil {
				return err
			}
			pending, err := r.Proposals(ctx, p.OrganizationID, l.ID, pricing.ProposalPending)
			if err != nil {
				return err
			}
			byProduct := map[id.ID][]pricing.Revision{}
			var ids []id.ID
			add := func(pid id.ID) {
				if _, ok := byProduct[pid]; !ok {
					byProduct[pid] = nil
					ids = append(ids, pid)
				}
			}
			if !only.IsZero() {
				add(only)
			}
			for _, x := range revs {
				add(x.ProductID)
				byProduct[x.ProductID] = append(byProduct[x.ProductID], x)
			}
			for _, x := range pending {
				if only.IsZero() || x.ProductID == only {
					add(x.ProductID)
				}
			}
			for _, pid := range ids {
				prod, err := r.Product(ctx, p.OrganizationID, pid)
				if err != nil {
					return err
				}
				v := PriceView{List: l, Product: prod, Margin: pricing.TargetMargin(lm, pm[pid])}
				if cur, ok := pricing.Current(byProduct[pid], now); ok {
					v.Current = &cur
				}
				for _, x := range byProduct[pid] {
					if x.Status == pricing.RevisionPublished && x.Scheduled(now) {
						v.Scheduled = append(v.Scheduled, x)
					}
				}
				slices.SortFunc(v.Scheduled, func(a, b pricing.Revision) int { return a.EffectiveAt.Compare(b.EffectiveAt) })
				if i := slices.IndexFunc(pending, func(x pricing.Proposal) bool { return x.ProductID == pid }); i >= 0 {
					v.Pending = &pending[i]
				}
				out = append(out, v)
			}
		}
		slices.SortStableFunc(out, func(a, b PriceView) int {
			if c := strings.Compare(a.List.Code.String(), b.List.Code.String()); c != 0 {
				return c
			}
			return strings.Compare(a.Product.SKU.String(), b.Product.SKU.String())
		})
		return nil
	})
	return out, err
}

// ---------- Sugerencias al recibir ----------

// Skipped explica por qué no se sugirió un precio.
type Skipped struct {
	List    pricing.List
	Product catalog.Product
	Reason  string
}

// SuggestWithin genera, dentro de la transacción de una recepción
// confirmada, una propuesta por lista activa con margen objetivo y producto
// recibido cuyo precio sugerido difiera del vigente. Nunca falla por falta
// de tasa: lo informa en Skipped, porque la recepción no depende de los
// precios (DEC-007.P2).
func SuggestWithin(ctx context.Context, r Repository, actor id.ID, c org.Company, receiptID id.ID, productIDs []id.ID, now time.Time) ([]ProposalView, []Skipped, error) {
	lists, err := r.PriceLists(ctx, c.OrganizationID, c.ID)
	if err != nil {
		return nil, nil, err
	}
	ids := slices.Clone(productIDs)
	slices.SortFunc(ids, func(a, b id.ID) int { return strings.Compare(a.String(), b.String()) })
	ids = slices.Compact(ids)
	var made []ProposalView
	var skipped []Skipped
	for _, l := range lists {
		if l.Status != org.StatusActive {
			continue
		}
		lm, pm, err := margins(ctx, r, c.OrganizationID, l.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, pid := range ids {
			margin := pricing.TargetMargin(lm, pm[pid])
			if margin == nil {
				continue
			}
			prod, err := r.Product(ctx, c.OrganizationID, pid)
			if err != nil {
				return nil, nil, err
			}
			ref, _, err := reference(ctx, r, c, l, pid, now)
			if errors.Is(err, exchange.ErrNoRate) {
				skipped = append(skipped, Skipped{l, prod, err.Error()})
				continue
			}
			if err != nil {
				return nil, nil, err
			}
			if ref == nil {
				skipped = append(skipped, Skipped{l, prod, "sin costo de referencia"})
				continue
			}
			price, err := pricing.Suggest(ref.ListCost, *margin, l.Currency)
			if err != nil {
				return nil, nil, err
			}
			_, cur, err := latest(ctx, r, c.OrganizationID, l.ID, pid, now)
			if err != nil {
				return nil, nil, err
			}
			if cur != nil && cur.Price.Equal(price) {
				continue
			}
			prop, err := pricing.NewProposal(l, pid, pricing.KindSuggested, price, ref, margin, "", actor, now)
			if err != nil {
				return nil, nil, err
			}
			prop.ReceiptID = receiptID
			if prop, err = record(ctx, r, c.OrganizationID, prop, now); err != nil {
				return nil, nil, err
			}
			v, err := proposalView(ctx, r, c.OrganizationID, prop)
			if err != nil {
				return nil, nil, err
			}
			made = append(made, v)
		}
	}
	return made, skipped, nil
}

// SuggestionsOf devuelve las propuestas que generó una recepción.
func SuggestionsOf(ctx context.Context, r Repository, orgID, receiptID id.ID) ([]ProposalView, error) {
	props, err := r.ProposalsOfReceipt(ctx, orgID, receiptID)
	if err != nil {
		return nil, err
	}
	out := make([]ProposalView, 0, len(props))
	for _, prop := range props {
		v, err := proposalView(ctx, r, orgID, prop)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

package stock

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/org"
)

// Existencia negativa por política, reservas, bloqueos y conteos físicos
// (BP-M2-003; DEC-007.F2, secciones 24.1–24.6; ratificado 2026-10-27).

// ---------- Política de existencia negativa ----------

// negativeLimit es el déficit que la política permite para el producto, y
// la versión de política que lo autoriza. Sin permiso, ErrInsufficient.
func negativeLimit(ctx context.Context, r Repository, company org.Company, prod catalog.Product) (decimal.Decimal, id.ID, error) {
	cp, pp, err := policies(ctx, r, company, prod.ID)
	if err != nil {
		return decimal.Decimal{}, id.ID{}, err
	}
	limit, ok := inventory.NegativeLimit(cp.Company, pp.Product)
	if !ok {
		return decimal.Decimal{}, id.ID{}, fmt.Errorf("%w: la política de la empresa %s no permite existencia negativa de %s", inventory.ErrInsufficient, company.Code, prod.SKU)
	}
	change := cp.ID
	if pp.Product.Mode == inventory.NegativeAllow {
		change = pp.ID
	}
	return limit, change, nil
}

// policies lee la política vigente de la empresa y del producto; la
// ausencia equivale a la predeterminada (deshabilitada, inherit).
func policies(ctx context.Context, r Repository, company org.Company, productID id.ID) (cp, pp inventory.PolicyChange, err error) {
	if cp, err = r.CurrentPolicy(ctx, company.OrganizationID, company.ID, id.ID{}); err != nil && !errors.Is(err, apporg.ErrNotFound) {
		return cp, pp, err
	}
	if productID.IsZero() {
		return cp, pp, nil
	}
	if pp, err = r.CurrentPolicy(ctx, company.OrganizationID, company.ID, productID); err != nil && !errors.Is(err, apporg.ErrNotFound) {
		return cp, pp, err
	}
	return cp, pp, nil
}

// ProductPolicyView es la política de un producto y su efecto.
type ProductPolicyView struct {
	inventory.PolicyChange
	Product catalog.Product
	Unit    catalog.Unit
	// Limit es el déficit efectivamente permitido; Allowed indica si lo hay.
	Limit   decimal.Decimal
	Allowed bool
}

// PolicyView es la política de existencia negativa de una empresa.
type PolicyView struct {
	Company  org.Company
	Current  inventory.PolicyChange // empresa; ID cero si nunca se fijó
	Products []ProductPolicyView
}

func (s *Service) company(ctx context.Context, r Repository, p identity.Principal, code string, perm identity.Permission) (org.Company, error) {
	cc, err := org.ParseCode(code)
	if err != nil {
		return org.Company{}, err
	}
	c, err := r.CompanyByCode(ctx, p.OrganizationID, cc)
	if err != nil {
		return c, err
	}
	if !p.Sees(identity.PermOrgRead, c.Scope()) && !p.Sees(perm, c.Scope()) {
		return org.Company{}, fmt.Errorf("%w: empresa %s", apporg.ErrNotFound, cc)
	}
	return c, nil
}

func (s *Service) policyView(ctx context.Context, r Repository, c org.Company, product string) (PolicyView, error) {
	v := PolicyView{Company: c}
	var err error
	if v.Current, _, err = policies(ctx, r, c, id.ID{}); err != nil {
		return v, err
	}
	var list []inventory.PolicyChange
	if strings.TrimSpace(product) != "" {
		prod, err := appcatalog.Resolve(ctx, r, c.OrganizationID, product)
		if err != nil {
			return v, err
		}
		_, pp, err := policies(ctx, r, c, prod.ID)
		if err != nil {
			return v, err
		}
		pp.ProductID = prod.ID
		list = []inventory.PolicyChange{pp}
	} else if list, err = r.ProductPolicies(ctx, c.OrganizationID, c.ID); err != nil {
		return v, err
	}
	for _, pp := range list {
		pv := ProductPolicyView{PolicyChange: pp}
		if pv.Product, err = r.Product(ctx, c.OrganizationID, pp.ProductID); err != nil {
			return v, err
		}
		if pv.Unit, err = r.Unit(ctx, c.OrganizationID, pv.Product.UnitID); err != nil {
			return v, err
		}
		pv.Limit, pv.Allowed = inventory.NegativeLimit(v.Current.Company, pp.Product)
		v.Products = append(v.Products, pv)
	}
	return v, nil
}

// Policy muestra la política de la empresa y la de los productos que tienen
// una propia (o la de product, si se indica).
func (s *Service) Policy(ctx context.Context, p identity.Principal, companyCode, product string) (PolicyView, error) {
	var out PolicyView
	err := s.store.InTx(ctx, func(r Repository) error {
		c, err := s.company(ctx, r, p, companyCode, identity.PermPolicyManage)
		if err != nil {
			return err
		}
		out, err = s.policyView(ctx, r, c, product)
		return err
	})
	return out, err
}

// SetCompanyPolicy habilita o deshabilita la existencia negativa en una
// empresa. Cada cambio queda registrado y no altera movimientos anteriores.
func (s *Service) SetCompanyPolicy(ctx context.Context, p identity.Principal, companyCode string, enabled bool, limit string) (PolicyView, error) {
	cp, err := inventory.NewCompanyPolicy(enabled, limit)
	if err != nil {
		return PolicyView{}, err
	}
	var out PolicyView
	err = s.store.InTx(ctx, func(r Repository) error {
		c, err := s.company(ctx, r, p, companyCode, identity.PermPolicyManage)
		if err != nil {
			return err
		}
		if err := p.Authorize(identity.PermPolicyManage, c.Scope(), "la empresa "+c.Code.String()); err != nil {
			return err
		}
		ch, err := inventory.NewPolicyChange(c.OrganizationID, c.ID, id.ID{}, cp, inventory.ProductPolicy{}, p.UserID, s.now())
		if err != nil {
			return err
		}
		if err := r.InsertPolicyChange(ctx, ch); err != nil {
			return err
		}
		out, err = s.policyView(ctx, r, c, "")
		return err
	})
	return out, err
}

// SetProductPolicy fija el modo de un producto en una empresa.
func (s *Service) SetProductPolicy(ctx context.Context, p identity.Principal, companyCode, product, mode, limit string) (PolicyView, error) {
	var out PolicyView
	err := s.store.InTx(ctx, func(r Repository) error {
		c, err := s.company(ctx, r, p, companyCode, identity.PermPolicyManage)
		if err != nil {
			return err
		}
		if err := p.Authorize(identity.PermPolicyManage, c.Scope(), "la empresa "+c.Code.String()); err != nil {
			return err
		}
		prod, err := appcatalog.Resolve(ctx, r, p.OrganizationID, product)
		if err != nil {
			return err
		}
		unit, err := r.Unit(ctx, p.OrganizationID, prod.UnitID)
		if err != nil {
			return err
		}
		pp, err := inventory.NewProductPolicy(mode, limit, unit.Scale)
		if err != nil {
			return err
		}
		ch, err := inventory.NewPolicyChange(c.OrganizationID, c.ID, prod.ID, inventory.CompanyPolicy{}, pp, p.UserID, s.now())
		if err != nil {
			return err
		}
		if err := r.InsertPolicyChange(ctx, ch); err != nil {
			return err
		}
		out, err = s.policyView(ctx, r, c, prod.ID.String())
		return err
	})
	return out, err
}

// ---------- Vencimiento de reservas ----------

// expireLocked libera las reservas vencidas de un saldo ya bloqueado y
// devuelve cuántas.
func (s *Service) expireLocked(ctx context.Context, r Repository, l *locked) (int, error) {
	now := s.now().UTC()
	due, err := r.DueReservations(ctx, HoldFilter{OrganizationID: l.Scope.Organization(), WarehouseID: l.Scope.Warehouse(), ProductID: l.ProductID}, now)
	if err != nil || len(due) == 0 {
		return 0, err
	}
	b := l.Balance
	for _, v := range due {
		closed, err := v.Close(inventory.HoldExpired, id.ID{}, "", now)
		if err != nil {
			return 0, err
		}
		if b, err = inventory.Commit(b, v.Remaining().Neg(), decimal.Zero()); err != nil {
			return 0, err
		}
		if err := r.UpdateReservation(ctx, closed); err != nil {
			return 0, err
		}
	}
	b.UpdatedAt = now
	if err := r.UpdateCommitments(ctx, b); err != nil {
		return 0, err
	}
	l.Balance = b
	return len(due), nil
}

// expire libera las reservas vencidas del filtro, bloqueando cada saldo
// afectado en orden fijo (las vencidas vienen ordenadas por almacén y
// producto).
func (s *Service) expire(ctx context.Context, r Repository, f HoldFilter) (int, error) {
	due, err := r.DueReservations(ctx, f, s.now().UTC())
	if err != nil {
		return 0, err
	}
	seen := map[inventory.Key]bool{}
	n := 0
	for _, v := range due {
		if seen[v.Key] {
			continue
		}
		seen[v.Key] = true
		b, err := r.BalanceForUpdate(ctx, v.Scope.Organization(), v.Scope.Warehouse(), v.ProductID)
		if err != nil {
			return 0, err
		}
		l := locked{Balance: b}
		k, err := s.expireLocked(ctx, r, &l)
		if err != nil {
			return 0, err
		}
		n += k
	}
	return n, nil
}

// ExpireReservations libera las reservas vencidas de los almacenes donde el
// principal gestiona reservas. Es idempotente: una reserva se libera una
// sola vez aunque el comando se repita o corra en paralelo.
func (s *Service) ExpireReservations(ctx context.Context, p identity.Principal) (int, error) {
	if !p.HasAnywhere(identity.PermReservation) {
		return 0, fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermReservation)
	}
	var n int
	err := s.write(ctx, func(r Repository) error {
		n = 0
		due, err := r.DueReservations(ctx, HoldFilter{OrganizationID: p.OrganizationID}, s.now().UTC())
		if err != nil {
			return err
		}
		done := map[inventory.Key]bool{}
		for _, v := range due {
			if done[v.Key] || !p.Can(identity.PermReservation, v.Scope) {
				continue
			}
			done[v.Key] = true
			k, err := s.expire(ctx, r, HoldFilter{OrganizationID: p.OrganizationID, WarehouseID: v.Scope.Warehouse(), ProductID: v.ProductID})
			if err != nil {
				return err
			}
			n += k
		}
		return nil
	})
	return n, err
}

// ---------- Reservas ----------

// ReserveInput reserva unidades libres para un documento.
type ReserveInput struct {
	Where          WarehouseRef
	Product        string
	Quantity       string
	Reference      string // p. ej. el número de pedido; obligatoria
	Note           string
	ExpiresAt      time.Time // opcional
	IdempotencyKey string
}

// HoldView describe una reserva o un bloqueo con su producto y almacén.
type HoldView struct {
	Product  catalog.Product
	Unit     catalog.Unit
	Location Location
	Replayed bool
}

// ReservationView es una reserva y, si se despachó, el movimiento.
type ReservationView struct {
	inventory.Reservation
	HoldView
	Movement inventory.Movement // despacho
}

// BlockView es un bloqueo y, si se dio de baja, el movimiento de merma.
type BlockView struct {
	inventory.Block
	HoldView
	Movement inventory.Movement
}

func (s *Service) holdView(ctx context.Context, r Repository, k inventory.Key) (HoldView, error) {
	var v HoldView
	var err error
	if v.Location, err = location(ctx, r, k.Scope); err != nil {
		return v, err
	}
	if v.Product, err = r.Product(ctx, k.Scope.Organization(), k.ProductID); err != nil {
		return v, err
	}
	v.Unit, err = r.Unit(ctx, k.Scope.Organization(), v.Product.UnitID)
	return v, err
}

// target resuelve almacén, producto, unidad y cantidad de una operación.
func target(ctx context.Context, r Repository, p identity.Principal, where WarehouseRef, product, quantity string, perm identity.Permission) (Location, catalog.Product, catalog.Unit, decimal.Decimal, error) {
	loc, err := locate(ctx, r, p, where, perm)
	if err != nil {
		return loc, catalog.Product{}, catalog.Unit{}, decimal.Decimal{}, err
	}
	prod, err := appcatalog.Resolve(ctx, r, p.OrganizationID, product)
	if err != nil {
		return loc, prod, catalog.Unit{}, decimal.Decimal{}, err
	}
	unit, err := r.Unit(ctx, p.OrganizationID, prod.UnitID)
	if err != nil {
		return loc, prod, unit, decimal.Decimal{}, err
	}
	qty, err := inventory.ParseQuantity(quantity, unit.Scale)
	return loc, prod, unit, qty, err
}

// Reserve compromete unidades libres: solo lo disponible del almacén, sin
// excepción de negativos (sección 24.4). Existencia y CPP no cambian.
func (s *Service) Reserve(ctx context.Context, p identity.Principal, in ReserveInput) (ReservationView, error) {
	key, err := checkKey(in.IdempotencyKey)
	if err != nil {
		return ReservationView{}, err
	}
	var out ReservationView
	err = s.write(ctx, func(r Repository) error {
		loc, prod, unit, qty, err := target(ctx, r, p, in.Where, in.Product, in.Quantity, identity.PermReservation)
		if err != nil {
			return err
		}
		now := s.now()
		v, err := inventory.NewReservation(inventory.Key{Scope: loc.Warehouse.Scope(), ProductID: prod.ID}, qty, in.Reference, in.Note, in.ExpiresAt, p.UserID, now)
		if err != nil {
			return err
		}
		v.IdempotencyKey = key
		v.RequestHash = digest("reserve", loc.Warehouse.ID.String(), prod.ID.String(), qty.StringFixed(unit.Scale), v.Reference, v.Note, v.ExpiresAt.Format(time.RFC3339))
		if key != "" {
			prior, err := r.ReservationByKey(ctx, p.OrganizationID, key)
			switch {
			case err == nil && prior.RequestHash != v.RequestHash:
				return ErrKeyReused
			case err == nil:
				out = ReservationView{Reservation: prior, HoldView: HoldView{Product: prod, Unit: unit, Location: loc, Replayed: true}}
				return nil
			case !errors.Is(err, apporg.ErrNotFound):
				return err
			}
		}
		if prod.Status != catalog.StatusActive || loc.Warehouse.Status != org.StatusActive {
			return fmt.Errorf("%w: el producto o el almacén están inactivos", inventory.ErrInvalid)
		}
		l, err := s.lock(ctx, r, p.OrganizationID, loc, prod.ID)
		if err != nil {
			return err
		}
		if l.isNew {
			return fmt.Errorf("%w: %s no tiene existencia en %s", inventory.ErrInsufficient, prod.SKU, loc)
		}
		b, err := inventory.Commit(l.Balance, qty, decimal.Zero())
		if err != nil {
			return err
		}
		b.UpdatedAt = now.UTC()
		if err := r.UpdateCommitments(ctx, b); err != nil {
			return err
		}
		if err := r.InsertReservation(ctx, v); err != nil {
			return err
		}
		out = ReservationView{Reservation: v, HoldView: HoldView{Product: prod, Unit: unit, Location: loc}}
		return nil
	})
	return out, err
}

// openReservation carga una reserva, comprueba el permiso en su almacén y
// bloquea el saldo (lo que libera las vencidas). Devuelve la reserva al día.
func (s *Service) openReservation(ctx context.Context, r Repository, p identity.Principal, reservationID string) (inventory.Reservation, HoldView, locked, error) {
	rid, err := id.Parse(strings.TrimSpace(reservationID))
	if err != nil {
		return inventory.Reservation{}, HoldView{}, locked{}, fmt.Errorf("%w: ID de reserva %q", inventory.ErrInvalid, reservationID)
	}
	v, err := r.Reservation(ctx, p.OrganizationID, rid)
	if err != nil {
		return v, HoldView{}, locked{}, err
	}
	hv, err := s.holdView(ctx, r, v.Key)
	if err != nil {
		return v, hv, locked{}, err
	}
	if _, err := locate(ctx, r, p, refOf(hv.Location), identity.PermReservation); err != nil {
		return v, hv, locked{}, err
	}
	l, err := s.lock(ctx, r, p.OrganizationID, hv.Location, v.ProductID)
	if err != nil {
		return v, hv, l, err
	}
	v, err = r.Reservation(ctx, p.OrganizationID, rid)
	return v, hv, l, err
}

func refOf(loc Location) WarehouseRef {
	return WarehouseRef{Company: loc.Company.Code.String(), Branch: loc.Branch.Code.String(), Warehouse: loc.Warehouse.Code.String()}
}

func closedErr(status inventory.HoldStatus, what string) error {
	return fmt.Errorf("%w: %s está %s", apporg.ErrConflict, what, status)
}

// ConsumeReservation despacha unidades reservadas: baja la existencia y lo
// reservado en la misma operación, con una salida valorada al CPP (ST-04).
// Sin quantity despacha todo lo pendiente.
func (s *Service) ConsumeReservation(ctx context.Context, p identity.Principal, reservationID, quantity, note, idempotencyKey string) (ReservationView, error) {
	key, err := checkKey(idempotencyKey)
	if err != nil {
		return ReservationView{}, err
	}
	n, err := inventory.ParseNote(note, inventory.ReasonReservation)
	if err != nil {
		return ReservationView{}, err
	}
	var out ReservationView
	err = s.write(ctx, func(r Repository) error {
		v, hv, l, err := s.openReservation(ctx, r, p, reservationID)
		if err != nil {
			return err
		}
		qty := v.Remaining()
		if strings.TrimSpace(quantity) != "" {
			if qty, err = inventory.ParseQuantity(quantity, hv.Unit.Scale); err != nil {
				return err
			}
		}
		hash := digest("consume", v.ID.String(), qty.StringFixed(hv.Unit.Scale), n)
		if prior, done, err := replayed(ctx, r, p.OrganizationID, key, hash); err != nil || done {
			if done {
				hv.Replayed = true
				out = ReservationView{Reservation: v, HoldView: hv, Movement: prior}
			}
			return err
		}
		if v.Status != inventory.HoldActive {
			return closedErr(v.Status, "la reserva")
		}
		now := s.now()
		consumed, err := v.Consume(qty, p.UserID, now)
		if err != nil {
			return err
		}
		if l.Balance, err = inventory.Commit(l.Balance, qty.Neg(), decimal.Zero()); err != nil {
			return err
		}
		st, err := s.stage(p, l, inventory.Movement{Kind: inventory.KindReservationOut, Reason: inventory.ReasonReservation, Quantity: qty.Neg(),
			Note: n, ReservationID: v.ID, IdempotencyKey: key, RequestHash: hash}, inventory.Costing{})
		if err != nil {
			return err
		}
		if err := st.commit(ctx, r); err != nil {
			return err
		}
		if err := r.UpdateReservation(ctx, consumed); err != nil {
			return err
		}
		out = ReservationView{Reservation: consumed, HoldView: hv, Movement: st.m}
		return nil
	})
	return out, err
}

// ReleaseReservation cancela lo pendiente de una reserva (ST-03): vuelve al
// disponible; existencia y CPP no cambian.
func (s *Service) ReleaseReservation(ctx context.Context, p identity.Principal, reservationID, note string) (ReservationView, error) {
	var out ReservationView
	err := s.write(ctx, func(r Repository) error {
		v, hv, l, err := s.openReservation(ctx, r, p, reservationID)
		if err != nil {
			return err
		}
		if v.Status != inventory.HoldActive {
			return closedErr(v.Status, "la reserva")
		}
		now := s.now()
		closed, err := v.Close(inventory.HoldReleased, p.UserID, note, now)
		if err != nil {
			return err
		}
		b, err := inventory.Commit(l.Balance, v.Remaining().Neg(), decimal.Zero())
		if err != nil {
			return err
		}
		b.UpdatedAt = now.UTC()
		if err := r.UpdateCommitments(ctx, b); err != nil {
			return err
		}
		if err := r.UpdateReservation(ctx, closed); err != nil {
			return err
		}
		out = ReservationView{Reservation: closed, HoldView: hv}
		return nil
	})
	return out, err
}

// HoldQuery filtra reservas o bloqueos visibles.
type HoldQuery struct {
	Where   *WarehouseRef
	Product string
	Status  inventory.HoldStatus
}

func (s *Service) holdFilter(ctx context.Context, r Repository, p identity.Principal, q HoldQuery) (HoldFilter, error) {
	f := HoldFilter{OrganizationID: p.OrganizationID, Status: q.Status}
	if q.Where != nil {
		loc, err := locate(ctx, r, p, *q.Where, identity.PermStockRead)
		if err != nil {
			return f, err
		}
		f.WarehouseID = loc.Warehouse.ID
	}
	if strings.TrimSpace(q.Product) != "" {
		prod, err := appcatalog.Resolve(ctx, r, p.OrganizationID, q.Product)
		if err != nil {
			return f, err
		}
		f.ProductID = prod.ID
	}
	return f, nil
}

// Reservations lista las reservas de los almacenes donde el principal ve
// existencias, tras liberar las vencidas.
func (s *Service) Reservations(ctx context.Context, p identity.Principal, q HoldQuery) ([]ReservationView, error) {
	if !p.HasAnywhere(identity.PermStockRead) {
		return nil, fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermStockRead)
	}
	var out []ReservationView
	err := s.write(ctx, func(r Repository) error {
		out = nil
		f, err := s.holdFilter(ctx, r, p, q)
		if err != nil {
			return err
		}
		if _, err := s.expire(ctx, r, HoldFilter{OrganizationID: f.OrganizationID, WarehouseID: f.WarehouseID, ProductID: f.ProductID}); err != nil {
			return err
		}
		list, err := r.Reservations(ctx, f)
		if err != nil {
			return err
		}
		for _, v := range list {
			if !p.Can(identity.PermStockRead, v.Scope) {
				continue
			}
			hv, err := s.holdView(ctx, r, v.Key)
			if err != nil {
				return err
			}
			out = append(out, ReservationView{Reservation: v, HoldView: hv})
		}
		return nil
	})
	return out, err
}

// ---------- Bloqueos ----------

// BlockInput retiene unidades libres del disponible.
type BlockInput struct {
	Where          WarehouseRef
	Product        string
	Quantity       string
	Reason, Note   string
	IdempotencyKey string
}

// Block retiene mercancía no vendible (ST-07): baja el disponible sin salida
// física; existencia y CPP no cambian.
func (s *Service) Block(ctx context.Context, p identity.Principal, in BlockInput) (BlockView, error) {
	reason, err := inventory.ParseBlockReason(in.Reason)
	if err != nil {
		return BlockView{}, err
	}
	key, err := checkKey(in.IdempotencyKey)
	if err != nil {
		return BlockView{}, err
	}
	var out BlockView
	err = s.write(ctx, func(r Repository) error {
		loc, prod, unit, qty, err := target(ctx, r, p, in.Where, in.Product, in.Quantity, identity.PermBlock)
		if err != nil {
			return err
		}
		now := s.now()
		v, err := inventory.NewBlock(inventory.Key{Scope: loc.Warehouse.Scope(), ProductID: prod.ID}, qty, reason, in.Note, p.UserID, now)
		if err != nil {
			return err
		}
		v.IdempotencyKey = key
		v.RequestHash = digest("block", loc.Warehouse.ID.String(), prod.ID.String(), qty.StringFixed(unit.Scale), string(reason), v.Note)
		if key != "" {
			prior, err := r.BlockByKey(ctx, p.OrganizationID, key)
			switch {
			case err == nil && prior.RequestHash != v.RequestHash:
				return ErrKeyReused
			case err == nil:
				out = BlockView{Block: prior, HoldView: HoldView{Product: prod, Unit: unit, Location: loc, Replayed: true}}
				return nil
			case !errors.Is(err, apporg.ErrNotFound):
				return err
			}
		}
		l, err := s.lock(ctx, r, p.OrganizationID, loc, prod.ID)
		if err != nil {
			return err
		}
		if l.isNew {
			return fmt.Errorf("%w: %s no tiene existencia en %s", inventory.ErrInsufficient, prod.SKU, loc)
		}
		b, err := inventory.Commit(l.Balance, decimal.Zero(), qty)
		if err != nil {
			return err
		}
		b.UpdatedAt = now.UTC()
		if err := r.UpdateCommitments(ctx, b); err != nil {
			return err
		}
		if err := r.InsertBlock(ctx, v); err != nil {
			return err
		}
		out = BlockView{Block: v, HoldView: HoldView{Product: prod, Unit: unit, Location: loc}}
		return nil
	})
	return out, err
}

// CloseBlock libera un bloqueo (ST-08) o, con writeOff, lo da de baja como
// merma: una salida valorada al CPP que exige además inventory.stock.adjust.
func (s *Service) CloseBlock(ctx context.Context, p identity.Principal, blockID string, writeOff bool, note string) (BlockView, error) {
	bid, err := id.Parse(strings.TrimSpace(blockID))
	if err != nil {
		return BlockView{}, fmt.Errorf("%w: ID de bloqueo %q", inventory.ErrInvalid, blockID)
	}
	var out BlockView
	err = s.write(ctx, func(r Repository) error {
		v, err := r.Block(ctx, p.OrganizationID, bid)
		if err != nil {
			return err
		}
		hv, err := s.holdView(ctx, r, v.Key)
		if err != nil {
			return err
		}
		if _, err := locate(ctx, r, p, refOf(hv.Location), identity.PermBlock); err != nil {
			return err
		}
		if writeOff {
			if err := p.Authorize(identity.PermStockAdjust, hv.Location.Warehouse.Scope(), "el almacén "+hv.Location.String()); err != nil {
				return err
			}
		}
		l, err := s.lock(ctx, r, p.OrganizationID, hv.Location, v.ProductID)
		if err != nil {
			return err
		}
		if v, err = r.Block(ctx, p.OrganizationID, bid); err != nil {
			return err
		}
		if v.Status != inventory.HoldActive {
			return closedErr(v.Status, "el bloqueo")
		}
		status := inventory.HoldReleased
		if writeOff {
			status = inventory.HoldWrittenOff
		}
		now := s.now()
		closed, err := v.Close(status, p.UserID, note, now)
		if err != nil {
			return err
		}
		if l.Balance, err = inventory.Commit(l.Balance, decimal.Zero(), v.Quantity.Neg()); err != nil {
			return err
		}
		out = BlockView{Block: closed, HoldView: hv}
		if writeOff {
			st, err := s.stage(p, l, inventory.Movement{Kind: inventory.KindAdjustmentOut, Reason: inventory.ReasonShrinkage, Quantity: v.Quantity.Neg(),
				Note: closed.CloseNote, BlockID: v.ID, RequestHash: digest("writeoff", v.ID.String())}, inventory.Costing{})
			if err != nil {
				return err
			}
			if err := st.commit(ctx, r); err != nil {
				return err
			}
			out.Movement = st.m
		} else {
			l.UpdatedAt = now.UTC()
			if err := r.UpdateCommitments(ctx, l.Balance); err != nil {
				return err
			}
		}
		return r.UpdateBlock(ctx, closed)
	})
	return out, err
}

// Blocks lista los bloqueos de los almacenes donde el principal ve
// existencias.
func (s *Service) Blocks(ctx context.Context, p identity.Principal, q HoldQuery) ([]BlockView, error) {
	if !p.HasAnywhere(identity.PermStockRead) {
		return nil, fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermStockRead)
	}
	var out []BlockView
	err := s.store.InTx(ctx, func(r Repository) error {
		f, err := s.holdFilter(ctx, r, p, q)
		if err != nil {
			return err
		}
		list, err := r.Blocks(ctx, f)
		if err != nil {
			return err
		}
		for _, v := range list {
			if !p.Can(identity.PermStockRead, v.Scope) {
				continue
			}
			hv, err := s.holdView(ctx, r, v.Key)
			if err != nil {
				return err
			}
			out = append(out, BlockView{Block: v, HoldView: hv})
		}
		return nil
	})
	return out, err
}

// ---------- Conteos físicos ----------

// CountLineView es una línea con su producto.
type CountLineView struct {
	inventory.CountLine
	Product catalog.Product
	Unit    catalog.Unit
}

// CountView es un conteo con sus líneas y, si se aplicó, los ajustes.
type CountView struct {
	inventory.Count
	Location  Location
	Lines     []CountLineView
	Movements []inventory.Movement
}

func (s *Service) countView(ctx context.Context, r Repository, c inventory.Count) (CountView, error) {
	v := CountView{Count: c}
	var err error
	if v.Location, err = location(ctx, r, c.Key.Scope); err != nil {
		return v, err
	}
	lines, err := r.CountLines(ctx, c.Key.Scope.Organization(), c.ID)
	if err != nil {
		return v, err
	}
	for _, l := range lines {
		lv := CountLineView{CountLine: l}
		if lv.Product, err = r.Product(ctx, c.Key.Scope.Organization(), l.ProductID); err != nil {
			return v, err
		}
		if lv.Unit, err = r.Unit(ctx, c.Key.Scope.Organization(), lv.Product.UnitID); err != nil {
			return v, err
		}
		v.Lines = append(v.Lines, lv)
	}
	return v, nil
}

// CreateCount abre un conteo en borrador para un almacén.
func (s *Service) CreateCount(ctx context.Context, p identity.Principal, where WarehouseRef, note string) (CountView, error) {
	var out CountView
	err := s.store.InTx(ctx, func(r Repository) error {
		loc, err := locate(ctx, r, p, where, identity.PermCountManage)
		if err != nil {
			return err
		}
		c, err := inventory.NewCount(inventory.Key{Scope: loc.Warehouse.Scope()}, note, p.UserID, s.now())
		if err != nil {
			return err
		}
		if err := r.InsertCount(ctx, c); err != nil {
			return err
		}
		out = CountView{Count: c, Location: loc}
		return nil
	})
	return out, err
}

// openCount carga un conteo y exige perm en su almacén.
func (s *Service) openCount(ctx context.Context, r Repository, p identity.Principal, countID string, perm identity.Permission) (inventory.Count, Location, error) {
	cid, err := id.Parse(strings.TrimSpace(countID))
	if err != nil {
		return inventory.Count{}, Location{}, fmt.Errorf("%w: ID de conteo %q", inventory.ErrInvalid, countID)
	}
	c, err := r.Count(ctx, p.OrganizationID, cid)
	if err != nil {
		return c, Location{}, err
	}
	loc, err := location(ctx, r, c.Key.Scope)
	if err != nil {
		return c, loc, err
	}
	loc, err = locate(ctx, r, p, refOf(loc), perm)
	return c, loc, err
}

// RecordCount guarda lo contado de un producto y la existencia registrada
// en ese momento; repetirlo reemplaza la captura mientras el conteo sea un
// borrador. unitCost es opcional: el costo de lo que sobre si el saldo no
// tiene CPP vigente ni último costo.
func (s *Service) RecordCount(ctx context.Context, p identity.Principal, countID, product, counted, unitCost string) (CountLineView, error) {
	var uc inventory.Amount
	if strings.TrimSpace(unitCost) != "" {
		v, err := inventory.ParseUnitCost(unitCost)
		if err != nil {
			return CountLineView{}, err
		}
		uc = inventory.Known(v)
	}
	var out CountLineView
	err := s.store.InTx(ctx, func(r Repository) error {
		c, loc, err := s.openCount(ctx, r, p, countID, identity.PermCountManage)
		if err != nil {
			return err
		}
		prod, err := appcatalog.Resolve(ctx, r, p.OrganizationID, product)
		if err != nil {
			return err
		}
		unit, err := r.Unit(ctx, p.OrganizationID, prod.UnitID)
		if err != nil {
			return err
		}
		b, err := r.Balance(ctx, p.OrganizationID, loc.Warehouse.ID, prod.ID)
		if errors.Is(err, apporg.ErrNotFound) {
			b = inventory.Balance{Key: inventory.Key{Scope: loc.Warehouse.Scope(), ProductID: prod.ID}}
		} else if err != nil {
			return err
		}
		line, err := inventory.NewCountLine(c, b, counted, unit.Scale, uc, p.UserID, s.now())
		if err != nil {
			return err
		}
		if err := r.SaveCountLine(ctx, p.OrganizationID, line); err != nil {
			return err
		}
		out = CountLineView{CountLine: line, Product: prod, Unit: unit}
		return nil
	})
	return out, err
}

// SubmitCount pasa el conteo a revisión.
func (s *Service) SubmitCount(ctx context.Context, p identity.Principal, countID string) (CountView, error) {
	var out CountView
	err := s.store.InTx(ctx, func(r Repository) error {
		c, _, err := s.openCount(ctx, r, p, countID, identity.PermCountManage)
		if err != nil {
			return err
		}
		lines, err := r.CountLines(ctx, p.OrganizationID, c.ID)
		if err != nil {
			return err
		}
		prev := c.Status
		if c, err = c.Submit(len(lines), p.UserID, s.now()); err != nil {
			return err
		}
		if err := r.UpdateCount(ctx, c, prev); err != nil {
			return err
		}
		out, err = s.countView(ctx, r, c)
		return err
	})
	return out, err
}

// DecideCount aprueba o rechaza un conteo en revisión. Aprobar aplica en la
// misma transacción un ajuste por producto con la diferencia contado −
// registrado al capturar, sobre el saldo actual: los movimientos posteriores
// a la captura se respetan y el kardex anterior no se toca (CT-01, CT-02).
// Aprueba un usuario distinto de quien contó.
func (s *Service) DecideCount(ctx context.Context, p identity.Principal, countID string, approve bool, note string) (CountView, error) {
	var out CountView
	err := s.write(ctx, func(r Repository) error {
		c, loc, err := s.openCount(ctx, r, p, countID, identity.PermCountApprove)
		if err != nil {
			return err
		}
		lines, err := r.CountLines(ctx, p.OrganizationID, c.ID)
		if err != nil {
			return err
		}
		prev := c.Status
		if c, err = c.Decide(approve, lines, p.UserID, note, s.now()); err != nil {
			return err
		}
		var movements []inventory.Movement
		if approve {
			slices.SortFunc(lines, func(a, b inventory.CountLine) int { return strings.Compare(a.ProductID.String(), b.ProductID.String()) })
			for _, line := range lines {
				diff := line.Difference()
				if diff.IsZero() {
					continue
				}
				l, err := s.lock(ctx, r, p.OrganizationID, loc, line.ProductID)
				if err != nil {
					return err
				}
				m := inventory.Movement{Kind: inventory.KindCountIn, Reason: inventory.ReasonCount, Quantity: diff, Note: c.DecisionNote,
					CountID: c.ID, RequestHash: digest("count", c.ID.String(), line.ProductID.String())}
				costing := inventory.Costing{Correction: true}
				if diff.IsNegative() {
					m.Kind = inventory.KindCountOut
				} else if line.UnitCost.Known {
					costing.UnitCost = line.UnitCost
				} else if !l.UnitCost().Known && l.LastUnitCost.Known {
					// Sin CPP vigente, lo que sobra entra al último costo válido.
					costing.UnitCost = l.LastUnitCost
				}
				st, err := s.stage(p, l, m, costing)
				if err != nil {
					prod, perr := r.Product(ctx, p.OrganizationID, line.ProductID)
					if perr == nil && errors.Is(err, inventory.ErrCostRequired) {
						return fmt.Errorf("%w (producto %s: capture el conteo con costo unitario)", err, prod.SKU)
					}
					return err
				}
				if err := st.commit(ctx, r); err != nil {
					return err
				}
				movements = append(movements, st.m)
			}
		}
		if err := r.UpdateCount(ctx, c, prev); err != nil {
			return err
		}
		if out, err = s.countView(ctx, r, c); err != nil {
			return err
		}
		out.Movements = movements
		return nil
	})
	return out, err
}

// ShowCount devuelve un conteo con sus líneas.
func (s *Service) ShowCount(ctx context.Context, p identity.Principal, countID string) (CountView, error) {
	var out CountView
	err := s.store.InTx(ctx, func(r Repository) error {
		c, _, err := s.openCount(ctx, r, p, countID, identity.PermStockRead)
		if err != nil {
			return err
		}
		out, err = s.countView(ctx, r, c)
		return err
	})
	return out, err
}

// Counts lista los conteos visibles, opcionalmente de un almacén y estado.
func (s *Service) Counts(ctx context.Context, p identity.Principal, where *WarehouseRef, status inventory.CountStatus) ([]CountView, error) {
	if !p.HasAnywhere(identity.PermStockRead) {
		return nil, fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermStockRead)
	}
	var out []CountView
	err := s.store.InTx(ctx, func(r Repository) error {
		var warehouseID id.ID
		if where != nil {
			loc, err := locate(ctx, r, p, *where, identity.PermStockRead)
			if err != nil {
				return err
			}
			warehouseID = loc.Warehouse.ID
		}
		list, err := r.Counts(ctx, p.OrganizationID, warehouseID, status)
		if err != nil {
			return err
		}
		for _, c := range list {
			if !p.Can(identity.PermStockRead, c.Key.Scope) {
				continue
			}
			v, err := s.countView(ctx, r, c)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

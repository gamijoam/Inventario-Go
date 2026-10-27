// Package stock contiene los casos de uso de existencias y kardex
// (BP-M2-001): ajustes de entrada y salida, reversos, consulta de saldos y
// kardex, y verificación de que cada saldo coincide con su kardex.
//
// Cada escritura ocurre en una transacción que lee el saldo, aplica el
// movimiento y actualiza el saldo solo si nadie lo cambió entretanto
// (versión). Si otro proceso ganó la carrera, la operación se repite desde
// cero con el saldo nuevo: así dos cajas nunca sacan la misma unidad.
package stock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

var (
	// ErrConcurrent lo devuelve el repositorio cuando otro proceso cambió el
	// saldo; el servicio reintenta.
	ErrConcurrent = errors.New("el saldo cambió durante la operación")
	// ErrBusy: se agotaron los reintentos por concurrencia.
	ErrBusy = fmt.Errorf("%w: el saldo cambia demasiado rápido; reintente", apporg.ErrConflict)
	// ErrKeyReused: la misma clave de idempotencia con datos distintos.
	ErrKeyReused = fmt.Errorf("%w: la clave de idempotencia ya se usó con otros datos", apporg.ErrConflict)
	// ErrAlreadyReversed: cada movimiento se revierte a lo sumo una vez.
	ErrAlreadyReversed = fmt.Errorf("%w: el movimiento ya fue revertido", apporg.ErrConflict)
)

// Repository amplía organización y catálogo con existencias; todo comparte
// la transacción.
type Repository interface {
	apporg.Repository
	appcatalog.Repository

	// Balance devuelve apporg.ErrNotFound si el producto nunca tuvo
	// movimientos en el almacén.
	Balance(ctx context.Context, orgID, warehouseID, productID id.ID) (inventory.Balance, error)
	// BalanceForUpdate es Balance reteniendo la fila hasta el fin de la
	// transacción, para que las escrituras concurrentes esperen su turno.
	BalanceForUpdate(ctx context.Context, orgID, warehouseID, productID id.ID) (inventory.Balance, error)
	// InsertBalance y UpdateBalance devuelven ErrConcurrent si otro proceso
	// creó el saldo o cambió su versión.
	InsertBalance(ctx context.Context, b inventory.Balance) error
	UpdateBalance(ctx context.Context, b inventory.Balance, previousVersion int64) error
	// Balances filtra por los IDs no nulos.
	Balances(ctx context.Context, orgID, warehouseID, productID id.ID) ([]inventory.Balance, error)

	// InsertMovement devuelve ErrConcurrent si la clave de idempotencia o el
	// reverso ya existen (otro proceso los insertó primero).
	InsertMovement(ctx context.Context, m inventory.Movement) error
	Movement(ctx context.Context, orgID, movementID id.ID) (inventory.Movement, error)
	MovementByKey(ctx context.Context, orgID id.ID, idempotencyKey string) (inventory.Movement, error)
	ReversalOf(ctx context.Context, orgID, movementID id.ID) (inventory.Movement, error)
	// Movements en orden de secuencia; limit 0 trae todos.
	Movements(ctx context.Context, orgID, warehouseID, productID id.ID, limit, offset int) ([]inventory.Movement, error)
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

// maxAttempts acota los reintentos por concurrencia.
const maxAttempts = 8

// write ejecuta fn en una transacción y la repite si hubo concurrencia.
func (s *Service) write(ctx context.Context, fn func(Repository) error) error {
	for range maxAttempts {
		err := s.store.InTx(ctx, fn)
		if !errors.Is(err, ErrConcurrent) {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return ErrBusy
}

// WarehouseRef identifica un almacén por códigos. Sin Warehouse se usa el
// único almacén activo de la sucursal.
type WarehouseRef struct {
	Company, Branch, Warehouse string
}

// Location es un almacén con los códigos de su ruta.
type Location struct {
	Company   org.Company
	Branch    org.Branch
	Warehouse org.Warehouse
}

func (l Location) String() string {
	return l.Company.Code.String() + "/" + l.Branch.Code.String() + "/" + l.Warehouse.Code.String()
}

// locate resuelve un almacén. Si el principal no lo ve, responde como si no
// existiera; si lo ve pero le falta perm, deniega.
func locate(ctx context.Context, r Repository, p identity.Principal, ref WarehouseRef, perm identity.Permission) (Location, error) {
	var loc Location
	code := func(s, what string) (org.Code, error) {
		if strings.TrimSpace(s) == "" {
			return org.Code{}, fmt.Errorf("%w: falta el código de %s", inventory.ErrInvalid, what)
		}
		return org.ParseCode(s)
	}
	cc, err := code(ref.Company, "empresa")
	if err != nil {
		return loc, err
	}
	bc, err := code(ref.Branch, "sucursal")
	if err != nil {
		return loc, err
	}
	if loc.Company, err = r.CompanyByCode(ctx, p.OrganizationID, cc); err != nil {
		return loc, err
	}
	if loc.Branch, err = r.BranchByCode(ctx, p.OrganizationID, loc.Company.ID, bc); err != nil {
		return loc, err
	}
	list, err := r.Warehouses(ctx, p.OrganizationID, loc.Company.ID, loc.Branch.ID)
	if err != nil {
		return loc, err
	}
	if strings.TrimSpace(ref.Warehouse) == "" {
		list = slices.DeleteFunc(list, func(w org.Warehouse) bool { return w.Status != org.StatusActive })
		if len(list) != 1 {
			return loc, fmt.Errorf("%w: la sucursal %s tiene %d almacenes activos; indique --warehouse", inventory.ErrInvalid, bc, len(list))
		}
		loc.Warehouse = list[0]
	} else {
		wc, err := org.ParseCode(ref.Warehouse)
		if err != nil {
			return loc, err
		}
		i := slices.IndexFunc(list, func(w org.Warehouse) bool { return w.Code == wc })
		if i < 0 {
			return loc, fmt.Errorf("%w: almacén %s/%s/%s", apporg.ErrNotFound, cc, bc, wc)
		}
		loc.Warehouse = list[i]
	}
	ws := loc.Warehouse.Scope()
	if !p.Can(perm, ws) && !p.Sees(identity.PermOrgRead, ws) {
		return Location{}, fmt.Errorf("%w: almacén %s", apporg.ErrNotFound, loc)
	}
	if err := p.Authorize(perm, ws, "el almacén "+loc.String()); err != nil {
		return Location{}, err
	}
	return loc, nil
}

// MovementView es un movimiento con su producto, unidad y almacén.
type MovementView struct {
	inventory.Movement
	Product  catalog.Product
	Unit     catalog.Unit
	Location Location
	// Replayed indica que la clave de idempotencia ya existía y se devolvió
	// el movimiento original sin aplicar nada.
	Replayed bool
}

// AdjustInput es un ajuste manual.
type AdjustInput struct {
	Where          WarehouseRef
	Product        string // SKU, código de barras o ID
	Direction      inventory.Direction
	Quantity       string
	Reason, Note   string
	IdempotencyKey string
}

// MaxKeyLength limita la clave de idempotencia.
const MaxKeyLength = 100

func checkKey(k string) (string, error) {
	k = strings.TrimSpace(k)
	if len(k) > MaxKeyLength || strings.ContainsFunc(k, func(r rune) bool { return r < 0x21 || r > 0x7e }) {
		return "", fmt.Errorf("%w: la clave de idempotencia admite hasta %d caracteres ASCII visibles", inventory.ErrInvalid, MaxKeyLength)
	}
	return k, nil
}

func digest(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(h[:])
}

// Adjust registra una entrada o salida manual con motivo.
func (s *Service) Adjust(ctx context.Context, p identity.Principal, in AdjustInput) (MovementView, error) {
	if in.Direction != inventory.In && in.Direction != inventory.Out {
		return MovementView{}, fmt.Errorf("%w: dirección", inventory.ErrInvalid)
	}
	reason, err := inventory.ParseReason(in.Reason, in.Direction)
	if err != nil {
		return MovementView{}, err
	}
	note, err := inventory.ParseNote(in.Note, reason)
	if err != nil {
		return MovementView{}, err
	}
	key, err := checkKey(in.IdempotencyKey)
	if err != nil {
		return MovementView{}, err
	}
	var out MovementView
	err = s.write(ctx, func(r Repository) error {
		loc, err := locate(ctx, r, p, in.Where, identity.PermStockAdjust)
		if err != nil {
			return err
		}
		prod, err := appcatalog.Resolve(ctx, r, p.OrganizationID, in.Product)
		if err != nil {
			return err
		}
		unit, err := r.Unit(ctx, p.OrganizationID, prod.UnitID)
		if err != nil {
			return err
		}
		qty, err := inventory.ParseQuantity(in.Quantity, unit.Scale)
		if err != nil {
			return err
		}
		if in.Direction == inventory.Out {
			qty = qty.Neg()
		}
		hash := digest("adjust", loc.Warehouse.ID.String(), prod.ID.String(), qty.StringFixed(unit.Scale), string(reason), note)
		if prior, done, err := replayed(ctx, r, p.OrganizationID, key, hash); err != nil || done {
			if done {
				out, err = s.view(ctx, r, prior)
				out.Replayed = true
			}
			return err
		}
		if prod.Status != catalog.StatusActive {
			return fmt.Errorf("%w: el producto %s está inactivo", inventory.ErrInvalid, prod.SKU)
		}
		if loc.Warehouse.Status != org.StatusActive {
			return fmt.Errorf("%w: el almacén %s está inactivo", inventory.ErrInvalid, loc)
		}
		kind := inventory.KindAdjustmentIn
		if in.Direction == inventory.Out {
			kind = inventory.KindAdjustmentOut
		}
		m := inventory.Movement{Kind: kind, Reason: reason, Quantity: qty, Note: note, IdempotencyKey: key, RequestHash: hash}
		m, err = s.apply(ctx, r, p, loc, prod.ID, m)
		if err != nil {
			return err
		}
		out = MovementView{Movement: m, Product: prod, Unit: unit, Location: loc}
		return nil
	})
	return out, err
}

// replayed busca un movimiento anterior con la misma clave.
func replayed(ctx context.Context, r Repository, orgID id.ID, key, hash string) (inventory.Movement, bool, error) {
	if key == "" {
		return inventory.Movement{}, false, nil
	}
	prior, err := r.MovementByKey(ctx, orgID, key)
	if errors.Is(err, apporg.ErrNotFound) {
		return inventory.Movement{}, false, nil
	}
	if err != nil {
		return inventory.Movement{}, false, err
	}
	if prior.RequestHash != hash {
		return inventory.Movement{}, false, ErrKeyReused
	}
	return prior, true, nil
}

// apply lee el saldo, aplica el movimiento y guarda ambos.
func (s *Service) apply(ctx context.Context, r Repository, p identity.Principal, loc Location, productID id.ID, m inventory.Movement) (inventory.Movement, error) {
	now := s.now().UTC()
	key := inventory.Key{Scope: loc.Warehouse.Scope(), ProductID: productID}
	b, err := r.BalanceForUpdate(ctx, p.OrganizationID, loc.Warehouse.ID, productID)
	isNew := errors.Is(err, apporg.ErrNotFound)
	if isNew {
		b = inventory.Balance{Key: key}
	} else if err != nil {
		return inventory.Movement{}, err
	}
	mid, err := id.New()
	if err != nil {
		return inventory.Movement{}, err
	}
	m.ID, m.Key, m.ActorID, m.OccurredAt = mid, key, p.UserID, now
	prev := b.Version
	b, m, err = inventory.Apply(b, m, now)
	if err != nil {
		return inventory.Movement{}, err
	}
	if isNew {
		err = r.InsertBalance(ctx, b)
	} else {
		err = r.UpdateBalance(ctx, b, prev)
	}
	if err != nil {
		return inventory.Movement{}, err
	}
	return m, r.InsertMovement(ctx, m)
}

// Reverse anula el efecto de un movimiento con otro de signo contrario. El
// original queda intacto en el kardex.
func (s *Service) Reverse(ctx context.Context, p identity.Principal, movementID, note, idempotencyKey string) (MovementView, error) {
	mid, err := id.Parse(strings.TrimSpace(movementID))
	if err != nil {
		return MovementView{}, fmt.Errorf("%w: ID de movimiento %q", inventory.ErrInvalid, movementID)
	}
	n, err := inventory.ParseNote(note, inventory.ReasonReversal)
	if err != nil {
		return MovementView{}, err
	}
	key, err := checkKey(idempotencyKey)
	if err != nil {
		return MovementView{}, err
	}
	var out MovementView
	err = s.write(ctx, func(r Repository) error {
		orig, err := r.Movement(ctx, p.OrganizationID, mid)
		if err != nil {
			return err
		}
		v, err := s.view(ctx, r, orig)
		if err != nil {
			return err
		}
		ref := WarehouseRef{Company: v.Location.Company.Code.String(), Branch: v.Location.Branch.Code.String(), Warehouse: v.Location.Warehouse.Code.String()}
		loc, err := locate(ctx, r, p, ref, identity.PermStockAdjust)
		if err != nil {
			return err
		}
		hash := digest("reverse", orig.ID.String(), n)
		if prior, done, err := replayed(ctx, r, p.OrganizationID, key, hash); err != nil || done {
			if done {
				out, err = s.view(ctx, r, prior)
				out.Replayed = true
			}
			return err
		}
		if orig.Kind == inventory.KindReversal {
			return fmt.Errorf("%w: un reverso no se revierte; registre un ajuste nuevo", inventory.ErrInvalid)
		}
		if _, err := r.ReversalOf(ctx, p.OrganizationID, orig.ID); err == nil {
			return ErrAlreadyReversed
		} else if !errors.Is(err, apporg.ErrNotFound) {
			return err
		}
		m := inventory.Movement{Kind: inventory.KindReversal, Reason: inventory.ReasonReversal, Quantity: orig.Quantity.Neg(),
			Note: n, ReversesID: orig.ID, IdempotencyKey: key, RequestHash: hash}
		if m, err = s.apply(ctx, r, p, loc, orig.ProductID, m); err != nil {
			return err
		}
		out = MovementView{Movement: m, Product: v.Product, Unit: v.Unit, Location: loc}
		return nil
	})
	return out, err
}

func (s *Service) view(ctx context.Context, r Repository, m inventory.Movement) (MovementView, error) {
	v := MovementView{Movement: m}
	loc, err := location(ctx, r, m.Scope)
	if err != nil {
		return v, err
	}
	v.Location = loc
	if v.Product, err = r.Product(ctx, m.Scope.Organization(), m.ProductID); err != nil {
		return v, err
	}
	v.Unit, err = r.Unit(ctx, m.Scope.Organization(), v.Product.UnitID)
	return v, err
}

func location(ctx context.Context, r Repository, sc scope.BusinessScope) (Location, error) {
	var loc Location
	var err error
	orgID := sc.Organization()
	if loc.Company, err = r.Company(ctx, orgID, sc.Company()); err != nil {
		return loc, err
	}
	if loc.Branch, err = r.Branch(ctx, orgID, sc.Company(), sc.Branch()); err != nil {
		return loc, err
	}
	list, err := r.Warehouses(ctx, orgID, sc.Company(), sc.Branch())
	if err != nil {
		return loc, err
	}
	i := slices.IndexFunc(list, func(w org.Warehouse) bool { return w.ID == sc.Warehouse() })
	if i < 0 {
		return loc, fmt.Errorf("%w: almacén", apporg.ErrNotFound)
	}
	loc.Warehouse = list[i]
	return loc, nil
}

// BalanceView es un saldo con su producto, unidad y almacén.
type BalanceView struct {
	inventory.Balance
	Product  catalog.Product
	Unit     catalog.Unit
	Location Location
}

func (s *Service) balanceView(ctx context.Context, r Repository, b inventory.Balance) (BalanceView, error) {
	v := BalanceView{Balance: b}
	var err error
	if v.Location, err = location(ctx, r, b.Scope); err != nil {
		return v, err
	}
	if v.Product, err = r.Product(ctx, b.Scope.Organization(), b.ProductID); err != nil {
		return v, err
	}
	v.Unit, err = r.Unit(ctx, b.Scope.Organization(), v.Product.UnitID)
	return v, err
}

// StockFilter limita la consulta de existencias; vacío trae todo lo visible.
type StockFilter struct {
	Where   *WarehouseRef
	Product string
}

// Stock devuelve los saldos de los almacenes donde el principal puede ver
// existencias.
func (s *Service) Stock(ctx context.Context, p identity.Principal, f StockFilter) ([]BalanceView, error) {
	if !p.HasAnywhere(identity.PermStockRead) {
		return nil, fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermStockRead)
	}
	var out []BalanceView
	err := s.store.InTx(ctx, func(r Repository) error {
		var warehouseID, productID id.ID
		if f.Where != nil {
			loc, err := locate(ctx, r, p, *f.Where, identity.PermStockRead)
			if err != nil {
				return err
			}
			warehouseID = loc.Warehouse.ID
		}
		if strings.TrimSpace(f.Product) != "" {
			prod, err := appcatalog.Resolve(ctx, r, p.OrganizationID, f.Product)
			if err != nil {
				return err
			}
			productID = prod.ID
		}
		list, err := r.Balances(ctx, p.OrganizationID, warehouseID, productID)
		if err != nil {
			return err
		}
		for _, b := range list {
			if !p.Can(identity.PermStockRead, b.Scope) {
				continue
			}
			v, err := s.balanceView(ctx, r, b)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

// MaxKardexLimit es el máximo de movimientos por página.
const MaxKardexLimit = 1000

// Kardex devuelve el saldo y los movimientos de un producto en un almacén.
func (s *Service) Kardex(ctx context.Context, p identity.Principal, where WarehouseRef, product string, limit, offset int) (BalanceView, []inventory.Movement, error) {
	if limit <= 0 || limit > MaxKardexLimit || offset < 0 {
		return BalanceView{}, nil, fmt.Errorf("%w: el límite debe estar entre 1 y %d", inventory.ErrInvalid, MaxKardexLimit)
	}
	var bv BalanceView
	var list []inventory.Movement
	err := s.store.InTx(ctx, func(r Repository) error {
		loc, err := locate(ctx, r, p, where, identity.PermStockRead)
		if err != nil {
			return err
		}
		prod, err := appcatalog.Resolve(ctx, r, p.OrganizationID, product)
		if err != nil {
			return err
		}
		b, err := r.Balance(ctx, p.OrganizationID, loc.Warehouse.ID, prod.ID)
		if errors.Is(err, apporg.ErrNotFound) {
			b = inventory.Balance{Key: inventory.Key{Scope: loc.Warehouse.Scope(), ProductID: prod.ID}}
		} else if err != nil {
			return err
		}
		if bv, err = s.balanceView(ctx, r, b); err != nil {
			return err
		}
		list, err = r.Movements(ctx, p.OrganizationID, loc.Warehouse.ID, prod.ID, limit, offset)
		return err
	})
	return bv, list, err
}

// Problem es un saldo que no coincide con su kardex.
type Problem struct {
	BalanceView
	Detail string
}

// Report es el resultado de Verify.
type Report struct {
	Balances, Movements int
	Problems            []Problem
}

// Verify reproduce el kardex de cada saldo de la organización y lo compara
// con el saldo guardado. Exige ver existencias en toda la organización.
func (s *Service) Verify(ctx context.Context, p identity.Principal) (Report, error) {
	if err := p.Authorize(identity.PermStockRead, p.OrganizationScope(), "toda la organización"); err != nil {
		return Report{}, err
	}
	var rep Report
	err := s.store.InTx(ctx, func(r Repository) error {
		list, err := r.Balances(ctx, p.OrganizationID, id.ID{}, id.ID{})
		if err != nil {
			return err
		}
		for _, b := range list {
			movements, err := r.Movements(ctx, p.OrganizationID, b.Scope.Warehouse(), b.ProductID, 0, 0)
			if err != nil {
				return err
			}
			rep.Balances++
			rep.Movements += len(movements)
			if err := inventory.Replay(b, movements); err != nil {
				v, verr := s.balanceView(ctx, r, b)
				if verr != nil {
					return verr
				}
				rep.Problems = append(rep.Problems, Problem{BalanceView: v, Detail: err.Error()})
			}
		}
		return nil
	})
	return rep, err
}

// FormatQuantity muestra una cantidad con los decimales de su unidad.
func FormatQuantity(d decimal.Decimal, u catalog.Unit) string { return d.StringFixed(u.Scale) }

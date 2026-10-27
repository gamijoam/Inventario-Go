// Package stock contiene los casos de uso de existencias y kardex
// (BP-M2-001): ajustes de entrada y salida, reversos, consulta de saldos y
// kardex, y verificación de que cada saldo coincide con su kardex. Desde
// BP-M2-002 cada movimiento se valora a costo promedio ponderado por
// producto y almacén, y hay transferencias entre almacenes de una sucursal.
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

	// InsertTransfer devuelve ErrConcurrent si la clave de idempotencia ya
	// existe. Se inserta antes que sus dos movimientos.
	InsertTransfer(ctx context.Context, t inventory.Transfer) error
	Transfer(ctx context.Context, orgID, transferID id.ID) (inventory.Transfer, error)
	TransferByKey(ctx context.Context, orgID id.ID, idempotencyKey string) (inventory.Transfer, error)
	Transfers(ctx context.Context, orgID id.ID) ([]inventory.Transfer, error)
	// TransferMovements devuelve las dos patas: salida y entrada.
	TransferMovements(ctx context.Context, orgID, transferID id.ID) ([]inventory.Movement, error)

	// UpdateCommitments guarda lo reservado y bloqueado de un saldo sin
	// avanzar su versión.
	UpdateCommitments(ctx context.Context, b inventory.Balance) error

	InsertPolicyChange(ctx context.Context, c inventory.PolicyChange) error
	// CurrentPolicy devuelve la versión vigente de la política de la empresa
	// (productID cero) o de un producto; apporg.ErrNotFound si nunca se fijó.
	CurrentPolicy(ctx context.Context, orgID, companyID, productID id.ID) (inventory.PolicyChange, error)
	ProductPolicies(ctx context.Context, orgID, companyID id.ID) ([]inventory.PolicyChange, error)

	InsertReservation(ctx context.Context, v inventory.Reservation) error
	// UpdateReservation guarda el avance o cierre de una reserva activa.
	UpdateReservation(ctx context.Context, v inventory.Reservation) error
	Reservation(ctx context.Context, orgID, reservationID id.ID) (inventory.Reservation, error)
	ReservationByKey(ctx context.Context, orgID id.ID, key string) (inventory.Reservation, error)
	Reservations(ctx context.Context, f HoldFilter) ([]inventory.Reservation, error)
	// DueReservations son las activas con vencimiento cumplido.
	DueReservations(ctx context.Context, f HoldFilter, now time.Time) ([]inventory.Reservation, error)

	InsertBlock(ctx context.Context, v inventory.Block) error
	UpdateBlock(ctx context.Context, v inventory.Block) error
	Block(ctx context.Context, orgID, blockID id.ID) (inventory.Block, error)
	BlockByKey(ctx context.Context, orgID id.ID, key string) (inventory.Block, error)
	Blocks(ctx context.Context, f HoldFilter) ([]inventory.Block, error)

	InsertCount(ctx context.Context, c inventory.Count) error
	// UpdateCount cambia el estado solo si sigue en previous.
	UpdateCount(ctx context.Context, c inventory.Count, previous inventory.CountStatus) error
	Count(ctx context.Context, orgID, countID id.ID) (inventory.Count, error)
	Counts(ctx context.Context, orgID, warehouseID id.ID, status inventory.CountStatus) ([]inventory.Count, error)
	SaveCountLine(ctx context.Context, orgID id.ID, l inventory.CountLine) error
	CountLines(ctx context.Context, orgID, countID id.ID) ([]inventory.CountLine, error)
}

// HoldFilter filtra reservas y bloqueos por los campos no nulos.
type HoldFilter struct {
	OrganizationID, WarehouseID, ProductID id.ID
	Status                                 inventory.HoldStatus
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
	Where     WarehouseRef
	Product   string // SKU, código de barras o ID
	Direction inventory.Direction
	Quantity  string
	// UnitCost es el costo unitario de una entrada en la moneda de
	// valoración de la empresa. Vacío entra al CPP vigente, que debe existir.
	UnitCost       string
	Reason, Note   string
	IdempotencyKey string
	// AllowNegative pide una salida en déficit: exige el permiso
	// extraordinario inventory.negative_stock.override y que la política de
	// la empresa y el producto lo permitan, dentro de su límite.
	AllowNegative bool
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
	var costing inventory.Costing
	if strings.TrimSpace(in.UnitCost) != "" {
		if in.Direction == inventory.Out {
			return MovementView{}, fmt.Errorf("%w: una salida se valora al costo promedio; no indique costo unitario", inventory.ErrInvalid)
		}
		uc, err := inventory.ParseUnitCost(in.UnitCost)
		if err != nil {
			return MovementView{}, err
		}
		costing.UnitCost = inventory.Known(uc)
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
		hash := digest("adjust", loc.Warehouse.ID.String(), prod.ID.String(), qty.StringFixed(unit.Scale), string(reason), note, costing.UnitCost.String(),
			fmt.Sprint(in.AllowNegative))
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
		var policyChange id.ID
		if in.AllowNegative {
			if in.Direction != inventory.Out {
				return fmt.Errorf("%w: solo una salida puede dejar existencia negativa", inventory.ErrInvalid)
			}
			if err := p.Authorize(identity.PermNegativeOverride, loc.Warehouse.Scope(), "el almacén "+loc.String()); err != nil {
				return err
			}
			limit, change, err := negativeLimit(ctx, r, loc.Company, prod)
			if err != nil {
				return err
			}
			costing.Overdraft, policyChange = inventory.Known(limit), change
		}
		l, err := s.lock(ctx, r, p.OrganizationID, loc, prod.ID)
		if err != nil {
			return err
		}
		st, err := s.stage(p, l, inventory.Movement{Kind: kind, Reason: reason, Quantity: qty, Note: note, IdempotencyKey: key, RequestHash: hash}, costing)
		if err != nil {
			return err
		}
		if st.m.OnHandAfter.IsNegative() {
			st.m.PolicyChangeID = policyChange
		}
		if err := st.commit(ctx, r); err != nil {
			return err
		}
		out = MovementView{Movement: st.m, Product: prod, Unit: unit, Location: loc}
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
func (s *Service) apply(ctx context.Context, r Repository, p identity.Principal, loc Location, productID id.ID, m inventory.Movement, c inventory.Costing) (inventory.Movement, error) {
	l, err := s.lock(ctx, r, p.OrganizationID, loc, productID)
	if err != nil {
		return inventory.Movement{}, err
	}
	return s.post(ctx, r, p, l, m, c)
}

// locked es un saldo leído con bloqueo, o uno nuevo aún sin fila.
type locked struct {
	inventory.Balance
	isNew bool
}

// lock lee el saldo con bloqueo y, antes de usarlo, libera las reservas
// vencidas que lo comprometían.
func (s *Service) lock(ctx context.Context, r Repository, orgID id.ID, loc Location, productID id.ID) (locked, error) {
	b, err := r.BalanceForUpdate(ctx, orgID, loc.Warehouse.ID, productID)
	if errors.Is(err, apporg.ErrNotFound) {
		return locked{Balance: inventory.Balance{Key: inventory.Key{Scope: loc.Warehouse.Scope(), ProductID: productID}}, isNew: true}, nil
	}
	if err != nil {
		return locked{}, err
	}
	l := locked{Balance: b}
	if _, err := s.expireLocked(ctx, r, &l); err != nil {
		return locked{}, err
	}
	return l, nil
}

// post aplica m sobre un saldo ya bloqueado y guarda saldo y movimiento.
func (s *Service) post(ctx context.Context, r Repository, p identity.Principal, l locked, m inventory.Movement, c inventory.Costing) (inventory.Movement, error) {
	st, err := s.stage(p, l, m, c)
	if err != nil {
		return inventory.Movement{}, err
	}
	return st.m, st.commit(ctx, r)
}

// staged es un movimiento aplicado en memoria, pendiente de guardar.
type staged struct {
	b     inventory.Balance
	prev  int64
	isNew bool
	m     inventory.Movement
}

func (s *Service) stage(p identity.Principal, l locked, m inventory.Movement, c inventory.Costing) (staged, error) {
	now := s.now().UTC()
	mid, err := id.New()
	if err != nil {
		return staged{}, err
	}
	m.ID, m.Key, m.ActorID, m.OccurredAt = mid, l.Key, p.UserID, now
	b, m, err := inventory.Apply(l.Balance, m, c, now)
	if err != nil {
		return staged{}, err
	}
	return staged{b: b, prev: l.Version, isNew: l.isNew, m: m}, nil
}

// commit guarda el saldo y luego el movimiento, que debe coincidir con él.
func (st staged) commit(ctx context.Context, r Repository) error {
	var err error
	if st.isNew {
		err = r.InsertBalance(ctx, st.b)
	} else {
		err = r.UpdateBalance(ctx, st.b, st.prev)
	}
	if err != nil {
		return err
	}
	return r.InsertMovement(ctx, st.m)
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
		if !orig.TransferID.IsZero() {
			return fmt.Errorf("%w: una transferencia no se revierte por partes; registre una transferencia en sentido contrario", inventory.ErrInvalid)
		}
		if _, err := r.ReversalOf(ctx, p.OrganizationID, orig.ID); err == nil {
			return ErrAlreadyReversed
		} else if !errors.Is(err, apporg.ErrNotFound) {
			return err
		}
		m := inventory.Movement{Kind: inventory.KindReversal, Reason: inventory.ReasonReversal, Quantity: orig.Quantity.Neg(),
			Note: n, ReversesID: orig.ID, IdempotencyKey: key, RequestHash: hash}
		// Deshace exactamente el valor original; si no tenía costo, el
		// reverso tampoco lo inventa.
		c := inventory.Costing{AllowUnknown: true}
		if orig.Cost.Known {
			c = inventory.Costing{Fixed: inventory.Known(orig.Cost.Value.Neg())}
		}
		if m, err = s.apply(ctx, r, p, loc, orig.ProductID, m, c); err != nil {
			return err
		}
		out = MovementView{Movement: m, Product: v.Product, Unit: v.Unit, Location: loc}
		return nil
	})
	return out, err
}

// TransferInput mueve existencia entre dos almacenes de una sucursal.
type TransferInput struct {
	Company, Branch string
	From, To        string // códigos de almacén
	Product         string // SKU, código de barras o ID
	Quantity        string
	Note            string
	IdempotencyKey  string
}

// TransferView es una transferencia con sus dos patas.
type TransferView struct {
	inventory.Transfer
	Out, In             inventory.Movement
	Product             catalog.Product
	Unit                catalog.Unit
	Origin, Destination Location
	// Replayed indica que la clave de idempotencia ya existía.
	Replayed bool
}

// Transfer saca la cantidad del almacén de origen al CPP vigente y la
// incorpora al destino con exactamente ese valor, en una sola transacción.
// Requiere inventory.transfer.create en ambos almacenes.
func (s *Service) Transfer(ctx context.Context, p identity.Principal, in TransferInput) (TransferView, error) {
	note, err := inventory.ParseNote(in.Note, inventory.ReasonTransfer)
	if err != nil {
		return TransferView{}, err
	}
	key, err := checkKey(in.IdempotencyKey)
	if err != nil {
		return TransferView{}, err
	}
	if strings.TrimSpace(in.From) == "" || strings.TrimSpace(in.To) == "" {
		return TransferView{}, fmt.Errorf("%w: indique el almacén de origen y el de destino", inventory.ErrInvalid)
	}
	var out TransferView
	err = s.write(ctx, func(r Repository) error {
		from, err := locate(ctx, r, p, WarehouseRef{Company: in.Company, Branch: in.Branch, Warehouse: in.From}, identity.PermTransferCreate)
		if err != nil {
			return err
		}
		to, err := locate(ctx, r, p, WarehouseRef{Company: in.Company, Branch: in.Branch, Warehouse: in.To}, identity.PermTransferCreate)
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
		hash := digest("transfer", from.Warehouse.ID.String(), to.Warehouse.ID.String(), prod.ID.String(), qty.StringFixed(unit.Scale), note)
		if key != "" {
			prior, err := r.TransferByKey(ctx, p.OrganizationID, key)
			switch {
			case err == nil && prior.RequestHash != hash:
				return ErrKeyReused
			case err == nil:
				out, err = s.transferView(ctx, r, prior)
				out.Replayed = true
				return err
			case !errors.Is(err, apporg.ErrNotFound):
				return err
			}
		}
		if prod.Status != catalog.StatusActive {
			return fmt.Errorf("%w: el producto %s está inactivo", inventory.ErrInvalid, prod.SKU)
		}
		for _, loc := range []Location{from, to} {
			if loc.Warehouse.Status != org.StatusActive {
				return fmt.Errorf("%w: el almacén %s está inactivo", inventory.ErrInvalid, loc)
			}
		}
		t, err := inventory.NewTransfer(from.Warehouse.Scope(), to.Warehouse.Scope(), prod.ID, qty, note, p.UserID, s.now())
		if err != nil {
			return err
		}
		t.IdempotencyKey, t.RequestHash = key, hash

		// Ambos saldos se bloquean siempre en el mismo orden: dos
		// transferencias en sentidos opuestos no se esperan mutuamente.
		first, second := from, to
		if to.Warehouse.ID.String() < from.Warehouse.ID.String() {
			first, second = to, from
		}
		l1, err := s.lock(ctx, r, p.OrganizationID, first, prod.ID)
		if err != nil {
			return err
		}
		l2, err := s.lock(ctx, r, p.OrganizationID, second, prod.ID)
		if err != nil {
			return err
		}
		src, dst := l1, l2
		if first.Warehouse.ID != from.Warehouse.ID {
			src, dst = l2, l1
		}

		outSt, err := s.stage(p, src, inventory.Movement{Kind: inventory.KindTransferOut, Reason: inventory.ReasonTransfer,
			Quantity: qty.Neg(), Note: note, TransferID: t.ID, RequestHash: hash}, inventory.Costing{})
		if err != nil {
			return err
		}
		if !outSt.m.Cost.Known {
			return fmt.Errorf("%w: la existencia de %s no está valorada; regístrele antes una entrada con costo unitario", inventory.ErrValuation, from)
		}
		t.Value = outSt.m.Cost.Value.Neg()
		inSt, err := s.stage(p, dst, inventory.Movement{Kind: inventory.KindTransferIn, Reason: inventory.ReasonTransfer,
			Quantity: qty, Note: note, TransferID: t.ID, RequestHash: hash}, inventory.Costing{Fixed: inventory.Known(t.Value)})
		if err != nil {
			return fmt.Errorf("almacén %s: %w", to, err)
		}
		if err := r.InsertTransfer(ctx, t); err != nil {
			return err
		}
		if err := outSt.commit(ctx, r); err != nil {
			return err
		}
		if err := inSt.commit(ctx, r); err != nil {
			return err
		}
		out = TransferView{Transfer: t, Out: outSt.m, In: inSt.m, Product: prod, Unit: unit, Origin: from, Destination: to}
		return nil
	})
	return out, err
}

func (s *Service) transferView(ctx context.Context, r Repository, t inventory.Transfer) (TransferView, error) {
	v := TransferView{Transfer: t}
	legs, err := r.TransferMovements(ctx, t.From.Organization(), t.ID)
	if err != nil {
		return v, err
	}
	for _, m := range legs {
		switch m.Kind {
		case inventory.KindTransferOut:
			v.Out = m
		case inventory.KindTransferIn:
			v.In = m
		}
	}
	if v.Origin, err = location(ctx, r, t.From); err != nil {
		return v, err
	}
	if v.Destination, err = location(ctx, r, t.To); err != nil {
		return v, err
	}
	if v.Product, err = r.Product(ctx, t.From.Organization(), t.ProductID); err != nil {
		return v, err
	}
	v.Unit, err = r.Unit(ctx, t.From.Organization(), v.Product.UnitID)
	return v, err
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
	// Negative deja solo los saldos en déficit.
	Negative bool
}

// Stock devuelve los saldos de los almacenes donde el principal puede ver
// existencias.
func (s *Service) Stock(ctx context.Context, p identity.Principal, f StockFilter) ([]BalanceView, error) {
	if !p.HasAnywhere(identity.PermStockRead) {
		return nil, fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermStockRead)
	}
	var out []BalanceView
	err := s.write(ctx, func(r Repository) error {
		out = nil
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
		if _, err := s.expire(ctx, r, HoldFilter{OrganizationID: p.OrganizationID, WarehouseID: warehouseID, ProductID: productID}); err != nil {
			return err
		}
		list, err := r.Balances(ctx, p.OrganizationID, warehouseID, productID)
		if err != nil {
			return err
		}
		for _, b := range list {
			if !p.Can(identity.PermStockRead, b.Scope) || f.Negative && !b.OnHand.IsNegative() {
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
	err := s.write(ctx, func(r Repository) error {
		loc, err := locate(ctx, r, p, where, identity.PermStockRead)
		if err != nil {
			return err
		}
		prod, err := appcatalog.Resolve(ctx, r, p.OrganizationID, product)
		if err != nil {
			return err
		}
		if _, err := s.expire(ctx, r, HoldFilter{OrganizationID: p.OrganizationID, WarehouseID: loc.Warehouse.ID, ProductID: prod.ID}); err != nil {
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

// TransferProblem es una transferencia cuyas patas no conservan cantidad o
// valor.
type TransferProblem struct {
	TransferID id.ID
	Detail     string
}

// Report es el resultado de Verify.
type Report struct {
	Balances, Movements, Transfers int
	Problems                       []Problem
	TransferProblems               []TransferProblem
	// Unvalued son saldos con existencia sin valor conocido (cargada antes
	// de BP-M2-002): no es un error, pero no tienen CPP hasta que una
	// entrada con costo los valore.
	Unvalued []BalanceView
	// Deficits son saldos con existencia negativa autorizada, pendientes de
	// regularizar; AtRisk, saldos cuyo reservado y bloqueado ya no tiene
	// respaldo físico (p. ej. tras un conteo). Ambos son avisos.
	Deficits, AtRisk []BalanceView
}

// OK indica que no hay diferencias; los saldos sin valorar son un aviso.
func (r Report) OK() bool { return len(r.Problems) == 0 && len(r.TransferProblems) == 0 }

// Verify reproduce el kardex de cada saldo de la organización y lo compara
// con el saldo guardado, y comprueba que cada transferencia saca y
// recibe la misma cantidad y el mismo valor. Exige ver existencias en toda
// la organización.
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
		legs := map[id.ID][]inventory.Movement{}
		committed, err := commitments(ctx, r, p.OrganizationID)
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
			for _, m := range movements {
				if !m.TransferID.IsZero() {
					legs[m.TransferID] = append(legs[m.TransferID], m)
				}
			}
			replayErr := inventory.Replay(b, movements)
			if replayErr == nil {
				replayErr = committed[b.Key].check(b)
			}
			unvalued := !b.Value.Known && !b.OnHand.IsZero()
			if replayErr == nil && !unvalued && !b.OnHand.IsNegative() && !b.AtRisk() {
				continue
			}
			v, err := s.balanceView(ctx, r, b)
			if err != nil {
				return err
			}
			switch {
			case replayErr != nil:
				rep.Problems = append(rep.Problems, Problem{BalanceView: v, Detail: replayErr.Error()})
				continue
			case unvalued:
				rep.Unvalued = append(rep.Unvalued, v)
			}
			if b.OnHand.IsNegative() {
				rep.Deficits = append(rep.Deficits, v)
			}
			if b.AtRisk() {
				rep.AtRisk = append(rep.AtRisk, v)
			}
		}
		transfers, err := r.Transfers(ctx, p.OrganizationID)
		if err != nil {
			return err
		}
		for _, t := range transfers {
			rep.Transfers++
			if err := checkTransfer(t, legs[t.ID]); err != nil {
				rep.TransferProblems = append(rep.TransferProblems, TransferProblem{TransferID: t.ID, Detail: err.Error()})
			}
			delete(legs, t.ID)
		}
		for tid := range legs {
			rep.TransferProblems = append(rep.TransferProblems, TransferProblem{TransferID: tid, Detail: "movimientos de una transferencia inexistente"})
		}
		return nil
	})
	return rep, err
}

// committed suma lo pendiente de las reservas y bloqueos activos de un saldo.
type committed struct{ reserved, blocked decimal.Decimal }

func (c committed) check(b inventory.Balance) error {
	if !c.reserved.Equal(b.Reserved) || !c.blocked.Equal(b.Blocked) {
		return fmt.Errorf("el saldo registra %s reservado y %s bloqueado, pero las reservas activas suman %s y los bloqueos %s",
			b.Reserved, b.Blocked, c.reserved, c.blocked)
	}
	return nil
}

func commitments(ctx context.Context, r Repository, orgID id.ID) (map[inventory.Key]committed, error) {
	out := map[inventory.Key]committed{}
	active := HoldFilter{OrganizationID: orgID, Status: inventory.HoldActive}
	reservations, err := r.Reservations(ctx, active)
	if err != nil {
		return nil, err
	}
	for _, v := range reservations {
		c := out[v.Key]
		c.reserved = c.reserved.Add(v.Remaining())
		out[v.Key] = c
	}
	blocks, err := r.Blocks(ctx, active)
	if err != nil {
		return nil, err
	}
	for _, v := range blocks {
		c := out[v.Key]
		c.blocked = c.blocked.Add(v.Quantity)
		out[v.Key] = c
	}
	return out, nil
}

// checkTransfer exige una salida del origen y una entrada al destino por la
// misma cantidad y el mismo valor.
func checkTransfer(t inventory.Transfer, legs []inventory.Movement) error {
	var out, in []inventory.Movement
	for _, m := range legs {
		switch {
		case m.Kind == inventory.KindTransferOut && m.Scope == t.From && m.ProductID == t.ProductID:
			out = append(out, m)
		case m.Kind == inventory.KindTransferIn && m.Scope == t.To && m.ProductID == t.ProductID:
			in = append(in, m)
		default:
			return fmt.Errorf("el movimiento %s no corresponde a la transferencia", m.ID)
		}
	}
	if len(out) != 1 || len(in) != 1 {
		return fmt.Errorf("se esperaba una salida y una entrada; hay %d y %d", len(out), len(in))
	}
	o, i := out[0], in[0]
	if !o.Quantity.Equal(t.Quantity.Neg()) || !i.Quantity.Equal(t.Quantity) {
		return fmt.Errorf("cantidades %s y %s para una transferencia de %s", o.Quantity, i.Quantity, t.Quantity)
	}
	if !o.Cost.Known || !i.Cost.Known || !o.Cost.Value.Equal(t.Value.Neg()) || !i.Cost.Value.Equal(t.Value) {
		return fmt.Errorf("valores %s y %s para una transferencia valorada en %s", o.Cost, i.Cost, t.Value)
	}
	return nil
}

// FormatQuantity muestra una cantidad con los decimales de su unidad.
func FormatQuantity(d decimal.Decimal, u catalog.Unit) string { return d.StringFixed(u.Scale) }

package cli

import (
	"context"
	"errors"
	"sync"

	"github.com/gamijoam/inventario-go/internal/adapters/tui"
	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/pricing"
)

// tuiBackend implementa tui.Backend sobre la base abierta y el archivo de
// sesión de este equipo: iniciar o cerrar sesión en la TUI vale también
// para los comandos, y al revés.
type tuiBackend struct {
	s     appSession
	mu    sync.Mutex
	token string
	who   identity.Principal
}

var _ tui.Backend = (*tuiBackend)(nil)

func newTUIBackend(s appSession) *tuiBackend { return &tuiBackend{s: s} }

// call limita cada operación como en los comandos y exige sesión.
func (b *tuiBackend) call(ctx context.Context) (context.Context, context.CancelFunc, identity.Principal, error) {
	b.mu.Lock()
	p := b.who
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	if p.UserID.IsZero() {
		return ctx, cancel, p, errNoSession
	}
	return ctx, cancel, p, nil
}

func (b *tuiBackend) session(ctx context.Context, token string) (tui.Session, error) {
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	who, err := b.s.auth.Authenticate(ctx, token)
	if err != nil {
		return tui.Session{}, err
	}
	b.mu.Lock()
	b.token, b.who = token, who.Principal
	b.mu.Unlock()
	return tui.Session{Username: who.User.Username.String(), DisplayName: who.User.DisplayName.String(),
		Company: b.s.cur.Company.Code.String(), Branch: b.s.cur.Branch.Code.String(), Principal: who.Principal}, nil
}

func (b *tuiBackend) Resume(ctx context.Context) (tui.Session, error) {
	token, err := readSessionFile(b.s.rt.loaded.Config.Session.File)
	if err != nil {
		return tui.Session{}, err
	}
	return b.session(ctx, token)
}

func (b *tuiBackend) Login(ctx context.Context, username, password string) (tui.Session, error) {
	lctx, cancel := context.WithTimeout(ctx, dbTimeout)
	res, err := b.s.auth.Login(lctx, username, password)
	cancel()
	if err != nil {
		return tui.Session{}, err
	}
	if err := writeSessionFile(b.s.rt.loaded.Config.Session.File, res.Token); err != nil {
		return tui.Session{}, err
	}
	return b.session(ctx, res.Token)
}

func (b *tuiBackend) Logout(ctx context.Context) error {
	b.mu.Lock()
	token := b.token
	b.token, b.who = "", identity.Principal{}
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	err := b.s.auth.Logout(ctx, token)
	if errors.Is(err, identity.ErrSessionInvalid) {
		err = nil // ya no estaba vigente
	}
	return errors.Join(err, removeSessionFile(b.s.rt.loaded.Config.Session.File))
}

func (b *tuiBackend) Products(ctx context.Context, search string) ([]appcatalog.ProductView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return nil, err
	}
	return b.s.cat.ListProducts(ctx, p, appcatalog.Filter{Search: search, IncludeInactive: true, Limit: appcatalog.MaxListLimit})
}

func (b *tuiBackend) Stock(ctx context.Context, product string) ([]stock.BalanceView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return nil, err
	}
	return b.s.stock.Stock(ctx, p, stock.StockFilter{Product: product})
}

func (b *tuiBackend) Kardex(ctx context.Context, where stock.WarehouseRef, product string) (stock.BalanceView, []inventory.Movement, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.BalanceView{}, nil, err
	}
	return b.s.stock.Kardex(ctx, p, where, product, stock.MaxKardexLimit, 0)
}

func (b *tuiBackend) Prices(ctx context.Context, product string) ([]apppricing.PriceView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return nil, err
	}
	return b.s.price.Prices(ctx, p, b.s.cur.Company.Code.String(), "", product)
}

func (b *tuiBackend) Proposals(ctx context.Context, status pricing.ProposalStatus) ([]apppricing.ProposalView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return nil, err
	}
	return b.s.price.Proposals(ctx, p, b.s.cur.Company.Code.String(), "", status)
}

func (b *tuiBackend) Proposal(ctx context.Context, ref string) (apppricing.ProposalView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return apppricing.ProposalView{}, err
	}
	return b.s.price.ShowProposal(ctx, p, ref)
}

func (b *tuiBackend) Approve(ctx context.Context, ref string, in apppricing.ApproveInput) (apppricing.ProposalView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return apppricing.ProposalView{}, err
	}
	return b.s.price.Approve(ctx, p, ref, in)
}

func (b *tuiBackend) Reject(ctx context.Context, ref, note string) (apppricing.ProposalView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return apppricing.ProposalView{}, err
	}
	return b.s.price.Reject(ctx, p, ref, note)
}

func (b *tuiBackend) CreateProduct(ctx context.Context, in appcatalog.NewProduct) (appcatalog.ProductView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return appcatalog.ProductView{}, err
	}
	return b.s.cat.CreateProduct(ctx, p, b.s.cur.Organization, in)
}

func (b *tuiBackend) UpdateProduct(ctx context.Context, ref string, c appcatalog.Changes) (appcatalog.ProductView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return appcatalog.ProductView{}, err
	}
	return b.s.cat.UpdateProduct(ctx, p, b.s.cur.Organization, ref, c)
}

func (b *tuiBackend) Units(ctx context.Context) ([]catalog.Unit, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return nil, err
	}
	return b.s.cat.ListUnits(ctx, p)
}

func (b *tuiBackend) CreateUnit(ctx context.Context, code, name string, scale int32) (catalog.Unit, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return catalog.Unit{}, err
	}
	return b.s.cat.CreateUnit(ctx, p, b.s.cur.Organization, code, name, scale)
}

// ---------- Inventario ----------

// here completa la empresa y la sucursal de la sesión.
func (b *tuiBackend) here(w stock.WarehouseRef) stock.WarehouseRef { return withDefaults(w, b.s) }

func (b *tuiBackend) Warehouses(ctx context.Context) ([]org.Warehouse, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return nil, err
	}
	return b.s.org.ListWarehouses(ctx, p, b.s.cur.Company.Code.String(), b.s.cur.Branch.Code.String())
}

func (b *tuiBackend) Adjust(ctx context.Context, in stock.AdjustInput) (stock.MovementView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.MovementView{}, err
	}
	in.Where = b.here(in.Where)
	return b.s.stock.Adjust(ctx, p, in)
}

func (b *tuiBackend) Reverse(ctx context.Context, movementID, note, key string) (stock.MovementView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.MovementView{}, err
	}
	return b.s.stock.Reverse(ctx, p, movementID, note, key)
}

func (b *tuiBackend) Transfer(ctx context.Context, in stock.TransferInput) (stock.TransferView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.TransferView{}, err
	}
	w := b.here(stock.WarehouseRef{Company: in.Company, Branch: in.Branch})
	in.Company, in.Branch = w.Company, w.Branch
	return b.s.stock.Transfer(ctx, p, in)
}

func (b *tuiBackend) Reservations(ctx context.Context, status inventory.HoldStatus) ([]stock.ReservationView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return nil, err
	}
	return b.s.stock.Reservations(ctx, p, stock.HoldQuery{Status: status})
}

func (b *tuiBackend) Reserve(ctx context.Context, in stock.ReserveInput) (stock.ReservationView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.ReservationView{}, err
	}
	in.Where = b.here(in.Where)
	return b.s.stock.Reserve(ctx, p, in)
}

func (b *tuiBackend) ConsumeReservation(ctx context.Context, id, quantity, note, key string) (stock.ReservationView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.ReservationView{}, err
	}
	return b.s.stock.ConsumeReservation(ctx, p, id, quantity, note, key)
}

func (b *tuiBackend) ReleaseReservation(ctx context.Context, id, note string) (stock.ReservationView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.ReservationView{}, err
	}
	return b.s.stock.ReleaseReservation(ctx, p, id, note)
}

func (b *tuiBackend) Blocks(ctx context.Context, status inventory.HoldStatus) ([]stock.BlockView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return nil, err
	}
	return b.s.stock.Blocks(ctx, p, stock.HoldQuery{Status: status})
}

func (b *tuiBackend) Block(ctx context.Context, in stock.BlockInput) (stock.BlockView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.BlockView{}, err
	}
	in.Where = b.here(in.Where)
	return b.s.stock.Block(ctx, p, in)
}

func (b *tuiBackend) CloseBlock(ctx context.Context, id string, writeOff bool, note string) (stock.BlockView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.BlockView{}, err
	}
	return b.s.stock.CloseBlock(ctx, p, id, writeOff, note)
}

func (b *tuiBackend) Counts(ctx context.Context, status inventory.CountStatus) ([]stock.CountView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return nil, err
	}
	return b.s.stock.Counts(ctx, p, nil, status)
}

func (b *tuiBackend) CreateCount(ctx context.Context, warehouse, note string) (stock.CountView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.CountView{}, err
	}
	return b.s.stock.CreateCount(ctx, p, b.here(stock.WarehouseRef{Warehouse: warehouse}), note)
}

func (b *tuiBackend) Count(ctx context.Context, id string) (stock.CountView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.CountView{}, err
	}
	return b.s.stock.ShowCount(ctx, p, id)
}

func (b *tuiBackend) RecordCount(ctx context.Context, id, product, counted, unitCost string) (stock.CountLineView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.CountLineView{}, err
	}
	return b.s.stock.RecordCount(ctx, p, id, product, counted, unitCost)
}

func (b *tuiBackend) SubmitCount(ctx context.Context, id string) (stock.CountView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.CountView{}, err
	}
	return b.s.stock.SubmitCount(ctx, p, id)
}

func (b *tuiBackend) DecideCount(ctx context.Context, id string, approve bool, note string) (stock.CountView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.CountView{}, err
	}
	return b.s.stock.DecideCount(ctx, p, id, approve, note)
}

func (b *tuiBackend) Policy(ctx context.Context) (stock.PolicyView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.PolicyView{}, err
	}
	return b.s.stock.Policy(ctx, p, b.s.cur.Company.Code.String(), "")
}

func (b *tuiBackend) SetCompanyPolicy(ctx context.Context, enabled bool, limit string) (stock.PolicyView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.PolicyView{}, err
	}
	return b.s.stock.SetCompanyPolicy(ctx, p, b.s.cur.Company.Code.String(), enabled, limit)
}

func (b *tuiBackend) SetProductPolicy(ctx context.Context, product, mode, limit string) (stock.PolicyView, error) {
	ctx, cancel, p, err := b.call(ctx)
	defer cancel()
	if err != nil {
		return stock.PolicyView{}, err
	}
	return b.s.stock.SetProductPolicy(ctx, p, b.s.cur.Company.Code.String(), product, mode, limit)
}

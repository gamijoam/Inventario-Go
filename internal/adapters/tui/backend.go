package tui

import (
	"context"

	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/application/purchasing"
	"github.com/gamijoam/inventario-go/internal/application/rates"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/pricing"
	"github.com/gamijoam/inventario-go/internal/domain/purchase"
)

// Backend conecta la TUI con los casos de uso de la capa application. La
// CLI lo implementa sobre la base abierta y la sesión de este equipo, la
// misma que usan los comandos: iniciar sesión en la TUI la inicia también
// para la CLI. Las reglas de negocio y los permisos se aplican del lado
// del backend; la TUI solo oculta lo que el usuario no puede usar.
type Backend interface {
	// Resume devuelve la sesión guardada en este equipo si sigue vigente.
	Resume(ctx context.Context) (Session, error)
	Login(ctx context.Context, username, password string) (Session, error)
	Logout(ctx context.Context) error

	Products(ctx context.Context, search string) ([]appcatalog.ProductView, error)
	CreateProduct(ctx context.Context, in appcatalog.NewProduct) (appcatalog.ProductView, error)
	// UpdateProduct edita un producto por SKU, código de barras o ID.
	UpdateProduct(ctx context.Context, ref string, c appcatalog.Changes) (appcatalog.ProductView, error)
	Units(ctx context.Context) ([]catalog.Unit, error)
	CreateUnit(ctx context.Context, code, name string, scale int32) (catalog.Unit, error)
	// Stock devuelve los saldos visibles; product vacío, todos.
	Stock(ctx context.Context, product string) ([]stock.BalanceView, error)
	Kardex(ctx context.Context, where stock.WarehouseRef, product string) (stock.BalanceView, []inventory.Movement, error)
	// Prices devuelve los precios de la empresa; product vacío, todos.
	Prices(ctx context.Context, product string) ([]apppricing.PriceView, error)
	Proposals(ctx context.Context, status pricing.ProposalStatus) ([]apppricing.ProposalView, error)
	Proposal(ctx context.Context, ref string) (apppricing.ProposalView, error)
	Approve(ctx context.Context, ref string, in apppricing.ApproveInput) (apppricing.ProposalView, error)
	Reject(ctx context.Context, ref, note string) (apppricing.ProposalView, error)

	// Inventario. Los almacenes van por código dentro de la empresa y la
	// sucursal de la sesión; vacío es el único almacén activo de la
	// sucursal. Cada formulario envía su propia clave de idempotencia: si
	// la respuesta se pierde y se reintenta, no se aplica dos veces.
	Warehouses(ctx context.Context) ([]org.Warehouse, error)
	Adjust(ctx context.Context, in stock.AdjustInput) (stock.MovementView, error)
	Reverse(ctx context.Context, movementID, note, key string) (stock.MovementView, error)
	Transfer(ctx context.Context, in stock.TransferInput) (stock.TransferView, error)

	Reservations(ctx context.Context, status inventory.HoldStatus) ([]stock.ReservationView, error)
	Reserve(ctx context.Context, in stock.ReserveInput) (stock.ReservationView, error)
	// ConsumeReservation despacha quantity (vacío: todo lo pendiente).
	ConsumeReservation(ctx context.Context, id, quantity, note, key string) (stock.ReservationView, error)
	ReleaseReservation(ctx context.Context, id, note string) (stock.ReservationView, error)

	Blocks(ctx context.Context, status inventory.HoldStatus) ([]stock.BlockView, error)
	Block(ctx context.Context, in stock.BlockInput) (stock.BlockView, error)
	// CloseBlock libera el bloqueo o, con writeOff, lo da de baja como merma.
	CloseBlock(ctx context.Context, id string, writeOff bool, note string) (stock.BlockView, error)

	// Counts lista los conteos; status vacío, todos.
	Counts(ctx context.Context, status inventory.CountStatus) ([]stock.CountView, error)
	CreateCount(ctx context.Context, warehouse, note string) (stock.CountView, error)
	Count(ctx context.Context, id string) (stock.CountView, error)
	RecordCount(ctx context.Context, id, product, counted, unitCost string) (stock.CountLineView, error)
	SubmitCount(ctx context.Context, id string) (stock.CountView, error)
	DecideCount(ctx context.Context, id string, approve bool, note string) (stock.CountView, error)

	// Policy es la política de existencia negativa de la empresa de la sesión.
	Policy(ctx context.Context) (stock.PolicyView, error)
	SetCompanyPolicy(ctx context.Context, enabled bool, limit string) (stock.PolicyView, error)
	SetProductPolicy(ctx context.Context, product, mode, limit string) (stock.PolicyView, error)

	// Compras. Las órdenes y recepciones van por número o ID; la empresa y
	// la sucursal son las de la sesión.
	Suppliers(ctx context.Context) ([]purchase.Supplier, error)
	CreateSupplier(ctx context.Context, code, name, rif string) (purchase.Supplier, error)
	Orders(ctx context.Context, status purchase.OrderStatus) ([]purchasing.OrderView, error)
	Order(ctx context.Context, ref string) (purchasing.OrderView, error)
	CreateOrder(ctx context.Context, in purchasing.OrderInput) (purchasing.OrderView, error)
	CancelOrder(ctx context.Context, ref string) (purchasing.OrderView, error)
	Receipts(ctx context.Context, status purchase.ReceiptStatus) ([]purchasing.ReceiptView, error)
	Receipt(ctx context.Context, ref string) (purchasing.ReceiptView, error)
	CreateReceipt(ctx context.Context, in purchasing.ReceiptInput) (purchasing.ReceiptView, error)
	SetReceiptLine(ctx context.Context, ref string, in purchasing.LineInput) (purchasing.ReceiptView, error)
	RemoveReceiptLine(ctx context.Context, ref, product string) (purchasing.ReceiptView, error)
	AddExpense(ctx context.Context, ref string, in purchasing.ExpenseInput) (purchasing.ReceiptView, error)
	ConfirmReceipt(ctx context.Context, ref string) (purchasing.ReceiptView, error)
	CancelReceipt(ctx context.Context, ref string) (purchasing.ReceiptView, error)
	// Costs reúne el CPP, la última compra y el costo de reposición.
	Costs(ctx context.Context, product string) (purchasing.CostView, error)
	SetReplacementCost(ctx context.Context, product, unitCost, currency, note string) (purchasing.CostView, error)

	// Tasas de cambio de la empresa de la sesión.
	Rates(ctx context.Context) ([]rates.RateView, error)
	SetRate(ctx context.Context, in rates.SetInput) (rates.RateView, error)
	// EffectiveRate es la tasa del par que rige en date (AAAA-MM-DD; vacía,
	// hoy); envuelve exchange.ErrNoRate si no hay.
	EffectiveRate(ctx context.Context, base, quote, date string) (rates.RateView, error)
}

// Session es el usuario conectado y el ámbito de esta instalación.
type Session struct {
	Username    string
	DisplayName string
	Company     string // código de la empresa de esta instalación
	Branch      string
	Principal   identity.Principal
}

// Can indica si el usuario tiene perm en algún ámbito; la TUI lo usa para
// mostrar u ocultar opciones, no para autorizar.
func (s Session) Can(perm identity.Permission) bool { return s.Principal.HasAnywhere(perm) }

// SetupInput son los datos del asistente de primer uso. Los códigos de
// empresa, sucursal y almacén toman sus valores por defecto (PRINCIPAL).
type SetupInput struct {
	BusinessName string
	LegalName    string // vacío: el nombre del negocio
	RIF          string
	Currency     string // de valoración: USD o VES
	AdminUser    string
	AdminName    string
	Password     string
}

// Setup crea la instalación, inicia la sesión del administrador y devuelve
// el backend ya conectado.
type Setup func(ctx context.Context, in SetupInput) (Backend, error)

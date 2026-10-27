package tui

import (
	"context"

	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/pricing"
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
	// Stock devuelve los saldos visibles; product vacío, todos.
	Stock(ctx context.Context, product string) ([]stock.BalanceView, error)
	Kardex(ctx context.Context, where stock.WarehouseRef, product string) (stock.BalanceView, []inventory.Movement, error)
	// Prices devuelve los precios de la empresa; product vacío, todos.
	Prices(ctx context.Context, product string) ([]apppricing.PriceView, error)
	Proposals(ctx context.Context, status pricing.ProposalStatus) ([]apppricing.ProposalView, error)
	Proposal(ctx context.Context, ref string) (apppricing.ProposalView, error)
	Approve(ctx context.Context, ref string, in apppricing.ApproveInput) (apppricing.ProposalView, error)
	Reject(ctx context.Context, ref, note string) (apppricing.ProposalView, error)
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

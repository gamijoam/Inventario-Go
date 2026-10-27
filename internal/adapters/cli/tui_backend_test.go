package cli_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
	"github.com/gamijoam/inventario-go/internal/adapters/tui"
	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/pricing"
)

// La TUI recibe un backend sobre la base real: comparte la sesión con los
// comandos y aplica los mismos permisos.
func TestTUIBackendUsesTheRealServices(t *testing.T) {
	admin := setupSQLite(t)
	mustRun(t, admin, "product", "create", "--name", "Harina", "--sku", "HARINA")
	mustRun(t, admin, "inventory", "in", "--product", "HARINA", "--qty", "10", "--unit-cost", "8", "--reason", "initial_load")
	mustRun(t, admin, "price-list", "create", "--code", "general", "--name", "General", "--margin", "20")
	mustRun(t, admin, "price", "propose", "--list", "general", "--product", "HARINA", "--price", "10")
	mustRun(t, admin, "auth", "logout")

	o := admin
	o.interactive = true
	o.tui = func(opts tui.Options) {
		ctx := context.Background()
		b := opts.Backend
		if b == nil || opts.Unavailable != "" {
			t.Fatalf("la TUI debe recibir el backend: %q", opts.Unavailable)
		}
		if _, err := b.Resume(ctx); !errors.Is(err, identity.ErrSessionInvalid) {
			t.Errorf("sin sesión guardada: %v", err)
		}
		if _, err := b.Products(ctx, ""); !errors.Is(err, identity.ErrSessionInvalid) {
			t.Errorf("sin sesión, los módulos se niegan: %v", err)
		}
		if _, err := b.Login(ctx, "admin", "mala"); err == nil {
			t.Error("contraseña incorrecta aceptada")
		}
		s, err := b.Login(ctx, "admin", adminPassword)
		if err != nil || s.Username != "admin" || s.Company != "PRINCIPAL" || !s.Can(identity.PermPriceApprove) {
			t.Fatalf("login: %+v %v", s, err)
		}
		prods, err := b.Products(ctx, "")
		if err != nil || len(prods) != 1 || prods[0].SKU.String() != "HARINA" {
			t.Fatalf("productos: %+v %v", prods, err)
		}
		bal, err := b.Stock(ctx, "HARINA")
		if err != nil || len(bal) != 1 || bal[0].OnHand.String() != "10" {
			t.Fatalf("existencias: %+v %v", bal, err)
		}
		where := stock.WarehouseRef{Company: "PRINCIPAL", Branch: "PRINCIPAL", Warehouse: "PRINCIPAL"}
		if _, moves, err := b.Kardex(ctx, where, "HARINA"); err != nil || len(moves) != 1 {
			t.Fatalf("kardex: %v %v", moves, err)
		}
		props, err := b.Proposals(ctx, pricing.ProposalPending)
		if err != nil || len(props) != 1 {
			t.Fatalf("propuestas: %+v %v", props, err)
		}
		if v, err := b.Approve(ctx, props[0].Number, apppricing.ApproveInput{}); err != nil || v.Status != pricing.ProposalApproved {
			t.Fatalf("aprobar: %+v %v", v, err)
		}
		if prices, err := b.Prices(ctx, "HARINA"); err != nil || len(prices) != 1 || prices[0].Current.Price.String() != "10" {
			t.Fatalf("precios: %+v %v", prices, err)
		}
	}
	if r := run(t, o); r.code != cli.ExitOK {
		t.Fatalf("tui: %d %s", r.code, r.stderr)
	}
	// La sesión iniciada en la TUI vale para los comandos.
	if r := run(t, admin, "auth", "whoami"); r.code != cli.ExitOK || !strings.Contains(r.stdout, "admin") {
		t.Fatalf("whoami tras la TUI: %d %s %s", r.code, r.stdout, r.stderr)
	}

	// Cerrar sesión en la TUI la cierra también para los comandos.
	o.tui = func(opts tui.Options) {
		ctx := context.Background()
		if _, err := opts.Backend.Resume(ctx); err != nil {
			t.Fatalf("retomar la sesión: %v", err)
		}
		if err := opts.Backend.Logout(ctx); err != nil {
			t.Fatalf("logout: %v", err)
		}
	}
	run(t, o)
	// Sin archivo de sesión (no solo revocada en la base).
	if r := run(t, admin, "auth", "whoami"); r.code != cli.ExitUnauthenticated || !strings.Contains(r.stderr, "no hay sesión iniciada") {
		t.Fatalf("whoami tras cerrar en la TUI: %d %s", r.code, r.stderr)
	}
}

func TestTUIWithoutSetupExplainsWhy(t *testing.T) {
	o, _ := sqliteEnv(t)
	o.interactive = true
	o.tui = func(opts tui.Options) {
		if opts.Backend != nil || !strings.Contains(opts.Unavailable, "balanzapro setup") {
			t.Errorf("sin setup: backend=%v %q", opts.Backend != nil, opts.Unavailable)
		}
	}
	if r := run(t, o); r.code != cli.ExitOK {
		t.Fatalf("tui: %d %s", r.code, r.stderr)
	}
}

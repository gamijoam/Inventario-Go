package cli_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
	"github.com/gamijoam/inventario-go/internal/adapters/tui"
	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
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

// Sin instalación, la TUI recibe el asistente de primer uso: crea la base,
// la organización y el administrador, y deja la sesión iniciada también
// para los comandos.
func TestTUISetupWizardInstalls(t *testing.T) {
	o, _ := sqliteEnv(t)
	o.interactive = true
	o.tui = func(opts tui.Options) {
		ctx := context.Background()
		if opts.Backend != nil || opts.Setup == nil || opts.Unavailable != "" {
			t.Fatalf("sin instalación: backend=%v setup=%v %q", opts.Backend != nil, opts.Setup != nil, opts.Unavailable)
		}
		if _, err := opts.Setup(ctx, tui.SetupInput{BusinessName: "Bodega Ana", Currency: "USD", AdminUser: "admin",
			AdminName: "Ana", Password: "corta"}); err == nil {
			t.Fatal("contraseña corta aceptada")
		}
		b, err := opts.Setup(ctx, tui.SetupInput{BusinessName: "Bodega Ana", RIF: "J-12345678-9", Currency: "USD",
			AdminName: "Ana", Password: adminPassword})
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		s, err := b.Resume(ctx)
		if err != nil || s.Username != "admin" || s.DisplayName != "Ana" || s.Company != "PRINCIPAL" || s.Branch != "PRINCIPAL" {
			t.Fatalf("sesión tras el asistente: %+v %v", s, err)
		}

		// Catálogo completo desde la TUI.
		units, err := b.Units(ctx)
		if err != nil || len(units) == 0 {
			t.Fatalf("unidades: %+v %v", units, err)
		}
		if u, err := b.CreateUnit(ctx, "SACO", "Saco de 45 kg", 3); err != nil || u.Code != "SACO" || u.Scale != 3 {
			t.Fatalf("unidad: %+v %v", u, err)
		}
		if _, err := b.CreateUnit(ctx, "SACO", "Otro", 0); !errors.Is(err, apporg.ErrConflict) {
			t.Errorf("unidad repetida: %v", err)
		}
		p, err := b.CreateProduct(ctx, appcatalog.NewProduct{Name: "Café 500 g", UnitCode: "KG", Barcodes: []string{"7591234567894"}})
		if err != nil || p.SKU.String() != "P-000001" || p.Unit.Code != "KG" || len(p.Barcodes) != 1 {
			t.Fatalf("producto: %+v %v", p, err)
		}
		name := "Café molido 500 g"
		p2, err := b.UpdateProduct(ctx, p.ID.String(), appcatalog.Changes{Name: &name, RemoveBarcodes: []string{"7591234567894"}, ExpectedRevision: p.Revision})
		if err != nil || p2.Name.String() != name || len(p2.Barcodes) != 0 {
			t.Fatalf("editar: %+v %v", p2, err)
		}
		off := false
		if _, err := b.UpdateProduct(ctx, p.ID.String(), appcatalog.Changes{Active: &off, ExpectedRevision: p.Revision}); !errors.Is(err, appcatalog.ErrStale) {
			t.Errorf("una revisión vieja no debe guardarse: %v", err)
		}
		if p3, err := b.UpdateProduct(ctx, p.ID.String(), appcatalog.Changes{Active: &off, ExpectedRevision: p2.Revision}); err != nil || p3.Status != catalog.StatusInactive {
			t.Fatalf("inactivar: %+v %v", p3, err)
		}
	}
	if r := run(t, o); r.code != cli.ExitOK {
		t.Fatalf("tui: %d %s", r.code, r.stderr)
	}
	// La instalación y la sesión valen para los comandos.
	r := run(t, o, "product", "list", "--all")
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "Café molido 500 g") {
		t.Fatalf("product list tras el asistente: %d %s %s", r.code, r.stdout, r.stderr)
	}
	// Ya instalada, la TUI no vuelve a ofrecer el asistente.
	o.tui = func(opts tui.Options) {
		if opts.Backend == nil || opts.Setup != nil {
			t.Errorf("instalada: backend=%v setup=%v", opts.Backend != nil, opts.Setup != nil)
		}
	}
	run(t, o)
}

// Las operaciones de inventario de la TUI usan los casos de uso reales, con
// el almacén de la sesión por defecto y la idempotencia de cada formulario.
func TestTUIInventoryOperations(t *testing.T) {
	admin := setupSQLite(t)
	mustRun(t, admin, "product", "create", "--name", "Harina", "--sku", "HARINA")
	mustRun(t, admin, "warehouse", "create", "--code", "deposito", "--name", "Depósito")
	var countID string
	o := admin
	o.interactive = true
	o.tui = func(opts tui.Options) {
		ctx := context.Background()
		b := opts.Backend
		if _, err := b.Resume(ctx); err != nil {
			t.Fatalf("sesión: %v", err)
		}
		ws, err := b.Warehouses(ctx)
		if err != nil || len(ws) != 2 {
			t.Fatalf("almacenes: %+v %v", ws, err)
		}
		in := stock.AdjustInput{Where: stock.WarehouseRef{Warehouse: "PRINCIPAL"}, Product: "HARINA", Direction: inventory.In,
			Quantity: "20", UnitCost: "1.5", Reason: "initial_load", IdempotencyKey: "tui-entrada-1"}
		m, err := b.Adjust(ctx, in)
		if err != nil || m.OnHandAfter.String() != "20" || m.Location.String() != "PRINCIPAL/PRINCIPAL/PRINCIPAL" {
			t.Fatalf("entrada: %+v %v", m, err)
		}
		if again, err := b.Adjust(ctx, in); err != nil || !again.Replayed || again.ID != m.ID {
			t.Fatalf("la misma clave no aplica dos veces: %+v %v", again, err)
		}
		if _, err := b.Adjust(ctx, stock.AdjustInput{Product: "HARINA", Direction: inventory.Out, Quantity: "2", Reason: "shrinkage"}); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("con dos almacenes hay que elegir uno: %v", err)
		}
		out, err := b.Adjust(ctx, stock.AdjustInput{Where: stock.WarehouseRef{Warehouse: "principal"}, Product: "HARINA", Direction: inventory.Out,
			Quantity: "2", Reason: "shrinkage", IdempotencyKey: "tui-salida-1"})
		if err != nil || out.OnHandAfter.String() != "18" {
			t.Fatalf("salida: %+v %v", out, err)
		}
		if r, err := b.Reverse(ctx, out.ID.String(), "error de captura", "tui-rev-1"); err != nil || r.OnHandAfter.String() != "20" {
			t.Fatalf("reverso: %+v %v", r, err)
		}
		if tr, err := b.Transfer(ctx, stock.TransferInput{From: "PRINCIPAL", To: "DEPOSITO", Product: "HARINA", Quantity: "5", IdempotencyKey: "tui-tr-1"}); err != nil ||
			tr.Destination.Warehouse.Code.String() != "DEPOSITO" {
			t.Fatalf("transferencia: %+v %v", tr, err)
		}

		rv, err := b.Reserve(ctx, stock.ReserveInput{Where: stock.WarehouseRef{Warehouse: "PRINCIPAL"}, Product: "HARINA", Quantity: "4", Reference: "PED-1", IdempotencyKey: "tui-res-1"})
		if err != nil {
			t.Fatalf("reserva: %v", err)
		}
		if _, err := b.Reserve(ctx, stock.ReserveInput{Where: stock.WarehouseRef{Warehouse: "PRINCIPAL"}, Product: "HARINA", Quantity: "100", Reference: "PED-2", IdempotencyKey: "tui-res-2"}); !errors.Is(err, inventory.ErrInsufficient) {
			t.Errorf("reservar más que el disponible: %v", err)
		}
		if c, err := b.ConsumeReservation(ctx, rv.ID.String(), "1", "", "tui-desp-1"); err != nil || c.Consumed.String() != "1" {
			t.Fatalf("despacho: %+v %v", c, err)
		}
		if r, err := b.ReleaseReservation(ctx, rv.ID.String(), "canceló"); err != nil || r.Status != inventory.HoldReleased {
			t.Fatalf("liberar: %+v %v", r, err)
		}
		if list, err := b.Reservations(ctx, ""); err != nil || len(list) != 1 {
			t.Fatalf("reservas: %+v %v", list, err)
		}
		bl, err := b.Block(ctx, stock.BlockInput{Where: stock.WarehouseRef{Warehouse: "PRINCIPAL"}, Product: "HARINA", Quantity: "2", Reason: "damage", IdempotencyKey: "tui-bl-1"})
		if err != nil {
			t.Fatalf("bloqueo: %v", err)
		}
		if active, _ := b.Blocks(ctx, inventory.HoldActive); len(active) != 1 {
			t.Fatalf("bloqueos activos: %+v", active)
		}
		if c, err := b.CloseBlock(ctx, bl.ID.String(), true, "roto"); err != nil || c.Status != inventory.HoldWrittenOff {
			t.Fatalf("baja: %+v %v", c, err)
		}

		if _, err := b.SetCompanyPolicy(ctx, true, ""); err == nil {
			t.Error("habilitar sin límite")
		}
		if p, err := b.SetCompanyPolicy(ctx, true, "3"); err != nil || !p.Current.Company.NegativeEnabled {
			t.Fatalf("política: %+v %v", p, err)
		}
		if p, err := b.SetProductPolicy(ctx, "HARINA", "deny", ""); err != nil || len(p.Products) != 1 || p.Products[0].Allowed {
			t.Fatalf("política de producto: %+v %v", p, err)
		}
		if p, err := b.Policy(ctx); err != nil || len(p.Products) != 1 {
			t.Fatalf("leer política: %+v %v", p, err)
		}

		c, err := b.CreateCount(ctx, "PRINCIPAL", "cierre")
		if err != nil {
			t.Fatalf("conteo: %v", err)
		}
		countID = c.ID.String()
		if l, err := b.RecordCount(ctx, countID, "HARINA", "11", ""); err != nil || l.Difference().String() != "-1" {
			t.Fatalf("captura: %+v %v", l, err)
		}
		if s, err := b.SubmitCount(ctx, countID); err != nil || s.Status != inventory.CountInReview {
			t.Fatalf("enviar: %+v %v", s, err)
		}
		if _, err := b.DecideCount(ctx, countID, true, ""); !errors.Is(err, inventory.ErrSameCounter) {
			t.Errorf("quien contó no aprueba: %v", err)
		}
	}
	if r := run(t, o); r.code != cli.ExitOK {
		t.Fatalf("tui: %d %s", r.code, r.stderr)
	}

	// Otro usuario, con permiso de aprobar, decide el conteo desde la TUI.
	enc := user(t, admin, "enc1", "ENCARGADO")
	enc.interactive = true
	enc.tui = func(opts tui.Options) {
		ctx := context.Background()
		if _, err := opts.Backend.Resume(ctx); err != nil {
			t.Fatalf("sesión: %v", err)
		}
		list, err := opts.Backend.Counts(ctx, inventory.CountInReview)
		if err != nil || len(list) != 1 {
			t.Fatalf("conteos en revisión: %+v %v", list, err)
		}
		if v, err := opts.Backend.DecideCount(ctx, countID, true, "conforme"); err != nil || v.Status != inventory.CountApplied {
			t.Fatalf("aprobar: %+v %v", v, err)
		}
	}
	run(t, enc)
	if r := run(t, admin, "inventory", "stock", "--product", "HARINA"); !strings.Contains(r.stdout, "11") {
		t.Fatalf("existencia tras el conteo: %s", r.stdout)
	}
	if r := run(t, admin, "inventory", "verify"); r.code != cli.ExitOK {
		t.Fatalf("verify: %d %s", r.code, r.stdout)
	}
}

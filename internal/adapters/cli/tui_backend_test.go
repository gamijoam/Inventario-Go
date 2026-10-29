package cli_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
	"github.com/gamijoam/inventario-go/internal/adapters/tui"
	"github.com/gamijoam/inventario-go/internal/application/auth"
	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/application/purchasing"
	"github.com/gamijoam/inventario-go/internal/application/rates"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/exchange"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/pricing"
	"github.com/gamijoam/inventario-go/internal/domain/purchase"
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

// Compras y tasas desde la TUI: orden, recepción en bolívares contra la
// orden, tasa registrada al vuelo, gasto, confirmación y costos.
func TestTUIPurchasesAndRates(t *testing.T) {
	admin := setupSQLite(t)
	mustRun(t, admin, "product", "create", "--name", "Harina", "--sku", "HARINA")
	o := admin
	o.interactive = true
	o.tui = func(opts tui.Options) {
		ctx := context.Background()
		b := opts.Backend
		if _, err := b.Resume(ctx); err != nil {
			t.Fatalf("sesión: %v", err)
		}
		if s, err := b.CreateSupplier(ctx, "dist", "Distribuidora", ""); err != nil || s.Code.String() != "DIST" {
			t.Fatalf("proveedor: %+v %v", s, err)
		}
		if list, err := b.Suppliers(ctx); err != nil || len(list) != 1 {
			t.Fatalf("proveedores: %+v %v", list, err)
		}
		ord, err := b.CreateOrder(ctx, purchasing.OrderInput{Supplier: "DIST", Currency: "VES",
			Lines: []purchasing.LineInput{{Product: "HARINA", Quantity: "30", UnitCost: "57"}}})
		if err != nil || ord.Status != purchase.OrderOpen {
			t.Fatalf("orden: %+v %v", ord, err)
		}
		if open, _ := b.Orders(ctx, purchase.OrderOpen); len(open) != 1 {
			t.Fatalf("órdenes abiertas: %+v", open)
		}
		rc, err := b.CreateReceipt(ctx, purchasing.ReceiptInput{Order: ord.Number, Document: "FAC-1"})
		if err != nil || rc.Currency.Code() != "VES" || rc.Order != ord.Number || rc.Supplier.Code.String() != "DIST" {
			t.Fatalf("recepción contra la orden: %+v %v", rc, err)
		}
		if _, err := b.SetReceiptLine(ctx, rc.Number, purchasing.LineInput{Product: "HARINA", Quantity: "30", UnitCost: "57"}); err != nil {
			t.Fatalf("línea: %v", err)
		}
		if _, err := b.EffectiveRate(ctx, "USD", "VES", rc.DocumentDate.Format("2006-01-02")); !errors.Is(err, exchange.ErrNoRate) {
			t.Fatalf("sin tasa: %v", err)
		}
		if _, err := b.ConfirmReceipt(ctx, rc.Number); !errors.Is(err, exchange.ErrNoRate) {
			t.Fatalf("confirmar sin tasa: %v", err)
		}
		if r, err := b.SetRate(ctx, rates.SetInput{Base: "USD", Quote: "VES", Rate: "45.725", Source: "official", Label: "BCV"}); err != nil || r.Version != 1 {
			t.Fatalf("tasa: %+v %v", r, err)
		}
		if r, err := b.EffectiveRate(ctx, "VES", "USD", ""); err != nil || r.Value.String() != "45.725" {
			t.Fatalf("tasa vigente: %+v %v", r, err)
		}
		if _, err := b.AddExpense(ctx, rc.Number, purchasing.ExpenseInput{Concept: "Flete", Amount: "2", Currency: "USD"}); err != nil {
			t.Fatalf("gasto: %v", err)
		}
		done, err := b.ConfirmReceipt(ctx, rc.Number)
		if err != nil || done.Status != purchase.ReceiptConfirmed || done.RateID.IsZero() {
			t.Fatalf("confirmar: %+v %v", done, err)
		}
		if got, _ := b.Order(ctx, ord.Number); got.Status != purchase.OrderReceived {
			t.Errorf("la orden recibida por completo: %s", got.Status)
		}
		c, err := b.Costs(ctx, "HARINA")
		if err != nil || c.Last == nil || c.Last.Receipt != rc.Number || len(c.Balances) != 1 || c.Balances[0].OnHand.String() != "30" {
			t.Fatalf("costos: %+v %v", c, err)
		}
		if c, err := b.SetReplacementCost(ctx, "HARINA", "1.40", "", "lista nueva"); err != nil || len(c.Replacement) != 1 {
			t.Fatalf("reposición: %+v %v", c, err)
		}
		if list, err := b.Rates(ctx); err != nil || len(list) != 1 {
			t.Fatalf("tasas: %+v %v", list, err)
		}
		draft, err := b.CreateReceipt(ctx, purchasing.ReceiptInput{Supplier: "DIST"})
		if err != nil {
			t.Fatalf("recepción directa: %v", err)
		}
		if v, err := b.CancelReceipt(ctx, draft.Number); err != nil || v.Status != purchase.ReceiptCancelled {
			t.Fatalf("anular: %+v %v", v, err)
		}
		if list, _ := b.Receipts(ctx, ""); len(list) != 2 {
			t.Fatalf("recepciones: %d", len(list))
		}
	}
	if r := run(t, o); r.code != cli.ExitOK {
		t.Fatalf("tui: %d %s", r.code, r.stderr)
	}
	if r := run(t, admin, "inventory", "verify"); r.code != cli.ExitOK {
		t.Fatalf("verify: %d %s", r.code, r.stdout)
	}
}

// Precios y administración desde la TUI sobre los servicios reales.
func TestTUIPricingAndAdministration(t *testing.T) {
	admin := setupSQLite(t)
	mustRun(t, admin, "product", "create", "--name", "Harina", "--sku", "HARINA")
	o := admin
	o.interactive = true
	o.tui = func(opts tui.Options) {
		ctx := context.Background()
		b := opts.Backend
		if _, err := b.Resume(ctx); err != nil {
			t.Fatalf("sesión: %v", err)
		}
		if l, err := b.CreatePriceList(ctx, apppricing.ListInput{Code: "general", Name: "General", Margin: "20"}); err != nil || l.Margin == nil {
			t.Fatalf("lista: %+v %v", l, err)
		}
		if _, err := b.SetMargin(ctx, apppricing.MarginInput{List: "GENERAL", Product: "HARINA", Margin: "30"}); err != nil {
			t.Fatalf("margen: %v", err)
		}
		if lists, err := b.PriceLists(ctx); err != nil || len(lists) != 1 {
			t.Fatalf("listas: %+v %v", lists, err)
		}
		pv, err := b.Propose(ctx, apppricing.ProposeInput{List: "GENERAL", Product: "HARINA", Price: "10"})
		if err != nil || pv.Status != pricing.ProposalPending {
			t.Fatalf("propuesta: %+v %v", pv, err)
		}
		if _, err := b.Approve(ctx, pv.Number, apppricing.ApproveInput{At: "2030-01-01 08:00"}); err != nil {
			t.Fatalf("aprobar programado: %v", err)
		}
		prices, err := b.Prices(ctx, "HARINA")
		if err != nil || len(prices) != 1 || len(prices[0].Scheduled) != 1 {
			t.Fatalf("precios: %+v %v", prices, err)
		}
		if r, err := b.CancelRevision(ctx, prices[0].Scheduled[0].ID.String()); err != nil || r.Status != pricing.RevisionCancelled {
			t.Fatalf("anular programado: %+v %v", r, err)
		}

		if _, err := b.CreateCompany(ctx, "otra", "Otra, C.A.", "", "VES"); err != nil {
			t.Fatalf("empresa: %v", err)
		}
		if _, err := b.CreateBranch(ctx, "OTRA", "centro", "Centro"); err != nil {
			t.Fatalf("sucursal: %v", err)
		}
		if _, err := b.CreateWarehouse(ctx, "OTRA", "CENTRO", "c1", "Centro 1"); err != nil {
			t.Fatalf("almacén: %v", err)
		}
		if cs, _ := b.Companies(ctx); len(cs) != 2 {
			t.Fatalf("empresas: %+v", cs)
		}
		if ws, err := b.WarehousesOf(ctx, "OTRA", "CENTRO"); err != nil || len(ws) != 1 {
			t.Fatalf("almacenes: %+v %v", ws, err)
		}
		if bs, err := b.Branches(ctx, "OTRA"); err != nil || len(bs) != 1 {
			t.Fatalf("sucursales: %+v %v", bs, err)
		}

		if _, err := b.CreateRole(ctx, "auditor", "Auditor", []string{string(identity.PermStockRead)}); err != nil {
			t.Fatalf("rol: %v", err)
		}
		if r, err := b.EditRole(ctx, "AUDITOR", []string{string(identity.PermPriceRead)}, []string{string(identity.PermStockRead)}); err != nil ||
			len(r.Permissions) != 1 || r.Permissions[0] != identity.PermPriceRead {
			t.Fatalf("editar rol: %+v %v", r, err)
		}
		if roles, err := b.Roles(ctx); err != nil || len(roles) < 2 {
			t.Fatalf("roles: %d %v", len(roles), err)
		}
		if _, err := b.CreateUser(ctx, auth.NewUserInput{Username: "pedro", DisplayName: "Pedro", Password: userPassword}); err != nil {
			t.Fatalf("usuario: %v", err)
		}
		if x, err := b.Assign(ctx, "pedro", "AUDITOR", auth.ScopeRef{Company: "OTRA", Branch: "CENTRO", Warehouse: "C1"}); err != nil || x.RoleCode.String() != "AUDITOR" {
			t.Fatalf("asignar: %+v %v", x, err)
		}
		if err := b.ResetPassword(ctx, "pedro", userPassword+"x"); err != nil {
			t.Fatalf("restablecer: %v", err)
		}
		if err := b.Unlock(ctx, "pedro"); err != nil {
			t.Fatalf("desbloquear: %v", err)
		}
		users, err := b.Users(ctx)
		if err != nil || len(users) != 2 {
			t.Fatalf("usuarios: %+v %v", users, err)
		}
		if err := b.Unassign(ctx, "pedro", "AUDITOR", auth.ScopeRef{Company: "OTRA", Branch: "CENTRO", Warehouse: "C1"}); err != nil {
			t.Fatalf("quitar: %v", err)
		}
		if u, err := b.SetUserActive(ctx, "pedro", false); err != nil || u.Status == identity.StatusActive {
			t.Fatalf("inactivar: %+v %v", u, err)
		}
		if err := b.ChangePassword(ctx, "mala", "nueva clave segura 1"); err == nil {
			t.Error("cambiar con la actual incorrecta")
		}
		if err := b.ChangePassword(ctx, adminPassword, "nueva clave segura 1"); err != nil {
			t.Fatalf("cambiar contraseña: %v", err)
		}
		if _, err := b.Products(ctx, ""); err != nil {
			t.Fatalf("la sesión actual sigue abierta tras cambiar la contraseña: %v", err)
		}
	}
	if r := run(t, o); r.code != cli.ExitOK {
		t.Fatalf("tui: %d %s", r.code, r.stderr)
	}
	if r := run(t, withStdin(admin, "nueva clave segura 1"), "auth", "login", "--user", "admin", "--password-stdin"); r.code != cli.ExitOK {
		t.Fatalf("login con la contraseña nueva: %d %s", r.code, r.stderr)
	}
}

// Un proveedor persona natural se registra con su cédula: el dígito final
// del RIF se calcula si no se escribe.
func TestTUISupplierRIFWithoutCheckDigit(t *testing.T) {
	admin := setupSQLite(t)
	o := admin
	o.interactive = true
	o.tui = func(opts tui.Options) {
		ctx := context.Background()
		b := opts.Backend
		if _, err := b.Resume(ctx); err != nil {
			t.Fatalf("sesión: %v", err)
		}
		s, err := b.CreateSupplier(ctx, "pedro", "Pedro Pérez", "v-12345678")
		if err != nil || s.RIF.String() != "V-12345678-1" {
			t.Fatalf("proveedor V sin dígito: %+v %v", s, err)
		}
		if s, err := b.CreateSupplier(ctx, "ana", "Ana", "V-11111111-9"); err != nil || s.RIF.String() != "V-11111111-9" {
			t.Fatalf("el dígito escrito se conserva: %+v %v", s, err)
		}
		if _, err := b.CreateSupplier(ctx, "x", "X", "V-1234"); err == nil || !strings.Contains(err.Error(), "V-12345678-9") {
			t.Fatalf("RIF corto: %v", err)
		}
	}
	if r := run(t, o); r.code != cli.ExitOK {
		t.Fatalf("tui: %d %s", r.code, r.stderr)
	}
	if r := run(t, admin, "purchase", "supplier", "create", "--code", "luis", "--name", "Luis", "--rif", "V87654321"); r.code != cli.ExitOK || !strings.Contains(r.stdout, "LUIS") {
		t.Fatalf("CLI: %d %s %s", r.code, r.stdout, r.stderr)
	}
}

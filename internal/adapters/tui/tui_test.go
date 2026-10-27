package tui_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/gamijoam/inventario-go/internal/adapters/tui"
	"github.com/gamijoam/inventario-go/internal/application/admin"
	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/pricing"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
	"github.com/gamijoam/inventario-go/internal/platform/buildinfo"
)

var (
	keyUp    = tea.KeyPressMsg{Code: tea.KeyUp}
	keyDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	keyTab   = tea.KeyPressMsg{Code: tea.KeyTab}
)

func key(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

// typeText envía cada carácter como una pulsación.
func typeText(t *testing.T, m tea.Model, s string) tea.Model {
	t.Helper()
	for _, r := range s {
		m, _ = send(t, m, key(r))
	}
	return m
}

// send aplica msg y ejecuta los comandos resultantes como el runtime de
// Bubble Tea, hasta que no quede ninguno; devuelve el último mensaje.
func send(t *testing.T, m tea.Model, msg tea.Msg) (tea.Model, tea.Msg) {
	t.Helper()
	next, cmd := m.Update(msg)
	var last tea.Msg
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 200 {
			t.Fatal("demasiados comandos encadenados")
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		out := runCmd(c)
		switch out := out.(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, out...)
		case tea.QuitMsg:
			return next, out
		default:
			last = out
			var more tea.Cmd
			next, more = next.Update(out)
			queue = append(queue, more)
		}
	}
	return next, last
}

// runCmd ejecuta un comando; los que esperan (temporizadores) se ignoran.
func runCmd(c tea.Cmd) tea.Msg {
	done := make(chan tea.Msg, 1)
	go func() { done <- c() }()
	select {
	case msg := <-done:
		return msg
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

func content(m tea.Model) string { return m.View().Content }

func start(t *testing.T, opts tui.Options) tea.Model {
	t.Helper()
	if opts.Build.Version == "" {
		opts.Build = buildinfo.Info{Version: "v0.0.0-test", GoVersion: "go1.27.3", OS: "linux", Arch: "amd64"}
	}
	var m tea.Model = tui.New(context.Background(), opts)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	if cmd := m.Init(); cmd != nil {
		next, _ := m.Update(runCmd(cmd))
		m = next
	}
	return m
}

func mustContain(t *testing.T, m tea.Model, want ...string) {
	t.Helper()
	v := content(m)
	for _, w := range want {
		if !strings.Contains(v, w) {
			t.Fatalf("falta %q en la pantalla:\n%s", w, v)
		}
	}
}

// ---------- Sin base de datos ----------

func withoutBackend(t *testing.T, doctorCalls *int) tea.Model {
	return start(t, tui.Options{
		Unavailable: "Instalación sin configurar: ejecute 'balanzapro setup'.",
		Doctor: func(context.Context) admin.Report {
			*doctorCalls++
			return admin.Report{Checks: []admin.Result{{Name: "timezone", Status: admin.StatusOK, Detail: "America/Caracas"}}}
		},
	})
}

func TestWithoutDatabaseOnlyDiagnosticsAndVersion(t *testing.T) {
	calls := 0
	m := withoutBackend(t, &calls)
	mustContain(t, m, "> 1. Diagnóstico", "2. Versión", "3. Salir", "ejecute 'balanzapro setup'")
	if strings.Contains(content(m), "Productos") {
		t.Fatal("sin base no debe ofrecer módulos")
	}
	m, _ = send(t, m, keyUp)
	mustContain(t, m, "> 3. Salir")
	m, _ = send(t, m, keyDown)
	m, _ = send(t, m, keyEnter)
	if calls != 1 {
		t.Fatalf("diagnóstico: %d llamadas", calls)
	}
	mustContain(t, m, "Diagnóstico", "[ OK ]", "timezone")
	m, _ = send(t, m, key('r'))
	if calls != 2 {
		t.Fatalf("r debe repetir el diagnóstico: %d", calls)
	}
	m, _ = send(t, m, keyEsc)
	m, _ = send(t, m, key('2'))
	mustContain(t, m, "v0.0.0-test", "desconocido")
	m, _ = send(t, m, keyEsc)
	mustContain(t, m, "> 2. Versión")
}

func TestQuitKeys(t *testing.T) {
	cases := map[string][]tea.Msg{
		"q":              {key('q')},
		"ctrl+c":         {tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}},
		"esc en el menú": {keyEsc},
		"opción Salir":   {keyUp, keyEnter},
	}
	for name, keys := range cases {
		calls := 0
		m := withoutBackend(t, &calls)
		var last tea.Msg
		for _, k := range keys {
			m, last = send(t, m, k)
		}
		if _, ok := last.(tea.QuitMsg); !ok {
			t.Errorf("%s: se esperaba QuitMsg, obtuvo %T", name, last)
		}
	}
}

func TestSmallTerminalShowsWarning(t *testing.T) {
	calls := 0
	m := withoutBackend(t, &calls)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: tui.MinWidth - 1, Height: 24})
	if v := content(m); !strings.Contains(v, "demasiado pequeña") || strings.Contains(v, "Diagnóstico") {
		t.Fatalf("se esperaba aviso de terminal pequeña:\n%s", v)
	}
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	mustContain(t, m, "Diagnóstico")
	if !m.View().AltScreen {
		t.Fatal("la TUI debe usar la pantalla alternativa")
	}
}

// ---------- Con backend ----------

// fake es un backend en memoria: un producto, un saldo con su kardex, un
// precio y una propuesta pendiente.
type fake struct {
	perms     []identity.Permission
	saved     bool // hay sesión guardada en el equipo
	loggedOut bool
	approved  []apppricing.ApproveInput
	rejected  []string
	prop      apppricing.ProposalView
}

var (
	harina = catalog.Product{ID: id.MustNew(), SKU: mustSKU("HARINA"), Name: mustName("Harina de maíz 1 kg"), Status: catalog.StatusActive}
	und    = catalog.Unit{Code: "UND", Scale: 0}
	list   = pricing.List{Code: mustCode("GENERAL"), Name: "General", Currency: money.USD, Status: org.StatusActive}
	ten    = decimal.MustParse("10")
)

func mustSKU(s string) catalog.SKU {
	v, err := catalog.ParseSKU(s)
	if err != nil {
		panic(err)
	}
	return v
}

func mustName(s string) org.Name {
	v, err := org.ParseName(s)
	if err != nil {
		panic(err)
	}
	return v
}

func mustCode(s string) org.Code {
	v, err := org.ParseCode(s)
	if err != nil {
		panic(err)
	}
	return v
}

func newFake(perms ...identity.Permission) *fake {
	cost := decimal.MustParse("9.6")
	f := &fake{perms: perms}
	f.prop = apppricing.ProposalView{
		Proposal: pricing.Proposal{Number: "PP-000002", Kind: pricing.KindSuggested, Status: pricing.ProposalPending, Current: &ten,
			Price: decimal.MustParse("12"), Reference: &pricing.Reference{Basis: pricing.BasisLastPurchase, Cost: cost, Currency: money.USD, ListCost: cost}},
		List: list, Product: harina,
	}
	return f
}

func (f *fake) session() tui.Session {
	orgID := id.MustNew()
	sc, err := scope.New(orgID, id.ID{}, id.ID{}, id.ID{})
	if err != nil {
		panic(err)
	}
	return tui.Session{Username: "admin", DisplayName: "Administrador", Company: "PRINCIPAL", Branch: "PRINCIPAL",
		Principal: identity.Principal{UserID: id.MustNew(), OrganizationID: orgID, Grants: []identity.Grant{{Scope: sc, Permissions: f.perms}}}}
}

func (f *fake) Resume(context.Context) (tui.Session, error) {
	if !f.saved {
		return tui.Session{}, identity.ErrSessionInvalid
	}
	return f.session(), nil
}

func (f *fake) Login(_ context.Context, user, pw string) (tui.Session, error) {
	if user != "admin" || pw != "clave segura" {
		return tui.Session{}, errors.New("usuario o contraseña incorrectos, o cuenta bloqueada temporalmente")
	}
	return f.session(), nil
}

func (f *fake) Logout(context.Context) error { f.loggedOut = true; return nil }

func (f *fake) Products(context.Context, string) ([]appcatalog.ProductView, error) {
	arroz := catalog.Product{ID: id.MustNew(), SKU: mustSKU("ARROZ"), Name: mustName("Arroz 1 kg"), Status: catalog.StatusActive}
	return []appcatalog.ProductView{{Product: harina, Unit: und}, {Product: arroz, Unit: und}}, nil
}

func (f *fake) balance() stock.BalanceView {
	b := stock.BalanceView{Product: harina, Unit: und, Location: stock.Location{
		Company: org.Company{Code: mustCode("PRINCIPAL"), Currency: money.USD}, Branch: org.Branch{Code: mustCode("PRINCIPAL")},
		Warehouse: org.Warehouse{Code: mustCode("PRINCIPAL")}}}
	b.OnHand, b.Value = decimal.MustParse("20"), inventory.Known(decimal.MustParse("176"))
	return b
}

func (f *fake) Stock(_ context.Context, product string) ([]stock.BalanceView, error) {
	return []stock.BalanceView{f.balance()}, nil
}

func (f *fake) Kardex(context.Context, stock.WarehouseRef, string) (stock.BalanceView, []inventory.Movement, error) {
	m := inventory.Movement{Sequence: 2, Kind: inventory.KindPurchaseIn, Reason: inventory.ReasonPurchase, Quantity: ten, OnHandAfter: decimal.MustParse("20"),
		Cost: inventory.Known(decimal.MustParse("96")), ValueAfter: inventory.Known(decimal.MustParse("176")), Note: "RC-000001", OccurredAt: time.Now()}
	return f.balance(), []inventory.Movement{m}, nil
}

func (f *fake) Prices(context.Context, string) ([]apppricing.PriceView, error) {
	rev := pricing.Revision{Version: 1, Price: ten, Status: pricing.RevisionPublished}
	return []apppricing.PriceView{{List: list, Product: harina, Current: &rev, Pending: &f.prop.Proposal}}, nil
}

func (f *fake) Proposals(_ context.Context, status pricing.ProposalStatus) ([]apppricing.ProposalView, error) {
	if status != "" && f.prop.Status != status {
		return nil, nil
	}
	return []apppricing.ProposalView{f.prop}, nil
}

func (f *fake) Proposal(context.Context, string) (apppricing.ProposalView, error) { return f.prop, nil }

func (f *fake) Approve(_ context.Context, ref string, in apppricing.ApproveInput) (apppricing.ProposalView, error) {
	if in.At == "ayer" {
		return f.prop, pricing.ErrInvalid
	}
	f.approved = append(f.approved, in)
	f.prop.Status = pricing.ProposalApproved
	return f.prop, nil
}

func (f *fake) Reject(_ context.Context, ref, note string) (apppricing.ProposalView, error) {
	f.rejected = append(f.rejected, note)
	f.prop.Status, f.prop.DecisionNote = pricing.ProposalRejected, note
	return f.prop, nil
}

var all = []identity.Permission{identity.PermProductRead, identity.PermStockRead, identity.PermPriceRead, identity.PermPriceApprove}

func login(t *testing.T, f *fake) tea.Model {
	t.Helper()
	m := start(t, tui.Options{Backend: f})
	mustContain(t, m, "Iniciar sesión", "Usuario:")
	m = typeText(t, m, "admin")
	m, _ = send(t, m, keyEnter) // pasa a la contraseña
	m = typeText(t, m, "clave segura")
	m, _ = send(t, m, keyEnter)
	return m
}

func TestLoginShowsErrorsAndHidesThePassword(t *testing.T) {
	f := newFake(all...)
	m := start(t, tui.Options{Backend: f})
	m = typeText(t, m, "admin")
	m, _ = send(t, m, keyTab)
	m = typeText(t, m, "mala")
	if strings.Contains(content(m), "mala") {
		t.Fatal("la contraseña no debe verse")
	}
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "usuario o contraseña incorrectos", "Iniciar sesión")
	m = typeText(t, m, "clave segura")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "Hola, Administrador.", "admin · PRINCIPAL/PRINCIPAL", "Productos", "Propuestas de precio", "Cerrar sesión")
}

func TestSavedSessionSkipsLoginAndMenuFollowsPermissions(t *testing.T) {
	f := newFake(identity.PermStockRead)
	f.saved = true
	m := start(t, tui.Options{Backend: f})
	mustContain(t, m, "Existencias", "Cerrar sesión")
	for _, hidden := range []string{"Productos", "Precios", "Iniciar sesión"} {
		if strings.Contains(content(m), hidden) {
			t.Errorf("no debía mostrarse %q:\n%s", hidden, content(m))
		}
	}
}

func TestProductsFilterStockAndKardex(t *testing.T) {
	m := login(t, newFake(all...))
	m, _ = send(t, m, key('1')) // Productos
	mustContain(t, m, "Productos", "HARINA", "ARROZ", "2 en total")
	m, _ = send(t, m, key('/'))
	m = typeText(t, m, "arroz q") // q escribe en el filtro, no sale
	mustContain(t, m, "0 de 2")
	for range 2 {
		m, _ = send(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "1 de 2", "ARROZ")
	m, _ = send(t, m, keyEsc) // con el filtro fijado, Esc vuelve al menú
	mustContain(t, m, "1. Productos")
	m, _ = send(t, m, key('1'))
	m, _ = send(t, m, keyEnter) // existencias de HARINA
	mustContain(t, m, "Existencias de HARINA", "PRINCIPAL/PRINCIPAL", "8.800000", "176.00 USD")
	m, _ = send(t, m, keyEnter) // kardex
	mustContain(t, m, "Kardex de HARINA", "compra", "RC-000001")
	m, _ = send(t, m, keyEsc)
	mustContain(t, m, "Existencias de HARINA")
}

func TestApproveAProposal(t *testing.T) {
	f := newFake(all...)
	m := login(t, f)
	m, _ = send(t, m, key('4')) // Propuestas
	mustContain(t, m, "PP-000002", "10 → 12 USD", "recepción")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "Propuesta PP-000002", "último costo de compra 9.6 USD", "a aprobar")
	m, _ = send(t, m, key('a'))
	mustContain(t, m, "Confirme la aprobación", "Precio (USD):")
	m, _ = send(t, m, keyTab)
	m = typeText(t, m, "ayer")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "dato de precio inválido") // el formulario sigue abierto
	for range 4 {
		m, _ = send(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	m, _ = send(t, m, keyEnter)
	if len(f.approved) != 1 || f.approved[0] != (apppricing.ApproveInput{}) {
		t.Fatalf("aprobaciones: %+v", f.approved)
	}
	mustContain(t, m, "PP-000002 aprobada", "aprobada")
	if strings.Contains(content(m), "a aprobar") {
		t.Fatal("una propuesta aprobada no ofrece aprobarla de nuevo")
	}
}

func TestRejectAProposalAndCancel(t *testing.T) {
	f := newFake(all...)
	m := login(t, f)
	m, _ = send(t, m, key('4'))
	m, _ = send(t, m, keyEnter)
	m, _ = send(t, m, key('x'))
	m, _ = send(t, m, keyEsc) // cancelar
	mustContain(t, m, "rechazo cancelado", "Propuesta PP-000002")
	m, _ = send(t, m, key('x'))
	m = typeText(t, m, "muy alto")
	m, _ = send(t, m, keyEnter)
	if len(f.rejected) != 1 || f.rejected[0] != "muy alto" {
		t.Fatalf("rechazos: %v", f.rejected)
	}
	mustContain(t, m, "rechazada; el precio vigente no cambia", "Motivo", "muy alto")
}

func TestReadOnlyUserCannotDecide(t *testing.T) {
	f := newFake(identity.PermPriceRead)
	f.saved = true
	m := start(t, tui.Options{Backend: f})
	m, _ = send(t, m, key('2')) // Propuestas (Precios es la 1)
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "Propuesta PP-000002")
	m, _ = send(t, m, key('a'))
	if strings.Contains(content(m), "Confirme") || strings.Contains(content(m), "a aprobar") {
		t.Fatalf("sin permiso no debe ofrecer aprobar:\n%s", content(m))
	}
}

func TestLogout(t *testing.T) {
	f := newFake(all...)
	m := login(t, f)
	m, _ = send(t, m, key('7')) // Cerrar sesión
	if !f.loggedOut {
		t.Fatal("no cerró la sesión")
	}
	mustContain(t, m, "Iniciar sesión", "sesión cerrada")
}

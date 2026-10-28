package tui_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/gamijoam/inventario-go/internal/adapters/tui"
	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/application/purchasing"
	"github.com/gamijoam/inventario-go/internal/application/rates"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/exchange"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/purchase"
)

// ---------- Fake: compras y tasas ----------

type buyLog struct {
	suppliers []purchase.Supplier
	orders    []purchasing.OrderView
	receipts  []purchasing.ReceiptView
	orderIn   []purchasing.OrderInput
	receiptIn []purchasing.ReceiptInput
	lines     []purchasing.LineInput
	expenses  []purchasing.ExpenseInput
	confirmed int
	rates     []rates.SetInput
	rate      *exchange.Rate
	costs     []string
}

func (f *fake) buy() *buyLog {
	if f.blog == nil {
		f.blog = &buyLog{suppliers: []purchase.Supplier{{ID: id.MustNew(), Code: mustCode("DIST"), Name: mustName("Distribuidora"), Status: org.StatusActive}}}
	}
	return f.blog
}

func (f *fake) Suppliers(context.Context) ([]purchase.Supplier, error) { return f.buy().suppliers, nil }

func (f *fake) CreateSupplier(_ context.Context, code, name, rif string) (purchase.Supplier, error) {
	s := purchase.Supplier{ID: id.MustNew(), Code: mustCode(code), Name: mustName(name), Status: org.StatusActive}
	f.buy().suppliers = append(f.buy().suppliers, s)
	return s, nil
}

func (f *fake) Orders(_ context.Context, status purchase.OrderStatus) ([]purchasing.OrderView, error) {
	var out []purchasing.OrderView
	for _, o := range f.buy().orders {
		if status == "" || o.Status == status {
			out = append(out, o)
		}
	}
	return out, nil
}

func (f *fake) Order(_ context.Context, ref string) (purchasing.OrderView, error) {
	for _, o := range f.buy().orders {
		if o.Number == ref {
			return o, nil
		}
	}
	return purchasing.OrderView{}, errors.New("no encontrado: orden " + ref)
}

func (f *fake) CreateOrder(_ context.Context, in purchasing.OrderInput) (purchasing.OrderView, error) {
	f.buy().orderIn = append(f.buy().orderIn, in)
	o := purchasing.OrderView{Order: purchase.Order{ID: id.MustNew(), Number: fmt.Sprintf("OC-%06d", len(f.buy().orders)+1), Currency: money.USD,
		Status: purchase.OrderOpen}, Supplier: f.buy().suppliers[0], Products: map[id.ID]purchasing.LineView{harina.ID: {Product: harina, Unit: und}}}
	for i, l := range in.Lines {
		o.Lines = append(o.Lines, purchase.OrderLine{LineNo: i + 1, ProductID: harina.ID, Quantity: decimal.MustParse(l.Quantity), UnitCost: decimal.MustParse(l.UnitCost)})
	}
	f.buy().orders = append(f.buy().orders, o)
	return o, nil
}

func (f *fake) CancelOrder(_ context.Context, ref string) (purchasing.OrderView, error) {
	for i, o := range f.buy().orders {
		if o.Number == ref {
			o.Status = purchase.OrderCancelled
			f.buy().orders[i] = o
			return o, nil
		}
	}
	return purchasing.OrderView{}, errors.New("no encontrado")
}

func (f *fake) Receipts(_ context.Context, status purchase.ReceiptStatus) ([]purchasing.ReceiptView, error) {
	var out []purchasing.ReceiptView
	for _, r := range f.buy().receipts {
		if status == "" || r.Status == status {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fake) receiptIndex(ref string) (int, error) {
	for i, r := range f.buy().receipts {
		if r.Number == ref {
			return i, nil
		}
	}
	return -1, errors.New("no encontrado: recepción " + ref)
}

func (f *fake) Receipt(_ context.Context, ref string) (purchasing.ReceiptView, error) {
	i, err := f.receiptIndex(ref)
	if err != nil {
		return purchasing.ReceiptView{}, err
	}
	return f.buy().receipts[i], nil
}

func (f *fake) CreateReceipt(_ context.Context, in purchasing.ReceiptInput) (purchasing.ReceiptView, error) {
	f.buy().receiptIn = append(f.buy().receiptIn, in)
	cur := money.USD
	if in.Currency == "VES" {
		cur = money.VES
	}
	date := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	r := purchasing.ReceiptView{Receipt: purchase.Receipt{ID: id.MustNew(), Number: fmt.Sprintf("RC-%06d", len(f.buy().receipts)+1), Currency: cur,
		DocumentDate: date, Status: purchase.ReceiptDraft, SupplierDocument: in.Document}, Supplier: f.buy().suppliers[0],
		Location: f.balance().Location, Valuation: money.USD, Order: in.Order, Products: map[id.ID]purchasing.LineView{harina.ID: {Product: harina, Unit: und}},
		Rates: map[id.ID]exchange.Rate{}}
	f.buy().receipts = append(f.buy().receipts, r)
	return r, nil
}

func (f *fake) SetReceiptLine(_ context.Context, ref string, in purchasing.LineInput) (purchasing.ReceiptView, error) {
	i, err := f.receiptIndex(ref)
	if err != nil {
		return purchasing.ReceiptView{}, err
	}
	if !strings.EqualFold(in.Product, "HARINA") {
		return purchasing.ReceiptView{}, errors.New("no encontrado: producto " + in.Product)
	}
	f.buy().lines = append(f.buy().lines, in)
	r := &f.buy().receipts[i]
	r.Lines = []purchase.ReceiptLine{{LineNo: 1, ProductID: harina.ID, Quantity: decimal.MustParse(in.Quantity), UnitCost: decimal.MustParse(in.UnitCost)}}
	return *r, nil
}

func (f *fake) RemoveReceiptLine(_ context.Context, ref, product string) (purchasing.ReceiptView, error) {
	i, err := f.receiptIndex(ref)
	if err != nil {
		return purchasing.ReceiptView{}, err
	}
	f.buy().receipts[i].Lines = nil
	return f.buy().receipts[i], nil
}

func (f *fake) AddExpense(_ context.Context, ref string, in purchasing.ExpenseInput) (purchasing.ReceiptView, error) {
	i, err := f.receiptIndex(ref)
	if err != nil {
		return purchasing.ReceiptView{}, err
	}
	f.buy().expenses = append(f.buy().expenses, in)
	r := &f.buy().receipts[i]
	r.Expenses = append(r.Expenses, purchase.Expense{Concept: in.Concept, Amount: decimal.MustParse(in.Amount), Currency: money.USD,
		Criterion: purchase.Criterion(in.Criterion), Late: r.Status == purchase.ReceiptConfirmed})
	return *r, nil
}

func (f *fake) ConfirmReceipt(_ context.Context, ref string) (purchasing.ReceiptView, error) {
	i, err := f.receiptIndex(ref)
	if err != nil {
		return purchasing.ReceiptView{}, err
	}
	r := &f.buy().receipts[i]
	if r.Currency != r.Valuation && f.buy().rate == nil {
		return purchasing.ReceiptView{}, exchange.ErrNoRate
	}
	f.buy().confirmed++
	r.Status = purchase.ReceiptConfirmed
	if f.buy().rate != nil && r.Currency != r.Valuation {
		r.RateID = f.buy().rate.ID
		r.Rates[r.RateID] = *f.buy().rate
	}
	out := *r
	out.Suggestions = []apppricing.ProposalView{f.prop}
	return out, nil
}

func (f *fake) CancelReceipt(_ context.Context, ref string) (purchasing.ReceiptView, error) {
	i, err := f.receiptIndex(ref)
	if err != nil {
		return purchasing.ReceiptView{}, err
	}
	f.buy().receipts[i].Status = purchase.ReceiptCancelled
	return f.buy().receipts[i], nil
}

func (f *fake) Costs(_ context.Context, product string) (purchasing.CostView, error) {
	if !strings.EqualFold(product, "HARINA") && product != harina.ID.String() {
		return purchasing.CostView{}, errors.New("no encontrado: producto " + product)
	}
	v := purchasing.CostView{Company: f.balance().Location.Company, Product: harina, Unit: und, Balances: []stock.BalanceView{f.balance()}}
	for _, c := range f.buy().costs {
		v.Replacement = append([]purchase.ReplacementCost{{UnitCost: decimal.MustParse(c), Currency: money.USD}}, v.Replacement...)
	}
	return v, nil
}

func (f *fake) SetReplacementCost(_ context.Context, product, unitCost, currency, note string) (purchasing.CostView, error) {
	f.buy().costs = append(f.buy().costs, unitCost)
	return f.Costs(context.Background(), product)
}

func (f *fake) Rates(context.Context) ([]rates.RateView, error) {
	if r := f.buy().rate; r != nil {
		return []rates.RateView{{Rate: *r}}, nil
	}
	return nil, nil
}

func (f *fake) SetRate(_ context.Context, in rates.SetInput) (rates.RateView, error) {
	f.buy().rates = append(f.buy().rates, in)
	v, err := decimal.Parse(in.Rate)
	if err != nil {
		return rates.RateView{}, errors.New("dato de tasa inválido: tasa " + in.Rate)
	}
	r := exchange.Rate{ID: id.MustNew(), Base: money.USD, Quote: money.VES, Value: v, Source: money.RateSource{Type: money.SourceOfficial, Label: in.Label},
		ValidOn: time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC), Version: 1}
	f.buy().rate = &r
	return rates.RateView{Rate: r}, nil
}

func (f *fake) EffectiveRate(context.Context, string, string, string) (rates.RateView, error) {
	if r := f.buy().rate; r != nil {
		return rates.RateView{Rate: *r}, nil
	}
	return rates.RateView{}, exchange.ErrNoRate
}

// ---------- Pruebas ----------

var buyer = append([]identity.Permission{identity.PermPurchaseRead, identity.PermPurchaseManage, identity.PermReceiptConfirm,
	identity.PermRateRead, identity.PermRateManage, identity.PermReplacementCost}, all...)

// compras entra con sesión y abre el menú Compras.
func compras(t *testing.T, f *fake) tea.Model {
	t.Helper()
	f.saved = true
	m := start(t, tui.Options{Backend: f})
	m = keys(t, m, keyRight, keyRight)
	mustContain(t, m, "► Recepciones", "Órdenes de compra", "Proveedores", "Costos de un producto", "Tasas de cambio")
	return m
}

func TestPurchasesMenuFollowsPermissions(t *testing.T) {
	f := newFake(identity.PermRateRead)
	f.saved = true
	m := start(t, tui.Options{Backend: f})
	mustContain(t, m, "Compras", "► Tasas de cambio") // sin inventario, Compras es el primero
	if strings.Contains(content(m), "Recepciones") {
		t.Fatal("sin permiso de compras no hay recepciones")
	}
	m, _ = send(t, m, keyEnter)
	if strings.Contains(content(m), "F2 Registrar") {
		t.Fatal("solo lectura no registra tasas")
	}
}

func TestReceiptDocumentWithRateShortcut(t *testing.T) {
	f := newFake(buyer...)
	m := compras(t, f)
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "Recepciones (borradores)", "No hay recepciones.")
	m, _ = send(t, m, keyF2)
	mustContain(t, m, "Nueva recepción", "‹ DIST · Distribuidora ›", "‹ ninguna (compra directa) ›", "‹ la de valoración ›")
	m = keys(t, m, keyDown, keyDown, keyDown, keyRight, keyRight) // moneda VES
	mustContain(t, m, "‹ VES ›")
	m = keys(t, m, keyDown, keyDown)
	m = typeText(t, m, "FAC-1")
	m, _ = send(t, m, keyF10)
	in := f.buy().receiptIn
	if len(in) != 1 || in[0].Supplier != "DIST" || in[0].Currency != "VES" || in[0].Order != "" || in[0].Document != "FAC-1" || in[0].Where.Warehouse != "PRINCIPAL" {
		t.Fatalf("recepción: %+v", in)
	}
	mustContain(t, m, "Recepción RC-000001 · borrador", "recepción RC-000001 creada en borrador", "Factura FAC-1 del 2026-10-28 en VES",
		"Falta la tasa VES/USD vigente al 2026-10-28", "F6 Registrar tasa", "Sin líneas")
	if strings.Contains(content(m), "F9 Confirmar") {
		t.Fatal("sin líneas no se confirma")
	}

	m, _ = send(t, m, keyF2)
	m = typeText(t, m, "HARINA")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "30")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "en VES, sin gastos")
	m = typeText(t, m, "57")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "línea guardada en RC-000001", "HARINA", "30 UND", "1710.00 VES", "Total compra: 1710.00 VES", "F9 Confirmar", "Enter Editar")

	m, _ = send(t, m, keyEnter) // editar la línea, con sus datos
	mustContain(t, m, "Editar línea 1 de RC-000001", "57")
	m, _ = send(t, m, keyEsc)

	m, _ = send(t, m, tea.KeyPressMsg{Code: tea.KeyF6})
	mustContain(t, m, "Registrar tasa", "‹ USD ›", "‹ VES ›", "2026-10-28", "BCV")
	m = keys(t, m, keyDown, keyDown)
	m = typeText(t, m, "45.725")
	m, _ = send(t, m, keyF10)
	if got := f.buy().rates; len(got) != 1 || got[0].Date != "2026-10-28" || got[0].Source != "official" || got[0].Label != "BCV" || got[0].Base != "USD" {
		t.Fatalf("tasa: %+v", got)
	}
	mustContain(t, m, "tasa registrada: 1 USD = 45.725 VES", "Tasa: 1 USD = 45.725 VES (oficial BCV", "se aplica al confirmar", "≈ 37.40 USD")

	m, _ = send(t, m, keyF3)
	m = typeText(t, m, "Flete")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "2")
	m = keys(t, m, keyEnter, keyRight, keyF10) // moneda USD
	if got := f.buy().expenses; len(got) != 1 || got[0].Currency != "USD" || got[0].Criterion != "value" {
		t.Fatalf("gastos: %+v", got)
	}
	mustContain(t, m, "gasto registrado", "Gasto: Flete 2 USD, por valor; se prorratea al confirmar")

	m, _ = send(t, m, tea.KeyPressMsg{Code: tea.KeyF9})
	mustContain(t, m, "Confirmar RC-000001", "1 líneas por 1710.00 VES y 1 gastos", "Es definitivo")
	m, _ = send(t, m, keyEnter)
	if f.buy().confirmed != 1 {
		t.Fatal("no confirmó")
	}
	mustContain(t, m, "recepción RC-000001 confirmada: mercancía en PRINCIPAL; 1 propuestas de precio para aprobar",
		"Recepción RC-000001 · confirmada", "Tasa aplicada: 1 USD = 45.725 VES", "F3 Gasto tardío")
	if strings.Contains(content(m), "F2 Línea") || strings.Contains(content(m), "F9") {
		t.Fatal("una recepción confirmada no se edita")
	}
}

func TestReceiptWithoutConfirmPermission(t *testing.T) {
	f := newFake(identity.PermPurchaseRead, identity.PermPurchaseManage)
	f.saved = true
	f.buy().receipts = nil
	m := start(t, tui.Options{Backend: f})
	m = keys(t, m, keyEnter, keyF2, keyF10) // Compras es el primero
	m, _ = send(t, m, keyF2)
	m = typeText(t, m, "HARINA")
	m = keys(t, m, keyEnter)
	m = typeText(t, m, "1")
	m = keys(t, m, keyEnter)
	m = typeText(t, m, "2")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "Recepción RC-000001", "F2 Línea", "F4 Anular")
	if strings.Contains(content(m), "F9 Confirmar") || strings.Contains(content(m), "F6") {
		t.Fatalf("sin permiso de confirmar ni de tasas:\n%s", content(m))
	}
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyF9}, {Code: tea.KeyF6}} {
		m, _ = send(t, m, k)
		if v := content(m); strings.Contains(v, "Confirmar RC-000001") || strings.Contains(v, "Registrar tasa") {
			t.Fatalf("%s no debía abrir nada:\n%s", k, v)
		}
	}
	m, _ = send(t, m, tea.KeyPressMsg{Code: tea.KeyF8})
	mustContain(t, m, "Quitar HARINA")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "línea quitada", "Sin líneas")
	m, _ = send(t, m, keyF4)
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "recepción RC-000001 anulada", "anulada")
}

func TestOrderCreateAndReceive(t *testing.T) {
	f := newFake(buyer...)
	m := compras(t, f)
	m = keys(t, m, keyDown, keyEnter)
	mustContain(t, m, "Órdenes de compra (abiertas)", "No hay órdenes.")
	m, _ = send(t, m, keyF2)
	mustContain(t, m, "Nueva orden de compra", "‹ DIST · Distribuidora ›")
	m, _ = send(t, m, keyF10)
	mustContain(t, m, "Nueva orden · DIST", "Sin líneas todavía.")
	m, _ = send(t, m, keyF10)
	mustContain(t, m, "agregue al menos una línea")
	for _, l := range [][3]string{{"harina", "10", "9"}, {"HARINA", "12", "9.5"}} { // el segundo reemplaza
		m = typeText(t, m, l[0])
		m, _ = send(t, m, keyEnter)
		m = typeText(t, m, l[1])
		m, _ = send(t, m, keyEnter)
		m = typeText(t, m, l[2])
		m, _ = send(t, m, keyEnter)
	}
	mustContain(t, m, "HARINA", "12", "9.5", "1 en total")
	m, _ = send(t, m, keyF10)
	in := f.buy().orderIn
	if len(in) != 1 || in[0].Supplier != "DIST" || len(in[0].Lines) != 1 || in[0].Lines[0] != (purchasing.LineInput{Product: "HARINA", Quantity: "12", UnitCost: "9.5"}) {
		t.Fatalf("orden: %+v", in)
	}
	mustContain(t, m, "orden OC-000001 creada", "OC-000001", "abierta")

	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "Orden OC-000001 · abierta", "12", "9.5 USD", "F5 Recibir")
	m, _ = send(t, m, tea.KeyPressMsg{Code: tea.KeyF5})
	mustContain(t, m, "Nueva recepción", "‹ OC-000001 · Distribuidora ›", "‹ el de la orden ›")
	m, _ = send(t, m, keyF10)
	if got := f.buy().receiptIn; len(got) != 1 || got[0].Order != "OC-000001" || got[0].Supplier != "" {
		t.Fatalf("recepción contra la orden: %+v", got)
	}
	mustContain(t, m, "Recepción RC-000001", "orden OC-000001")
	m, _ = send(t, m, keyEsc)
	mustContain(t, m, "Orden OC-000001")
	m, _ = send(t, m, keyF4)
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "orden OC-000001 anulada", "anulada")
}

func TestSuppliersCostsAndRates(t *testing.T) {
	f := newFake(buyer...)
	m := compras(t, f)
	m = keys(t, m, keyDown, keyDown, keyEnter, keyF2)
	m = typeText(t, m, "POLAR")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "Alimentos Polar")
	m, _ = send(t, m, keyF10)
	mustContain(t, m, "proveedor creado: POLAR", "Alimentos Polar", "2 en total")

	m = keys(t, m, keyEsc, keyDown, keyEnter)
	mustContain(t, m, "Costos de un producto", "Indique un producto.")
	m = typeText(t, m, "NADA")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "no encontrado: producto NADA")
	for range 4 {
		m, _ = send(t, m, keyBack)
	}
	m = typeText(t, m, "HARINA")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "HARINA Harina de maíz 1 kg", "Costo promedio en PRINCIPAL: 8.800000 USD", "Sin compras confirmadas.",
		"Sin costo de reposición.", "F2 Costo de reposición")
	m, _ = send(t, m, keyF2)
	m = typeText(t, m, "9.9")
	m, _ = send(t, m, keyF10)
	mustContain(t, m, "costo de reposición registrado", "Reposición vigente: 9.9 USD")

	m = keys(t, m, keyEsc, keyDown, keyEnter)
	mustContain(t, m, "Tasas de cambio", "No hay tasas registradas.")
	m, _ = send(t, m, keyF2)
	m = keys(t, m, keyDown, keyDown)
	m = typeText(t, m, "abc")
	m, _ = send(t, m, keyF10)
	mustContain(t, m, "dato de tasa inválido")
	for range 3 {
		m, _ = send(t, m, keyBack)
	}
	m = typeText(t, m, "46.1")
	m, _ = send(t, m, keyF10)
	mustContain(t, m, "tasa registrada", "1 USD = 46.1 VES", "oficial BCV")
}

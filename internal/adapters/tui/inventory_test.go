package tui_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/gamijoam/inventario-go/internal/adapters/tui"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/org"
)

// ---------- Fake: inventario ----------

// invLog registra lo que la TUI pidió al backend de inventario.
type invLog struct {
	adjusts      []stock.AdjustInput
	transfers    []stock.TransferInput
	reversed     []string
	reserves     []stock.ReserveInput
	consumed     []string
	released     []string
	blocks       []stock.BlockInput
	closed       []string
	counted      []string
	submitted    int
	decided      []bool
	companyPol   []string
	productPol   []string
	reservations []stock.ReservationView
	blockList    []stock.BlockView
	count        *stock.CountView
}

func (f *fake) inv() *invLog {
	if f.log == nil {
		f.log = &invLog{}
	}
	return f.log
}

func (f *fake) Warehouses(context.Context) ([]org.Warehouse, error) {
	return []org.Warehouse{{Code: mustCode("PRINCIPAL"), Name: mustName("Principal"), Status: org.StatusActive},
		{Code: mustCode("DEPOSITO"), Name: mustName("Depósito"), Status: org.StatusActive},
		{Code: mustCode("VIEJO"), Name: mustName("Cerrado"), Status: org.StatusInactive}}, nil
}

func (f *fake) movement(product string, qty decimal.Decimal) stock.MovementView {
	b := f.balance()
	return stock.MovementView{Movement: inventory.Movement{Quantity: qty, OnHandAfter: b.OnHand.Add(qty)}, Product: harina, Unit: und, Location: b.Location}
}

func (f *fake) Adjust(_ context.Context, in stock.AdjustInput) (stock.MovementView, error) {
	f.inv().adjusts = append(f.inv().adjusts, in)
	q, err := decimal.Parse(in.Quantity)
	if err != nil || q.Sign() <= 0 {
		return stock.MovementView{}, errors.New("dato de inventario inválido: la cantidad debe ser positiva")
	}
	if in.Direction == inventory.Out {
		q = q.Neg()
	}
	return f.movement(in.Product, q), nil
}

func (f *fake) Reverse(_ context.Context, movementID, note, key string) (stock.MovementView, error) {
	f.inv().reversed = append(f.inv().reversed, movementID+"|"+note+"|"+key)
	m := f.movement("HARINA", ten.Neg())
	m.Kind = inventory.KindReversal
	return m, nil
}

func (f *fake) Transfer(_ context.Context, in stock.TransferInput) (stock.TransferView, error) {
	f.inv().transfers = append(f.inv().transfers, in)
	b := f.balance()
	dest := b.Location
	dest.Warehouse = org.Warehouse{Code: mustCode(in.To)}
	return stock.TransferView{In: inventory.Movement{Quantity: decimal.MustParse(in.Quantity)}, Product: harina, Unit: und,
		Origin: b.Location, Destination: dest}, nil
}

func (f *fake) Reservations(_ context.Context, status inventory.HoldStatus) ([]stock.ReservationView, error) {
	var out []stock.ReservationView
	for _, r := range f.inv().reservations {
		if status == "" || r.Status == status {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fake) Reserve(_ context.Context, in stock.ReserveInput) (stock.ReservationView, error) {
	f.inv().reserves = append(f.inv().reserves, in)
	r := stock.ReservationView{Reservation: inventory.Reservation{ID: id.MustNew(), Quantity: decimal.MustParse(in.Quantity),
		Status: inventory.HoldActive, Reference: in.Reference, ExpiresAt: in.ExpiresAt}, HoldView: stock.HoldView{Product: harina, Unit: und, Location: f.balance().Location}}
	f.inv().reservations = append(f.inv().reservations, r)
	return r, nil
}

func (f *fake) ConsumeReservation(_ context.Context, rid, quantity, note, key string) (stock.ReservationView, error) {
	f.inv().consumed = append(f.inv().consumed, rid+"|"+quantity+"|"+note)
	for i, r := range f.inv().reservations {
		if r.ID.String() == rid {
			q := r.Quantity
			if quantity != "" {
				q = decimal.MustParse(quantity)
			}
			r.Consumed = r.Consumed.Add(q)
			if r.Consumed.Equal(r.Quantity) {
				r.Status = inventory.HoldConsumed
			}
			f.inv().reservations[i] = r
			return r, nil
		}
	}
	return stock.ReservationView{}, errors.New("reserva no encontrada")
}

func (f *fake) ReleaseReservation(_ context.Context, rid, note string) (stock.ReservationView, error) {
	f.inv().released = append(f.inv().released, rid+"|"+note)
	for i, r := range f.inv().reservations {
		if r.ID.String() == rid {
			r.Status = inventory.HoldReleased
			f.inv().reservations[i] = r
			return r, nil
		}
	}
	return stock.ReservationView{}, errors.New("reserva no encontrada")
}

func (f *fake) Blocks(_ context.Context, status inventory.HoldStatus) ([]stock.BlockView, error) {
	var out []stock.BlockView
	for _, b := range f.inv().blockList {
		if status == "" || b.Status == status {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fake) Block(_ context.Context, in stock.BlockInput) (stock.BlockView, error) {
	f.inv().blocks = append(f.inv().blocks, in)
	b := stock.BlockView{Block: inventory.Block{ID: id.MustNew(), Quantity: decimal.MustParse(in.Quantity), Reason: inventory.BlockReason(in.Reason),
		Status: inventory.HoldActive, Note: in.Note, CreatedAt: time.Now()}, HoldView: stock.HoldView{Product: harina, Unit: und, Location: f.balance().Location}}
	f.inv().blockList = append(f.inv().blockList, b)
	return b, nil
}

func (f *fake) CloseBlock(_ context.Context, bid string, writeOff bool, note string) (stock.BlockView, error) {
	for i, b := range f.inv().blockList {
		if b.ID.String() == bid {
			f.inv().closed = append(f.inv().closed, note)
			b.Status = inventory.HoldReleased
			if writeOff {
				b.Status = inventory.HoldWrittenOff
			}
			f.inv().blockList[i] = b
			return b, nil
		}
	}
	return stock.BlockView{}, errors.New("bloqueo no encontrado")
}

func (f *fake) Counts(context.Context, inventory.CountStatus) ([]stock.CountView, error) {
	if c := f.inv().count; c != nil {
		return []stock.CountView{*c}, nil
	}
	return nil, nil
}

func (f *fake) CreateCount(_ context.Context, warehouse, note string) (stock.CountView, error) {
	loc := f.balance().Location
	loc.Warehouse = org.Warehouse{Code: mustCode(warehouse)}
	c := stock.CountView{Count: inventory.Count{ID: id.MustNew(), Status: inventory.CountDraft, Note: note}, Location: loc}
	f.inv().count = &c
	return c, nil
}

func (f *fake) Count(context.Context, string) (stock.CountView, error) { return *f.inv().count, nil }

func (f *fake) RecordCount(_ context.Context, cid, product, counted, unitCost string) (stock.CountLineView, error) {
	f.inv().counted = append(f.inv().counted, product+"="+counted)
	if !strings.EqualFold(product, "HARINA") && product != "7591234567894" {
		return stock.CountLineView{}, errors.New("no encontrado: producto " + product)
	}
	l := stock.CountLineView{CountLine: inventory.CountLine{Counted: decimal.MustParse(counted), Expected: decimal.MustParse("20")}, Product: harina, Unit: und}
	c := f.inv().count
	c.Lines = append(c.Lines, l)
	return l, nil
}

func (f *fake) SubmitCount(context.Context, string) (stock.CountView, error) {
	f.inv().submitted++
	f.inv().count.Status = inventory.CountInReview
	return *f.inv().count, nil
}

func (f *fake) DecideCount(_ context.Context, _ string, approve bool, note string) (stock.CountView, error) {
	f.inv().decided = append(f.inv().decided, approve)
	f.inv().count.Status, f.inv().count.DecisionNote = inventory.CountRejected, note
	if approve {
		f.inv().count.Status = inventory.CountApplied
	}
	return *f.inv().count, nil
}

func (f *fake) Policy(context.Context) (stock.PolicyView, error) {
	v := stock.PolicyView{Company: org.Company{Code: mustCode("PRINCIPAL")}}
	if len(f.inv().companyPol) > 0 && strings.HasPrefix(f.inv().companyPol[len(f.inv().companyPol)-1], "true") {
		v.Current.Company = inventory.CompanyPolicy{NegativeEnabled: true, Limit: decimal.MustParse("5")}
	}
	return v, nil
}

func (f *fake) SetCompanyPolicy(_ context.Context, enabled bool, limit string) (stock.PolicyView, error) {
	if enabled && limit == "" {
		return stock.PolicyView{}, errors.New("dato de inventario inválido: habilitar la existencia negativa exige un límite de déficit")
	}
	f.inv().companyPol = append(f.inv().companyPol, map[bool]string{true: "true", false: "false"}[enabled]+"|"+limit)
	return f.Policy(context.Background())
}

func (f *fake) SetProductPolicy(_ context.Context, product, mode, limit string) (stock.PolicyView, error) {
	f.inv().productPol = append(f.inv().productPol, product+"|"+mode+"|"+limit)
	return f.Policy(context.Background())
}

// ---------- Pruebas ----------

var stocker = append([]identity.Permission{identity.PermStockAdjust, identity.PermTransferCreate, identity.PermReservation,
	identity.PermBlock, identity.PermCountManage, identity.PermCountApprove, identity.PermPolicyManage}, all...)

// movements entra con sesión y abre el menú Movimientos.
func movements(t *testing.T, f *fake) tea.Model {
	t.Helper()
	f.saved = true
	m := start(t, tui.Options{Backend: f})
	m, _ = send(t, m, keyRight)
	mustContain(t, m, "Movimientos")
	return m
}

func TestMovementsMenuFollowsPermissions(t *testing.T) {
	f := newFake(identity.PermStockRead)
	m := movements(t, f)
	mustContain(t, m, "► Reservas", "Bloqueos")
	for _, hidden := range []string{"Entrada de mercancía", "Transferencia", "Conteos", "Política"} {
		if strings.Contains(content(m), hidden) {
			t.Errorf("sin permiso no debía mostrarse %q", hidden)
		}
	}
	m, _ = send(t, m, keyEnter)
	if strings.Contains(content(m), "F2 Nueva") {
		t.Fatal("solo lectura no crea reservas")
	}
	m = start(t, tui.Options{Backend: newFake(identity.PermCountApprove)}) // sin sesión guardada: login
	if strings.Contains(content(m), "Conteos") {
		t.Fatal("sin sesión no hay módulos")
	}
}

func TestEntryRecognizesProductAndKeepsItsKey(t *testing.T) {
	f := newFake(stocker...)
	m := movements(t, f)
	m, _ = send(t, m, keyEnter) // Entrada de mercancía
	mustContain(t, m, "Entrada de mercancía", "‹ PRINCIPAL · Principal ›", "F5 lista")
	m, _ = send(t, m, keyRight)
	mustContain(t, m, "‹ DEPOSITO · Depósito ›")
	m, _ = send(t, m, keyRight) // el inactivo no se ofrece: vuelve al primero
	mustContain(t, m, "‹ PRINCIPAL · Principal ›")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "Harina de")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "HARINA Harina de maíz 1 kg (UND) · disponible PRINCIPAL 20")
	m, _ = send(t, m, keyUp)
	for range len("Harina de") {
		m, _ = send(t, m, keyBack)
	}
	m = typeText(t, m, "1 kg")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "2 productos coinciden; pulse F5")

	// F5: lista de productos, con el filtro listo.
	m, _ = send(t, m, keyUp)
	m, _ = send(t, m, tea.KeyPressMsg{Code: tea.KeyF5})
	mustContain(t, m, "Elegir producto", "HARINA", "ARROZ")
	m = typeText(t, m, "arroz")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "Entrada de mercancía", "→ ARROZ Arroz 1 kg (UND)")

	m, _ = send(t, m, keyDown)
	m = typeText(t, m, "0")
	m = keys(t, m, keyEnter)
	m = typeText(t, m, "1.10")
	mustContain(t, m, "‹ Carga inicial de existencia ›")
	m = keys(t, m, keyEnter, keyEnter, keyEnter) // motivo, nota: guarda
	mustContain(t, m, "la cantidad debe ser positiva", "Entrada de mercancía")
	m = keys(t, m, keyUp, keyUp, keyUp, keyBack)
	m = typeText(t, m, "20")
	m, _ = send(t, m, keyF10)
	log := f.inv().adjusts
	if len(log) != 2 {
		t.Fatalf("ajustes: %+v", log)
	}
	got := log[1]
	if got.Where.Warehouse != "PRINCIPAL" || got.Product != "ARROZ" || got.Quantity != "20" || got.UnitCost != "1.10" ||
		got.Reason != "initial_load" || got.Direction != inventory.In || got.AllowNegative {
		t.Fatalf("entrada: %+v", got)
	}
	if got.IdempotencyKey == "" || got.IdempotencyKey != log[0].IdempotencyKey {
		t.Fatalf("el formulario debe conservar su clave al reintentar: %q %q", log[0].IdempotencyKey, got.IdempotencyKey)
	}
	mustContain(t, m, "entrada registrada: 20 UND HARINA en PRINCIPAL/PRINCIPAL/PRINCIPAL; existencia 40")

	m = keys(t, m, keyEnter, keyF10, keyEsc) // otro formulario: otra clave
	m = keys(t, m, keyEnter)
	m = typeText(t, m, "HARINA")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "1")
	m, _ = send(t, m, keyF10)
	if n := len(f.inv().adjusts); n < 3 || f.inv().adjusts[n-1].IdempotencyKey == got.IdempotencyKey {
		t.Fatal("cada formulario nuevo lleva una clave nueva")
	}
}

func TestExitOffersNegativeOnlyWithThePermission(t *testing.T) {
	for _, extra := range []bool{false, true} {
		perms := stocker
		if extra {
			perms = append([]identity.Permission{identity.PermNegativeOverride}, stocker...)
		}
		f := newFake(perms...)
		m := movements(t, f)
		m = keys(t, m, keyDown, keyEnter)
		mustContain(t, m, "Salida de mercancía", "‹ Corrección de un error de registro ›")
		if has := strings.Contains(content(m), "Permitir negativo"); has != extra {
			t.Fatalf("permiso extraordinario %v: campo visible %v", extra, has)
		}
		m, _ = send(t, m, keyDown)
		m = typeText(t, m, "HARINA")
		m, _ = send(t, m, keyEnter)
		m = typeText(t, m, "3")
		m, _ = send(t, m, keyEnter)
		m, _ = send(t, m, keyRight) // Merma
		m, _ = send(t, m, keyEnter)
		m = typeText(t, m, "se rompió")
		if extra {
			m = keys(t, m, keyEnter, keyRight)
		}
		m, _ = send(t, m, keyF10)
		got := f.inv().adjusts[0]
		if got.Direction != inventory.Out || got.Reason != "shrinkage" || got.Note != "se rompió" || got.AllowNegative != extra {
			t.Fatalf("salida: %+v", got)
		}
		mustContain(t, m, "salida registrada: -3 UND HARINA")
	}
}

func TestTransfer(t *testing.T) {
	f := newFake(stocker...)
	m := movements(t, f)
	m = keys(t, m, keyDown, keyDown, keyEnter)
	mustContain(t, m, "Transferencia entre almacenes", "Origen", "‹ PRINCIPAL · Principal ›", "‹ DEPOSITO · Depósito ›")
	m, _ = send(t, m, keyDown)
	m, _ = send(t, m, keyLeft) // destino = origen
	m, _ = send(t, m, keyDown)
	m = typeText(t, m, "HARINA")
	m, _ = send(t, m, keyDown)
	m = typeText(t, m, "5")
	m, _ = send(t, m, keyF10)
	mustContain(t, m, "el origen y el destino deben ser almacenes distintos")
	if len(f.inv().transfers) != 0 {
		t.Fatal("no debía llamar al backend")
	}
	m = keys(t, m, keyUp, keyUp, keyRight, keyF10)
	if len(f.inv().transfers) != 1 || f.inv().transfers[0].From != "PRINCIPAL" || f.inv().transfers[0].To != "DEPOSITO" || f.inv().transfers[0].IdempotencyKey == "" {
		t.Fatalf("transferencias: %+v", f.inv().transfers)
	}
	mustContain(t, m, "transferidos 5 UND de HARINA: PRINCIPAL → DEPOSITO")
}

func TestReverseFromKardex(t *testing.T) {
	f := newFake(stocker...)
	f.saved = true
	m := start(t, tui.Options{Backend: f})
	m = keys(t, m, keyDown, keyDown, keyEnter, keyEnter) // Existencias › kardex
	mustContain(t, m, "Kardex de HARINA", "F8 Revertir")
	m, _ = send(t, m, tea.KeyPressMsg{Code: tea.KeyF8})
	mustContain(t, m, "Revertir movimiento #2", "compra de 10 UND HARINA")
	m = typeText(t, m, "factura duplicada")
	m, _ = send(t, m, keyEnter)
	if len(f.inv().reversed) != 1 || !strings.HasPrefix(f.inv().reversed[0], kardexMove.String()+"|factura duplicada|") {
		t.Fatalf("reversos: %v", f.inv().reversed)
	}
	mustContain(t, m, "reverso registrada", "Kardex de HARINA")

	ro := newFake(identity.PermStockRead)
	ro.saved = true
	m = start(t, tui.Options{Backend: ro})
	m = keys(t, m, keyEnter, keyEnter, tea.KeyPressMsg{Code: tea.KeyF8})
	if strings.Contains(content(m), "Revertir") {
		t.Fatal("sin permiso de ajuste no se revierte")
	}
}

func TestReservationLifecycle(t *testing.T) {
	f := newFake(stocker...)
	m := movements(t, f)
	m = keys(t, m, keyDown, keyDown, keyDown, keyEnter)
	mustContain(t, m, "Reservas (activos)", "No hay reservas.", "F2 Nueva")
	m, _ = send(t, m, keyF2)
	m, _ = send(t, m, keyDown)
	m = typeText(t, m, "HARINA")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "4")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "PED-1024")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "mañana")
	m, _ = send(t, m, keyF10)
	mustContain(t, m, "use AAAA-MM-DD HH:MM")
	for range len("mañana") {
		m, _ = send(t, m, keyBack)
	}
	m = typeText(t, m, "2026-10-30 18:00")
	m, _ = send(t, m, keyF10)
	in := f.inv().reserves
	if len(in) != 1 || in[0].Reference != "PED-1024" || in[0].Quantity != "4" || in[0].ExpiresAt.UTC().Format(time.RFC3339) != "2026-10-30T22:00:00Z" {
		t.Fatalf("reservas: %+v", in)
	}
	mustContain(t, m, "reservados 4 UND de HARINA para PED-1024", "PED-1024", "2026-10-30 18:00", "F3 Despachar", "F4 Liberar")

	m, _ = send(t, m, keyF3)
	mustContain(t, m, "Despachar PED-1024", "pendiente 4 UND")
	m = typeText(t, m, "1")
	m, _ = send(t, m, keyF10)
	mustContain(t, m, "despachado PED-1024: 1 de 4 UND")
	m, _ = send(t, m, keyF4)
	m = typeText(t, m, "cliente canceló")
	m, _ = send(t, m, keyEnter)
	if len(f.inv().released) != 1 || !strings.HasSuffix(f.inv().released[0], "|cliente canceló") {
		t.Fatalf("liberadas: %v", f.inv().released)
	}
	mustContain(t, m, "reserva PED-1024 liberada", "No hay reservas.")
	m, _ = send(t, m, key('t'))
	mustContain(t, m, "PED-1024", "liberada", "T Solo activos")
	if strings.Contains(content(m), "F3 Despachar") {
		t.Fatal("una reserva liberada no se despacha")
	}
}

func TestBlockAndWriteOff(t *testing.T) {
	f := newFake(stocker...)
	m := movements(t, f)
	m = keys(t, m, keyDown, keyDown, keyDown, keyDown, keyEnter)
	mustContain(t, m, "Bloqueos (activos)")
	m, _ = send(t, m, keyF2)
	m, _ = send(t, m, keyDown)
	m = typeText(t, m, "HARINA")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "2")
	m = keys(t, m, keyEnter, keyRight) // Cuarentena
	mustContain(t, m, "‹ Cuarentena ›")
	m, _ = send(t, m, keyF10)
	if got := f.inv().blocks; len(got) != 1 || got[0].Reason != "quarantine" || got[0].Quantity != "2" {
		t.Fatalf("bloqueos: %+v", got)
	}
	mustContain(t, m, "bloqueados 2 UND de HARINA", "Cuarentena", "F4 Dar de baja")
	m, _ = send(t, m, keyF4)
	mustContain(t, m, "salen del almacén como merma")
	m = typeText(t, m, "vencida")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "dados de baja 2 UND de HARINA")
	if f.inv().blockList[0].Status != inventory.HoldWrittenOff {
		t.Fatal("debía darse de baja")
	}
}

func TestCountCaptureSubmitAndApprove(t *testing.T) {
	f := newFake(stocker...)
	m := movements(t, f)
	m = keys(t, m, keyUp, keyUp, keyEnter) // Conteos físicos
	mustContain(t, m, "Conteos físicos", "No hay conteos.")
	m, _ = send(t, m, keyF2)
	m, _ = send(t, m, keyDown)
	m = typeText(t, m, "pasillo 3")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "conteo creado en PRINCIPAL/PRINCIPAL/PRINCIPAL", "borrador", "Producto:", "F9 Enviar")

	m = typeText(t, m, "7591234567894") // lector de códigos
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "18")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "HARINA contado 18 (registrado 20, diferencia -2)", "-2")
	m = typeText(t, m, "NOEXISTE")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "1")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "no encontrado: producto NOEXISTE")
	if got := f.inv().counted; len(got) != 2 || got[0] != "7591234567894=18" {
		t.Fatalf("capturas: %v", got)
	}

	m, _ = send(t, m, tea.KeyPressMsg{Code: tea.KeyF9})
	mustContain(t, m, "Enviar a revisión", "con 1 líneas")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "conteo enviado a revisión", "en revisión", "A Aprobar")
	if strings.Contains(content(m), "Producto:") {
		t.Fatal("en revisión ya no se captura")
	}
	m, _ = send(t, m, key('a'))
	m = typeText(t, m, "ok")
	m, _ = send(t, m, keyEnter)
	if len(f.inv().decided) != 1 || !f.inv().decided[0] {
		t.Fatalf("decisiones: %v", f.inv().decided)
	}
	mustContain(t, m, "conteo aprobado; diferencias aplicadas", "aplicado", "Decisión: ok")
}

func TestNegativeStockPolicy(t *testing.T) {
	f := newFake(stocker...)
	m := movements(t, f)
	m = keys(t, m, keyUp, keyEnter)
	mustContain(t, m, "Política de existencia negativa", "deshabilitada", "Ninguno: todos siguen a la empresa.")
	m, _ = send(t, m, keyF2)
	m, _ = send(t, m, keyRight)
	m, _ = send(t, m, keyF10)
	mustContain(t, m, "habilitar la existencia negativa exige")
	m, _ = send(t, m, keyDown)
	m = typeText(t, m, "5")
	m, _ = send(t, m, keyF10)
	mustContain(t, m, "política de la empresa actualizada", "habilitada, hasta 5")
	m, _ = send(t, m, keyF3)
	m = typeText(t, m, "HARINA")
	m = keys(t, m, keyEnter, keyRight, keyF10) // prohíbe
	if got := f.inv().productPol; len(got) != 1 || got[0] != "HARINA|deny|" {
		t.Fatalf("política de producto: %v", got)
	}
	mustContain(t, m, "política del producto actualizada")
}

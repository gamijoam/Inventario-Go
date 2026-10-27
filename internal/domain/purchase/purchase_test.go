package purchase_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/exchange"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/purchase"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

var (
	now   = time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	today = timezone.DateOf(now)
)

func d(s string) decimal.Decimal { return decimal.MustParse(s) }

func ws(t *testing.T) scope.BusinessScope {
	t.Helper()
	s, err := scope.New(id.MustNew(), id.MustNew(), id.MustNew(), id.MustNew())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func line(n int, qty, cost string) purchase.ReceiptLine {
	// Sin cambio de moneda, lo valorado es cantidad × costo.
	return purchase.ReceiptLine{LineNo: n, ProductID: id.MustNew(), Quantity: d(qty), UnitCost: d(cost), Valued: d(qty).Mul(d(cost))}
}

func TestSupplier(t *testing.T) {
	s, err := purchase.NewSupplier(id.MustNew(), "distribuidora-1", "Distribuidora Uno, C.A.", "j123456789", now)
	if err != nil || s.Code.String() != "DISTRIBUIDORA-1" || s.RIF.String() != "J-12345678-9" {
		t.Fatal(s, err)
	}
	if _, err := purchase.NewSupplier(id.MustNew(), "x", " ", "", now); !errors.Is(err, purchase.ErrInvalid) {
		t.Errorf("sin nombre: %v", err)
	}
}

// Prorrateo por valor y por cantidad; el residuo va a la línea mayor y la
// suma cuadra exacta.
func TestAllocate(t *testing.T) {
	lines := []purchase.ReceiptLine{line(1, "10", "8"), line(2, "5", "19"), line(3, "1", "5")} // valores 80, 95, 5
	got, err := purchase.Allocate(lines, d("10"), purchase.ByValue)
	if err != nil {
		t.Fatal(err)
	}
	// 10 × 80/180 = 4.444444, 10 × 95/180 = 5.277778, 10 × 5/180 = 0.277778 → suma 10.000000.
	want := []string{"4.444444", "5.277778", "0.277778"}
	sum := decimal.Zero()
	for i, a := range got {
		if a.Amount.String() != want[i] || a.LineNo != lines[i].LineNo {
			t.Errorf("línea %d: %s", a.LineNo, a.Amount)
		}
		sum = sum.Add(a.Amount)
	}
	if sum.String() != "10" {
		t.Fatalf("suma %s", sum)
	}
	// Con residuo: 1 entre 3 partes iguales → la mayor (la primera) absorbe.
	eq := []purchase.ReceiptLine{line(1, "1", "1"), line(2, "1", "1"), line(3, "1", "1")}
	got, _ = purchase.Allocate(eq, d("1"), purchase.ByQuantity)
	if got[0].Amount.String() != "0.333334" || got[1].Amount.String() != "0.333333" || got[2].Amount.String() != "0.333333" {
		t.Fatalf("residuo: %v", got)
	}
	if _, err := purchase.Allocate([]purchase.ReceiptLine{line(1, "1", "0")}, d("5"), purchase.ByValue); !errors.Is(err, purchase.ErrInvalid) {
		t.Errorf("base cero por valor: %v", err)
	}
	if _, err := purchase.Allocate(nil, d("5"), purchase.ByValue); !errors.Is(err, purchase.ErrInvalid) {
		t.Errorf("sin líneas: %v", err)
	}
	if _, err := purchase.ParseCriterion("peso"); !errors.Is(err, purchase.ErrInvalid) {
		t.Errorf("criterio: %v", err)
	}
	if _, err := purchase.ParseAmount("-1"); !errors.Is(err, purchase.ErrInvalid) {
		t.Error("importe negativo")
	}
	if _, err := purchase.ParseAmount("0.1234567"); !errors.Is(err, purchase.ErrInvalid) {
		t.Error("más de 6 decimales")
	}
}

func TestOrderLifecycle(t *testing.T) {
	sc := ws(t)
	p1, p2 := id.MustNew(), id.MustNew()
	actor := id.MustNew()
	lines := []purchase.OrderLine{{ProductID: p1, Quantity: d("10"), UnitCost: d("80")}, {ProductID: p2, Quantity: d("5"), UnitCost: d("95")}}
	o, err := purchase.NewOrder(sc, "OC-000001", id.MustNew(), money.USD, lines, "", actor, now)
	if err != nil || o.Status != purchase.OrderOpen || o.Lines[1].LineNo != 2 {
		t.Fatal(o, err)
	}
	if _, err := purchase.NewOrder(sc, "OC-2", id.MustNew(), money.USD, append(lines, purchase.OrderLine{ProductID: p1, Quantity: d("1"), UnitCost: d("1")}), "", actor, now); !errors.Is(err, purchase.ErrInvalid) {
		t.Error("producto repetido")
	}
	if _, err := purchase.NewOrder(sc, "OC-2", id.MustNew(), money.USD, nil, "", actor, now); !errors.Is(err, purchase.ErrInvalid) {
		t.Error("sin líneas")
	}
	if o, err = o.Receive(1, d("4")); err != nil || o.Lines[0].Pending().String() != "6" || o.Status != purchase.OrderOpen {
		t.Fatal(o, err)
	}
	if _, err := o.Receive(1, d("7")); !errors.Is(err, purchase.ErrInvalid) {
		t.Error("recibir más de lo pendiente")
	}
	o, _ = o.Receive(1, d("6"))
	if o, _ = o.Receive(2, d("5")); o.Status != purchase.OrderReceived {
		t.Fatalf("orden completa: %v", o.Status)
	}
	if _, err := o.Cancel(actor, now); !errors.Is(err, purchase.ErrInvalid) {
		t.Error("cancelar una recibida")
	}
}

func TestReceiptLifecycle(t *testing.T) {
	actor := id.MustNew()
	r, err := purchase.NewReceipt(ws(t), "RC-000001", id.MustNew(), id.ID{}, money.USD, today, "FAC-77", "", actor, now)
	if err != nil || r.Status != purchase.ReceiptDraft {
		t.Fatal(r, err)
	}
	if _, err := r.Confirm(actor, now); !errors.Is(err, purchase.ErrInvalid) {
		t.Error("confirmar sin líneas")
	}
	l := line(0, "10", "8")
	if r, err = r.SetLine(l); err != nil || r.Lines[0].LineNo != 1 {
		t.Fatal(r, err)
	}
	l2 := l
	l2.Quantity = d("12")
	if r, _ = r.SetLine(l2); len(r.Lines) != 1 || r.Lines[0].Quantity.String() != "12" {
		t.Fatal("reemplazar la línea del mismo producto", r.Lines)
	}
	if _, err := r.SetLine(line(0, "0", "1")); !errors.Is(err, purchase.ErrInvalid) {
		t.Error("cantidad cero")
	}
	r, _ = r.SetLine(line(0, "3", "0"))
	if r.Lines[1].BaseCost().String() != "0" || r.Lines[0].BaseCost().String() != "96" {
		t.Fatal(r.Lines)
	}
	c, err := r.Confirm(actor, now)
	if err != nil || c.Status != purchase.ReceiptConfirmed {
		t.Fatal(c, err)
	}
	if _, err := c.SetLine(line(0, "1", "1")); !errors.Is(err, purchase.ErrInvalid) {
		t.Error("cambiar una confirmada")
	}
	if _, err := c.Cancel(actor, now); !errors.Is(err, purchase.ErrInvalid) {
		t.Error("cancelar una confirmada")
	}
}

// Una compra en VES en una empresa que valora en USD: cada línea se
// convierte con una sola redondeada a 6 decimales y la recepción guarda la
// tasa. Un gasto se convierte con la tasa de su fecha al registrarse.
func TestValuateInAnotherCurrency(t *testing.T) {
	actor := id.MustNew()
	r, err := purchase.NewReceipt(ws(t), "RC-1", id.MustNew(), id.ID{}, money.VES, today, "", "", actor, now)
	if err != nil {
		t.Fatal(err)
	}
	r, _ = r.SetLine(purchase.ReceiptLine{ProductID: id.MustNew(), Quantity: d("3"), UnitCost: d("33.333333")})
	r, _ = r.SetLine(purchase.ReceiptLine{ProductID: id.MustNew(), Quantity: d("2"), UnitCost: d("45.725")})
	if _, err := r.Valuate(money.USD, nil); !errors.Is(err, exchange.ErrNoRate) {
		t.Fatalf("sin tasa: %v", err)
	}
	rate, err := exchange.NewRate(id.MustNew(), id.MustNew(), exchange.RateInput{Base: money.USD, Quote: money.VES, Value: "45.725",
		Source: money.RateSource{Type: money.SourceOfficial, Label: "BCV"}, ValidOn: today}, actor, now)
	if err != nil {
		t.Fatal(err)
	}
	v, err := r.Valuate(money.USD, &rate)
	if err != nil || v.RateID != rate.ID {
		t.Fatal(v, err)
	}
	// 99.999999 / 45.725 = 2.186987402952... → 2.186987; 91.45 / 45.725 = 2.
	if v.Lines[0].Valued.String() != "2.186987" || v.Lines[1].Valued.String() != "2" || v.Lines[1].TotalCost().String() != "2" {
		t.Fatalf("valorado: %v / %v", v.Lines[0].Valued, v.Lines[1].Valued)
	}
	if r.Lines[0].Valued.Sign() != 0 {
		t.Error("Valuate modificó el original")
	}
	same, err := purchase.NewReceipt(ws(t), "RC-2", id.MustNew(), id.ID{}, money.USD, today, "", "", actor, now)
	if err != nil {
		t.Fatal(err)
	}
	same, _ = same.SetLine(purchase.ReceiptLine{ProductID: id.MustNew(), Quantity: d("3"), UnitCost: d("0.3333333")})
	if same, err = same.Valuate(money.USD, nil); err != nil || !same.RateID.IsZero() || same.Lines[0].Valued.String() != "1" {
		t.Fatalf("misma moneda: %v %v", same.Lines, err)
	}

	e, err := purchase.NewExpense(v, "Flete", d("91.45"), money.VES, today, purchase.ByValue, money.USD, &rate, actor, now)
	if err != nil || e.Valued.String() != "2" || e.RateID != rate.ID || e.Currency != money.VES {
		t.Fatalf("gasto en VES: %+v %v", e, err)
	}
	if _, err := purchase.NewExpense(v, "Flete", d("1"), money.VES, today, purchase.ByValue, money.USD, nil, actor, now); !errors.Is(err, exchange.ErrNoRate) {
		t.Errorf("gasto sin tasa: %v", err)
	}
	if _, err := purchase.NewExpense(v, "Flete", d("0.000001"), money.VES, today, purchase.ByValue, money.USD, &rate, actor, now); !errors.Is(err, purchase.ErrInvalid) {
		t.Errorf("gasto que convertido es cero: %v", err)
	}
	if _, err := purchase.NewReceipt(ws(t), "RC-3", id.MustNew(), id.ID{}, money.USD, today.AddDate(0, 0, 1), "", "", actor, now); !errors.Is(err, purchase.ErrInvalid) {
		t.Errorf("fecha futura: %v", err)
	}
	if _, err := purchase.NewReceipt(ws(t), "RC-3", id.MustNew(), id.ID{}, money.Currency{}, today, "", "", actor, now); !errors.Is(err, purchase.ErrInvalid) {
		t.Errorf("sin moneda: %v", err)
	}
}

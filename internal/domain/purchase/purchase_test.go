package purchase_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/purchase"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

var now = time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)

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
	return purchase.ReceiptLine{LineNo: n, ProductID: id.MustNew(), Quantity: d(qty), UnitCost: d(cost)}
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
	o, err := purchase.NewOrder(sc, "OC-000001", id.MustNew(), lines, "", actor, now)
	if err != nil || o.Status != purchase.OrderOpen || o.Lines[1].LineNo != 2 {
		t.Fatal(o, err)
	}
	if _, err := purchase.NewOrder(sc, "OC-2", id.MustNew(), append(lines, purchase.OrderLine{ProductID: p1, Quantity: d("1"), UnitCost: d("1")}), "", actor, now); !errors.Is(err, purchase.ErrInvalid) {
		t.Error("producto repetido")
	}
	if _, err := purchase.NewOrder(sc, "OC-2", id.MustNew(), nil, "", actor, now); !errors.Is(err, purchase.ErrInvalid) {
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
	r, err := purchase.NewReceipt(ws(t), "RC-000001", id.MustNew(), id.ID{}, "FAC-77", "", actor, now)
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

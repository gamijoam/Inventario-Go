package inventory_test

import (
	"errors"
	"testing"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
)

func d(s string) decimal.Decimal { return decimal.MustParse(s) }

func atUnit(s string) inventory.Costing { return inventory.Costing{UnitCost: inventory.Known(d(s))} }

func fixed(s string) inventory.Costing { return inventory.Costing{Fixed: inventory.Known(d(s))} }

// ledger aplica movimientos sobre un saldo y guarda su kardex.
type ledger struct {
	t      *testing.T
	b      inventory.Balance
	kardex []inventory.Movement
}

func newLedger(t *testing.T) *ledger { return &ledger{t: t, b: inventory.Balance{Key: key(t)}} }

func (l *ledger) apply(qty string, c inventory.Costing) inventory.Movement {
	l.t.Helper()
	b, m, err := inventory.Apply(l.b, mv(l.b.Key, qty), c, now)
	if err != nil {
		l.t.Fatalf("%s: %v", qty, err)
	}
	l.b, l.kardex = b, append(l.kardex, m)
	return m
}

func (l *ledger) try(qty string, c inventory.Costing) error {
	_, _, err := inventory.Apply(l.b, mv(l.b.Key, qty), c, now)
	return err
}

func (l *ledger) want(onHand, value string) {
	l.t.Helper()
	if l.b.OnHand.String() != onHand || !l.b.Value.Known || l.b.Value.Value.String() != value {
		l.t.Fatalf("saldo %s / %v, se esperaba %s / %s", l.b.OnHand, l.b.Value, onHand, value)
	}
	if err := inventory.Replay(l.b, l.kardex); err != nil {
		l.t.Fatalf("replay: %v", err)
	}
}

// Sección 19: 10 a 80 y 5 a 95 → CPP 85; una salida de 3 cuesta 255.
func TestMovingAverageFromTheMasterDocument(t *testing.T) {
	l := newLedger(t)
	l.apply("10", atUnit("80"))
	l.apply("5", atUnit("95"))
	l.want("15", "1275")
	if cpp := l.b.UnitCost(); !cpp.Known || cpp.Value.String() != "85" {
		t.Fatalf("CPP = %v", cpp)
	}
	out := l.apply("-3", inventory.Costing{})
	if out.Cost.Value.String() != "-255" || out.UnitCost.Value.String() != "85" || !out.ValueAdjustment.IsZero() {
		t.Fatalf("salida = %+v", out)
	}
	l.want("12", "1020")
	// Una entrada sin costo declarado entra al CPP vigente.
	in := l.apply("2", inventory.Costing{})
	if in.Cost.Value.String() != "170" || in.UnitCost.Value.String() != "85" {
		t.Fatalf("entrada al CPP = %+v", in)
	}
	l.want("14", "1190")
}

func TestRoundingNeverLeavesPhantomValue(t *testing.T) {
	l := newLedger(t)
	l.apply("3", atUnit("3.333333"))
	l.want("3", "9.999999")
	// Valor 10/3 por unidad: cada salida se redondea a 6 decimales.
	l.apply("1", atUnit("0.000001"))
	l.want("4", "10")
	for range 3 {
		if m := l.apply("-1", inventory.Costing{}); m.Cost.Value.String() != "-2.5" {
			t.Fatalf("costo = %s", m.Cost.Value)
		}
	}
	// La salida que agota se lleva exactamente el valor restante.
	last := l.apply("-1", inventory.Costing{})
	if last.Cost.Value.String() != "-2.5" || !last.ValueAfter.Value.IsZero() {
		t.Fatalf("última salida = %+v", last)
	}
	l.want("0", "0")

	r := newLedger(t)
	r.apply("3", atUnit("1"))
	r.apply("-1", inventory.Costing{}) // 1
	r.apply("1", atUnit("0.5"))        // 2.5 por 3
	m := r.apply("-1", inventory.Costing{})
	if m.Cost.Value.String() != "-0.833333" || m.UnitCost.Value.String() != "0.833333333333" {
		t.Fatalf("redondeo de la salida = %+v", m)
	}
	r.want("2", "1.666667")
	m = r.apply("-2", inventory.Costing{})
	if m.Cost.Value.String() != "-1.666667" {
		t.Fatalf("la última salida toma el resto: %s", m.Cost.Value)
	}
	r.want("0", "0")
}

func TestEntriesNeedACostWhenThereIsNoCurrentAverage(t *testing.T) {
	l := newLedger(t)
	if err := l.try("5", inventory.Costing{}); !errors.Is(err, inventory.ErrCostRequired) || !errors.Is(err, inventory.ErrInvalid) {
		t.Fatalf("carga inicial sin costo: %v", err)
	}
	l.apply("5", atUnit("2"))
	l.apply("-5", inventory.Costing{})
	if err := l.try("1", inventory.Costing{}); !errors.Is(err, inventory.ErrCostRequired) {
		t.Fatalf("existencia en cero sin costo: %v", err)
	}
	if err := l.try("1", atUnit("-1")); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatalf("costo negativo: %v", err)
	}
	// Costo cero declarado es válido (mercancía recibida sin costo).
	l.apply("1", atUnit("0"))
	l.want("1", "0")
}

func TestFixedCostReversesExactlyOrTakesTheRest(t *testing.T) {
	l := newLedger(t)
	l.apply("10", atUnit("10"))
	wrong := l.apply("10", atUnit("100")) // costo mal digitado
	l.want("20", "1100")
	// El reverso saca el valor que entró, no el CPP mezclado.
	rev := l.apply("-10", fixed(wrong.Cost.Value.Neg().String()))
	if rev.Cost.Value.String() != "-1000" || !rev.ValueAdjustment.IsZero() {
		t.Fatalf("reverso = %+v", rev)
	}
	l.want("10", "100")

	// Si el reverso agota la existencia, se lleva el resto y la diferencia
	// queda a la vista como ajuste de valor.
	k := newLedger(t)
	in := k.apply("10", atUnit("1"))
	k.apply("10", atUnit("100"))
	k.apply("-10", inventory.Costing{})
	k.want("10", "505")
	m := k.apply("-10", fixed(in.Cost.Value.Neg().String()))
	if m.Cost.Value.String() != "-10" || m.ValueAdjustment.String() != "-495" || !m.ValueAfter.Value.IsZero() {
		t.Fatalf("reverso que agota = %+v", m)
	}
	k.want("0", "0")

	// Sin agotar, el valor no puede quedar negativo.
	n := newLedger(t)
	n.apply("10", atUnit("100"))
	n.apply("10", atUnit("1"))
	n.apply("-15", inventory.Costing{}) // quedan 5 por 252.5
	if err := n.try("-1", fixed("-1000")); !errors.Is(err, inventory.ErrValuation) {
		t.Fatalf("valor negativo: %v", err)
	}
	if err := n.try("-1", fixed("3")); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatalf("costo con signo contrario: %v", err)
	}
}

// Sección 21.1: A 15×85, B 6×100; transferir 4 de A a B.
func TestTransferConservesValue(t *testing.T) {
	a, b := newLedger(t), newLedger(t)
	a.apply("15", atUnit("85"))
	b.apply("6", atUnit("100"))
	out := a.apply("-4", inventory.Costing{})
	b.apply("4", fixed(out.Cost.Value.Neg().String()))
	a.want("11", "935")
	b.want("10", "940")
	if cpp := b.b.UnitCost(); cpp.Value.String() != "94" {
		t.Fatalf("CPP destino = %v", cpp)
	}
	total := a.b.Value.Value.Add(b.b.Value.Value)
	if total.String() != "1875" {
		t.Fatalf("valor total = %s", total)
	}
}

// Existencia cargada antes de BP-M2-002: cantidad sin valor conocido.
func legacy(t *testing.T, onHand string) *ledger {
	l := newLedger(t)
	m := mv(l.b.Key, onHand)
	m.Sequence, m.OnHandAfter = 1, d(onHand)
	l.kardex = []inventory.Movement{m}
	l.b.OnHand, l.b.Version = d(onHand), 1
	return l
}

func TestUnvaluedStockGetsValuedByTheNextCostedEntry(t *testing.T) {
	l := legacy(t, "10")
	if err := inventory.Replay(l.b, l.kardex); err != nil {
		t.Fatalf("el kardex sin valor es válido: %v", err)
	}
	if l.b.UnitCost().Known {
		t.Fatal("sin valor no hay CPP")
	}
	// Las salidas siguen sin costo conocido.
	out := l.apply("-2", inventory.Costing{})
	if out.Cost.Known || out.ValueAfter.Known {
		t.Fatalf("salida sin valorar = %+v", out)
	}
	if err := l.try("1", inventory.Costing{}); !errors.Is(err, inventory.ErrCostRequired) {
		t.Fatalf("entrada sin costo en saldo sin valorar: %v", err)
	}
	// La entrada con costo valora también las 8 que había.
	in := l.apply("2", atUnit("5"))
	if in.Cost.Value.String() != "10" || in.ValueAdjustment.String() != "40" || in.ValueAfter.Value.String() != "50" {
		t.Fatalf("entrada que valora = %+v", in)
	}
	l.want("10", "50")
	// Ya valorado, nunca vuelve a quedar sin valor.
	if m := l.apply("-1", inventory.Costing{}); m.Cost.Value.String() != "-5" {
		t.Fatalf("salida valorada = %+v", m)
	}
	l.want("9", "45")

	// Un saldo sin valorar que llega a cero vale cero.
	z := legacy(t, "3")
	if m := z.apply("-3", inventory.Costing{}); m.Cost.Known || !m.ValueAfter.Known || !m.ValueAfter.Value.IsZero() {
		t.Fatalf("agotar sin valor = %+v", m)
	}
	z.want("0", "0")
	// Reverso de una salida sin costo mientras el saldo sigue sin valorar.
	u := legacy(t, "4")
	u.apply("-1", inventory.Costing{})
	if m := u.apply("1", inventory.Costing{AllowUnknown: true}); m.Cost.Known || m.ValueAfter.Known {
		t.Fatalf("reverso sin valor = %+v", m)
	}
	if err := inventory.Replay(u.b, u.kardex); err != nil {
		t.Fatal(err)
	}
	// Una transferencia no mezcla valor conocido con existencia sin valorar.
	if err := u.try("1", fixed("10")); !errors.Is(err, inventory.ErrValuation) {
		t.Fatalf("entrada de costo fijo en saldo sin valorar: %v", err)
	}
	if err := u.try("-1", fixed("-10")); !errors.Is(err, inventory.ErrValuation) {
		t.Fatalf("salida de costo fijo en saldo sin valorar: %v", err)
	}
}

func TestReplayDetectsValueTampering(t *testing.T) {
	l := newLedger(t)
	l.apply("10", atUnit("2"))
	l.apply("-4", inventory.Costing{})
	l.apply("1", atUnit("3"))
	l.want("7", "15")
	clone := func() []inventory.Movement { return append([]inventory.Movement(nil), l.kardex...) }
	cases := map[string]func() (inventory.Balance, []inventory.Movement){
		"valor del saldo alterado": func() (inventory.Balance, []inventory.Movement) {
			b := l.b
			b.Value = inventory.Known(d("16"))
			return b, clone()
		},
		"saldo sin valor": func() (inventory.Balance, []inventory.Movement) {
			b := l.b
			b.Value = inventory.Amount{}
			return b, clone()
		},
		"costo editado": func() (inventory.Balance, []inventory.Movement) {
			c := clone()
			c[1].Cost = inventory.Known(d("-7"))
			return l.b, c
		},
		"valor intermedio editado": func() (inventory.Balance, []inventory.Movement) {
			c := clone()
			c[1].ValueAfter = inventory.Known(d("13"))
			return l.b, c
		},
		"costo borrado tras valorar": func() (inventory.Balance, []inventory.Movement) {
			c := clone()
			c[2].Cost, c[2].UnitCost = inventory.Amount{}, inventory.Amount{}
			return l.b, c
		},
		"ajuste inventado": func() (inventory.Balance, []inventory.Movement) {
			c := clone()
			c[2].ValueAdjustment = d("1")
			c[2].ValueAfter = inventory.Known(d("16"))
			b := l.b
			b.Value = c[2].ValueAfter
			return b, c
		},
	}
	for name, mk := range cases {
		b, k := mk()
		if err := inventory.Replay(b, k); !errors.Is(err, inventory.ErrReplayMismatch) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseUnitCost(t *testing.T) {
	for _, c := range []struct {
		s  string
		ok bool
	}{{"0", true}, {"85", true}, {"8.333333333333", true}, {"8.3333333333333", false}, {"-1", false}, {"1e2", false}, {"", false}, {"1234567890123456789012", false}} {
		if _, err := inventory.ParseUnitCost(c.s); (err == nil) != c.ok {
			t.Errorf("%q: %v", c.s, err)
		}
	}
}

func TestTransferInput(t *testing.T) {
	from, to := key(t), key(t)
	if _, err := inventory.NewTransfer(from.Scope, from.Scope, id.MustNew(), d("1"), "", id.MustNew(), now); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatalf("mismo almacén: %v", err)
	}
	if _, err := inventory.NewTransfer(from.Scope, to.Scope, id.MustNew(), d("1"), "", id.MustNew(), now); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatalf("otra sucursal: %v", err)
	}
}

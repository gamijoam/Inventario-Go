package inventory_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
)

func overdraft(limit string) inventory.Costing {
	return inventory.Costing{Overdraft: inventory.Known(d(limit))}
}

// Sección 24.7: existencia 0, último costo 8; salida excepcional de 2 deja
// −2 con costo provisional 16. Llegan 5 a 10: 2 liquidan el déficit con una
// diferencia de 4 y quedan 3 a 10.
func TestDeficitIsValuedAtTheLastValidCostAndSettledByTheNextEntry(t *testing.T) {
	l := newLedger(t)
	l.apply("2", atUnit("8"))
	l.apply("-2", inventory.Costing{})
	if last := l.b.LastUnitCost; !last.Known || last.Value.String() != "8" {
		t.Fatalf("último costo = %v", last)
	}
	if err := l.try("-2", inventory.Costing{}); !errors.Is(err, inventory.ErrInsufficient) {
		t.Fatalf("sin excepción no hay negativo: %v", err)
	}
	out := l.apply("-2", overdraft("5"))
	if out.Cost.Value.String() != "-16" || out.OnHandAfter.String() != "-2" || out.ValueAfter.Value.String() != "-16" {
		t.Fatalf("salida en déficit = %+v", out)
	}
	l.want("-2", "-16")
	if l.b.UnitCost().Known {
		t.Fatal("sin existencia positiva no hay CPP vigente")
	}
	// Sin costo declarado no hay CPP con qué entrar.
	if err := l.try("1", inventory.Costing{}); !errors.Is(err, inventory.ErrCostRequired) {
		t.Fatalf("entrada sin costo en déficit: %v", err)
	}
	in := l.apply("5", atUnit("10"))
	if in.Cost.Value.String() != "50" || in.ValueAdjustment.String() != "-4" || in.ValueAfter.Value.String() != "30" {
		t.Fatalf("entrada que liquida = %+v", in)
	}
	l.want("3", "30")
	if cpp := l.b.UnitCost(); cpp.Value.String() != "10" {
		t.Fatalf("CPP tras liquidar = %v", cpp)
	}
}

func TestDeficitLimitAndCommitments(t *testing.T) {
	l := newLedger(t)
	l.apply("3", atUnit("2"))
	// Sale lo positivo a su valor y el resto al último costo.
	m := l.apply("-5", overdraft("2"))
	if m.Cost.Value.String() != "-10" || m.ValueAfter.Value.String() != "-4" {
		t.Fatalf("salida mixta = %+v", m)
	}
	l.want("-2", "-4")
	if err := l.try("-1", overdraft("2")); !errors.Is(err, inventory.ErrInsufficient) {
		t.Fatalf("superar el límite: %v", err)
	}
	if err := l.try("-1", overdraft("3")); err != nil {
		t.Fatalf("dentro del límite: %v", err)
	}
	// Una entrada que no alcanza a cubrir el déficit lo reduce a su costo real.
	part := l.apply("1", atUnit("5"))
	if part.ValueAdjustment.String() != "-3" || part.ValueAfter.Value.String() != "-2" {
		t.Fatalf("liquidación parcial = %+v", part)
	}
	l.want("-1", "-2")

	// Con unidades reservadas o bloqueadas no se sale más allá de lo libre.
	r := newLedger(t)
	r.apply("5", atUnit("1"))
	r.b.Reserved = d("2")
	if err := r.try("-4", overdraft("10")); !errors.Is(err, inventory.ErrInsufficient) {
		t.Fatalf("salida sobre lo reservado: %v", err)
	}
	// Un conteo corrige la realidad aunque deje lo reservado sin respaldo,
	// pero nunca deja existencia negativa.
	if err := r.try("-4", inventory.Costing{Correction: true}); err != nil {
		t.Fatalf("corrección por conteo: %v", err)
	}
	if err := r.try("-6", inventory.Costing{Correction: true}); !errors.Is(err, inventory.ErrInsufficient) {
		t.Fatalf("corrección que deja negativo: %v", err)
	}
	// Sin costo previo no se puede valorar un déficit.
	n := legacy(t, "1")
	if err := n.try("-3", overdraft("5")); !errors.Is(err, inventory.ErrValuation) {
		t.Fatalf("déficit sin valor: %v", err)
	}
}

// El reverso de una salida en déficit la deshace exactamente.
func TestReversingADeficitExitRestoresTheValue(t *testing.T) {
	l := newLedger(t)
	l.apply("2", atUnit("8"))
	out := l.apply("-5", overdraft("5"))
	l.want("-3", "-24")
	l.apply("5", fixed(out.Cost.Value.Neg().String()))
	l.want("2", "16")
	if cpp := l.b.UnitCost(); cpp.Value.String() != "8" {
		t.Fatalf("CPP = %v", cpp)
	}
}

func TestReplayChecksSettlementAndLastCost(t *testing.T) {
	l := newLedger(t)
	l.apply("2", atUnit("8"))
	l.apply("-4", overdraft("5"))
	l.apply("5", atUnit("10"))
	l.want("3", "30")
	c := append([]inventory.Movement(nil), l.kardex...)
	c[2].ValueAdjustment = d("-5")
	c[2].ValueAfter = inventory.Known(d("29"))
	b := l.b
	b.Value = c[2].ValueAfter
	if err := inventory.Replay(b, c); !errors.Is(err, inventory.ErrReplayMismatch) {
		t.Fatalf("liquidación alterada: %v", err)
	}
	// El costo provisional debe ser el último CPP válido del kardex.
	c = append([]inventory.Movement(nil), l.kardex...)
	c[1].Cost = inventory.Known(d("-34"))
	c[1].ValueAfter = inventory.Known(d("-18"))
	if err := inventory.Replay(l.b, c); !errors.Is(err, inventory.ErrReplayMismatch) {
		t.Fatalf("costo provisional alterado: %v", err)
	}
}

func TestReservationAndBlockArithmetic(t *testing.T) {
	// ST-01: 100 físicas, 5 bloqueadas y 20 reservadas → 75 disponibles.
	b := inventory.Balance{OnHand: d("100"), Reserved: d("20"), Blocked: d("5")}
	if b.Available().String() != "75" {
		t.Fatal(b.Available())
	}
	if _, err := inventory.Commit(b, d("76"), d("0")); !errors.Is(err, inventory.ErrInsufficient) {
		t.Fatalf("ST-02 reservar más que el disponible: %v", err)
	}
	r, err := inventory.Commit(b, d("20"), d("0"))
	if err != nil || r.Reserved.String() != "40" || r.Available().String() != "55" || r.OnHand.String() != "100" {
		t.Fatalf("reservar: %+v %v", r, err)
	}
	r, err = inventory.Commit(r, d("-20"), d("5"))
	if err != nil || r.Reserved.String() != "20" || r.Blocked.String() != "10" {
		t.Fatalf("liberar y bloquear: %+v %v", r, err)
	}
	if _, err := inventory.Commit(r, d("-21"), d("0")); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatalf("liberar más de lo reservado: %v", err)
	}
	// Con déficit no se reserva ni se bloquea nada.
	neg := inventory.Balance{OnHand: d("-1")}
	if _, err := inventory.Commit(neg, d("0"), d("1")); !errors.Is(err, inventory.ErrInsufficient) {
		t.Fatalf("bloquear en déficit: %v", err)
	}
}

func TestNegativePolicy(t *testing.T) {
	off := inventory.CompanyPolicy{}
	on := inventory.CompanyPolicy{NegativeEnabled: true, Limit: d("5")}
	cases := []struct {
		name    string
		company inventory.CompanyPolicy
		product inventory.ProductPolicy
		limit   string // vacío: no permitido
	}{
		{"NG-01 empresa por defecto", off, inventory.ProductPolicy{}, ""},
		{"producto permitido no salta a la empresa", off, inventory.ProductPolicy{Mode: inventory.NegativeAllow, Limit: d("3")}, ""},
		{"NG-02 empresa habilitada, producto bloqueado", on, inventory.ProductPolicy{Mode: inventory.NegativeDeny}, ""},
		{"hereda el límite de la empresa", on, inventory.ProductPolicy{}, "5"},
		{"límite propio del producto", on, inventory.ProductPolicy{Mode: inventory.NegativeAllow, Limit: d("2")}, "2"},
	}
	for _, c := range cases {
		limit, ok := inventory.NegativeLimit(c.company, c.product)
		if got := map[bool]string{true: limit.String(), false: ""}[ok]; got != c.limit {
			t.Errorf("%s: %q", c.name, got)
		}
	}
	if _, err := inventory.NewCompanyPolicy(true, ""); !errors.Is(err, inventory.ErrInvalid) {
		t.Error("habilitar sin límite")
	}
	if _, err := inventory.NewProductPolicy("allow", "", 0); !errors.Is(err, inventory.ErrInvalid) {
		t.Error("permitir sin límite")
	}
	if _, err := inventory.NewProductPolicy("allow", "1.5", 0); !errors.Is(err, inventory.ErrInvalid) {
		t.Error("límite con más decimales que la unidad")
	}
	if p, err := inventory.NewProductPolicy("deny", "", 0); err != nil || p.Mode != inventory.NegativeDeny {
		t.Error(p, err)
	}
}

func TestHoldLifecycles(t *testing.T) {
	k := key(t)
	ana, beto := id.MustNew(), id.MustNew()
	if _, err := inventory.NewReservation(k, d("1"), " ", "", time.Time{}, ana, now); !errors.Is(err, inventory.ErrInvalid) {
		t.Error("reserva sin referencia")
	}
	if _, err := inventory.NewReservation(k, d("1"), "PED-1", "", now.Add(-time.Minute), ana, now); !errors.Is(err, inventory.ErrInvalid) {
		t.Error("vencimiento pasado")
	}
	r, err := inventory.NewReservation(k, d("5"), "PED-1", "", now.Add(time.Hour), ana, now)
	if err != nil || r.Due(now) || !r.Due(now.Add(time.Hour)) {
		t.Fatal(r, err)
	}
	if _, err := r.Consume(d("6"), ana, now); !errors.Is(err, inventory.ErrInvalid) {
		t.Error("despachar más de lo reservado")
	}
	r, _ = r.Consume(d("2"), ana, now)
	if r.Status != inventory.HoldActive || r.Remaining().String() != "3" {
		t.Fatal(r)
	}
	if r, _ = r.Consume(d("3"), ana, now); r.Status != inventory.HoldConsumed {
		t.Fatal(r)
	}
	if _, err := r.Close(inventory.HoldReleased, ana, "", now); !errors.Is(err, inventory.ErrInvalid) {
		t.Error("liberar una reserva consumida")
	}
	reason, err := inventory.ParseBlockReason(" Quarantine ")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := inventory.NewBlock(k, d("1"), reason, "", ana, now)
	if b, err = b.Close(inventory.HoldWrittenOff, ana, "", now); err != nil || b.Status != inventory.HoldWrittenOff {
		t.Fatal(b, err)
	}
	if _, err := b.Close(inventory.HoldReleased, ana, "", now); !errors.Is(err, inventory.ErrInvalid) {
		t.Error("cerrar dos veces")
	}

	c, _ := inventory.NewCount(k, "", ana, now)
	bal := inventory.Balance{Key: k, OnHand: d("100"), Version: 7}
	if _, err := inventory.NewCountLine(c, bal, "-1", 0, inventory.Amount{}, ana, now); !errors.Is(err, inventory.ErrInvalid) {
		t.Error("conteo negativo")
	}
	line, err := inventory.NewCountLine(c, bal, "96", 0, inventory.Amount{}, ana, now)
	if err != nil || line.Difference().String() != "-4" || line.ExpectedVersion != 7 {
		t.Fatal(line, err)
	}
	if _, err := c.Submit(0, ana, now); !errors.Is(err, inventory.ErrInvalid) {
		t.Error("enviar vacío")
	}
	c, _ = c.Submit(1, ana, now)
	if _, err := inventory.NewCountLine(c, bal, "1", 0, inventory.Amount{}, ana, now); !errors.Is(err, inventory.ErrInvalid) {
		t.Error("capturar fuera de borrador")
	}
	if _, err := c.Decide(true, []inventory.CountLine{line}, ana, "", now); !errors.Is(err, inventory.ErrSameCounter) {
		t.Errorf("aprobar lo propio: %v", err)
	}
	if c, err = c.Decide(true, []inventory.CountLine{line}, beto, "", now); err != nil || c.Status != inventory.CountApplied {
		t.Fatal(c, err)
	}
	if _, err := c.Decide(false, nil, beto, "", now); !errors.Is(err, inventory.ErrInvalid) {
		t.Error("decidir dos veces")
	}
}

func TestRevaluationAddsValueWithoutQuantity(t *testing.T) {
	l := newLedger(t)
	l.apply("10", atUnit("8"))
	b, m, err := inventory.Revalue(l.b, mv(l.b.Key, "0"), d("5"), now)
	if err != nil || m.Kind != inventory.KindRevaluation || !m.Quantity.IsZero() || m.ValueAfter.Value.String() != "85" || b.UnitCost().Value.String() != "8.5" {
		t.Fatalf("revalorar: %+v %v", m, err)
	}
	l.b, l.kardex = b, append(l.kardex, m)
	l.want("10", "85")
	// Una revaloración alterada no cuadra.
	c := append([]inventory.Movement(nil), l.kardex...)
	c[1].ValueAdjustment = d("6")
	if err := inventory.Replay(l.b, c); !errors.Is(err, inventory.ErrReplayMismatch) {
		t.Fatalf("replay: %v", err)
	}
	if _, _, err := inventory.Revalue(inventory.Balance{Key: l.b.Key}, mv(l.b.Key, "0"), d("5"), now); !errors.Is(err, inventory.ErrValuation) {
		t.Errorf("sin existencia: %v", err)
	}
}

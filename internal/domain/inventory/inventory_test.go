package inventory_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

var now = time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)

func key(t *testing.T) inventory.Key {
	t.Helper()
	s, err := scope.New(id.MustNew(), id.MustNew(), id.MustNew(), id.MustNew())
	if err != nil {
		t.Fatal(err)
	}
	return inventory.Key{Scope: s, ProductID: id.MustNew()}
}

func mv(k inventory.Key, qty string) inventory.Movement {
	return inventory.Movement{ID: id.MustNew(), Key: k, Quantity: decimal.MustParse(qty)}
}

func TestApplyKeepsBalanceAndKardexInStep(t *testing.T) {
	k := key(t)
	b := inventory.Balance{Key: k}
	var kardex []inventory.Movement
	for _, q := range []string{"10", "-3.5", "0.125", "-6.625"} {
		var m inventory.Movement
		var err error
		b, m, err = inventory.Apply(b, mv(k, q), now)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		kardex = append(kardex, m)
	}
	if !b.OnHand.IsZero() || b.Version != 4 || kardex[1].Sequence != 2 || kardex[1].OnHandAfter.String() != "6.5" {
		t.Fatalf("saldo=%+v kardex=%+v", b, kardex)
	}
	if err := inventory.Replay(b, kardex); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestOutboundCannotExceedAvailable(t *testing.T) {
	k := key(t)
	b, _, _ := inventory.Apply(inventory.Balance{Key: k}, mv(k, "5"), now)
	if _, _, err := inventory.Apply(b, mv(k, "-5.001"), now); !errors.Is(err, inventory.ErrInsufficient) {
		t.Fatalf("salida mayor: %v", err)
	}
	b.Reserved, b.Blocked = decimal.MustParse("1"), decimal.MustParse("2")
	if b.Available().String() != "2" {
		t.Fatalf("disponible = %s", b.Available())
	}
	if _, _, err := inventory.Apply(b, mv(k, "-3"), now); !errors.Is(err, inventory.ErrInsufficient) {
		t.Fatalf("lo reservado y bloqueado no se puede sacar: %v", err)
	}
	if b2, _, err := inventory.Apply(b, mv(k, "-2"), now); err != nil || b2.OnHand.String() != "3" {
		t.Fatalf("salida del disponible: %+v %v", b2, err)
	}
	if _, _, err := inventory.Apply(b, mv(k, "0"), now); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatalf("cero: %v", err)
	}
	if _, _, err := inventory.Apply(b, mv(key(t), "1"), now); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatalf("otro saldo: %v", err)
	}
}

func TestReplayDetectsTampering(t *testing.T) {
	k := key(t)
	b := inventory.Balance{Key: k}
	var kardex []inventory.Movement
	for _, q := range []string{"10", "-4", "2"} {
		var m inventory.Movement
		b, m, _ = inventory.Apply(b, mv(k, q), now)
		kardex = append(kardex, m)
	}
	clone := func() []inventory.Movement { return append([]inventory.Movement(nil), kardex...) }

	cases := map[string]func() (inventory.Balance, []inventory.Movement){
		"saldo alterado": func() (inventory.Balance, []inventory.Movement) {
			bb := b
			bb.OnHand = decimal.MustParse("9")
			return bb, clone()
		},
		"movimiento borrado": func() (inventory.Balance, []inventory.Movement) {
			c := clone()
			return b, append(c[:1], c[2:]...)
		},
		"cantidad editada": func() (inventory.Balance, []inventory.Movement) {
			c := clone()
			c[1].Quantity = decimal.MustParse("-3")
			return b, c
		},
		"versión adelantada": func() (inventory.Balance, []inventory.Movement) {
			bb := b
			bb.Version = 4
			return bb, clone()
		},
		"movimiento ajeno": func() (inventory.Balance, []inventory.Movement) {
			c := clone()
			c[2].Key = key(t)
			return b, c
		},
	}
	for name, mk := range cases {
		bb, kk := mk()
		if err := inventory.Replay(bb, kk); !errors.Is(err, inventory.ErrReplayMismatch) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := inventory.Replay(b, kardex); err != nil {
		t.Fatalf("control: %v", err)
	}
}

func TestReasonsAndInputs(t *testing.T) {
	if r, err := inventory.ParseReason(" Initial_Load ", inventory.In); err != nil || r != inventory.ReasonInitialLoad {
		t.Fatal(r, err)
	}
	for _, c := range []struct {
		s string
		d inventory.Direction
	}{{"initial_load", inventory.Out}, {"shrinkage", inventory.In}, {"internal_use", inventory.In}, {"reversal", inventory.In}, {"robo", inventory.Out}} {
		if _, err := inventory.ParseReason(c.s, c.d); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("%s en %v aceptado", c.s, c.d)
		}
	}
	if _, err := inventory.ParseNote(" ", inventory.ReasonOther); !errors.Is(err, inventory.ErrInvalid) {
		t.Error("other sin nota")
	}
	if n, err := inventory.ParseNote(" rotura \n en estante ", inventory.ReasonShrinkage); err != nil || n != "rotura \n en estante" {
		t.Error(n, err)
	}
	for _, c := range []struct {
		s     string
		scale int32
		ok    bool
	}{{"3", 0, true}, {"0.125", 3, true}, {"0.1250", 3, true}, {"0.1251", 3, false}, {"1.5", 0, false}, {"0", 0, false}, {"-2", 0, false}, {"1e3", 0, false}, {"abc", 0, false}} {
		if _, err := inventory.ParseQuantity(c.s, c.scale); (err == nil) != c.ok {
			t.Errorf("%q escala %d: %v", c.s, c.scale, err)
		}
	}
}

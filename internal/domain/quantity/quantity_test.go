package quantity_test

import (
	"errors"
	"testing"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/quantity"
)

var (
	unit = mustUnit("UND", 0)
	kg   = mustUnit("KG", 3)
)

func mustUnit(code string, scale int32) quantity.Unit {
	u, err := quantity.NewUnit(code, scale)
	if err != nil {
		panic(err)
	}
	return u
}

func TestNewUnitValidates(t *testing.T) {
	for _, code := range []string{"", "kg", "K G", "DEMASIADO_LARGO_PARA_UNA_UNIDAD"} {
		if _, err := quantity.NewUnit(code, 0); !errors.Is(err, quantity.ErrInvalidUnit) {
			t.Errorf("NewUnit(%q) debía fallar, obtuvo %v", code, err)
		}
	}
	for _, scale := range []int32{-1, 7} {
		if _, err := quantity.NewUnit("KG", scale); !errors.Is(err, quantity.ErrInvalidUnit) {
			t.Errorf("escala %d debía fallar, obtuvo %v", scale, err)
		}
	}
}

func TestParseRespectsUnitPrecision(t *testing.T) {
	q, err := quantity.Parse("0.125", kg)
	if err != nil {
		t.Fatal(err)
	}
	if got := q.String(); got != "0.125 KG" {
		t.Fatalf("String() = %q", got)
	}
	if _, err := quantity.Parse("0.1255", kg); !errors.Is(err, quantity.ErrPrecisionExceeded) {
		t.Errorf("0.1255 KG debía exceder precisión, obtuvo %v", err)
	}
	if _, err := quantity.Parse("1.5", unit); !errors.Is(err, quantity.ErrPrecisionExceeded) {
		t.Errorf("1.5 UND debía exceder precisión, obtuvo %v", err)
	}
	if _, err := quantity.Parse("2.000", unit); err != nil {
		t.Errorf("2.000 UND equivale a 2 y es válido: %v", err)
	}
	if _, err := quantity.Parse("1", quantity.Unit{}); !errors.Is(err, quantity.ErrInvalidUnit) {
		t.Errorf("unidad vacía debía fallar, obtuvo %v", err)
	}
	if _, err := quantity.Parse("1e3", kg); !errors.Is(err, decimal.ErrInvalidDecimal) {
		t.Errorf("se esperaba ErrInvalidDecimal, obtuvo %v", err)
	}
}

func TestAddSubSameUnit(t *testing.T) {
	a, _ := quantity.Parse("1.250", kg)
	b, _ := quantity.Parse("0.375", kg)
	sum, err := a.Add(b)
	if err != nil || sum.String() != "1.625 KG" {
		t.Fatalf("Add = %s, %v", sum, err)
	}
	diff, err := b.Sub(a)
	if err != nil || diff.String() != "-0.875 KG" || !diff.IsNegative() {
		t.Fatalf("Sub = %s, %v (los saldos firmados pueden ser negativos)", diff, err)
	}
}

func TestMixingUnitsWithoutConversionFails(t *testing.T) {
	a, _ := quantity.Parse("1", unit)
	b, _ := quantity.Parse("1", kg)
	if _, err := a.Add(b); !errors.Is(err, quantity.ErrUnitMismatch) {
		t.Errorf("Add: se esperaba ErrUnitMismatch, obtuvo %v", err)
	}
	if _, err := a.Sub(b); !errors.Is(err, quantity.ErrUnitMismatch) {
		t.Errorf("Sub: se esperaba ErrUnitMismatch, obtuvo %v", err)
	}
	if _, err := a.Cmp(b); !errors.Is(err, quantity.ErrUnitMismatch) {
		t.Errorf("Cmp: se esperaba ErrUnitMismatch, obtuvo %v", err)
	}
}

func TestCmpAndZero(t *testing.T) {
	a, _ := quantity.Parse("3", unit)
	c, err := quantity.Zero(unit).Cmp(a)
	if err != nil || c != -1 {
		t.Fatalf("Cmp = %d, %v", c, err)
	}
	if !quantity.Zero(kg).IsZero() || quantity.Zero(kg).Unit() != kg {
		t.Fatalf("Zero inconsistente")
	}
	if !a.Equal(a) || a.Neg().Equal(a) {
		t.Fatalf("Equal inconsistente")
	}
}

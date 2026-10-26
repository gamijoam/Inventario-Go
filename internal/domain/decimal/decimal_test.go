package decimal_test

import (
	"errors"
	"testing"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
)

func TestParseAcceptsCanonicalDecimals(t *testing.T) {
	cases := map[string]string{
		"0":        "0",
		"0.10":     "0.1",
		"12.000":   "12",
		"-3.25":    "-3.25",
		"45.725":   "45.725",
		"-0":       "0",
		"1000000":  "1000000",
		"0.000001": "0.000001",
	}
	for in, want := range cases {
		d, err := decimal.Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q) error inesperado: %v", in, err)
		}
		if got := d.String(); got != want {
			t.Errorf("Parse(%q).String() = %q, se esperaba %q", in, got, want)
		}
	}
}

func TestParseRejectsAmbiguousInput(t *testing.T) {
	invalid := []string{
		"", " ", "1,5", "1e3", "+1", ".5", "5.", "007", "1.2.3",
		"NaN", "Inf", " 1", "1 ", "0x10", "١",
		"0.0000000000000000001", // supera MaxScale
	}
	for _, in := range invalid {
		if _, err := decimal.Parse(in); !errors.Is(err, decimal.ErrInvalidDecimal) {
			t.Errorf("Parse(%q) debía fallar con ErrInvalidDecimal, obtuvo %v", in, err)
		}
	}
}

func TestAdditionIsExact(t *testing.T) {
	sum := decimal.MustParse("0.10").Add(decimal.MustParse("0.20"))
	if !sum.Equal(decimal.MustParse("0.30")) {
		t.Fatalf("0.10 + 0.20 = %s, se esperaba 0.30", sum)
	}
	if got := sum.StringFixed(2); got != "0.30" {
		t.Fatalf("StringFixed(2) = %q, se esperaba 0.30", got)
	}
}

func TestArithmetic(t *testing.T) {
	a := decimal.MustParse("12.5")
	b := decimal.MustParse("3.75")
	if got := a.Sub(b).String(); got != "8.75" {
		t.Errorf("Sub = %s", got)
	}
	if got := a.Mul(b).String(); got != "46.875" {
		t.Errorf("Mul = %s", got)
	}
	if got := a.Neg().String(); got != "-12.5" {
		t.Errorf("Neg = %s", got)
	}
	if got := a.Neg().Abs().String(); got != "12.5" {
		t.Errorf("Abs = %s", got)
	}
	if a.Cmp(b) != 1 || b.Cmp(a) != -1 || a.Cmp(a) != 0 {
		t.Errorf("Cmp inconsistente")
	}
	if !decimal.Zero().IsZero() || decimal.Zero().Sign() != 0 {
		t.Errorf("Zero no es cero")
	}
	if !a.Neg().IsNegative() || a.IsNegative() {
		t.Errorf("IsNegative inconsistente")
	}
	if got := decimal.FromInt(-42).String(); got != "-42" {
		t.Errorf("FromInt = %s", got)
	}
}

func TestScaleIgnoresTrailingZeros(t *testing.T) {
	cases := map[string]int32{"12": 0, "12.50": 1, "0.125": 3, "-0.001": 3, "100": 0}
	for in, want := range cases {
		if got := decimal.MustParse(in).Scale(); got != want {
			t.Errorf("Scale(%q) = %d, se esperaba %d", in, got, want)
		}
	}
}

func TestRoundModes(t *testing.T) {
	cases := []struct {
		in    string
		scale int32
		mode  decimal.RoundingMode
		want  string
	}{
		{"2.345", 2, decimal.HalfAwayFromZero, "2.35"},
		{"-2.345", 2, decimal.HalfAwayFromZero, "-2.35"},
		{"2.345", 2, decimal.HalfEven, "2.34"},
		{"2.355", 2, decimal.HalfEven, "2.36"},
		{"-2.345", 2, decimal.HalfEven, "-2.34"},
		{"2.349", 2, decimal.TowardZero, "2.34"},
		{"-2.349", 2, decimal.TowardZero, "-2.34"},
		{"548.700", 2, decimal.HalfAwayFromZero, "548.7"},
	}
	for _, c := range cases {
		got := decimal.MustParse(c.in).Round(c.scale, c.mode)
		if got.String() != c.want {
			t.Errorf("Round(%s, %d, %v) = %s, se esperaba %s", c.in, c.scale, c.mode, got, c.want)
		}
	}
}

func TestDivRequiresExplicitPolicy(t *testing.T) {
	cases := []struct {
		a, b  string
		scale int32
		mode  decimal.RoundingMode
		want  string
	}{
		{"100", "12", 12, decimal.HalfAwayFromZero, "8.333333333333"},
		{"2", "3", 2, decimal.HalfAwayFromZero, "0.67"},
		{"-2", "3", 2, decimal.HalfAwayFromZero, "-0.67"},
		{"2", "3", 2, decimal.TowardZero, "0.66"},
		{"1", "8", 2, decimal.HalfEven, "0.12"},
		{"3", "8", 2, decimal.HalfEven, "0.38"},
		{"-1", "8", 2, decimal.HalfEven, "-0.12"},
		{"1", "8", 2, decimal.HalfAwayFromZero, "0.13"},
		{"1", "-8", 2, decimal.HalfAwayFromZero, "-0.13"},
		{"10", "4", 0, decimal.HalfEven, "2"},
		{"6", "3", 4, decimal.HalfEven, "2"},
	}
	for _, c := range cases {
		got, err := decimal.MustParse(c.a).Div(decimal.MustParse(c.b), c.scale, c.mode)
		if err != nil {
			t.Fatalf("Div(%s, %s) error: %v", c.a, c.b, err)
		}
		if got.String() != c.want {
			t.Errorf("Div(%s, %s, %d, %v) = %s, se esperaba %s", c.a, c.b, c.scale, c.mode, got, c.want)
		}
	}
}

func TestDivByZeroFails(t *testing.T) {
	_, err := decimal.FromInt(1).Div(decimal.Zero(), 2, decimal.HalfEven)
	if !errors.Is(err, decimal.ErrDivisionByZero) {
		t.Fatalf("se esperaba ErrDivisionByZero, obtuvo %v", err)
	}
}

func TestInvalidRoundingModeIsRejected(t *testing.T) {
	_, err := decimal.FromInt(1).Div(decimal.FromInt(3), 2, decimal.RoundingMode(99))
	if !errors.Is(err, decimal.ErrInvalidRoundingMode) {
		t.Fatalf("se esperaba ErrInvalidRoundingMode, obtuvo %v", err)
	}
}

func FuzzParseRoundTrip(f *testing.F) {
	for _, seed := range []string{"0", "0.10", "-3.25", "45.725", "1e3", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := decimal.Parse(s)
		if err != nil {
			return
		}
		again, err := decimal.Parse(d.String())
		if err != nil {
			t.Fatalf("String() de %q no es parseable: %q (%v)", s, d.String(), err)
		}
		if !again.Equal(d) {
			t.Fatalf("ida y vuelta cambió el valor: %q -> %q", s, again)
		}
	})
}

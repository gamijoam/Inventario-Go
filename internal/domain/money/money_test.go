package money_test

import (
	"errors"
	"testing"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/money"
)

func usd(t *testing.T, s string) money.Money {
	t.Helper()
	m, err := money.Parse(s, money.USD)
	if err != nil {
		t.Fatalf("Parse(%q, USD): %v", s, err)
	}
	return m
}

func TestNewCurrencyValidatesCodeAndScale(t *testing.T) {
	if _, err := money.NewCurrency("EUR", 2); err != nil {
		t.Fatalf("EUR válido: %v", err)
	}
	for _, code := range []string{"", "usd", "US", "USDT", "U$D"} {
		if _, err := money.NewCurrency(code, 2); !errors.Is(err, money.ErrInvalidCurrency) {
			t.Errorf("NewCurrency(%q) debía fallar, obtuvo %v", code, err)
		}
	}
	for _, scale := range []int32{-1, 9} {
		if _, err := money.NewCurrency("EUR", scale); !errors.Is(err, money.ErrInvalidCurrency) {
			t.Errorf("escala %d debía fallar, obtuvo %v", scale, err)
		}
	}
}

func TestBuiltInCurrencies(t *testing.T) {
	if money.USD.Code() != "USD" || money.USD.Scale() != 2 {
		t.Errorf("USD = %v", money.USD)
	}
	if money.VES.Code() != "VES" || money.VES.Scale() != 2 {
		t.Errorf("VES = %v", money.VES)
	}
}

func TestZeroCurrencyIsRejected(t *testing.T) {
	if _, err := money.New(decimal.FromInt(1), money.Currency{}); !errors.Is(err, money.ErrInvalidCurrency) {
		t.Fatalf("se esperaba ErrInvalidCurrency, obtuvo %v", err)
	}
	if _, err := money.Parse("1", money.Currency{}); !errors.Is(err, money.ErrInvalidCurrency) {
		t.Fatalf("se esperaba ErrInvalidCurrency, obtuvo %v", err)
	}
}

func TestParseRejectsInvalidAmount(t *testing.T) {
	if _, err := money.Parse("1,50", money.USD); !errors.Is(err, decimal.ErrInvalidDecimal) {
		t.Fatalf("se esperaba ErrInvalidDecimal, obtuvo %v", err)
	}
}

func TestAddIsExact(t *testing.T) {
	sum, err := usd(t, "0.10").Add(usd(t, "0.20"))
	if err != nil {
		t.Fatal(err)
	}
	if !sum.Equal(usd(t, "0.30")) {
		t.Fatalf("0.10 + 0.20 = %s, se esperaba 0.30 USD", sum)
	}
	if got := sum.String(); got != "0.30 USD" {
		t.Fatalf("String() = %q", got)
	}
}

func TestMixingCurrenciesWithoutConversionFails(t *testing.T) {
	ves, _ := money.Parse("548.70", money.VES)
	if _, err := usd(t, "12").Add(ves); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Errorf("Add: se esperaba ErrCurrencyMismatch, obtuvo %v", err)
	}
	if _, err := usd(t, "12").Sub(ves); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Errorf("Sub: se esperaba ErrCurrencyMismatch, obtuvo %v", err)
	}
	if _, err := usd(t, "12").Cmp(ves); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Errorf("Cmp: se esperaba ErrCurrencyMismatch, obtuvo %v", err)
	}
	if usd(t, "0").Equal(ves) {
		t.Errorf("Equal entre monedas distintas debe ser false")
	}
}

func TestSubAndCmp(t *testing.T) {
	diff, err := usd(t, "10").Sub(usd(t, "12.5"))
	if err != nil {
		t.Fatal(err)
	}
	if !diff.IsNegative() || diff.Amount().String() != "-2.5" {
		t.Fatalf("10 - 12.5 = %s", diff)
	}
	c, err := usd(t, "1").Cmp(usd(t, "2"))
	if err != nil || c != -1 {
		t.Fatalf("Cmp = %d, %v", c, err)
	}
}

func TestMulKeepsFullPrecisionUntilRounded(t *testing.T) {
	line := usd(t, "1.99").Mul(decimal.MustParse("0.125"))
	if got := line.Amount().String(); got != "0.24875" {
		t.Fatalf("Mul = %s, no debe redondear implícitamente", got)
	}
	settled, err := line.Round(decimal.HalfAwayFromZero)
	if err != nil {
		t.Fatal(err)
	}
	if got := settled.String(); got != "0.25 USD" {
		t.Fatalf("Round = %s", got)
	}
}

func TestRoundUsesCurrencyScale(t *testing.T) {
	m := usd(t, "2.345")
	cases := map[decimal.RoundingMode]string{
		decimal.HalfAwayFromZero: "2.35 USD",
		decimal.HalfEven:         "2.34 USD",
		decimal.TowardZero:       "2.34 USD",
	}
	for mode, want := range cases {
		got, err := m.Round(mode)
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != want {
			t.Errorf("Round(%v) = %s, se esperaba %s", mode, got, want)
		}
	}
	if _, err := m.Round(decimal.RoundingMode(0)); !errors.Is(err, decimal.ErrInvalidRoundingMode) {
		t.Errorf("modo inválido debía fallar, obtuvo %v", err)
	}
}

func TestStringShowsFullPrecisionWhenNotSettled(t *testing.T) {
	if got := usd(t, "8.333333333333").String(); got != "8.333333333333 USD" {
		t.Fatalf("String() = %q, no debe ocultar precisión", got)
	}
}

func TestNegAndZero(t *testing.T) {
	z := money.Zero(money.VES)
	if !z.IsZero() || z.Currency() != money.VES {
		t.Fatalf("Zero = %s", z)
	}
	if got := usd(t, "3").Neg().String(); got != "-3.00 USD" {
		t.Fatalf("Neg = %s", got)
	}
}

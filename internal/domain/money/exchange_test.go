package money_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

var (
	effective = time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	manual    = money.RateSource{Type: money.SourceManual, Label: "Gerencia"}
)

// Tasa totalmente ficticia del documento maestro: 1 USD = 45.725 VES.
func usdVES(t *testing.T) money.ExchangeRate {
	t.Helper()
	r, err := money.NewExchangeRate(money.USD, money.VES, decimal.MustParse("45.725"), manual, effective)
	if err != nil {
		t.Fatalf("NewExchangeRate: %v", err)
	}
	return r
}

func TestExchangeRateKeepsDirectionAndMetadata(t *testing.T) {
	r := usdVES(t)
	if r.Base() != money.USD || r.Quote() != money.VES {
		t.Fatalf("dirección = %s/%s", r.Base(), r.Quote())
	}
	if r.Rate().String() != "45.725" || r.Source() != manual {
		t.Fatalf("tasa o fuente alteradas: %s %v", r.Rate(), r.Source())
	}
	if got := r.String(); got != "1 USD = 45.725 VES" {
		t.Fatalf("String() = %q", got)
	}
}

func TestExchangeRateStoresEffectiveInstantInUTC(t *testing.T) {
	local := timezone.InCaracas(effective)
	r, err := money.NewExchangeRate(money.USD, money.VES, decimal.MustParse("45.725"), manual, local)
	if err != nil {
		t.Fatal(err)
	}
	if r.EffectiveAt().Location() != time.UTC || !r.EffectiveAt().Equal(effective) {
		t.Fatalf("EffectiveAt = %v", r.EffectiveAt())
	}
}

func TestExchangeRateRejectsInvalidInput(t *testing.T) {
	rate := decimal.MustParse("45.725")
	cases := []struct {
		name        string
		base, quote money.Currency
		rate        decimal.Decimal
		source      money.RateSource
		at          time.Time
	}{
		{"tasa cero", money.USD, money.VES, decimal.Zero(), manual, effective},
		{"tasa negativa", money.USD, money.VES, rate.Neg(), manual, effective},
		{"misma moneda", money.USD, money.USD, rate, manual, effective},
		{"base vacía", money.Currency{}, money.VES, rate, manual, effective},
		{"cotizada vacía", money.USD, money.Currency{}, rate, manual, effective},
		{"sin fuente", money.USD, money.VES, rate, money.RateSource{}, effective},
		{"tipo de fuente desconocido", money.USD, money.VES, rate, money.RateSource{Type: "rumor", Label: "x"}, effective},
		{"fuente sin etiqueta", money.USD, money.VES, rate, money.RateSource{Type: money.SourceOfficial}, effective},
		{"sin vigencia", money.USD, money.VES, rate, manual, time.Time{}},
	}
	for _, c := range cases {
		_, err := money.NewExchangeRate(c.base, c.quote, c.rate, c.source, c.at)
		if !errors.Is(err, money.ErrInvalidExchangeRate) && !errors.Is(err, money.ErrInvalidCurrency) {
			t.Errorf("%s: se esperaba error de validación, obtuvo %v", c.name, err)
		}
	}
}

func TestConvertBaseToQuote(t *testing.T) {
	// Ejemplo del documento: 12 USD × 45.725 = 548.700 VES → 548.70 VES.
	c, err := usdVES(t).Convert(usd(t, "12"), decimal.HalfAwayFromZero)
	if err != nil {
		t.Fatal(err)
	}
	if c.From.String() != "12.00 USD" {
		t.Errorf("From = %s", c.From)
	}
	if c.Unrounded.Amount().String() != "548.7" || c.Unrounded.Currency() != money.VES {
		t.Errorf("Unrounded = %s", c.Unrounded)
	}
	if c.Settled.String() != "548.70 VES" {
		t.Errorf("Settled = %s", c.Settled)
	}
	if c.Mode != decimal.HalfAwayFromZero || c.Rate.Rate().String() != "45.725" {
		t.Errorf("snapshot incompleto: %+v", c)
	}
}

func TestConvertRoundsOnlyAtSettlement(t *testing.T) {
	c, err := usdVES(t).Convert(usd(t, "0.01"), decimal.HalfEven)
	if err != nil {
		t.Fatal(err)
	}
	// 0.01 × 45.725 = 0.45725: el equivalente exacto se conserva.
	if c.Unrounded.Amount().String() != "0.45725" || c.Settled.String() != "0.46 VES" {
		t.Fatalf("Unrounded = %s, Settled = %s", c.Unrounded, c.Settled)
	}
}

func TestConvertQuoteToBaseUsesDeclaredPolicy(t *testing.T) {
	ves, _ := money.Parse("1000", money.VES)
	r := usdVES(t)

	up, err := r.Convert(ves, decimal.HalfAwayFromZero)
	if err != nil {
		t.Fatal(err)
	}
	// 1000 / 45.725 = 21.86987424822307271733...
	if up.Settled.String() != "21.87 USD" {
		t.Errorf("Settled = %s", up.Settled)
	}
	if up.Unrounded.Amount().String() != "21.869874248223072717" {
		t.Errorf("Unrounded = %s", up.Unrounded.Amount())
	}

	down, err := r.Convert(ves, decimal.TowardZero)
	if err != nil {
		t.Fatal(err)
	}
	if down.Settled.String() != "21.86 USD" {
		t.Errorf("TowardZero Settled = %s", down.Settled)
	}
}

func TestConvertRejectsUnrelatedCurrency(t *testing.T) {
	eur, _ := money.NewCurrency("EUR", 2)
	m, _ := money.Parse("5", eur)
	if _, err := usdVES(t).Convert(m, decimal.HalfEven); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("se esperaba ErrCurrencyMismatch, obtuvo %v", err)
	}
}

func TestConvertRejectsInvalidMode(t *testing.T) {
	if _, err := usdVES(t).Convert(usd(t, "1"), decimal.RoundingMode(0)); !errors.Is(err, decimal.ErrInvalidRoundingMode) {
		t.Fatalf("se esperaba ErrInvalidRoundingMode, obtuvo %v", err)
	}
}

func TestZeroValueExchangeRateCannotConvert(t *testing.T) {
	if _, err := (money.ExchangeRate{}).Convert(usd(t, "1"), decimal.HalfEven); !errors.Is(err, money.ErrInvalidExchangeRate) {
		t.Fatalf("se esperaba ErrInvalidExchangeRate, obtuvo %v", err)
	}
}

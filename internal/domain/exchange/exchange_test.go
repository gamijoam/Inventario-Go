package exchange_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/exchange"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/money"
)

var (
	now  = time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	bcv  = money.RateSource{Type: money.SourceOfficial, Label: "BCV"}
	day  = func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	orgs = id.MustNew()
)

func rate(t *testing.T, base, quote money.Currency, v string, on time.Time) exchange.Rate {
	t.Helper()
	r, err := exchange.NewRate(orgs, id.MustNew(), exchange.RateInput{Base: base, Quote: quote, Value: v, Source: bcv, ValidOn: on}, id.MustNew(), now)
	if err != nil {
		t.Fatalf("NewRate: %v", err)
	}
	return r
}

func TestNewRateValidates(t *testing.T) {
	ok := exchange.RateInput{Base: money.USD, Quote: money.VES, Value: "45.725", Source: bcv, ValidOn: day(27)}
	if _, err := exchange.NewRate(orgs, id.MustNew(), ok, id.MustNew(), now); err != nil {
		t.Fatalf("tasa válida: %v", err)
	}
	cases := map[string]func(*exchange.RateInput){
		"cero":            func(in *exchange.RateInput) { in.Value = "0" },
		"negativa":        func(in *exchange.RateInput) { in.Value = "-1" },
		"13 decimales":    func(in *exchange.RateInput) { in.Value = "1.0000000000001" },
		"absurda":         func(in *exchange.RateInput) { in.Value = "1000000000001" },
		"misma moneda":    func(in *exchange.RateInput) { in.Quote = money.USD },
		"sin fuente":      func(in *exchange.RateInput) { in.Source.Label = " " },
		"tipo de fuente":  func(in *exchange.RateInput) { in.Source.Type = "rumor" },
		"sin fecha":       func(in *exchange.RateInput) { in.ValidOn = time.Time{} },
		"control en nota": func(in *exchange.RateInput) { in.Note = "a\x00b" },
	}
	for name, mutate := range cases {
		in := ok
		mutate(&in)
		if _, err := exchange.NewRate(orgs, id.MustNew(), in, id.MustNew(), now); !errors.Is(err, exchange.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := exchange.ParseSourceType("manual"); err != nil {
		t.Errorf("manual: %v", err)
	}
}

func TestConvertBothDirections(t *testing.T) {
	r := rate(t, money.USD, money.VES, "45.725", day(27))
	// 12 USD → VES: multiplicación exacta.
	if got, _ := r.Convert(decimal.MustParse("12"), money.USD, 6); got.String() != "548.7" {
		t.Errorf("USD→VES = %s", got)
	}
	// 100 VES → USD: 2.186987424822... redondeado una sola vez a 6.
	if got, _ := r.Convert(decimal.MustParse("100"), money.VES, 6); got.String() != "2.186987" {
		t.Errorf("VES→USD = %s", got)
	}
	if _, err := r.Convert(decimal.MustParse("1"), money.Currency{}, 6); !errors.Is(err, exchange.ErrInvalid) {
		t.Errorf("moneda ajena: %v", err)
	}
	if !r.Covers(money.VES, money.USD) || r.Covers(money.USD, money.USD) {
		t.Error("Covers")
	}
	if exchange.Pair(money.VES, money.USD) != "USD/VES" || r.String() != "1 USD = 45.725 VES" {
		t.Errorf("par %s, %s", exchange.Pair(money.VES, money.USD), r)
	}
}

func TestLatestPicksDateThenVersion(t *testing.T) {
	a := rate(t, money.USD, money.VES, "40", day(20))
	a.Version = 1
	b := rate(t, money.USD, money.VES, "45", day(25))
	b.Version = 2
	c := rate(t, money.USD, money.VES, "46", day(25)) // corrección del 25
	c.Version = 3
	d := rate(t, money.USD, money.VES, "50", day(30)) // programada
	d.Version = 4
	all := []exchange.Rate{d, c, b, a}
	for on, want := range map[int]string{20: "40", 24: "40", 25: "46", 29: "46", 30: "50"} {
		got, err := exchange.Latest(all, day(on))
		if err != nil || got.Value.String() != want {
			t.Errorf("día %d: %v %v, se esperaba %s", on, got.Value, err, want)
		}
	}
	if _, err := exchange.Latest(all, day(19)); !errors.Is(err, exchange.ErrNoRate) {
		t.Errorf("antes de toda tasa: %v", err)
	}
}

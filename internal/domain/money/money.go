// Package money modela importes con moneda obligatoria.
//
// Las operaciones entre monedas distintas fallan: convertir exige una tasa
// explícita y auditable (ExchangeRate, ticket posterior). Los cálculos
// conservan toda la precisión; solo Round cuantiza, y siempre con un modo
// declarado por quien llama.
package money

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
)

// maxCurrencyScale limita los decimales de liquidación de una moneda.
const maxCurrencyScale int32 = 8

var (
	ErrInvalidCurrency  = errors.New("moneda inválida")
	ErrCurrencyMismatch = errors.New("monedas distintas sin conversión")
)

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

// Currency identifica una moneda por su código ISO 4217 y los decimales
// con que se liquidan sus importes. El valor cero no es una moneda válida.
type Currency struct {
	code  string
	scale int32
}

// Monedas habilitadas desde V1. La escala de liquidación es provisional
// (ADR-0002) hasta ratificar la política por moneda y medio de cobro.
var (
	USD = mustCurrency("USD", 2)
	VES = mustCurrency("VES", 2)
)

// Lookup devuelve una moneda habilitada por su código (USD o VES).
func Lookup(code string) (Currency, error) {
	switch code {
	case USD.code:
		return USD, nil
	case VES.code:
		return VES, nil
	}
	return Currency{}, fmt.Errorf("%w: %q no está habilitada (use USD o VES)", ErrInvalidCurrency, code)
}

// NewCurrency crea una moneda; code debe ser de tres letras mayúsculas.
func NewCurrency(code string, scale int32) (Currency, error) {
	if !currencyCode.MatchString(code) {
		return Currency{}, fmt.Errorf("%w: código %q", ErrInvalidCurrency, code)
	}
	if scale < 0 || scale > maxCurrencyScale {
		return Currency{}, fmt.Errorf("%w: escala %d fuera de [0, %d]", ErrInvalidCurrency, scale, maxCurrencyScale)
	}
	return Currency{code: code, scale: scale}, nil
}

func mustCurrency(code string, scale int32) Currency {
	c, err := NewCurrency(code, scale)
	if err != nil {
		panic(err)
	}
	return c
}

func (c Currency) Code() string   { return c.code }
func (c Currency) Scale() int32   { return c.scale }
func (c Currency) String() string { return c.code }
func (c Currency) valid() bool    { return c.code != "" }

// Money es un importe exacto en una moneda. Es inmutable.
type Money struct {
	amount   decimal.Decimal
	currency Currency
}

// New crea un importe; la moneda es obligatoria.
func New(amount decimal.Decimal, c Currency) (Money, error) {
	if !c.valid() {
		return Money{}, ErrInvalidCurrency
	}
	return Money{amount: amount, currency: c}, nil
}

// Parse crea un importe desde una cadena decimal canónica.
func Parse(amount string, c Currency) (Money, error) {
	if !c.valid() {
		return Money{}, ErrInvalidCurrency
	}
	d, err := decimal.Parse(amount)
	if err != nil {
		return Money{}, err
	}
	return Money{amount: d, currency: c}, nil
}

// Zero devuelve un importe cero en la moneda c.
func Zero(c Currency) Money { return Money{currency: c} }

func (m Money) Amount() decimal.Decimal { return m.amount }
func (m Money) Currency() Currency      { return m.currency }
func (m Money) IsZero() bool            { return m.amount.IsZero() }
func (m Money) IsNegative() bool        { return m.amount.IsNegative() }
func (m Money) Sign() int               { return m.amount.Sign() }
func (m Money) Neg() Money              { return Money{amount: m.amount.Neg(), currency: m.currency} }

func (m Money) Add(o Money) (Money, error) {
	if err := m.sameCurrency(o); err != nil {
		return Money{}, err
	}
	return Money{amount: m.amount.Add(o.amount), currency: m.currency}, nil
}

func (m Money) Sub(o Money) (Money, error) {
	if err := m.sameCurrency(o); err != nil {
		return Money{}, err
	}
	return Money{amount: m.amount.Sub(o.amount), currency: m.currency}, nil
}

// Mul multiplica por un factor sin redondear (cantidad, porcentaje, etc.).
func (m Money) Mul(factor decimal.Decimal) Money {
	return Money{amount: m.amount.Mul(factor), currency: m.currency}
}

// Cmp compara dos importes de la misma moneda.
func (m Money) Cmp(o Money) (int, error) {
	if err := m.sameCurrency(o); err != nil {
		return 0, err
	}
	return m.amount.Cmp(o.amount), nil
}

// Equal es true solo si moneda y valor numérico coinciden.
func (m Money) Equal(o Money) bool {
	return m.currency == o.currency && m.amount.Equal(o.amount)
}

// Round liquida el importe a la escala de su moneda con el modo indicado.
func (m Money) Round(mode decimal.RoundingMode) (Money, error) {
	// Div entre 1 valida el modo sin entrar en pánico ante datos externos.
	d, err := m.amount.Div(decimal.FromInt(1), m.currency.scale, mode)
	if err != nil {
		return Money{}, err
	}
	return Money{amount: d, currency: m.currency}, nil
}

// String muestra al menos la escala de la moneda y nunca oculta precisión:
// "0.30 USD", "8.333333333333 USD".
func (m Money) String() string {
	places := m.currency.scale
	if s := m.amount.Scale(); s > places {
		places = s
	}
	return m.amount.StringFixed(places) + " " + m.currency.code
}

func (m Money) sameCurrency(o Money) error {
	if m.currency != o.currency {
		return fmt.Errorf("%w: %s y %s", ErrCurrencyMismatch, m.currency, o.currency)
	}
	return nil
}

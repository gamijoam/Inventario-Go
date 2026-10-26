package money

import (
	"errors"
	"fmt"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
)

var ErrInvalidExchangeRate = errors.New("tasa de cambio inválida")

// RateSourceType clasifica el origen de una tasa (sección 25.7).
type RateSourceType string

const (
	SourceOfficial RateSourceType = "official"
	SourceManual   RateSourceType = "manually_entered"
	SourceProvider RateSourceType = "provider"
)

// RateSource identifica de dónde proviene una tasa. Si una fuente es válida
// para fines fiscales lo decide otra capa, no este tipo.
type RateSource struct {
	Type  RateSourceType
	Label string
}

func (s RateSource) valid() bool {
	switch s.Type {
	case SourceOfficial, SourceManual, SourceProvider:
		return s.Label != ""
	default:
		return false
	}
}

// ExchangeRate expresa cuántas unidades de Quote equivalen a 1 unidad de
// Base: USD/VES = 45.725 significa 1 USD = 45.725 VES. Es inmutable; las
// versiones, aprobaciones y estados de vigencia pertenecen a la persistencia.
type ExchangeRate struct {
	base, quote Currency
	rate        decimal.Decimal
	source      RateSource
	effectiveAt time.Time
}

// NewExchangeRate valida dirección, tasa positiva, fuente e instante de vigencia.
func NewExchangeRate(base, quote Currency, rate decimal.Decimal, source RateSource, effectiveAt time.Time) (ExchangeRate, error) {
	if !base.valid() || !quote.valid() {
		return ExchangeRate{}, ErrInvalidCurrency
	}
	if base == quote {
		return ExchangeRate{}, fmt.Errorf("%w: base y cotizada son %s", ErrInvalidExchangeRate, base)
	}
	if rate.Sign() <= 0 {
		return ExchangeRate{}, fmt.Errorf("%w: la tasa debe ser positiva, es %s", ErrInvalidExchangeRate, rate)
	}
	if !source.valid() {
		return ExchangeRate{}, fmt.Errorf("%w: fuente %+v", ErrInvalidExchangeRate, source)
	}
	if effectiveAt.IsZero() {
		return ExchangeRate{}, fmt.Errorf("%w: falta el instante de vigencia", ErrInvalidExchangeRate)
	}
	return ExchangeRate{base: base, quote: quote, rate: rate, source: source, effectiveAt: effectiveAt.UTC()}, nil
}

func (r ExchangeRate) Base() Currency         { return r.base }
func (r ExchangeRate) Quote() Currency        { return r.quote }
func (r ExchangeRate) Rate() decimal.Decimal  { return r.rate }
func (r ExchangeRate) Source() RateSource     { return r.source }
func (r ExchangeRate) EffectiveAt() time.Time { return r.effectiveAt }

func (r ExchangeRate) String() string {
	return "1 " + r.base.code + " = " + r.rate.String() + " " + r.quote.code
}

// Conversion es la instantánea auditable de una conversión: importe de
// origen, tasa aplicada, equivalente antes de redondear, importe liquidado
// y modo de redondeo. Se guarda con la operación y no se recalcula.
type Conversion struct {
	From      Money
	Rate      ExchangeRate
	Unrounded Money
	Settled   Money
	Mode      decimal.RoundingMode
}

// Convert convierte m en cualquiera de las dos direcciones de la tasa y
// liquida a la escala de la moneda destino con mode.
//
// Base → cotizada es una multiplicación exacta. Cotizada → base es una
// división: Settled se calcula directamente a la escala destino (sin doble
// redondeo) y Unrounded se informa con decimal.MaxScale decimales.
func (r ExchangeRate) Convert(m Money, mode decimal.RoundingMode) (Conversion, error) {
	if r.rate.Sign() <= 0 {
		return Conversion{}, ErrInvalidExchangeRate
	}
	var unrounded, settled decimal.Decimal
	var target Currency
	var err error
	switch m.currency {
	case r.base:
		target = r.quote
		unrounded = m.amount.Mul(r.rate)
		settled, err = unrounded.Div(decimal.FromInt(1), target.scale, mode)
	case r.quote:
		target = r.base
		unrounded, err = m.amount.Div(r.rate, decimal.MaxScale, mode)
		if err == nil {
			settled, err = m.amount.Div(r.rate, target.scale, mode)
		}
	default:
		return Conversion{}, fmt.Errorf("%w: %s no corresponde a la tasa %s/%s", ErrCurrencyMismatch, m.currency, r.base, r.quote)
	}
	if err != nil {
		return Conversion{}, err
	}
	return Conversion{
		From:      m,
		Rate:      r,
		Unrounded: Money{amount: unrounded, currency: target},
		Settled:   Money{amount: settled, currency: target},
		Mode:      mode,
	}, nil
}

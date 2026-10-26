// Package decimal ofrece el número decimal exacto que usa todo el dominio.
//
// Envuelve github.com/shopspring/decimal para que la biblioteca no se filtre
// a los módulos de negocio (ADR-0002). Nunca se construye desde float: la
// única entrada textual es Parse, que solo acepta la forma canónica.
package decimal

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	sd "github.com/shopspring/decimal"
)

// MaxScale es la cantidad máxima de decimales aceptada al parsear.
// Valor provisional (ADR-0002): cubre tasas y CPP de alta precisión y evita
// entradas absurdas. Se ratificará junto con las escalas por campo.
const MaxScale int32 = 18

var (
	ErrInvalidDecimal      = errors.New("decimal inválido")
	ErrDivisionByZero      = errors.New("división entre cero")
	ErrInvalidRoundingMode = errors.New("modo de redondeo inválido")
)

// canonical: signo opcional, sin ceros a la izquierda, sin exponente ni
// separador de miles; el punto es el único separador decimal.
var canonical = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// RoundingMode declara cómo cuantizar. No hay modo por defecto: cada
// redondeo o división debe nombrar su política.
type RoundingMode int

const (
	// HalfAwayFromZero redondea 0.5 alejándose de cero (2.345 → 2.35, -2.345 → -2.35).
	HalfAwayFromZero RoundingMode = iota + 1
	// HalfEven redondea 0.5 al dígito par (redondeo bancario).
	HalfEven
	// TowardZero trunca los decimales sobrantes.
	TowardZero
)

func (m RoundingMode) String() string {
	switch m {
	case HalfAwayFromZero:
		return "half_away_from_zero"
	case HalfEven:
		return "half_even"
	case TowardZero:
		return "toward_zero"
	default:
		return fmt.Sprintf("RoundingMode(%d)", int(m))
	}
}

func (m RoundingMode) valid() bool {
	return m >= HalfAwayFromZero && m <= TowardZero
}

// Decimal es un valor inmutable de precisión exacta. El valor cero es 0.
type Decimal struct {
	d sd.Decimal
}

// Parse convierte una cadena decimal canónica, por ejemplo "12.345" o "-0.5".
func Parse(s string) (Decimal, error) {
	if !canonical.MatchString(s) {
		return Decimal{}, fmt.Errorf("%w: %q", ErrInvalidDecimal, s)
	}
	if i := strings.IndexByte(s, '.'); i >= 0 && int32(len(s)-i-1) > MaxScale {
		return Decimal{}, fmt.Errorf("%w: %q supera %d decimales", ErrInvalidDecimal, s, MaxScale)
	}
	d, err := sd.NewFromString(s)
	if err != nil {
		return Decimal{}, fmt.Errorf("%w: %q", ErrInvalidDecimal, s)
	}
	return Decimal{d: d}, nil
}

// MustParse es Parse para constantes conocidas; entra en pánico si s es inválido.
func MustParse(s string) Decimal {
	d, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

// Zero devuelve 0.
func Zero() Decimal { return Decimal{} }

// FromInt convierte un entero sin pérdida.
func FromInt(n int64) Decimal { return Decimal{d: sd.NewFromInt(n)} }

func (a Decimal) Add(b Decimal) Decimal { return Decimal{d: a.d.Add(b.d)} }
func (a Decimal) Sub(b Decimal) Decimal { return Decimal{d: a.d.Sub(b.d)} }
func (a Decimal) Mul(b Decimal) Decimal { return Decimal{d: a.d.Mul(b.d)} }
func (a Decimal) Neg() Decimal          { return Decimal{d: a.d.Neg()} }
func (a Decimal) Abs() Decimal          { return Decimal{d: a.d.Abs()} }

// Cmp devuelve -1, 0 o 1 según a sea menor, igual o mayor que b.
func (a Decimal) Cmp(b Decimal) int    { return a.d.Cmp(b.d) }
func (a Decimal) Equal(b Decimal) bool { return a.d.Equal(b.d) }
func (a Decimal) Sign() int            { return a.d.Sign() }
func (a Decimal) IsZero() bool         { return a.d.IsZero() }
func (a Decimal) IsNegative() bool     { return a.d.IsNegative() }

// Scale es el número de decimales significativos (12.50 → 1).
func (a Decimal) Scale() int32 {
	s := a.d.String()
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return int32(len(s) - i - 1)
	}
	return 0
}

// Round cuantiza a scale decimales con el modo indicado. Un modo inválido
// entra en pánico porque es un error de programación, no de datos.
func (a Decimal) Round(scale int32, mode RoundingMode) Decimal {
	switch mode {
	case HalfAwayFromZero:
		return Decimal{d: a.d.Round(scale)}
	case HalfEven:
		return Decimal{d: a.d.RoundBank(scale)}
	case TowardZero:
		return Decimal{d: a.d.Truncate(scale)}
	default:
		panic(fmt.Errorf("%w: %v", ErrInvalidRoundingMode, mode))
	}
}

// Div divide a entre b y cuantiza el cociente a scale decimales con mode.
// La decisión de redondeo se toma sobre el resto exacto, sin aproximaciones.
func (a Decimal) Div(b Decimal, scale int32, mode RoundingMode) (Decimal, error) {
	if !mode.valid() {
		return Decimal{}, fmt.Errorf("%w: %v", ErrInvalidRoundingMode, mode)
	}
	if b.IsZero() {
		return Decimal{}, ErrDivisionByZero
	}
	// q está truncado hacia cero; |r| < |b|·10^-scale.
	q, r := a.d.QuoRem(b.d, scale)
	if r.IsZero() || mode == TowardZero {
		return Decimal{d: q}, nil
	}
	unit := sd.New(1, -scale)
	half := r.Abs().Mul(sd.NewFromInt(2)).Cmp(b.d.Abs().Mul(unit))
	bump := half > 0 || half == 0 && (mode == HalfAwayFromZero || isOddAt(q, scale))
	if bump {
		if a.d.Sign()*b.d.Sign() < 0 {
			q = q.Sub(unit)
		} else {
			q = q.Add(unit)
		}
	}
	return Decimal{d: q}, nil
}

// isOddAt indica si el último dígito de q en la posición scale es impar.
func isOddAt(q sd.Decimal, scale int32) bool {
	return !q.Shift(scale).Mod(sd.NewFromInt(2)).IsZero()
}

// String devuelve la forma canónica mínima, sin ceros finales ("0.30" → "0.3").
func (a Decimal) String() string { return a.d.String() }

// StringFixed devuelve el valor con exactamente places decimales. Redondea
// alejándose de cero si sobran dígitos; para presentación, no para cálculo.
func (a Decimal) StringFixed(places int32) string { return a.d.StringFixed(places) }

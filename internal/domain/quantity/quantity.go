// Package quantity modela cantidades exactas con unidad obligatoria.
//
// Una cantidad nunca tiene más decimales que su unidad admite (unidades
// enteras, kilos con tres decimales, etc.) y no se mezcla con otra unidad sin
// una conversión definida. Puede ser negativa: los saldos de inventario son
// firmados y la política de negativos se aplica en otra capa.
package quantity

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
)

const maxUnitScale int32 = 6

var (
	ErrInvalidUnit       = errors.New("unidad inválida")
	ErrUnitMismatch      = errors.New("unidades distintas sin conversión")
	ErrPrecisionExceeded = errors.New("la cantidad supera la precisión de la unidad")
)

var unitCode = regexp.MustCompile(`^[A-Z0-9]{1,16}$`)

// Unit es una unidad de medida y la cantidad de decimales que admite.
// El valor cero no es una unidad válida.
type Unit struct {
	code  string
	scale int32
}

func NewUnit(code string, scale int32) (Unit, error) {
	if !unitCode.MatchString(code) {
		return Unit{}, fmt.Errorf("%w: código %q", ErrInvalidUnit, code)
	}
	if scale < 0 || scale > maxUnitScale {
		return Unit{}, fmt.Errorf("%w: escala %d fuera de [0, %d]", ErrInvalidUnit, scale, maxUnitScale)
	}
	return Unit{code: code, scale: scale}, nil
}

func (u Unit) Code() string   { return u.code }
func (u Unit) Scale() int32   { return u.scale }
func (u Unit) String() string { return u.code }
func (u Unit) valid() bool    { return u.code != "" }

// Quantity es una cantidad exacta en una unidad. Es inmutable.
type Quantity struct {
	amount decimal.Decimal
	unit   Unit
}

// New valida unidad y precisión.
func New(amount decimal.Decimal, u Unit) (Quantity, error) {
	if !u.valid() {
		return Quantity{}, ErrInvalidUnit
	}
	if amount.Scale() > u.scale {
		return Quantity{}, fmt.Errorf("%w: %s admite %d decimales", ErrPrecisionExceeded, u, u.scale)
	}
	return Quantity{amount: amount, unit: u}, nil
}

// Parse crea una cantidad desde una cadena decimal canónica.
func Parse(amount string, u Unit) (Quantity, error) {
	if !u.valid() {
		return Quantity{}, ErrInvalidUnit
	}
	d, err := decimal.Parse(amount)
	if err != nil {
		return Quantity{}, err
	}
	return New(d, u)
}

// Zero devuelve cero en la unidad u.
func Zero(u Unit) Quantity { return Quantity{unit: u} }

func (q Quantity) Amount() decimal.Decimal { return q.amount }
func (q Quantity) Unit() Unit              { return q.unit }
func (q Quantity) IsZero() bool            { return q.amount.IsZero() }
func (q Quantity) IsNegative() bool        { return q.amount.IsNegative() }
func (q Quantity) Sign() int               { return q.amount.Sign() }
func (q Quantity) Neg() Quantity           { return Quantity{amount: q.amount.Neg(), unit: q.unit} }

// Add y Sub preservan la precisión: ambos operandos ya respetan la escala.
func (q Quantity) Add(o Quantity) (Quantity, error) {
	if err := q.sameUnit(o); err != nil {
		return Quantity{}, err
	}
	return Quantity{amount: q.amount.Add(o.amount), unit: q.unit}, nil
}

func (q Quantity) Sub(o Quantity) (Quantity, error) {
	if err := q.sameUnit(o); err != nil {
		return Quantity{}, err
	}
	return Quantity{amount: q.amount.Sub(o.amount), unit: q.unit}, nil
}

func (q Quantity) Cmp(o Quantity) (int, error) {
	if err := q.sameUnit(o); err != nil {
		return 0, err
	}
	return q.amount.Cmp(o.amount), nil
}

func (q Quantity) Equal(o Quantity) bool {
	return q.unit == o.unit && q.amount.Equal(o.amount)
}

// String muestra la cantidad con la escala de su unidad: "0.125 KG", "3 UND".
func (q Quantity) String() string {
	return q.amount.StringFixed(q.unit.scale) + " " + q.unit.code
}

func (q Quantity) sameUnit(o Quantity) error {
	if q.unit != o.unit {
		return fmt.Errorf("%w: %s y %s", ErrUnitMismatch, q.unit, o.unit)
	}
	return nil
}

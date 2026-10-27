// Package inventory modela saldos de existencia y el kardex de movimientos
// por producto y almacén (DEC-007.F, secciones 23.2 y 24.3).
//
// El kardex es la fuente de verdad: cada movimiento es inmutable, lleva un
// número de secuencia por saldo y el saldo resultante. El saldo
// materializado es una proyección que debe poder reconstruirse exactamente
// reproduciendo el kardex (Replay). Un error se corrige con un movimiento de
// reverso, nunca editando el anterior.
package inventory

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

var (
	ErrInvalid = errors.New("dato de inventario inválido")
	// ErrInsufficient indica que la salida dejaría el disponible en
	// negativo. Las excepciones por política llegan en BP-M2-003.
	ErrInsufficient = errors.New("existencia disponible insuficiente")
	// ErrReplayMismatch indica que el saldo no coincide con su kardex.
	ErrReplayMismatch = errors.New("el saldo no coincide con el kardex")
)

// Kind es el tipo de movimiento.
type Kind string

const (
	KindAdjustmentIn  Kind = "adjustment_in"
	KindAdjustmentOut Kind = "adjustment_out"
	KindReversal      Kind = "reversal"
)

// Reason es el motivo de un ajuste, de un catálogo fijo (ratificado
// 2026-10-27) para poder agrupar en reportes; el detalle va en la nota.
type Reason string

const (
	ReasonInitialLoad Reason = "initial_load"
	ReasonCorrection  Reason = "correction"
	ReasonShrinkage   Reason = "shrinkage"
	ReasonInternalUse Reason = "internal_use"
	ReasonOther       Reason = "other"
	// ReasonReversal es exclusivo de los movimientos de reverso.
	ReasonReversal Reason = "reversal"
)

// ReasonInfo describe un motivo y en qué dirección se admite.
type ReasonInfo struct {
	Reason      Reason
	Description string
	In, Out     bool
}

var reasons = []ReasonInfo{
	{ReasonInitialLoad, "Carga inicial de existencia", true, false},
	{ReasonCorrection, "Corrección de un error de registro", true, true},
	{ReasonShrinkage, "Merma, daño o pérdida", false, true},
	{ReasonInternalUse, "Consumo interno", false, true},
	{ReasonOther, "Otro (explique en la nota)", true, true},
}

// Reasons devuelve los motivos de ajuste en orden estable.
func Reasons() []ReasonInfo { return append([]ReasonInfo(nil), reasons...) }

// Direction es el sentido de un ajuste.
type Direction int

const (
	In Direction = iota + 1
	Out
)

func (d Direction) String() string {
	if d == In {
		return "entrada"
	}
	return "salida"
}

// ParseReason valida el motivo para la dirección del ajuste.
func ParseReason(s string, d Direction) (Reason, error) {
	r := Reason(strings.TrimSpace(strings.ToLower(s)))
	for _, info := range reasons {
		if info.Reason != r {
			continue
		}
		if (d == In && !info.In) || (d == Out && !info.Out) {
			return "", fmt.Errorf("%w: el motivo %s no se admite en una %s", ErrInvalid, r, d)
		}
		return r, nil
	}
	return "", fmt.Errorf("%w: motivo %q (use initial_load, correction, shrinkage, internal_use u other)", ErrInvalid, s)
}

// MaxNoteLength es el largo máximo de la nota, en caracteres.
const MaxNoteLength = 500

// ParseNote normaliza la nota; ReasonOther la exige.
func ParseNote(s string, r Reason) (string, error) {
	for _, ch := range s {
		if unicode.IsControl(ch) && ch != '\n' {
			return "", fmt.Errorf("%w: la nota contiene caracteres de control", ErrInvalid)
		}
	}
	v := strings.TrimSpace(s)
	if utf8.RuneCountInString(v) > MaxNoteLength {
		return "", fmt.Errorf("%w: la nota admite hasta %d caracteres", ErrInvalid, MaxNoteLength)
	}
	if v == "" && r == ReasonOther {
		return "", fmt.Errorf("%w: el motivo other exige una nota", ErrInvalid)
	}
	return v, nil
}

// ParseQuantity lee una cantidad positiva con a lo sumo scale decimales.
func ParseQuantity(s string, scale int32) (decimal.Decimal, error) {
	q, err := decimal.Parse(strings.TrimSpace(s))
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%w: cantidad %q", ErrInvalid, s)
	}
	if q.Sign() <= 0 {
		return decimal.Decimal{}, fmt.Errorf("%w: la cantidad debe ser mayor que cero", ErrInvalid)
	}
	if q.Scale() > scale {
		return decimal.Decimal{}, fmt.Errorf("%w: la unidad admite %d decimales", ErrInvalid, scale)
	}
	return q, nil
}

// Key identifica un saldo: un producto en un almacén.
type Key struct {
	Scope     scope.BusinessScope // ámbito de almacén
	ProductID id.ID
}

// Balance es la proyección del kardex de un producto en un almacén.
type Balance struct {
	Key
	OnHand, Reserved, Blocked decimal.Decimal
	// Version es la secuencia del último movimiento aplicado.
	Version   int64
	UpdatedAt time.Time
}

// Available es la existencia física no reservada ni bloqueada; puede ser
// negativa y no se normaliza a cero (sección 24.3).
func (b Balance) Available() decimal.Decimal { return b.OnHand.Sub(b.Reserved).Sub(b.Blocked) }

// Movement es una línea inmutable del kardex.
type Movement struct {
	ID id.ID
	Key
	Sequence int64
	Kind     Kind
	Reason   Reason
	// Quantity tiene signo: positiva entra, negativa sale.
	Quantity       decimal.Decimal
	OnHandAfter    decimal.Decimal
	Note           string
	ActorID        id.ID
	OccurredAt     time.Time
	ReversesID     id.ID // solo en reversos
	IdempotencyKey string
	RequestHash    string
}

// Apply devuelve el saldo y el movimiento resultantes de sumar delta. Una
// salida exige disponible suficiente: sin política de negativos (BP-M2-003)
// ninguna salida deja el disponible bajo cero.
func Apply(b Balance, m Movement, now time.Time) (Balance, Movement, error) {
	if m.Quantity.IsZero() {
		return Balance{}, Movement{}, fmt.Errorf("%w: cantidad cero", ErrInvalid)
	}
	if m.Key != b.Key {
		return Balance{}, Movement{}, fmt.Errorf("%w: el movimiento es de otro saldo", ErrInvalid)
	}
	if m.Quantity.IsNegative() && b.Available().Add(m.Quantity).IsNegative() {
		return Balance{}, Movement{}, fmt.Errorf("%w: disponible %s, salida %s", ErrInsufficient, b.Available(), m.Quantity.Neg())
	}
	b.OnHand = b.OnHand.Add(m.Quantity)
	b.Version++
	b.UpdatedAt = now.UTC()
	m.Sequence = b.Version
	m.OnHandAfter = b.OnHand
	return b, m, nil
}

// Replay reconstruye la existencia física a partir del kardex ordenado por
// secuencia y la compara con el saldo guardado. Detecta huecos, saltos,
// saldos intermedios alterados y diferencias finales.
func Replay(b Balance, movements []Movement) error {
	onHand := decimal.Zero()
	for i, m := range movements {
		if m.Key != b.Key {
			return fmt.Errorf("%w: el movimiento %s es de otro saldo", ErrReplayMismatch, m.ID)
		}
		if m.Sequence != int64(i+1) {
			return fmt.Errorf("%w: se esperaba la secuencia %d y se encontró %d", ErrReplayMismatch, i+1, m.Sequence)
		}
		onHand = onHand.Add(m.Quantity)
		if !onHand.Equal(m.OnHandAfter) {
			return fmt.Errorf("%w: tras la secuencia %d el kardex suma %s pero registra %s", ErrReplayMismatch, m.Sequence, onHand, m.OnHandAfter)
		}
	}
	if int64(len(movements)) != b.Version {
		return fmt.Errorf("%w: el saldo está en la versión %d y el kardex tiene %d movimientos", ErrReplayMismatch, b.Version, len(movements))
	}
	if !onHand.Equal(b.OnHand) {
		return fmt.Errorf("%w: el kardex suma %s y el saldo registra %s", ErrReplayMismatch, onHand, b.OnHand)
	}
	return nil
}

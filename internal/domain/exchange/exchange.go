// Package exchange modela el registro de tasas de cambio de una empresa
// (BP-M3-002, sección 25.7).
//
// Una tasa se registra a mano con su fuente declarada y rige desde una
// fecha comercial de Caracas hasta que otra la reemplace. Nunca se edita:
// una corrección es una versión nueva. Los documentos guardan la tasa que
// aplicaron, así que una tasa posterior no los recalcula.
package exchange

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/money"
)

var (
	ErrInvalid = errors.New("tasa de cambio inválida")
	// ErrNoRate indica que no hay tasa vigente para un par y una fecha:
	// la operación que la necesita se bloquea, nunca se inventa una tasa.
	ErrNoRate = errors.New("no hay tasa de cambio vigente")
)

// Scale es el máximo de decimales de una tasa.
const Scale = 12

// maxRate acota la tasa para evitar valores absurdos y desbordamientos.
var maxRate = decimal.MustParse("1000000000000")

const rounding = decimal.HalfAwayFromZero

// MaxTextLength acota la etiqueta de la fuente, la referencia y la nota.
const MaxTextLength = 200

// ParseSourceType acepta official, manual o provider.
func ParseSourceType(s string) (money.RateSourceType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "official":
		return money.SourceOfficial, nil
	case "manual", string(money.SourceManual):
		return money.SourceManual, nil
	case "provider":
		return money.SourceProvider, nil
	}
	return "", fmt.Errorf("%w: fuente %q (use official, manual o provider)", ErrInvalid, s)
}

// ParseValue lee una tasa positiva con a lo sumo Scale decimales.
func ParseValue(s string) (decimal.Decimal, error) {
	v, err := decimal.Parse(strings.TrimSpace(s))
	if err != nil || v.Sign() <= 0 || v.Scale() > Scale || v.Cmp(maxRate) > 0 {
		return decimal.Decimal{}, fmt.Errorf("%w: %q (positiva, hasta %s y con a lo sumo %d decimales)", ErrInvalid, s, maxRate, Scale)
	}
	return v, nil
}

func parseText(s, what string, required bool) (string, error) {
	v := strings.TrimSpace(s)
	if strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) || utf8.RuneCountInString(v) > MaxTextLength {
		return "", fmt.Errorf("%w: %s admite hasta %d caracteres sin caracteres de control", ErrInvalid, what, MaxTextLength)
	}
	if required && v == "" {
		return "", fmt.Errorf("%w: falta %s", ErrInvalid, what)
	}
	return v, nil
}

// Pair identifica un par de monedas sin importar la dirección: la tasa
// USD/VES y una VES/USD compiten por el mismo par.
func Pair(a, b money.Currency) string {
	if a.Code() > b.Code() {
		a, b = b, a
	}
	return a.Code() + "/" + b.Code()
}

// Rate es una versión de la tasa de un par en una empresa: 1 Base = Value
// Quote desde la fecha ValidOn.
type Rate struct {
	ID             id.ID
	OrganizationID id.ID
	CompanyID      id.ID
	Base, Quote    money.Currency
	Value          decimal.Decimal
	Source         money.RateSource
	Reference      string    // p. ej. la publicación de la que se tomó
	ValidOn        time.Time // fecha comercial (medianoche UTC) desde la que rige
	Version        int       // por empresa y par; la mayor gana en una misma fecha
	Note           string
	CreatedBy      id.ID
	CreatedAt      time.Time
}

// RateInput son los datos que declara quien registra una tasa.
type RateInput struct {
	Base, Quote money.Currency
	Value       string
	Source      money.RateSource
	Reference   string
	ValidOn     time.Time
	Note        string
}

// NewRate valida una tasa nueva. La versión la asigna el registro.
func NewRate(orgID, companyID id.ID, in RateInput, actor id.ID, now time.Time) (Rate, error) {
	v, err := ParseValue(in.Value)
	if err != nil {
		return Rate{}, err
	}
	if in.ValidOn.IsZero() {
		return Rate{}, fmt.Errorf("%w: falta la fecha de vigencia", ErrInvalid)
	}
	label, err := parseText(in.Source.Label, "la etiqueta de la fuente", true)
	if err != nil {
		return Rate{}, err
	}
	src := money.RateSource{Type: in.Source.Type, Label: label}
	if _, err := money.NewExchangeRate(in.Base, in.Quote, v, src, in.ValidOn); err != nil {
		return Rate{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	ref, err := parseText(in.Reference, "la referencia", false)
	if err != nil {
		return Rate{}, err
	}
	note, err := parseText(in.Note, "la nota", false)
	if err != nil {
		return Rate{}, err
	}
	rid, err := id.New()
	if err != nil {
		return Rate{}, err
	}
	return Rate{ID: rid, OrganizationID: orgID, CompanyID: companyID, Base: in.Base, Quote: in.Quote, Value: v, Source: src,
		Reference: ref, ValidOn: in.ValidOn.UTC(), Note: note, CreatedBy: actor, CreatedAt: now.UTC()}, nil
}

func (r Rate) Pair() string { return Pair(r.Base, r.Quote) }

func (r Rate) String() string {
	return "1 " + r.Base.Code() + " = " + r.Value.String() + " " + r.Quote.Code()
}

// Covers indica si la tasa convierte entre a y b, en cualquier dirección.
func (r Rate) Covers(a, b money.Currency) bool {
	return a != b && r.Pair() == Pair(a, b)
}

// Convert lleva amount de la moneda from a la otra del par y redondea una
// sola vez a scale decimales: base → cotizada multiplica, cotizada → base
// divide.
func (r Rate) Convert(amount decimal.Decimal, from money.Currency, scale int32) (decimal.Decimal, error) {
	switch from {
	case r.Base:
		return amount.Mul(r.Value).Round(scale, rounding), nil
	case r.Quote:
		return amount.Div(r.Value, scale, rounding)
	}
	return decimal.Decimal{}, fmt.Errorf("%w: %s no corresponde a la tasa %s", ErrInvalid, from, r)
}

// Latest elige la tasa vigente el día on entre candidatas de un mismo par:
// la de fecha más reciente no posterior a on y, en esa fecha, la de mayor
// versión. ErrNoRate si ninguna rige todavía.
func Latest(candidates []Rate, on time.Time) (Rate, error) {
	var best Rate
	found := false
	for _, c := range candidates {
		if c.ValidOn.After(on) {
			continue
		}
		if !found || c.ValidOn.After(best.ValidOn) || c.ValidOn.Equal(best.ValidOn) && c.Version > best.Version {
			best, found = c, true
		}
	}
	if !found {
		return Rate{}, ErrNoRate
	}
	return best, nil
}

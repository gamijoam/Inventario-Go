// Package pricing modela listas de precios, márgenes objetivo, propuestas
// de precio y revisiones versionadas (BP-M3-003, DEC-007.P2).
//
// Recibir mercancía no cambia precios: genera propuestas que un usuario
// autorizado aprueba o rechaza. Una propuesta aprobada se publica como una
// revisión nueva, inmediata o programada, que rige para las ventas futuras
// de la lista; las ventas pasadas conservan su precio. Nada se edita: los
// márgenes y los precios tienen historial.
package pricing

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/org"
)

var (
	ErrInvalid = errors.New("dato de precio inválido")
	// ErrStale indica que el precio cambió desde que se hizo la propuesta:
	// hay que proponer de nuevo, nunca pisar el cambio en silencio.
	ErrStale = errors.New("el precio cambió desde la propuesta")
)

// MarginScale es el máximo de decimales de un margen en porcentaje.
const MarginScale = 4

// CostScale es la escala del costo de referencia convertido a la moneda de
// la lista.
const CostScale = 6

const rounding = decimal.HalfAwayFromZero

// MaxTextLength acota nombres y notas.
const MaxTextLength = 200

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

// ParseMargin lee un margen sobre venta en porcentaje: 0 ≤ m < 100.
func ParseMargin(s string) (decimal.Decimal, error) {
	m, err := decimal.Parse(strings.TrimSpace(s))
	if err != nil || m.IsNegative() || m.Cmp(decimal.FromInt(100)) >= 0 || m.Scale() > MarginScale {
		return decimal.Decimal{}, fmt.Errorf("%w: margen %q (porcentaje de 0 a menos de 100, hasta %d decimales)", ErrInvalid, s, MarginScale)
	}
	return m, nil
}

// ParsePrice lee un precio no negativo con a lo sumo los decimales de
// liquidación de la moneda.
func ParsePrice(s string, c money.Currency) (decimal.Decimal, error) {
	p, err := decimal.Parse(strings.TrimSpace(s))
	if err != nil || p.IsNegative() || p.Scale() > c.Scale() {
		return decimal.Decimal{}, fmt.Errorf("%w: precio %q (cero o más, con a lo sumo %d decimales en %s)", ErrInvalid, s, c.Scale(), c)
	}
	return p, nil
}

// Suggest es el precio que deja el margen m (en %) sobre la venta:
// costo ÷ (1 − m/100), redondeado a la escala de la moneda. Con costo 100 y
// margen 20, 125 (sección 19.3).
func Suggest(cost, margin decimal.Decimal, c money.Currency) (decimal.Decimal, error) {
	if cost.IsNegative() {
		return decimal.Decimal{}, fmt.Errorf("%w: costo negativo", ErrInvalid)
	}
	hundred := decimal.FromInt(100)
	return cost.Mul(hundred).Div(hundred.Sub(margin), c.Scale(), rounding)
}

// MarginOf es el margen sobre venta de un precio, en %, o falso si el
// precio es cero.
func MarginOf(price, cost decimal.Decimal) (decimal.Decimal, bool) {
	if price.Sign() <= 0 {
		return decimal.Decimal{}, false
	}
	m, err := price.Sub(cost).Mul(decimal.FromInt(100)).Div(price, 2, rounding)
	return m, err == nil
}

// ---------- Listas ----------

// List es una lista de precios de una empresa, en una sola moneda.
type List struct {
	ID             id.ID
	OrganizationID id.ID
	CompanyID      id.ID
	Code           org.Code
	Name           string
	Currency       money.Currency
	Status         org.Status
	CreatedBy      id.ID
	CreatedAt      time.Time
}

func NewList(orgID, companyID id.ID, code, name string, currency money.Currency, actor id.ID, now time.Time) (List, error) {
	c, err := org.ParseCode(code)
	if err != nil {
		return List{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	n, err := parseText(name, "el nombre", true)
	if err != nil {
		return List{}, err
	}
	if currency.Code() == "" {
		return List{}, fmt.Errorf("%w: falta la moneda", ErrInvalid)
	}
	lid, err := id.New()
	if err != nil {
		return List{}, err
	}
	return List{ID: lid, OrganizationID: orgID, CompanyID: companyID, Code: c, Name: n, Currency: currency, Status: org.StatusActive,
		CreatedBy: actor, CreatedAt: now.UTC()}, nil
}

// MarginChange fija el margen objetivo de una lista (ProductID cero) o la
// excepción de un producto. Sin Margin, la lista deja de sugerir o el
// producto vuelve al margen de la lista. El último cambio rige.
type MarginChange struct {
	ID         id.ID
	ListID     id.ID
	ProductID  id.ID
	Margin     *decimal.Decimal
	ActorID    id.ID
	OccurredAt time.Time
}

func NewMarginChange(listID, productID id.ID, margin *decimal.Decimal, actor id.ID, now time.Time) (MarginChange, error) {
	mid, err := id.New()
	if err != nil {
		return MarginChange{}, err
	}
	return MarginChange{ID: mid, ListID: listID, ProductID: productID, Margin: margin, ActorID: actor, OccurredAt: now.UTC()}, nil
}

// TargetMargin elige el margen de un producto: su excepción si la tiene,
// si no el de la lista.
func TargetMargin(list, product *decimal.Decimal) *decimal.Decimal {
	if product != nil {
		return product
	}
	return list
}

// ---------- Revisiones ----------

type RevisionStatus string

const (
	RevisionPublished RevisionStatus = "published"
	// RevisionCancelled es una revisión programada que se anuló antes de
	// regir.
	RevisionCancelled RevisionStatus = "cancelled"
)

// Revision es una versión del precio de un producto en una lista, vigente
// desde EffectiveAt.
type Revision struct {
	ID          id.ID
	ListID      id.ID
	ProductID   id.ID
	Version     int // por lista y producto, creciente
	Price       decimal.Decimal
	EffectiveAt time.Time
	Status      RevisionStatus
	CreatedBy   id.ID
	CreatedAt   time.Time
	CancelledBy id.ID
	CancelledAt time.Time
}

// Scheduled indica que la revisión todavía no rige en now.
func (r Revision) Scheduled(now time.Time) bool { return r.EffectiveAt.After(now) }

// Cancel anula una revisión programada que aún no rige.
func (r Revision) Cancel(actor id.ID, now time.Time) (Revision, error) {
	if r.Status != RevisionPublished || !r.Scheduled(now) {
		return Revision{}, fmt.Errorf("%w: solo se anula una revisión programada que todavía no rige", ErrInvalid)
	}
	r.Status, r.CancelledBy, r.CancelledAt = RevisionCancelled, actor, now.UTC()
	return r, nil
}

// Current elige el precio vigente en now entre revisiones de un mismo
// producto y lista: la publicada de vigencia más reciente no posterior a
// now y, a igual vigencia, la de mayor versión. Falso si no hay precio.
func Current(revisions []Revision, now time.Time) (Revision, bool) {
	var best Revision
	found := false
	for _, r := range revisions {
		if r.Status != RevisionPublished || r.EffectiveAt.After(now) {
			continue
		}
		if !found || r.EffectiveAt.After(best.EffectiveAt) || r.EffectiveAt.Equal(best.EffectiveAt) && r.Version > best.Version {
			best, found = r, true
		}
	}
	return best, found
}

// ---------- Propuestas ----------

type ProposalKind string

const (
	// KindSuggested la genera la confirmación de una recepción.
	KindSuggested ProposalKind = "suggested"
	// KindManual la propone un usuario.
	KindManual ProposalKind = "manual"
)

type ProposalStatus string

const (
	ProposalPending    ProposalStatus = "pending"
	ProposalApproved   ProposalStatus = "approved"
	ProposalRejected   ProposalStatus = "rejected"
	ProposalSuperseded ProposalStatus = "superseded" // otra propuesta la reemplazó
)

// CostBasis dice de dónde salió el costo de referencia.
type CostBasis string

const (
	BasisReplacement  CostBasis = "replacement"
	BasisLastPurchase CostBasis = "last_purchase"
)

// Reference es el costo con que se calculó o se evalúa una propuesta: en
// su moneda de origen y convertido a la de la lista con la tasa usada.
type Reference struct {
	Basis    CostBasis
	Cost     decimal.Decimal
	Currency money.Currency
	RateID   id.ID // cero si no hubo conversión
	ListCost decimal.Decimal
}

// Proposal es un cambio de precio pendiente de decisión.
type Proposal struct {
	ID           id.ID
	Number       string
	ListID       id.ID
	ProductID    id.ID
	Kind         ProposalKind
	Status       ProposalStatus
	BaseVersion  int              // versión más reciente al proponer; 0 si no había precio
	Current      *decimal.Decimal // precio vigente al proponer
	Price        decimal.Decimal
	Reference    *Reference       // nil si el producto no tiene costo de referencia
	Margin       *decimal.Decimal // margen objetivo con que se sugirió
	ReceiptID    id.ID            // recepción que la originó
	Note         string
	ProposedBy   id.ID
	CreatedAt    time.Time
	DecidedBy    id.ID
	DecidedAt    time.Time
	DecisionNote string // motivo del rechazo
	RevisionID   id.ID  // publicada al aprobar
}

// NewProposal valida una propuesta; Number y BaseVersion los fija quien
// llama.
func NewProposal(list List, productID id.ID, kind ProposalKind, price decimal.Decimal, ref *Reference, margin *decimal.Decimal, note string, actor id.ID, now time.Time) (Proposal, error) {
	if list.Status != org.StatusActive {
		return Proposal{}, fmt.Errorf("%w: la lista %s está inactiva", ErrInvalid, list.Code)
	}
	if price.IsNegative() || price.Scale() > list.Currency.Scale() {
		return Proposal{}, fmt.Errorf("%w: precio %s en %s", ErrInvalid, price, list.Currency)
	}
	n, err := parseText(note, "la nota", false)
	if err != nil {
		return Proposal{}, err
	}
	pid, err := id.New()
	if err != nil {
		return Proposal{}, err
	}
	return Proposal{ID: pid, ListID: list.ID, ProductID: productID, Kind: kind, Status: ProposalPending, Price: price, Reference: ref,
		Margin: margin, Note: n, ProposedBy: actor, CreatedAt: now.UTC()}, nil
}

// BelowCost indica que price queda por debajo del costo de referencia.
func (p Proposal) BelowCost(price decimal.Decimal) bool {
	return p.Reference != nil && price.Cmp(p.Reference.ListCost) < 0
}

func (p Proposal) decide(status ProposalStatus, actor id.ID, now time.Time) (Proposal, error) {
	if p.Status != ProposalPending {
		return Proposal{}, fmt.Errorf("%w: la propuesta %s está %s", ErrInvalid, p.Number, p.Status)
	}
	p.Status, p.DecidedBy, p.DecidedAt = status, actor, now.UTC()
	return p, nil
}

// Approve publica la propuesta como la revisión version+1 desde effective
// (now si es cero). price puede ajustar lo propuesto. latest es la versión
// más reciente del producto en la lista: si cambió desde la propuesta, el
// precio de referencia ya no es el mismo y se rechaza con ErrStale.
func (p Proposal) Approve(price decimal.Decimal, c money.Currency, latest int, effective time.Time, actor id.ID, now time.Time) (Proposal, Revision, error) {
	if p.Status != ProposalPending {
		return Proposal{}, Revision{}, fmt.Errorf("%w: la propuesta %s está %s", ErrInvalid, p.Number, p.Status)
	}
	if latest != p.BaseVersion {
		return Proposal{}, Revision{}, fmt.Errorf("%w: %s (versión %d, la propuesta partió de la %d); proponga de nuevo", ErrStale, p.Number, latest, p.BaseVersion)
	}
	if price.IsNegative() || price.Scale() > c.Scale() {
		return Proposal{}, Revision{}, fmt.Errorf("%w: precio %s en %s", ErrInvalid, price, c)
	}
	if effective.IsZero() {
		effective = now
	}
	if effective.Before(now.Add(-time.Minute)) {
		return Proposal{}, Revision{}, fmt.Errorf("%w: la vigencia no puede ser pasada", ErrInvalid)
	}
	if effective.Before(now) {
		effective = now
	}
	out, err := p.decide(ProposalApproved, actor, now)
	if err != nil {
		return Proposal{}, Revision{}, err
	}
	rid, err := id.New()
	if err != nil {
		return Proposal{}, Revision{}, err
	}
	rev := Revision{ID: rid, ListID: p.ListID, ProductID: p.ProductID, Version: latest + 1, Price: price, EffectiveAt: effective.UTC(),
		Status: RevisionPublished, CreatedBy: actor, CreatedAt: now.UTC()}
	out.Price, out.RevisionID = price, rid
	return out, rev, nil
}

// Reject descarta la propuesta; el precio vigente no cambia.
func (p Proposal) Reject(note string, actor id.ID, now time.Time) (Proposal, error) {
	n, err := parseText(note, "la nota", false)
	if err != nil {
		return Proposal{}, err
	}
	out, err := p.decide(ProposalRejected, actor, now)
	if err != nil {
		return Proposal{}, err
	}
	out.DecisionNote = n
	return out, nil
}

// Supersede marca una propuesta pendiente como reemplazada por otra nueva
// del mismo producto y lista.
func (p Proposal) Supersede(actor id.ID, now time.Time) (Proposal, error) {
	return p.decide(ProposalSuperseded, actor, now)
}

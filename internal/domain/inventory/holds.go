package inventory

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
)

// Políticas de existencia negativa, reservas, bloqueos y conteos físicos
// (BP-M2-003; DEC-007.F2, secciones 24.1–24.6; ratificado 2026-10-27).

// Commit cambia lo reservado y lo bloqueado de un saldo, sin tocar la
// existencia física ni el valor. Un aumento exige disponible suficiente:
// nunca se reserva ni se bloquea en déficit ni por encima de lo libre.
func Commit(b Balance, reserved, blocked decimal.Decimal) (Balance, error) {
	increase := decimal.Zero()
	for _, d := range []decimal.Decimal{reserved, blocked} {
		if d.Sign() > 0 {
			increase = increase.Add(d)
		}
	}
	// Las liberaciones de la misma operación cuentan como disponibles.
	free := b.Available()
	for _, d := range []decimal.Decimal{reserved, blocked} {
		if d.IsNegative() {
			free = free.Sub(d)
		}
	}
	if increase.Sign() > 0 && free.Cmp(increase) < 0 {
		return Balance{}, fmt.Errorf("%w: disponible %s, se pide comprometer %s", ErrInsufficient, b.Available(), increase)
	}
	b.Reserved, b.Blocked = b.Reserved.Add(reserved), b.Blocked.Add(blocked)
	if b.Reserved.IsNegative() || b.Blocked.IsNegative() {
		return Balance{}, fmt.Errorf("%w: lo reservado o bloqueado quedaría negativo", ErrInvalid)
	}
	return b, nil
}

// NegativeMode es la política de un producto ante la existencia negativa.
type NegativeMode string

const (
	// NegativeInherit (o vacío) sigue a la empresa y su límite.
	NegativeInherit NegativeMode = "inherit"
	NegativeDeny    NegativeMode = "deny"
	// NegativeAllow permite el déficit con un límite propio, si la empresa
	// lo habilitó.
	NegativeAllow NegativeMode = "allow"
)

// CompanyPolicy habilita la excepción de existencia negativa en una empresa.
// Deshabilitada por defecto (sección 24.1).
type CompanyPolicy struct {
	NegativeEnabled bool
	// Limit es el déficit máximo por producto y almacén, en la unidad de
	// cada producto, para los productos que heredan la política.
	Limit decimal.Decimal
	// ChangeID identifica el registro de auditoría de la versión vigente.
	ChangeID id.ID
}

// ProductPolicy ajusta la política de un producto dentro de una empresa.
type ProductPolicy struct {
	Mode     NegativeMode
	Limit    decimal.Decimal // solo con NegativeAllow
	ChangeID id.ID
}

func parseLimit(s string, scale int32) (decimal.Decimal, error) {
	v, err := decimal.Parse(strings.TrimSpace(s))
	if err != nil || v.Sign() <= 0 || v.Scale() > scale {
		return decimal.Decimal{}, fmt.Errorf("%w: límite de déficit %q (positivo, con a lo sumo %d decimales)", ErrInvalid, s, scale)
	}
	return v, nil
}

// NewCompanyPolicy exige un límite al habilitar la excepción.
func NewCompanyPolicy(enabled bool, limit string) (CompanyPolicy, error) {
	if !enabled {
		if strings.TrimSpace(limit) != "" {
			return CompanyPolicy{}, fmt.Errorf("%w: el límite solo aplica al habilitar la existencia negativa", ErrInvalid)
		}
		return CompanyPolicy{}, nil
	}
	if strings.TrimSpace(limit) == "" {
		return CompanyPolicy{}, fmt.Errorf("%w: habilitar la existencia negativa exige un límite de déficit", ErrInvalid)
	}
	l, err := parseLimit(limit, ValueScale)
	return CompanyPolicy{NegativeEnabled: true, Limit: l}, err
}

// NewProductPolicy valida el modo; allow exige un límite con los decimales
// de la unidad del producto.
func NewProductPolicy(mode, limit string, scale int32) (ProductPolicy, error) {
	m := NegativeMode(strings.ToLower(strings.TrimSpace(mode)))
	switch m {
	case NegativeInherit, NegativeDeny:
		if strings.TrimSpace(limit) != "" {
			return ProductPolicy{}, fmt.Errorf("%w: el límite solo aplica al modo allow", ErrInvalid)
		}
		return ProductPolicy{Mode: m}, nil
	case NegativeAllow:
		if strings.TrimSpace(limit) == "" {
			return ProductPolicy{}, fmt.Errorf("%w: el modo allow exige un límite de déficit", ErrInvalid)
		}
		l, err := parseLimit(limit, scale)
		return ProductPolicy{Mode: m, Limit: l}, err
	}
	return ProductPolicy{}, fmt.Errorf("%w: modo %q (use inherit, deny o allow)", ErrInvalid, mode)
}

// NegativeLimit es el déficit permitido para un producto, si lo hay. Un
// producto nunca salta una prohibición de la empresa.
func NegativeLimit(c CompanyPolicy, p ProductPolicy) (decimal.Decimal, bool) {
	switch {
	case !c.NegativeEnabled || p.Mode == NegativeDeny:
		return decimal.Decimal{}, false
	case p.Mode == NegativeAllow:
		return p.Limit, true
	default:
		return c.Limit, true
	}
}

// MaxReferenceLength acota la referencia de una reserva (p. ej. un pedido).
const MaxReferenceLength = 100

func parseReference(s string) (string, error) {
	v := strings.TrimSpace(s)
	if v == "" || len(v) > MaxReferenceLength || strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", fmt.Errorf("%w: la referencia es obligatoria (hasta %d caracteres, sin caracteres de control)", ErrInvalid, MaxReferenceLength)
	}
	return v, nil
}

// HoldStatus es el estado de una reserva o un bloqueo.
type HoldStatus string

const (
	HoldActive HoldStatus = "active"
	// Reservas.
	HoldConsumed HoldStatus = "consumed"
	HoldReleased HoldStatus = "released"
	HoldExpired  HoldStatus = "expired"
	// Bloqueos.
	HoldWrittenOff HoldStatus = "written_off"
)

// Reservation compromete unidades libres de un almacén para un documento.
// No es una salida: la existencia y el CPP no cambian hasta despacharla.
type Reservation struct {
	ID id.ID
	Key
	Quantity, Consumed decimal.Decimal
	Status             HoldStatus
	Reference, Note    string
	// ExpiresAt es opcional: sin vencimiento, la reserva dura hasta que se
	// despache o se libere.
	ExpiresAt      time.Time
	CreatedBy      id.ID
	CreatedAt      time.Time
	ClosedBy       id.ID // cero si venció
	ClosedAt       time.Time
	CloseNote      string
	IdempotencyKey string
	RequestHash    string
}

func NewReservation(k Key, qty decimal.Decimal, reference, note string, expires time.Time, actor id.ID, now time.Time) (Reservation, error) {
	if qty.Sign() <= 0 {
		return Reservation{}, fmt.Errorf("%w: la cantidad debe ser mayor que cero", ErrInvalid)
	}
	ref, err := parseReference(reference)
	if err != nil {
		return Reservation{}, err
	}
	n, err := ParseNote(note, ReasonReservation)
	if err != nil {
		return Reservation{}, err
	}
	if !expires.IsZero() && !expires.After(now) {
		return Reservation{}, fmt.Errorf("%w: el vencimiento debe ser futuro", ErrInvalid)
	}
	rid, err := id.New()
	if err != nil {
		return Reservation{}, err
	}
	if !expires.IsZero() {
		expires = expires.UTC()
	}
	return Reservation{ID: rid, Key: k, Quantity: qty, Consumed: decimal.Zero(), Status: HoldActive, Reference: ref, Note: n,
		ExpiresAt: expires, CreatedBy: actor, CreatedAt: now.UTC()}, nil
}

// Remaining es lo reservado aún sin despachar.
func (r Reservation) Remaining() decimal.Decimal { return r.Quantity.Sub(r.Consumed) }

// Due indica que la reserva activa ya venció.
func (r Reservation) Due(now time.Time) bool {
	return r.Status == HoldActive && !r.ExpiresAt.IsZero() && !now.Before(r.ExpiresAt)
}

// Consume despacha q unidades; al agotarse queda consumida.
func (r Reservation) Consume(q decimal.Decimal, actor id.ID, now time.Time) (Reservation, error) {
	if r.Status != HoldActive {
		return Reservation{}, fmt.Errorf("%w: la reserva está %s", ErrInvalid, r.Status)
	}
	if q.Sign() <= 0 || q.Cmp(r.Remaining()) > 0 {
		return Reservation{}, fmt.Errorf("%w: se pueden despachar hasta %s", ErrInvalid, r.Remaining())
	}
	r.Consumed = r.Consumed.Add(q)
	if r.Remaining().IsZero() {
		r.Status, r.ClosedBy, r.ClosedAt = HoldConsumed, actor, now.UTC()
	}
	return r, nil
}

// Close libera lo pendiente: released por un usuario o expired por
// vencimiento. Solo una vez.
func (r Reservation) Close(status HoldStatus, actor id.ID, note string, now time.Time) (Reservation, error) {
	if status != HoldReleased && status != HoldExpired {
		return Reservation{}, fmt.Errorf("%w: cierre %s", ErrInvalid, status)
	}
	if r.Status != HoldActive {
		return Reservation{}, fmt.Errorf("%w: la reserva ya está %s", ErrInvalid, r.Status)
	}
	n, err := ParseNote(note, ReasonReservation)
	if err != nil {
		return Reservation{}, err
	}
	r.Status, r.ClosedBy, r.ClosedAt, r.CloseNote = status, actor, now.UTC(), n
	return r, nil
}

// BlockReason clasifica la mercancía no vendible (sección 24.5).
type BlockReason string

const (
	BlockDamage         BlockReason = "damage"
	BlockQuarantine     BlockReason = "quarantine"
	BlockQuality        BlockReason = "quality"
	BlockAdministrative BlockReason = "administrative"
	BlockInvestigation  BlockReason = "investigation"
)

// BlockReasonInfo describe un motivo de bloqueo.
type BlockReasonInfo struct {
	Reason      BlockReason
	Description string
}

var blockReasons = []BlockReasonInfo{
	{BlockDamage, "Mercancía dañada"},
	{BlockQuarantine, "Cuarentena"},
	{BlockQuality, "Revisión de calidad"},
	{BlockAdministrative, "Retención administrativa"},
	{BlockInvestigation, "Investigación"},
}

// BlockReasons devuelve los motivos de bloqueo en orden estable.
func BlockReasons() []BlockReasonInfo { return slices.Clone(blockReasons) }

func ParseBlockReason(s string) (BlockReason, error) {
	r := BlockReason(strings.ToLower(strings.TrimSpace(s)))
	if slices.ContainsFunc(blockReasons, func(i BlockReasonInfo) bool { return i.Reason == r }) {
		return r, nil
	}
	return "", fmt.Errorf("%w: motivo de bloqueo %q (use damage, quarantine, quality, administrative o investigation)", ErrInvalid, s)
}

// Block retiene unidades físicas del disponible. No es una salida ni una
// merma: la existencia y el CPP no cambian (sección 24.5).
type Block struct {
	ID id.ID
	Key
	Quantity       decimal.Decimal
	Reason         BlockReason
	Note           string
	Status         HoldStatus
	CreatedBy      id.ID
	CreatedAt      time.Time
	ClosedBy       id.ID
	ClosedAt       time.Time
	CloseNote      string
	IdempotencyKey string
	RequestHash    string
}

func NewBlock(k Key, qty decimal.Decimal, reason BlockReason, note string, actor id.ID, now time.Time) (Block, error) {
	if qty.Sign() <= 0 {
		return Block{}, fmt.Errorf("%w: la cantidad debe ser mayor que cero", ErrInvalid)
	}
	n, err := ParseNote(note, ReasonShrinkage)
	if err != nil {
		return Block{}, err
	}
	bid, err := id.New()
	if err != nil {
		return Block{}, err
	}
	return Block{ID: bid, Key: k, Quantity: qty, Reason: reason, Note: n, Status: HoldActive, CreatedBy: actor, CreatedAt: now.UTC()}, nil
}

// Close libera el bloqueo (released) o lo da de baja como merma
// (written_off). Solo una vez.
func (b Block) Close(status HoldStatus, actor id.ID, note string, now time.Time) (Block, error) {
	if status != HoldReleased && status != HoldWrittenOff {
		return Block{}, fmt.Errorf("%w: cierre %s", ErrInvalid, status)
	}
	if b.Status != HoldActive {
		return Block{}, fmt.Errorf("%w: el bloqueo ya está %s", ErrInvalid, b.Status)
	}
	n, err := ParseNote(note, ReasonShrinkage)
	if err != nil {
		return Block{}, err
	}
	b.Status, b.ClosedBy, b.ClosedAt, b.CloseNote = status, actor, now.UTC(), n
	return b, nil
}

// CountStatus es el estado de un conteo físico (sección 24.6).
type CountStatus string

const (
	CountDraft    CountStatus = "draft"
	CountInReview CountStatus = "in_review"
	CountApplied  CountStatus = "applied"
	CountRejected CountStatus = "rejected"
)

// Count es un conteo físico de un almacén, independiente del ajuste que
// produce al aprobarse.
type Count struct {
	ID           id.ID
	Key          Key // solo el ámbito; ProductID va en cada línea
	Status       CountStatus
	Note         string
	CreatedBy    id.ID
	CreatedAt    time.Time
	SubmittedBy  id.ID
	SubmittedAt  time.Time
	DecidedBy    id.ID
	DecidedAt    time.Time
	DecisionNote string
}

// CountLine es lo contado de un producto y la existencia registrada en ese
// momento. La diferencia se aplica sobre el saldo actual al aprobar, así
// que los movimientos posteriores a la captura se respetan.
type CountLine struct {
	CountID         id.ID
	ProductID       id.ID
	Counted         decimal.Decimal
	Expected        decimal.Decimal
	ExpectedVersion int64
	// UnitCost es opcional: el costo de las unidades que sobran si el
	// saldo no tiene CPP vigente ni último costo.
	UnitCost   Amount
	CapturedBy id.ID
	CapturedAt time.Time
}

// Difference es contado − registrado al capturar.
func (l CountLine) Difference() decimal.Decimal { return l.Counted.Sub(l.Expected) }

func NewCount(k Key, note string, actor id.ID, now time.Time) (Count, error) {
	n, err := ParseNote(note, ReasonCount)
	if err != nil {
		return Count{}, err
	}
	cid, err := id.New()
	if err != nil {
		return Count{}, err
	}
	k.ProductID = id.ID{}
	return Count{ID: cid, Key: k, Status: CountDraft, Note: n, CreatedBy: actor, CreatedAt: now.UTC()}, nil
}

// NewCountLine valida lo contado: nunca negativo y con los decimales de la
// unidad.
func NewCountLine(c Count, b Balance, counted string, scale int32, unitCost Amount, actor id.ID, now time.Time) (CountLine, error) {
	if c.Status != CountDraft {
		return CountLine{}, fmt.Errorf("%w: el conteo está %s; solo se captura en borrador", ErrInvalid, c.Status)
	}
	q, err := decimal.Parse(strings.TrimSpace(counted))
	if err != nil || q.IsNegative() || q.Scale() > scale {
		return CountLine{}, fmt.Errorf("%w: cantidad contada %q (cero o más, con a lo sumo %d decimales)", ErrInvalid, counted, scale)
	}
	return CountLine{CountID: c.ID, ProductID: b.ProductID, Counted: q, Expected: b.OnHand, ExpectedVersion: b.Version,
		UnitCost: unitCost, CapturedBy: actor, CapturedAt: now.UTC()}, nil
}

// Submit pasa el conteo a revisión; exige al menos una línea.
func (c Count) Submit(lines int, actor id.ID, now time.Time) (Count, error) {
	if c.Status != CountDraft {
		return Count{}, fmt.Errorf("%w: el conteo está %s", ErrInvalid, c.Status)
	}
	if lines == 0 {
		return Count{}, fmt.Errorf("%w: el conteo no tiene productos contados", ErrInvalid)
	}
	c.Status, c.SubmittedBy, c.SubmittedAt = CountInReview, actor, now.UTC()
	return c, nil
}

// ErrSameCounter: quien aprueba no puede haber contado ni enviado el conteo.
var ErrSameCounter = fmt.Errorf("%w: el conteo lo debe aprobar un usuario distinto de quien contó", ErrInvalid)

// Decide aplica (approve) o rechaza un conteo en revisión.
func (c Count) Decide(approve bool, lines []CountLine, actor id.ID, note string, now time.Time) (Count, error) {
	if c.Status != CountInReview {
		return Count{}, fmt.Errorf("%w: el conteo está %s; solo se decide en revisión", ErrInvalid, c.Status)
	}
	if actor == c.SubmittedBy || slices.ContainsFunc(lines, func(l CountLine) bool { return l.CapturedBy == actor }) {
		return Count{}, ErrSameCounter
	}
	n, err := ParseNote(note, ReasonCount)
	if err != nil {
		return Count{}, err
	}
	c.Status = CountRejected
	if approve {
		c.Status = CountApplied
	}
	c.DecidedBy, c.DecidedAt, c.DecisionNote = actor, now.UTC(), n
	return c, nil
}

// PolicyChange es una versión de la política de existencia negativa de una
// empresa (ProductID cero) o de un producto en ella. Se registran todas; la
// vigente es la más reciente.
type PolicyChange struct {
	ID             id.ID
	OrganizationID id.ID
	CompanyID      id.ID
	ProductID      id.ID
	Company        CompanyPolicy // si ProductID es cero
	Product        ProductPolicy // si no
	ActorID        id.ID
	OccurredAt     time.Time
}

// NewPolicyChange registra una política de empresa (product cero) o de
// producto.
func NewPolicyChange(orgID, companyID, productID id.ID, c CompanyPolicy, p ProductPolicy, actor id.ID, now time.Time) (PolicyChange, error) {
	cid, err := id.New()
	if err != nil {
		return PolicyChange{}, err
	}
	if p.Mode == "" {
		p.Mode = NegativeInherit
	}
	ch := PolicyChange{ID: cid, OrganizationID: orgID, CompanyID: companyID, ProductID: productID, ActorID: actor, OccurredAt: now.UTC()}
	if productID.IsZero() {
		c.ChangeID = cid
		ch.Company = c
	} else {
		p.ChangeID = cid
		ch.Product = p
	}
	return ch, nil
}

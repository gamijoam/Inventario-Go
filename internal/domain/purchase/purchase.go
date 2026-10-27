// Package purchase modela proveedores, órdenes de compra, recepciones y
// gastos atribuibles (BP-M3-001, secciones 19.2–19.4).
//
// La recepción confirmada es lo único que mueve existencia y costo: cada
// línea entra al almacén con su costo capitalizable (costo de compra más los
// gastos que se le prorratean) y recalcula el CPP. El precio de venta no
// cambia: eso es otro flujo con su propia aprobación (DEC-007.P2).
//
// Los documentos van en su moneda original (BP-M3-002). Al confirmar, cada
// línea se convierte a la moneda de valoración de la empresa con la tasa
// vigente a la fecha del documento, y la tasa aplicada queda registrada.
package purchase

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/exchange"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

var ErrInvalid = errors.New("dato de compra inválido")

// AmountScale es la escala de importes de compra y gastos: la del valor del
// inventario.
const AmountScale = inventory.ValueScale

const rounding = decimal.HalfAwayFromZero

// Valuate lleva amount de la moneda from a la de valoración to, redondeado a
// AmountScale. Sin cambio de moneda no hace falta tasa; con cambio, rate
// debe cubrir el par.
func Valuate(amount decimal.Decimal, from, to money.Currency, rate *exchange.Rate) (decimal.Decimal, error) {
	if from == to {
		return amount.Round(AmountScale, rounding), nil
	}
	if rate == nil || !rate.Covers(from, to) {
		return decimal.Decimal{}, fmt.Errorf("%w: %s", exchange.ErrNoRate, exchange.Pair(from, to))
	}
	return rate.Convert(amount, from, AmountScale)
}

func requireCurrency(c money.Currency) error {
	if c.Code() == "" {
		return fmt.Errorf("%w: falta la moneda", ErrInvalid)
	}
	return nil
}

// Supplier es un proveedor de la organización.
type Supplier struct {
	ID             id.ID
	OrganizationID id.ID
	Code           org.Code
	Name           org.Name
	RIF            org.RIF
	Status         org.Status
	CreatedAt      time.Time
}

func NewSupplier(orgID id.ID, code, name, rif string, now time.Time) (Supplier, error) {
	c, err := org.ParseCode(code)
	if err != nil {
		return Supplier{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	n, err := org.ParseName(name)
	if err != nil {
		return Supplier{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var r org.RIF
	if strings.TrimSpace(rif) != "" {
		if r, err = org.ParseRIF(rif); err != nil {
			return Supplier{}, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	sid, err := id.New()
	if err != nil {
		return Supplier{}, err
	}
	return Supplier{ID: sid, OrganizationID: orgID, Code: c, Name: n, RIF: r, Status: org.StatusActive, CreatedAt: now.UTC()}, nil
}

// ParseAmount lee un importe no negativo con a lo sumo AmountScale decimales.
func ParseAmount(s string) (decimal.Decimal, error) {
	v, err := decimal.Parse(strings.TrimSpace(s))
	if err != nil || v.IsNegative() || v.Scale() > AmountScale {
		return decimal.Decimal{}, fmt.Errorf("%w: importe %q (cero o más, con a lo sumo %d decimales)", ErrInvalid, s, AmountScale)
	}
	return v, nil
}

// MaxTextLength acota notas, conceptos y números de documento.
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

// ---------- Órdenes de compra ----------

type OrderStatus string

const (
	OrderOpen      OrderStatus = "open"
	OrderReceived  OrderStatus = "received"
	OrderCancelled OrderStatus = "cancelled"
)

// OrderLine es lo pedido de un producto y lo recibido hasta ahora.
type OrderLine struct {
	LineNo    int
	ProductID id.ID
	Quantity  decimal.Decimal
	UnitCost  decimal.Decimal // costo pactado
	Received  decimal.Decimal
}

func (l OrderLine) Pending() decimal.Decimal { return l.Quantity.Sub(l.Received) }

// Order es una orden de compra de una sucursal. No mueve existencia.
type Order struct {
	ID         id.ID
	Scope      scope.BusinessScope // sucursal
	Number     string
	SupplierID id.ID
	Currency   money.Currency // de los costos pactados
	Status     OrderStatus
	Note       string
	CreatedBy  id.ID
	CreatedAt  time.Time
	ClosedBy   id.ID
	ClosedAt   time.Time
	Lines      []OrderLine
}

func NewOrder(branch scope.BusinessScope, number string, supplierID id.ID, currency money.Currency, lines []OrderLine, note string, actor id.ID, now time.Time) (Order, error) {
	if err := requireCurrency(currency); err != nil {
		return Order{}, err
	}
	if len(lines) == 0 {
		return Order{}, fmt.Errorf("%w: la orden no tiene líneas", ErrInvalid)
	}
	n, err := parseText(note, "la nota", false)
	if err != nil {
		return Order{}, err
	}
	out := make([]OrderLine, len(lines))
	for i, l := range lines {
		if l.Quantity.Sign() <= 0 || l.UnitCost.IsNegative() {
			return Order{}, fmt.Errorf("%w: línea %d: cantidad positiva y costo no negativo", ErrInvalid, i+1)
		}
		if slices.ContainsFunc(lines[:i], func(o OrderLine) bool { return o.ProductID == l.ProductID }) {
			return Order{}, fmt.Errorf("%w: un producto aparece dos veces en la orden", ErrInvalid)
		}
		l.LineNo, l.Received = i+1, decimal.Zero()
		out[i] = l
	}
	oid, err := id.New()
	if err != nil {
		return Order{}, err
	}
	return Order{ID: oid, Scope: branch, Number: number, SupplierID: supplierID, Currency: currency, Status: OrderOpen, Note: n, CreatedBy: actor, CreatedAt: now.UTC(), Lines: out}, nil
}

// Line devuelve la línea lineNo.
func (o Order) Line(lineNo int) (OrderLine, bool) {
	i := slices.IndexFunc(o.Lines, func(l OrderLine) bool { return l.LineNo == lineNo })
	if i < 0 {
		return OrderLine{}, false
	}
	return o.Lines[i], true
}

// Receive suma qty a lo recibido de una línea; al completarse todas, la
// orden queda recibida.
func (o Order) Receive(lineNo int, qty decimal.Decimal) (Order, error) {
	if o.Status != OrderOpen {
		return Order{}, fmt.Errorf("%w: la orden %s está %s", ErrInvalid, o.Number, o.Status)
	}
	i := slices.IndexFunc(o.Lines, func(l OrderLine) bool { return l.LineNo == lineNo })
	if i < 0 {
		return Order{}, fmt.Errorf("%w: la orden %s no tiene línea %d", ErrInvalid, o.Number, lineNo)
	}
	if qty.Cmp(o.Lines[i].Pending()) > 0 {
		return Order{}, fmt.Errorf("%w: la línea %d de %s tiene pendiente %s", ErrInvalid, lineNo, o.Number, o.Lines[i].Pending())
	}
	o.Lines = slices.Clone(o.Lines)
	o.Lines[i].Received = o.Lines[i].Received.Add(qty)
	if !slices.ContainsFunc(o.Lines, func(l OrderLine) bool { return l.Pending().Sign() > 0 }) {
		o.Status = OrderReceived
	}
	return o, nil
}

// Cancel cierra lo pendiente de una orden abierta.
func (o Order) Cancel(actor id.ID, now time.Time) (Order, error) {
	if o.Status != OrderOpen {
		return Order{}, fmt.Errorf("%w: la orden %s está %s", ErrInvalid, o.Number, o.Status)
	}
	o.Status, o.ClosedBy, o.ClosedAt = OrderCancelled, actor, now.UTC()
	return o, nil
}

// ---------- Recepciones ----------

type ReceiptStatus string

const (
	ReceiptDraft     ReceiptStatus = "draft"
	ReceiptConfirmed ReceiptStatus = "confirmed"
	ReceiptCancelled ReceiptStatus = "cancelled"
)

// ReceiptLine es lo recibido de un producto con su costo real de compra.
type ReceiptLine struct {
	LineNo      int
	ProductID   id.ID
	OrderLineNo int // 0 si no viene de una orden
	Quantity    decimal.Decimal
	UnitCost    decimal.Decimal // en la moneda del documento
	// Valued es el costo de compra en la moneda de valoración; se fija al
	// confirmar.
	Valued decimal.Decimal
	// Expenses es lo capitalizado de gastos prorrateados a la línea, en la
	// moneda de valoración.
	Expenses   decimal.Decimal
	MovementID id.ID // entrada al kardex, al confirmar
}

// BaseCost es cantidad × costo unitario en la moneda del documento, a la
// escala del valor.
func (l ReceiptLine) BaseCost() decimal.Decimal {
	return l.Quantity.Mul(l.UnitCost).Round(AmountScale, rounding)
}

// TotalCost es el costo capitalizable de la línea en la moneda de
// valoración: compra + gastos.
func (l ReceiptLine) TotalCost() decimal.Decimal { return l.Valued.Add(l.Expenses) }

// Receipt es la entrada física de mercancía de un proveedor a un almacén.
type Receipt struct {
	ID               id.ID
	Scope            scope.BusinessScope // almacén
	Number           string
	SupplierID       id.ID
	OrderID          id.ID // cero si es directa
	Currency         money.Currency
	DocumentDate     time.Time // fecha comercial de la factura; decide la tasa
	RateID           id.ID     // tasa aplicada al confirmar; cero sin conversión
	Status           ReceiptStatus
	SupplierDocument string // p. ej. el número de factura
	Note             string
	CreatedBy        id.ID
	CreatedAt        time.Time
	ClosedBy         id.ID // quien confirmó o canceló
	ClosedAt         time.Time
	Lines            []ReceiptLine
}

func NewReceipt(warehouse scope.BusinessScope, number string, supplierID, orderID id.ID, currency money.Currency, date time.Time, document, note string, actor id.ID, now time.Time) (Receipt, error) {
	if err := requireCurrency(currency); err != nil {
		return Receipt{}, err
	}
	if err := documentDate(date, now); err != nil {
		return Receipt{}, err
	}
	doc, err := parseText(document, "el documento del proveedor", false)
	if err != nil {
		return Receipt{}, err
	}
	n, err := parseText(note, "la nota", false)
	if err != nil {
		return Receipt{}, err
	}
	rid, err := id.New()
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{ID: rid, Scope: warehouse, Number: number, SupplierID: supplierID, OrderID: orderID, Currency: currency, DocumentDate: date, Status: ReceiptDraft,
		SupplierDocument: doc, Note: n, CreatedBy: actor, CreatedAt: now.UTC()}, nil
}

// SetLine agrega la línea de un producto o reemplaza la existente.
func (r Receipt) SetLine(l ReceiptLine) (Receipt, error) {
	if r.Status != ReceiptDraft {
		return Receipt{}, fmt.Errorf("%w: la recepción %s está %s", ErrInvalid, r.Number, r.Status)
	}
	if l.Quantity.Sign() <= 0 || l.UnitCost.IsNegative() {
		return Receipt{}, fmt.Errorf("%w: cantidad positiva y costo no negativo", ErrInvalid)
	}
	l.Valued, l.Expenses, l.MovementID = decimal.Zero(), decimal.Zero(), id.ID{}
	r.Lines = slices.Clone(r.Lines)
	if i := slices.IndexFunc(r.Lines, func(x ReceiptLine) bool { return x.ProductID == l.ProductID }); i >= 0 {
		l.LineNo = r.Lines[i].LineNo
		r.Lines[i] = l
		return r, nil
	}
	l.LineNo = 1
	for _, x := range r.Lines {
		l.LineNo = max(l.LineNo, x.LineNo+1)
	}
	r.Lines = append(r.Lines, l)
	return r, nil
}

// RemoveLine quita la línea de un producto en borrador.
func (r Receipt) RemoveLine(productID id.ID) (Receipt, error) {
	if r.Status != ReceiptDraft {
		return Receipt{}, fmt.Errorf("%w: la recepción %s está %s", ErrInvalid, r.Number, r.Status)
	}
	i := slices.IndexFunc(r.Lines, func(x ReceiptLine) bool { return x.ProductID == productID })
	if i < 0 {
		return Receipt{}, fmt.Errorf("%w: el producto no está en la recepción", ErrInvalid)
	}
	r.Lines = slices.Delete(slices.Clone(r.Lines), i, i+1)
	return r, nil
}

// documentDate exige una fecha comercial que no sea posterior a hoy.
func documentDate(date, now time.Time) error {
	if date.IsZero() {
		return fmt.Errorf("%w: falta la fecha del documento", ErrInvalid)
	}
	if date.After(timezone.DateOf(now)) {
		return fmt.Errorf("%w: la fecha %s es futura", ErrInvalid, date.Format(timezone.DateLayout))
	}
	return nil
}

// Valuate fija el costo de compra de cada línea en la moneda de valoración
// to, con la tasa vigente a la fecha del documento (nil sin conversión).
func (r Receipt) Valuate(to money.Currency, rate *exchange.Rate) (Receipt, error) {
	r.Lines = slices.Clone(r.Lines)
	r.RateID = id.ID{}
	if r.Currency != to {
		if rate == nil {
			return Receipt{}, fmt.Errorf("%w: %s", exchange.ErrNoRate, exchange.Pair(r.Currency, to))
		}
		r.RateID = rate.ID
	}
	for i, l := range r.Lines {
		v, err := Valuate(l.Quantity.Mul(l.UnitCost), r.Currency, to, rate)
		if err != nil {
			return Receipt{}, err
		}
		r.Lines[i].Valued = v
	}
	return r, nil
}

// Confirm cierra el borrador; el servicio registra en la misma transacción
// las entradas al kardex.
func (r Receipt) Confirm(actor id.ID, now time.Time) (Receipt, error) {
	if r.Status != ReceiptDraft {
		return Receipt{}, fmt.Errorf("%w: la recepción %s está %s", ErrInvalid, r.Number, r.Status)
	}
	if len(r.Lines) == 0 {
		return Receipt{}, fmt.Errorf("%w: la recepción %s no tiene líneas", ErrInvalid, r.Number)
	}
	r.Status, r.ClosedBy, r.ClosedAt = ReceiptConfirmed, actor, now.UTC()
	return r, nil
}

// Cancel descarta un borrador.
func (r Receipt) Cancel(actor id.ID, now time.Time) (Receipt, error) {
	if r.Status != ReceiptDraft {
		return Receipt{}, fmt.Errorf("%w: la recepción %s está %s; una confirmada no se cancela", ErrInvalid, r.Number, r.Status)
	}
	r.Status, r.ClosedBy, r.ClosedAt = ReceiptCancelled, actor, now.UTC()
	return r, nil
}

// ---------- Gastos atribuibles ----------

// Criterion es la base del prorrateo de un gasto (sección 19.3).
type Criterion string

const (
	ByValue    Criterion = "value"
	ByQuantity Criterion = "quantity"
)

func ParseCriterion(s string) (Criterion, error) {
	switch c := Criterion(strings.ToLower(strings.TrimSpace(s))); c {
	case "", ByValue:
		return ByValue, nil
	case ByQuantity:
		return c, nil
	}
	return "", fmt.Errorf("%w: criterio de prorrateo %q (use value o quantity)", ErrInvalid, s)
}

// Allocation es la parte de un gasto que corresponde a una línea. Capitalized
// entra al valor del inventario; Variance es la parte de unidades que ya no
// están en el almacén (gasto tardío) y queda como diferencia de costo.
type Allocation struct {
	LineNo      int
	Base        decimal.Decimal
	Amount      decimal.Decimal
	Capitalized decimal.Decimal
	Variance    decimal.Decimal
	MovementID  id.ID // ajuste de valoración de un gasto tardío
}

// Expense es un gasto atribuible a una recepción (flete, seguro…). Va en
// su moneda; Valued es su importe en la moneda de valoración, convertido
// al registrarlo con la tasa vigente a su fecha.
type Expense struct {
	ID          id.ID
	ReceiptID   id.ID
	Concept     string
	Amount      decimal.Decimal
	Currency    money.Currency
	Date        time.Time // fecha comercial del comprobante
	RateID      id.ID     // cero sin conversión
	Valued      decimal.Decimal
	Criterion   Criterion
	Late        bool // registrado tras confirmar la recepción
	CreatedBy   id.ID
	CreatedAt   time.Time
	Allocations []Allocation
}

// NewExpense registra un gasto en currency con fecha date y lo convierte a
// la moneda de valoración to con rate (nil si no hay cambio de moneda).
func NewExpense(r Receipt, concept string, amount decimal.Decimal, currency money.Currency, date time.Time, c Criterion,
	to money.Currency, rate *exchange.Rate, actor id.ID, now time.Time) (Expense, error) {
	if err := requireCurrency(currency); err != nil {
		return Expense{}, err
	}
	if err := documentDate(date, now); err != nil {
		return Expense{}, err
	}
	if r.Status == ReceiptCancelled {
		return Expense{}, fmt.Errorf("%w: la recepción %s está cancelada", ErrInvalid, r.Number)
	}
	con, err := parseText(concept, "el concepto del gasto", true)
	if err != nil {
		return Expense{}, err
	}
	if amount.Sign() <= 0 {
		return Expense{}, fmt.Errorf("%w: el gasto debe ser mayor que cero", ErrInvalid)
	}
	valued, err := Valuate(amount, currency, to, rate)
	if err != nil {
		return Expense{}, err
	}
	if valued.Sign() <= 0 {
		return Expense{}, fmt.Errorf("%w: el gasto convertido a %s es cero", ErrInvalid, to)
	}
	var rateID id.ID
	if currency != to {
		rateID = rate.ID
	}
	eid, err := id.New()
	if err != nil {
		return Expense{}, err
	}
	return Expense{ID: eid, ReceiptID: r.ID, Concept: con, Amount: amount, Currency: currency, Date: date, RateID: rateID, Valued: valued,
		Criterion: c, Late: r.Status == ReceiptConfirmed, CreatedBy: actor, CreatedAt: now.UTC()}, nil
}

// Allocate reparte amount entre las líneas según el criterio, redondeando
// a AmountScale. El residuo de redondeo va a la línea de mayor base (la
// primera si empatan), para que la suma sea exactamente amount.
func Allocate(lines []ReceiptLine, amount decimal.Decimal, c Criterion) ([]Allocation, error) {
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: la recepción no tiene líneas a las que prorratear", ErrInvalid)
	}
	out := make([]Allocation, len(lines))
	total, largest := decimal.Zero(), 0
	for i, l := range lines {
		base := l.Quantity
		if c == ByValue {
			base = l.Valued
		}
		out[i] = Allocation{LineNo: l.LineNo, Base: base}
		total = total.Add(base)
		if base.Cmp(out[largest].Base) > 0 {
			largest = i
		}
	}
	if total.Sign() <= 0 {
		return nil, fmt.Errorf("%w: las líneas no tienen valor; prorratee por cantidad", ErrInvalid)
	}
	sum := decimal.Zero()
	for i := range out {
		a, err := amount.Mul(out[i].Base).Div(total, AmountScale, rounding)
		if err != nil {
			return nil, err
		}
		out[i].Amount, out[i].Capitalized, out[i].Variance = a, a, decimal.Zero()
		sum = sum.Add(a)
	}
	out[largest].Amount = out[largest].Amount.Add(amount.Sub(sum))
	out[largest].Capitalized = out[largest].Amount
	return out, nil
}

// LateShare divide la parte de un gasto tardío de una línea entre lo que
// todavía está en el almacén (remaining, a lo sumo lo recibido) y lo que ya
// salió: lo primero se capitaliza, lo segundo queda como diferencia.
func LateShare(a Allocation, received, onHand decimal.Decimal) Allocation {
	remaining := onHand
	if remaining.Cmp(received) > 0 {
		remaining = received
	}
	if remaining.Sign() <= 0 {
		a.Capitalized, a.Variance = decimal.Zero(), a.Amount
		return a
	}
	capitalized, err := a.Amount.Mul(remaining).Div(received, AmountScale, rounding)
	if err != nil {
		panic(err) // received nunca es cero
	}
	a.Capitalized, a.Variance = capitalized, a.Amount.Sub(capitalized)
	return a
}

// LastPurchase es el último costo de compra de un producto en una empresa:
// la línea confirmada más reciente.
type LastPurchase struct {
	UnitCost   decimal.Decimal // costo de compra en la moneda del documento, sin gastos
	Currency   money.Currency  // del documento
	ValuedUnit decimal.Decimal // costo de compra en la moneda de valoración
	TotalUnit  decimal.Decimal // en la moneda de valoración, con gastos capitalizados al recibir
	Receipt    string
	ReceivedAt time.Time
}

// ---------- Costo de reposición ----------

// ReplacementCost es una estimación comercial del costo de volver a
// comprar un producto (sección 19.3). Nunca sobrescribe el CPP ni cambia
// precios por sí sola. Cada estimación nueva reemplaza a la anterior, que
// queda en el historial.
type ReplacementCost struct {
	ID             id.ID
	OrganizationID id.ID
	CompanyID      id.ID
	ProductID      id.ID
	UnitCost       decimal.Decimal
	Currency       money.Currency
	Note           string
	ActorID        id.ID
	OccurredAt     time.Time
}

func NewReplacementCost(orgID, companyID, productID id.ID, unitCost string, currency money.Currency, note string, actor id.ID, now time.Time) (ReplacementCost, error) {
	if err := requireCurrency(currency); err != nil {
		return ReplacementCost{}, err
	}
	c, err := inventory.ParseUnitCost(unitCost)
	if err != nil {
		return ReplacementCost{}, err
	}
	n, err := parseText(note, "la nota", false)
	if err != nil {
		return ReplacementCost{}, err
	}
	rid, err := id.New()
	if err != nil {
		return ReplacementCost{}, err
	}
	return ReplacementCost{ID: rid, OrganizationID: orgID, CompanyID: companyID, ProductID: productID, UnitCost: c, Currency: currency, Note: n, ActorID: actor, OccurredAt: now.UTC()}, nil
}

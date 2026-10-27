package inventory

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

// Valoración a costo promedio ponderado móvil por producto y almacén
// (DEC-007.E2, secciones 21.1, 25.3 y 25.8; ratificado 2026-10-27).
//
// El saldo guarda el valor total de la existencia, no el CPP: el CPP es
// valor ÷ existencia, una derivación. Cada salida cuesta
// valor × cantidad ÷ existencia redondeado a ValueScale, y la salida que
// agota la existencia se lleva exactamente el valor restante. Así nunca
// queda valor fantasma con existencia cero y el valor se conserva sin
// residuos de redondeo.

const (
	// ValueScale es la escala del valor de saldos y movimientos.
	ValueScale int32 = 6
	// UnitCostScale es la escala del costo unitario declarado o derivado.
	UnitCostScale int32 = 12
	// maxUnitCostDigits acota la parte entera de un costo unitario.
	maxUnitCostDigits = 16
)

// valueRounding es el redondeo de todo importe de valoración.
const valueRounding = decimal.HalfAwayFromZero

var (
	// ErrCostRequired: una entrada sin costo declarado y sin CPP vigente.
	ErrCostRequired = fmt.Errorf("%w: el saldo no tiene costo promedio vigente; indique el costo unitario", ErrInvalid)
	// ErrValuation: el estado de valoración del saldo impide la operación.
	ErrValuation = errors.New("la valoración del saldo no permite la operación")
)

// Amount es un importe que puede ser desconocido: el de existencia anterior
// a la valoración que aún no tiene costo.
type Amount struct {
	Value decimal.Decimal
	Known bool
}

// Known construye un importe conocido.
func Known(d decimal.Decimal) Amount { return Amount{Value: d, Known: true} }

func (a Amount) Equal(b Amount) bool {
	return a.Known == b.Known && (!a.Known || a.Value.Equal(b.Value))
}

func (a Amount) String() string {
	if !a.Known {
		return "sin valor"
	}
	return a.Value.String()
}

// normalized trata la existencia cero sin valor como valor cero: un saldo
// vacío no puede valer nada.
func normalized(onHand decimal.Decimal, v Amount) Amount {
	if !v.Known && onHand.IsZero() {
		return Known(decimal.Zero())
	}
	return v
}

// EffectiveValue es el valor del saldo; la existencia cero vale cero aunque
// nunca se haya valorado.
func (b Balance) EffectiveValue() Amount { return normalized(b.OnHand, b.Value) }

// UnitCost es el CPP vigente; desconocido sin existencia o sin valor.
func (b Balance) UnitCost() Amount {
	v := b.EffectiveValue()
	if !v.Known || b.OnHand.Sign() <= 0 {
		return Amount{}
	}
	return Known(unit(v.Value, b.OnHand))
}

func unit(value, qty decimal.Decimal) decimal.Decimal {
	u, err := value.Div(qty, UnitCostScale, valueRounding)
	if err != nil {
		panic(err) // qty nunca es cero aquí
	}
	return u
}

func share(value, num, den decimal.Decimal) decimal.Decimal {
	v, err := value.Mul(num).Div(den, ValueScale, valueRounding)
	if err != nil {
		panic(err) // den nunca es cero aquí
	}
	return v
}

// ParseUnitCost lee un costo unitario no negativo con a lo sumo
// UnitCostScale decimales.
func ParseUnitCost(s string) (decimal.Decimal, error) {
	v := strings.TrimSpace(s)
	c, err := decimal.Parse(v)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%w: costo unitario %q", ErrInvalid, s)
	}
	if c.IsNegative() {
		return decimal.Decimal{}, fmt.Errorf("%w: el costo unitario no puede ser negativo", ErrInvalid)
	}
	if c.Scale() > UnitCostScale {
		return decimal.Decimal{}, fmt.Errorf("%w: el costo unitario admite %d decimales", ErrInvalid, UnitCostScale)
	}
	if whole, _, _ := strings.Cut(v, "."); len(whole) > maxUnitCostDigits {
		return decimal.Decimal{}, fmt.Errorf("%w: costo unitario demasiado grande", ErrInvalid)
	}
	return c, nil
}

// Costing indica cómo valorar un movimiento y, en una salida, qué la
// autoriza más allá del disponible. El valor cero valora al CPP vigente:
// así salen todas las salidas y entran las entradas sin costo.
type Costing struct {
	// UnitCost es el costo unitario declarado de una entrada.
	UnitCost Amount
	// Fixed es el costo total con el signo de la cantidad: el de un
	// reverso, que deshace exactamente lo que entró o salió, o el de la
	// entrada de una transferencia, que recibe el valor que salió del origen.
	Fixed Amount
	// AllowUnknown admite una entrada sin costo en un saldo sin valorar
	// (reverso de una salida que no tenía costo).
	AllowUnknown bool
	// ValuesPrior permite que una entrada de costo fijo valore también la
	// existencia previa sin valor, a su costo unitario: una compra real
	// puede hacerlo; una transferencia o un reverso, no.
	ValuesPrior bool
	// Overdraft es el déficit máximo que una excepción autorizada permite
	// dejar (DEC-007.F2). Las unidades en déficit salen al último CPP
	// válido, como costo provisional.
	Overdraft Amount
	// Correction es un ajuste por conteo: refleja lo que hay aunque deje lo
	// reservado o bloqueado sin respaldo, sin llegar a existencia negativa.
	Correction bool
}

type valuation struct {
	unitCost, cost, after Amount
	adjustment            decimal.Decimal
}

func valuate(b Balance, q decimal.Decimal, c Costing) (valuation, error) {
	cur := normalized(b.OnHand, b.Value)
	after := b.OnHand.Add(q)
	if q.Sign() < 0 {
		return valuateExit(b, cur, q, after, c)
	}

	// Entrada: primero su costo total.
	var cost decimal.Decimal
	var uc Amount
	switch {
	case c.Fixed.Known:
		cost = c.Fixed.Value
		if cost.IsNegative() {
			return valuation{}, fmt.Errorf("%w: el costo fijo debe tener el signo de la cantidad", ErrInvalid)
		}
		if !cur.Known && !c.ValuesPrior {
			return valuation{}, fmt.Errorf("%w: la existencia del almacén no está valorada; registre antes una entrada con costo unitario", ErrValuation)
		}
		uc = Known(unit(cost, q))
	case c.UnitCost.Known:
		if c.UnitCost.Value.IsNegative() {
			return valuation{}, fmt.Errorf("%w: el costo unitario no puede ser negativo", ErrInvalid)
		}
		uc = c.UnitCost
		cost = q.Mul(uc.Value).Round(ValueScale, valueRounding)
	default:
		cpp := b.UnitCost()
		if !cpp.Known {
			if !cur.Known && c.AllowUnknown {
				return valuation{}, nil
			}
			return valuation{}, ErrCostRequired
		}
		uc, cost = cpp, share(cur.Value, q, b.OnHand)
	}
	v := valuation{unitCost: uc, cost: Known(cost)}
	switch {
	case !cur.Known:
		// La primera entrada con costo valora también la existencia previa.
		v.adjustment = b.OnHand.Mul(uc.Value).Round(ValueScale, valueRounding)
		v.after = Known(cost.Add(v.adjustment))
	case b.OnHand.IsNegative():
		v.after = Known(settle(cur.Value, b.OnHand, q, cost))
		v.adjustment = v.after.Value.Sub(cur.Value).Sub(cost)
	default:
		v.after = Known(cur.Value.Add(cost))
	}
	return v, nil
}

// settle aplica una entrada de cantidad q y costo total cost a un saldo en
// déficit (onHand < 0, valor provisional value ≤ 0). Las primeras unidades
// cubren el déficit: su costo real reemplaza el provisional y la diferencia
// queda como ajuste de valor; el resto entra a su costo (sección 24.7).
func settle(value, onHand, q, cost decimal.Decimal) decimal.Decimal {
	deficit := onHand.Neg()
	covered := q
	if deficit.Cmp(q) < 0 {
		covered = deficit
	}
	provisional := value.Neg()
	if !covered.Equal(deficit) {
		provisional = share(value.Neg(), covered, deficit)
	}
	actual := cost
	if !covered.Equal(q) {
		actual = share(cost, covered, q)
	}
	return value.Add(provisional).Add(cost.Sub(actual))
}

func valuateExit(b Balance, cur Amount, q, after decimal.Decimal, c Costing) (valuation, error) {
	if c.UnitCost.Known {
		return valuation{}, fmt.Errorf("%w: una salida se valora al costo promedio", ErrInvalid)
	}
	if !cur.Known {
		if c.Fixed.Known {
			return valuation{}, fmt.Errorf("%w: la existencia del almacén no está valorada; registre antes una entrada con costo unitario", ErrValuation)
		}
		if after.IsNegative() {
			return valuation{}, fmt.Errorf("%w: la existencia no está valorada; no hay costo con qué valorar un déficit", ErrValuation)
		}
		v := valuation{}
		if after.IsZero() {
			v.after = Known(decimal.Zero())
		}
		return v, nil
	}
	if c.Fixed.Known {
		cost := c.Fixed.Value
		if cost.Sign() > 0 {
			return valuation{}, fmt.Errorf("%w: el costo fijo debe tener el signo de la cantidad", ErrInvalid)
		}
		if after.IsNegative() {
			return valuation{}, fmt.Errorf("%w: un costo fijo no se aplica a un déficit", ErrValuation)
		}
		v := valuation{unitCost: Known(unit(cost.Abs(), q.Abs())), cost: Known(cost)}
		total := cur.Value.Add(cost)
		switch {
		case after.IsZero():
			v.adjustment, total = total.Neg(), decimal.Zero()
		case total.IsNegative():
			return valuation{}, fmt.Errorf("%w: el valor de la existencia (%s) no alcanza para sacar %s", ErrValuation, cur.Value, cost.Neg())
		}
		v.after = Known(total)
		return v, nil
	}
	if !after.IsNegative() {
		cost := cur.Value.Neg()
		if !after.IsZero() {
			cost = share(cur.Value, q.Abs(), b.OnHand).Neg()
		}
		return valuation{unitCost: b.UnitCost(), cost: Known(cost), after: Known(cur.Value.Add(cost))}, nil
	}
	// Déficit: lo positivo sale con todo su valor y el resto al último CPP
	// válido, como costo provisional.
	if !b.LastUnitCost.Known {
		return valuation{}, fmt.Errorf("%w: el producto nunca tuvo costo en este almacén; no hay costo provisional para el déficit", ErrValuation)
	}
	cost := deficitCost(b.OnHand, cur.Value, q, b.LastUnitCost)
	return valuation{unitCost: Known(unit(cost.Value.Abs(), q.Abs())), cost: cost, after: Known(cur.Value.Add(cost.Value))}, nil
}

// deficitCost es el costo de una salida que deja la existencia negativa: el
// valor de lo positivo más el déficit al último CPP válido.
func deficitCost(onHand, value, q decimal.Decimal, last Amount) Amount {
	if !last.Known {
		return Amount{}
	}
	short := onHand.Add(q).Neg()
	if onHand.IsNegative() {
		short = q.Abs()
	}
	cost := short.Mul(last.Value).Round(ValueScale, valueRounding).Neg()
	if onHand.Sign() > 0 {
		cost = cost.Sub(value)
	}
	return Known(cost)
}

// LastValidCost es el CPP de un saldo con existencia positiva valorada; el
// repositorio lo usa para derivar el último costo válido del kardex.
func LastValidCost(onHand decimal.Decimal, value Amount) Amount {
	return lastCost(onHand, value, Amount{})
}

// lastCost es el CPP tras un movimiento si hay existencia positiva
// valorada, o el último conocido si no.
func lastCost(onHand decimal.Decimal, value, prev Amount) Amount {
	if onHand.Sign() > 0 && value.Known {
		return Known(unit(value.Value, onHand))
	}
	return prev
}

// replayValue comprueba la valoración de m sobre la existencia y el valor
// previos y devuelve el valor resultante.
func replayValue(before decimal.Decimal, value, last Amount, m Movement) (Amount, error) {
	adj := m.ValueAdjustment
	if m.Cost.Known != m.UnitCost.Known {
		return Amount{}, errors.New("costo y costo unitario incompletos")
	}
	if !m.ValueAfter.Known {
		if value.Known {
			return Amount{}, errors.New("el saldo valorado perdió su valor")
		}
		if m.Cost.Known || !adj.IsZero() {
			return Amount{}, errors.New("costo sin valor resultante")
		}
		return Amount{}, nil
	}
	after := m.ValueAfter.Value
	switch m.OnHandAfter.Sign() {
	case 0:
		if !after.IsZero() {
			return Amount{}, fmt.Errorf("existencia cero con valor %s", after)
		}
	case 1:
		if after.IsNegative() {
			return Amount{}, fmt.Errorf("valor negativo %s con existencia positiva", after)
		}
	default:
		if after.Sign() > 0 {
			return Amount{}, fmt.Errorf("déficit con valor positivo %s", after)
		}
	}
	prev := normalized(before, value)
	switch {
	case prev.Known:
		if !m.Cost.Known {
			return Amount{}, errors.New("movimiento sin costo en un saldo valorado")
		}
		if m.OnHandAfter.IsNegative() && m.Quantity.IsNegative() {
			if want := deficitCost(before, prev.Value, m.Quantity, last); !want.Equal(m.Cost) {
				return Amount{}, fmt.Errorf("la salida en déficit debía costar %s y registra %s", want, m.Cost)
			}
		}
		if before.IsNegative() && m.Quantity.Sign() > 0 {
			if want := settle(prev.Value, before, m.Quantity, m.Cost.Value); !want.Equal(after) {
				return Amount{}, fmt.Errorf("la liquidación del déficit debía dejar %s y registra %s", want, after)
			}
		} else if !adj.IsZero() && !m.OnHandAfter.IsZero() && m.Kind != KindRevaluation {
			return Amount{}, fmt.Errorf("ajuste de valor %s sin agotar la existencia", adj)
		}
		if want := prev.Value.Add(m.Cost.Value).Add(adj); !want.Equal(after) {
			return Amount{}, fmt.Errorf("valor %s + costo %s + ajuste %s no da %s", prev.Value, m.Cost.Value, adj, after)
		}
	case m.Cost.Known:
		if want := before.Mul(m.UnitCost.Value).Round(ValueScale, valueRounding); !adj.Equal(want) {
			return Amount{}, fmt.Errorf("la valoración de la existencia previa es %s y debía ser %s", adj, want)
		}
		if want := m.Cost.Value.Add(adj); !want.Equal(after) {
			return Amount{}, fmt.Errorf("costo %s + valoración previa %s no da %s", m.Cost.Value, adj, after)
		}
	default:
		if !after.IsZero() || !adj.IsZero() {
			return Amount{}, errors.New("valor sin costo")
		}
	}
	return m.ValueAfter, nil
}

// Transfer mueve existencia entre dos almacenes de una misma sucursal: una
// salida del origen y una entrada al destino, vinculadas y en una sola
// transacción. El destino recibe exactamente el valor que sale del origen,
// sin utilidad interna (sección 21.1, invariante 3).
type Transfer struct {
	ID         id.ID
	From, To   scope.BusinessScope // ámbitos de almacén
	ProductID  id.ID
	Quantity   decimal.Decimal // positiva
	Value      decimal.Decimal // valor trasladado
	Note       string
	ActorID    id.ID
	OccurredAt time.Time
	// IdempotencyKey evita duplicar la transferencia ante un reintento.
	IdempotencyKey string
	RequestHash    string
}

// NewTransfer valida una transferencia; Value se fija al valorar la salida.
func NewTransfer(from, to scope.BusinessScope, productID id.ID, qty decimal.Decimal, note string, actor id.ID, now time.Time) (Transfer, error) {
	if from.Require(scope.LevelWarehouse) != nil || to.Require(scope.LevelWarehouse) != nil {
		return Transfer{}, fmt.Errorf("%w: una transferencia va de almacén a almacén", ErrInvalid)
	}
	if from.Organization() != to.Organization() || from.Company() != to.Company() || from.Branch() != to.Branch() {
		return Transfer{}, fmt.Errorf("%w: solo se transfiere entre almacenes de la misma sucursal; entre sucursales hay despacho y recepción (DEC-006)", ErrInvalid)
	}
	if from.Warehouse() == to.Warehouse() {
		return Transfer{}, fmt.Errorf("%w: el almacén de origen y el de destino son el mismo", ErrInvalid)
	}
	if qty.Sign() <= 0 {
		return Transfer{}, fmt.Errorf("%w: la cantidad debe ser mayor que cero", ErrInvalid)
	}
	tid, err := id.New()
	if err != nil {
		return Transfer{}, err
	}
	return Transfer{ID: tid, From: from, To: to, ProductID: productID, Quantity: qty, Note: note, ActorID: actor, OccurredAt: now.UTC()}, nil
}

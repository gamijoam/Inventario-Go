// Package purchasing contiene los casos de uso de compras (BP-M3-001):
// proveedores, órdenes, recepciones con gastos atribuibles y costo de
// reposición.
//
// Confirmar una recepción hace entrar cada línea al almacén con su costo
// capitalizable (compra + gastos prorrateados) en la misma transacción que
// cierra el documento y avanza la orden: el CPP cambia ahí. El precio de
// venta no se toca, y el costo de reposición es una estimación aparte que
// nunca sobrescribe el CPP (sección 19.3).
//
// Los documentos van en su moneda (BP-M3-002). Al confirmar, cada línea se
// convierte a la moneda de valoración de la empresa con la tasa vigente a
// la fecha del documento; un gasto se convierte al registrarse con la de
// su propia fecha. Sin tasa vigente la operación se rechaza.
package purchasing

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/application/rates"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/exchange"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/purchase"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

// Repository amplía el de existencias con los documentos de compra y las
// tasas; todo comparte la transacción.
type Repository interface {
	stock.Repository
	rates.Repository

	InsertSupplier(ctx context.Context, s purchase.Supplier) error
	Supplier(ctx context.Context, orgID, supplierID id.ID) (purchase.Supplier, error)
	SupplierByCode(ctx context.Context, orgID id.ID, code org.Code) (purchase.Supplier, error)
	Suppliers(ctx context.Context, orgID id.ID) ([]purchase.Supplier, error)

	InsertOrder(ctx context.Context, o purchase.Order) error
	// UpdateOrder guarda estado, cierre y lo recibido de cada línea.
	UpdateOrder(ctx context.Context, o purchase.Order) error
	// Order lee una orden con sus líneas; forUpdate la retiene hasta el fin
	// de la transacción.
	Order(ctx context.Context, orgID, orderID id.ID, forUpdate bool) (purchase.Order, error)
	OrderByNumber(ctx context.Context, orgID id.ID, number string) (purchase.Order, error)
	Orders(ctx context.Context, orgID id.ID, status purchase.OrderStatus) ([]purchase.Order, error)

	InsertReceipt(ctx context.Context, r purchase.Receipt) error
	// UpdateReceipt guarda estado y cierre solo si seguía en borrador;
	// stock.ErrConcurrent si otro proceso la cerró.
	UpdateReceipt(ctx context.Context, r purchase.Receipt) error
	// SaveReceiptLines reemplaza las líneas de un borrador.
	SaveReceiptLines(ctx context.Context, r purchase.Receipt) error
	Receipt(ctx context.Context, orgID, receiptID id.ID, forUpdate bool) (purchase.Receipt, error)
	ReceiptByNumber(ctx context.Context, orgID id.ID, number string) (purchase.Receipt, error)
	Receipts(ctx context.Context, orgID id.ID, status purchase.ReceiptStatus) ([]purchase.Receipt, error)

	InsertExpense(ctx context.Context, orgID id.ID, e purchase.Expense) error
	// InsertAllocations registra el prorrateo de un gasto.
	InsertAllocations(ctx context.Context, orgID id.ID, e purchase.Expense) error
	Expenses(ctx context.Context, orgID, receiptID id.ID) ([]purchase.Expense, error)

	InsertReplacementCost(ctx context.Context, c purchase.ReplacementCost) error
	ReplacementCosts(ctx context.Context, orgID, companyID, productID id.ID) ([]purchase.ReplacementCost, error)
	// LastPurchase es la línea confirmada más reciente del producto en la
	// empresa; apporg.ErrNotFound si nunca se compró.
	LastPurchase(ctx context.Context, orgID, companyID, productID id.ID) (LastPurchase, error)
	// NextCounter numera documentos (de catálogo).
	NextCounter(ctx context.Context, orgID id.ID, name string) (int64, error)
}

// LastPurchase es el último costo de compra de un producto.
type LastPurchase struct {
	UnitCost   decimal.Decimal // costo de compra en la moneda del documento, sin gastos
	Currency   money.Currency  // del documento
	ValuedUnit decimal.Decimal // costo de compra en la moneda de valoración
	TotalUnit  decimal.Decimal // en la moneda de valoración, con gastos capitalizados al recibir
	Receipt    string
	ReceivedAt time.Time
}

type Store interface {
	InTx(ctx context.Context, fn func(Repository) error) error
}

type Service struct {
	store Store
	stock *stock.Service
	now   func() time.Time
}

func NewService(store Store, st *stock.Service, now func() time.Time) *Service {
	return &Service{store: store, stock: st, now: now}
}

const maxAttempts = 8

func (s *Service) write(ctx context.Context, fn func(Repository) error) error {
	for range maxAttempts {
		err := s.store.InTx(ctx, fn)
		if !errors.Is(err, stock.ErrConcurrent) {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return stock.ErrBusy
}

// ---------- Proveedores ----------

// CreateSupplier registra un proveedor de la organización; como el
// catálogo, exige gestionar compras con ámbito de organización.
func (s *Service) CreateSupplier(ctx context.Context, p identity.Principal, code, name, rif string) (purchase.Supplier, error) {
	sup, err := purchase.NewSupplier(p.OrganizationID, code, name, rif, s.now())
	if err != nil {
		return sup, err
	}
	if err := p.Authorize(identity.PermPurchaseManage, p.OrganizationScope(), "toda la organización"); err != nil {
		return purchase.Supplier{}, err
	}
	return sup, s.store.InTx(ctx, func(r Repository) error { return r.InsertSupplier(ctx, sup) })
}

func (s *Service) Suppliers(ctx context.Context, p identity.Principal) ([]purchase.Supplier, error) {
	if !p.HasAnywhere(identity.PermPurchaseRead) {
		return nil, fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermPurchaseRead)
	}
	var out []purchase.Supplier
	err := s.store.InTx(ctx, func(r Repository) error {
		var err error
		out, err = r.Suppliers(ctx, p.OrganizationID)
		return err
	})
	return out, err
}

func supplier(ctx context.Context, r Repository, orgID id.ID, code string) (purchase.Supplier, error) {
	c, err := org.ParseCode(code)
	if err != nil {
		return purchase.Supplier{}, fmt.Errorf("%w: %w", purchase.ErrInvalid, err)
	}
	sup, err := r.SupplierByCode(ctx, orgID, c)
	if err != nil {
		return sup, err
	}
	if sup.Status != org.StatusActive {
		return sup, fmt.Errorf("%w: el proveedor %s está inactivo", purchase.ErrInvalid, sup.Code)
	}
	return sup, nil
}

// ---------- Órdenes ----------

// LineInput es una línea de orden o recepción.
type LineInput struct {
	Product  string // SKU, código de barras o ID
	Quantity string
	UnitCost string
}

// OrderInput crea una orden de compra para una sucursal.
type OrderInput struct {
	Company, Branch string
	Supplier        string // código
	Currency        string // de los costos; vacía es la de valoración
	Lines           []LineInput
	Note            string
}

// LineView es una línea con su producto y unidad.
type LineView struct {
	Product catalog.Product
	Unit    catalog.Unit
}

// OrderView es una orden con su proveedor y sus productos.
type OrderView struct {
	purchase.Order
	Supplier purchase.Supplier
	Company  org.Company
	Branch   org.Branch
	Products map[id.ID]LineView
}

func products(ctx context.Context, r Repository, orgID id.ID, ids []id.ID) (map[id.ID]LineView, error) {
	out := map[id.ID]LineView{}
	for _, pid := range ids {
		if _, ok := out[pid]; ok {
			continue
		}
		prod, err := r.Product(ctx, orgID, pid)
		if err != nil {
			return nil, err
		}
		unit, err := r.Unit(ctx, orgID, prod.UnitID)
		if err != nil {
			return nil, err
		}
		out[pid] = LineView{Product: prod, Unit: unit}
	}
	return out, nil
}

// line resuelve producto, cantidad y costo de una línea.
func line(ctx context.Context, r Repository, orgID id.ID, in LineInput) (catalog.Product, decimal.Decimal, decimal.Decimal, error) {
	prod, err := appcatalog.Resolve(ctx, r, orgID, in.Product)
	if err != nil {
		return prod, decimal.Decimal{}, decimal.Decimal{}, err
	}
	if prod.Status != catalog.StatusActive {
		return prod, decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("%w: el producto %s está inactivo", purchase.ErrInvalid, prod.SKU)
	}
	unit, err := r.Unit(ctx, orgID, prod.UnitID)
	if err != nil {
		return prod, decimal.Decimal{}, decimal.Decimal{}, err
	}
	qty, err := inventory.ParseQuantity(in.Quantity, unit.Scale)
	if err != nil {
		return prod, decimal.Decimal{}, decimal.Decimal{}, err
	}
	cost, err := inventory.ParseUnitCost(in.UnitCost)
	return prod, qty, cost, err
}

// branch resuelve una sucursal visible y exige perm en ella.
func branch(ctx context.Context, r Repository, p identity.Principal, companyCode, branchCode string, perm identity.Permission) (org.Company, org.Branch, error) {
	cc, err := org.ParseCode(companyCode)
	if err != nil {
		return org.Company{}, org.Branch{}, err
	}
	bc, err := org.ParseCode(branchCode)
	if err != nil {
		return org.Company{}, org.Branch{}, err
	}
	c, err := r.CompanyByCode(ctx, p.OrganizationID, cc)
	if err != nil {
		return c, org.Branch{}, err
	}
	b, err := r.BranchByCode(ctx, p.OrganizationID, c.ID, bc)
	if err != nil {
		return c, b, err
	}
	if !p.Sees(identity.PermOrgRead, b.Scope()) && !p.Can(perm, b.Scope()) {
		return c, b, fmt.Errorf("%w: sucursal %s/%s", apporg.ErrNotFound, cc, bc)
	}
	return c, b, p.Authorize(perm, b.Scope(), "la sucursal "+cc.String()+"/"+bc.String())
}

func (s *Service) CreateOrder(ctx context.Context, p identity.Principal, in OrderInput) (OrderView, error) {
	var out OrderView
	err := s.store.InTx(ctx, func(r Repository) error {
		c, b, err := branch(ctx, r, p, in.Company, in.Branch, identity.PermPurchaseManage)
		if err != nil {
			return err
		}
		sup, err := supplier(ctx, r, p.OrganizationID, in.Supplier)
		if err != nil {
			return err
		}
		cur, err := currencyOr(in.Currency, c.Currency)
		if err != nil {
			return err
		}
		lines := make([]purchase.OrderLine, 0, len(in.Lines))
		for _, l := range in.Lines {
			prod, qty, cost, err := line(ctx, r, p.OrganizationID, l)
			if err != nil {
				return err
			}
			lines = append(lines, purchase.OrderLine{ProductID: prod.ID, Quantity: qty, UnitCost: cost})
		}
		n, err := r.NextCounter(ctx, p.OrganizationID, "purchase_order")
		if err != nil {
			return err
		}
		o, err := purchase.NewOrder(b.Scope(), fmt.Sprintf("OC-%06d", n), sup.ID, cur, lines, in.Note, p.UserID, s.now())
		if err != nil {
			return err
		}
		if err := r.InsertOrder(ctx, o); err != nil {
			return err
		}
		out, err = s.orderView(ctx, r, o)
		out.Company, out.Branch = c, b
		return err
	})
	return out, err
}

func (s *Service) orderView(ctx context.Context, r Repository, o purchase.Order) (OrderView, error) {
	v := OrderView{Order: o}
	var err error
	orgID := o.Scope.Organization()
	if v.Supplier, err = r.Supplier(ctx, orgID, o.SupplierID); err != nil {
		return v, err
	}
	if v.Company, err = r.Company(ctx, orgID, o.Scope.Company()); err != nil {
		return v, err
	}
	if v.Branch, err = r.Branch(ctx, orgID, o.Scope.Company(), o.Scope.Branch()); err != nil {
		return v, err
	}
	ids := make([]id.ID, len(o.Lines))
	for i, l := range o.Lines {
		ids[i] = l.ProductID
	}
	v.Products, err = products(ctx, r, orgID, ids)
	return v, err
}

// findOrder acepta número (OC-000001) o ID.
func findOrder(ctx context.Context, r Repository, p identity.Principal, ref string, forUpdate bool) (purchase.Order, error) {
	ref = strings.TrimSpace(ref)
	if oid, err := id.Parse(ref); err == nil {
		return r.Order(ctx, p.OrganizationID, oid, forUpdate)
	}
	o, err := r.OrderByNumber(ctx, p.OrganizationID, strings.ToUpper(ref))
	if err != nil || !forUpdate {
		return o, err
	}
	return r.Order(ctx, p.OrganizationID, o.ID, true)
}

// visible exige ver documentos de compra en el ámbito sc.
func visible(p identity.Principal, sc scope.BusinessScope, what string) error {
	if !p.Sees(identity.PermPurchaseRead, sc) {
		return fmt.Errorf("%w: %s", apporg.ErrNotFound, what)
	}
	return nil
}

func (s *Service) ShowOrder(ctx context.Context, p identity.Principal, ref string) (OrderView, error) {
	var out OrderView
	err := s.store.InTx(ctx, func(r Repository) error {
		o, err := findOrder(ctx, r, p, ref, false)
		if err != nil {
			return err
		}
		if err := visible(p, o.Scope, "orden "+ref); err != nil {
			return err
		}
		out, err = s.orderView(ctx, r, o)
		return err
	})
	return out, err
}

func (s *Service) Orders(ctx context.Context, p identity.Principal, status purchase.OrderStatus) ([]OrderView, error) {
	if !p.HasAnywhere(identity.PermPurchaseRead) {
		return nil, fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermPurchaseRead)
	}
	var out []OrderView
	err := s.store.InTx(ctx, func(r Repository) error {
		list, err := r.Orders(ctx, p.OrganizationID, status)
		if err != nil {
			return err
		}
		for _, o := range list {
			if !p.Sees(identity.PermPurchaseRead, o.Scope) {
				continue
			}
			v, err := s.orderView(ctx, r, o)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

// CancelOrder cierra lo pendiente de una orden abierta.
func (s *Service) CancelOrder(ctx context.Context, p identity.Principal, ref string) (OrderView, error) {
	var out OrderView
	err := s.store.InTx(ctx, func(r Repository) error {
		o, err := findOrder(ctx, r, p, ref, true)
		if err != nil {
			return err
		}
		if err := visible(p, o.Scope, "orden "+ref); err != nil {
			return err
		}
		if err := p.Authorize(identity.PermPurchaseManage, o.Scope, "la orden "+o.Number); err != nil {
			return err
		}
		if o, err = o.Cancel(p.UserID, s.now()); err != nil {
			return err
		}
		if err := r.UpdateOrder(ctx, o); err != nil {
			return err
		}
		out, err = s.orderView(ctx, r, o)
		return err
	})
	return out, err
}

// ---------- Recepciones ----------

// ReceiptInput abre una recepción en borrador. Con Order, se precargan sus
// líneas pendientes al costo pactado.
type ReceiptInput struct {
	Where    stock.WarehouseRef
	Supplier string // código; con Order, por defecto el de la orden
	Order    string // número o ID, opcional
	Currency string // vacía: la de la orden o la de valoración
	Date     string // de la factura, AAAA-MM-DD; vacía es hoy
	Document string // número de factura o guía del proveedor
	Note     string
}

// ReceiptView es una recepción con su contexto.
type ReceiptView struct {
	purchase.Receipt
	Supplier  purchase.Supplier
	Location  stock.Location
	Valuation money.Currency // de la empresa
	Order     string         // número de la orden, si la hay
	Expenses  []purchase.Expense
	Rates     map[id.ID]exchange.Rate // aplicadas por la recepción y sus gastos
	Products  map[id.ID]LineView
	Movements []inventory.Movement // entradas y revaloraciones registradas por esta operación
	Replayed  bool
}

func (s *Service) receiptView(ctx context.Context, r Repository, rc purchase.Receipt) (ReceiptView, error) {
	v := ReceiptView{Receipt: rc}
	orgID := rc.Scope.Organization()
	var err error
	if v.Supplier, err = r.Supplier(ctx, orgID, rc.SupplierID); err != nil {
		return v, err
	}
	if v.Location, err = stock.LocationOf(ctx, r, rc.Scope); err != nil {
		return v, err
	}
	v.Valuation = v.Location.Company.Currency
	if !rc.OrderID.IsZero() {
		o, err := r.Order(ctx, orgID, rc.OrderID, false)
		if err != nil {
			return v, err
		}
		v.Order = o.Number
	}
	if v.Expenses, err = r.Expenses(ctx, orgID, rc.ID); err != nil {
		return v, err
	}
	v.Rates = map[id.ID]exchange.Rate{}
	rateIDs := []id.ID{rc.RateID}
	for _, e := range v.Expenses {
		rateIDs = append(rateIDs, e.RateID)
	}
	for _, rid := range rateIDs {
		if _, ok := v.Rates[rid]; ok || rid.IsZero() {
			continue
		}
		if v.Rates[rid], err = r.Rate(ctx, orgID, rid); err != nil {
			return v, err
		}
	}
	ids := make([]id.ID, len(rc.Lines))
	for i, l := range rc.Lines {
		ids[i] = l.ProductID
	}
	v.Products, err = products(ctx, r, orgID, ids)
	return v, err
}

func (s *Service) CreateReceipt(ctx context.Context, p identity.Principal, in ReceiptInput) (ReceiptView, error) {
	var out ReceiptView
	err := s.store.InTx(ctx, func(r Repository) error {
		loc, err := stock.Locate(ctx, r, p, in.Where, identity.PermPurchaseManage)
		if err != nil {
			return err
		}
		if loc.Warehouse.Status != org.StatusActive {
			return fmt.Errorf("%w: el almacén %s está inactivo", purchase.ErrInvalid, loc)
		}
		var o purchase.Order
		if strings.TrimSpace(in.Order) != "" {
			if o, err = findOrder(ctx, r, p, in.Order, false); err != nil {
				return err
			}
			if o.Status != purchase.OrderOpen {
				return fmt.Errorf("%w: la orden %s está %s", purchase.ErrInvalid, o.Number, o.Status)
			}
			if o.Scope.Company() != loc.Company.ID || o.Scope.Branch() != loc.Branch.ID {
				return fmt.Errorf("%w: la orden %s es de otra sucursal", purchase.ErrInvalid, o.Number)
			}
		}
		supID := o.SupplierID
		if strings.TrimSpace(in.Supplier) != "" {
			sup, err := supplier(ctx, r, p.OrganizationID, in.Supplier)
			if err != nil {
				return err
			}
			if !supID.IsZero() && sup.ID != supID {
				return fmt.Errorf("%w: la orden %s es de otro proveedor", purchase.ErrInvalid, o.Number)
			}
			supID = sup.ID
		}
		if supID.IsZero() {
			return fmt.Errorf("%w: indique el proveedor o la orden", purchase.ErrInvalid)
		}
		cur := loc.Company.Currency
		if !o.ID.IsZero() {
			cur = o.Currency
		}
		if strings.TrimSpace(in.Currency) != "" {
			given, err := rates.ParseCurrency(in.Currency)
			if err != nil {
				return err
			}
			if !o.ID.IsZero() && given != o.Currency {
				return fmt.Errorf("%w: la orden %s está en %s", purchase.ErrInvalid, o.Number, o.Currency)
			}
			cur = given
		}
		date, err := dateOr(in.Date, s.now())
		if err != nil {
			return err
		}
		n, err := r.NextCounter(ctx, p.OrganizationID, "purchase_receipt")
		if err != nil {
			return err
		}
		rc, err := purchase.NewReceipt(loc.Warehouse.Scope(), fmt.Sprintf("RC-%06d", n), supID, o.ID, cur, date, in.Document, in.Note, p.UserID, s.now())
		if err != nil {
			return err
		}
		for _, ol := range o.Lines {
			if ol.Pending().Sign() > 0 {
				if rc, err = rc.SetLine(purchase.ReceiptLine{ProductID: ol.ProductID, OrderLineNo: ol.LineNo, Quantity: ol.Pending(), UnitCost: ol.UnitCost}); err != nil {
					return err
				}
			}
		}
		if err := r.InsertReceipt(ctx, rc); err != nil {
			return err
		}
		if err := r.SaveReceiptLines(ctx, rc); err != nil {
			return err
		}
		out, err = s.receiptView(ctx, r, rc)
		return err
	})
	return out, err
}

// findReceipt acepta número (RC-000001) o ID y exige perm en su almacén.
func findReceipt(ctx context.Context, r Repository, p identity.Principal, ref string, perm identity.Permission, forUpdate bool) (purchase.Receipt, stock.Location, error) {
	ref = strings.TrimSpace(ref)
	var rc purchase.Receipt
	var err error
	if rid, perr := id.Parse(ref); perr == nil {
		rc, err = r.Receipt(ctx, p.OrganizationID, rid, forUpdate)
	} else if rc, err = r.ReceiptByNumber(ctx, p.OrganizationID, strings.ToUpper(ref)); err == nil && forUpdate {
		rc, err = r.Receipt(ctx, p.OrganizationID, rc.ID, true)
	}
	if err != nil {
		return rc, stock.Location{}, err
	}
	loc, err := stock.LocationOf(ctx, r, rc.Scope)
	if err != nil {
		return rc, loc, err
	}
	ref2 := stock.WarehouseRef{Company: loc.Company.Code.String(), Branch: loc.Branch.Code.String(), Warehouse: loc.Warehouse.Code.String()}
	loc, err = stock.Locate(ctx, r, p, ref2, perm)
	return rc, loc, err
}

// SetReceiptLine agrega o reemplaza lo recibido de un producto en un
// borrador. Si el producto está en la orden, se vincula a su línea.
func (s *Service) SetReceiptLine(ctx context.Context, p identity.Principal, ref string, in LineInput) (ReceiptView, error) {
	return s.editDraft(ctx, p, ref, func(r Repository, rc purchase.Receipt) (purchase.Receipt, error) {
		prod, qty, cost, err := line(ctx, r, p.OrganizationID, in)
		if err != nil {
			return rc, err
		}
		l := purchase.ReceiptLine{ProductID: prod.ID, Quantity: qty, UnitCost: cost}
		if !rc.OrderID.IsZero() {
			o, err := r.Order(ctx, p.OrganizationID, rc.OrderID, false)
			if err != nil {
				return rc, err
			}
			if i := slices.IndexFunc(o.Lines, func(ol purchase.OrderLine) bool { return ol.ProductID == prod.ID }); i >= 0 {
				l.OrderLineNo = o.Lines[i].LineNo
			}
		}
		return rc.SetLine(l)
	})
}

// RemoveReceiptLine quita un producto de un borrador.
func (s *Service) RemoveReceiptLine(ctx context.Context, p identity.Principal, ref, product string) (ReceiptView, error) {
	return s.editDraft(ctx, p, ref, func(r Repository, rc purchase.Receipt) (purchase.Receipt, error) {
		prod, err := appcatalog.Resolve(ctx, r, p.OrganizationID, product)
		if err != nil {
			return rc, err
		}
		return rc.RemoveLine(prod.ID)
	})
}

func (s *Service) editDraft(ctx context.Context, p identity.Principal, ref string, fn func(Repository, purchase.Receipt) (purchase.Receipt, error)) (ReceiptView, error) {
	var out ReceiptView
	err := s.store.InTx(ctx, func(r Repository) error {
		rc, _, err := findReceipt(ctx, r, p, ref, identity.PermPurchaseManage, true)
		if err != nil {
			return err
		}
		if rc, err = fn(r, rc); err != nil {
			return err
		}
		if err := r.SaveReceiptLines(ctx, rc); err != nil {
			return err
		}
		out, err = s.receiptView(ctx, r, rc)
		return err
	})
	return out, err
}

// AddExpense registra un gasto atribuible. En un borrador se prorratea al
// confirmar; en una recepción confirmada (gasto tardío) se prorratea ya: la
// parte de unidades que siguen en el almacén revaloriza el inventario y la
// de unidades que ya salieron queda como diferencia de costo, sin tocar
// ventas cerradas (sección 19.3). El gasto tardío exige confirmar compras.
//
// El gasto va en su moneda y se convierte ya, con la tasa vigente a su
// fecha: por defecto la del documento en un borrador y hoy en un gasto
// tardío.
func (s *Service) AddExpense(ctx context.Context, p identity.Principal, ref string, in ExpenseInput) (ReceiptView, error) {
	a, err := purchase.ParseAmount(in.Amount)
	if err != nil {
		return ReceiptView{}, err
	}
	c, err := purchase.ParseCriterion(in.Criterion)
	if err != nil {
		return ReceiptView{}, err
	}
	var out ReceiptView
	err = s.write(ctx, func(r Repository) error {
		rc, loc, err := findReceipt(ctx, r, p, ref, identity.PermPurchaseManage, true)
		if err != nil {
			return err
		}
		now := s.now()
		cur := rc.Currency
		if strings.TrimSpace(in.Currency) != "" {
			if cur, err = rates.ParseCurrency(in.Currency); err != nil {
				return err
			}
		}
		date := rc.DocumentDate
		if rc.Status == purchase.ReceiptConfirmed {
			date = timezone.DateOf(now)
		}
		if strings.TrimSpace(in.Date) != "" {
			if date, err = rates.ParseDate(in.Date); err != nil {
				return err
			}
		}
		rate, err := rateFor(ctx, r, loc.Company, cur, date)
		if err != nil {
			return err
		}
		e, err := purchase.NewExpense(rc, in.Concept, a, cur, date, c, loc.Company.Currency, rate, p.UserID, now)
		if err != nil {
			return err
		}
		var moves []inventory.Movement
		if e.Late {
			if err := p.Authorize(identity.PermReceiptConfirm, loc.Warehouse.Scope(), "el almacén "+loc.String()); err != nil {
				return err
			}
			if e.Allocations, err = purchase.Allocate(rc.Lines, e.Valued, e.Criterion); err != nil {
				return err
			}
			if moves, err = s.lateAllocations(ctx, r, p, loc, rc, &e); err != nil {
				return err
			}
		}
		if err := r.InsertExpense(ctx, p.OrganizationID, e); err != nil {
			return err
		}
		if e.Late {
			if err := r.InsertAllocations(ctx, p.OrganizationID, e); err != nil {
				return err
			}
		}
		if out, err = s.receiptView(ctx, r, rc); err != nil {
			return err
		}
		out.Movements = moves
		return nil
	})
	return out, err
}

// lateAllocations capitaliza la parte de un gasto tardío que corresponde a
// unidades aún en el almacén, con una revaloración por línea.
func (s *Service) lateAllocations(ctx context.Context, r Repository, p identity.Principal, loc stock.Location, rc purchase.Receipt, e *purchase.Expense) ([]inventory.Movement, error) {
	byLine := map[int]purchase.ReceiptLine{}
	for _, l := range rc.Lines {
		byLine[l.LineNo] = l
	}
	order := slices.Clone(e.Allocations)
	slices.SortFunc(order, func(a, b purchase.Allocation) int {
		return strings.Compare(byLine[a.LineNo].ProductID.String(), byLine[b.LineNo].ProductID.String())
	})
	var moves []inventory.Movement
	for _, a := range order {
		l := byLine[a.LineNo]
		var share purchase.Allocation
		m, _, err := s.stock.RevalueWithin(ctx, r, p, loc, l.ProductID, func(b inventory.Balance) decimal.Decimal {
			onHand := b.OnHand
			if !b.Value.Known {
				onHand = decimal.Zero() // sin valor no hay a qué capitalizar
			}
			share = purchase.LateShare(a, l.Quantity, onHand)
			return share.Capitalized
		}, inventory.Movement{Reason: inventory.ReasonPurchaseExpense, Note: e.Concept, ReceiptID: rc.ID, RequestHash: digest(e.ID, a.LineNo)})
		if err != nil {
			return nil, err
		}
		share.MovementID = m.ID
		if !m.ID.IsZero() {
			moves = append(moves, m)
		}
		i := slices.IndexFunc(e.Allocations, func(x purchase.Allocation) bool { return x.LineNo == a.LineNo })
		e.Allocations[i] = share
	}
	return moves, nil
}

// ConfirmReceipt hace entrar la mercancía: prorratea los gastos del
// borrador, registra una entrada valorada por línea (recalcula el CPP),
// avanza la orden y cierra la recepción, todo en una transacción.
// Confirmar dos veces devuelve la recepción ya confirmada sin repetir nada.
func (s *Service) ConfirmReceipt(ctx context.Context, p identity.Principal, ref string) (ReceiptView, error) {
	var out ReceiptView
	err := s.write(ctx, func(r Repository) error {
		rc, loc, err := findReceipt(ctx, r, p, ref, identity.PermReceiptConfirm, true)
		if err != nil {
			return err
		}
		if rc.Status == purchase.ReceiptConfirmed {
			out, err = s.receiptView(ctx, r, rc)
			out.Replayed = true
			return err
		}
		now := s.now()
		if rc, err = rc.Confirm(p.UserID, now); err != nil {
			return err
		}
		if loc.Warehouse.Status != org.StatusActive {
			return fmt.Errorf("%w: el almacén %s está inactivo", purchase.ErrInvalid, loc)
		}
		rate, err := rateFor(ctx, r, loc.Company, rc.Currency, rc.DocumentDate)
		if err != nil {
			return err
		}
		if rc, err = rc.Valuate(loc.Company.Currency, rate); err != nil {
			return err
		}
		expenses, err := r.Expenses(ctx, p.OrganizationID, rc.ID)
		if err != nil {
			return err
		}
		for i := range expenses {
			if expenses[i].Allocations, err = purchase.Allocate(rc.Lines, expenses[i].Valued, expenses[i].Criterion); err != nil {
				return fmt.Errorf("gasto %q: %w", expenses[i].Concept, err)
			}
			for _, a := range expenses[i].Allocations {
				j := slices.IndexFunc(rc.Lines, func(l purchase.ReceiptLine) bool { return l.LineNo == a.LineNo })
				rc.Lines[j].Expenses = rc.Lines[j].Expenses.Add(a.Capitalized)
			}
		}
		// La orden se retiene antes que los saldos: el mismo orden siempre.
		var o purchase.Order
		if !rc.OrderID.IsZero() {
			if o, err = r.Order(ctx, p.OrganizationID, rc.OrderID, true); err != nil {
				return err
			}
		}
		lines := slices.Clone(rc.Lines)
		slices.SortFunc(lines, func(a, b purchase.ReceiptLine) int {
			return strings.Compare(a.ProductID.String(), b.ProductID.String())
		})
		var moves []inventory.Movement
		for _, l := range lines {
			prod, err := r.Product(ctx, p.OrganizationID, l.ProductID)
			if err != nil {
				return err
			}
			if prod.Status != catalog.StatusActive {
				return fmt.Errorf("%w: el producto %s está inactivo", purchase.ErrInvalid, prod.SKU)
			}
			m, err := s.stock.PostWithin(ctx, r, p, loc, l.ProductID, inventory.Movement{Kind: inventory.KindPurchaseIn, Reason: inventory.ReasonPurchase,
				Quantity: l.Quantity, Note: rc.Number, ReceiptID: rc.ID, RequestHash: digest(rc.ID, l.LineNo)},
				inventory.Costing{Fixed: inventory.Known(l.TotalCost()), ValuesPrior: true})
			if err != nil {
				return fmt.Errorf("%s: %w", prod.SKU, err)
			}
			j := slices.IndexFunc(rc.Lines, func(x purchase.ReceiptLine) bool { return x.LineNo == l.LineNo })
			rc.Lines[j].MovementID = m.ID
			moves = append(moves, m)
			if l.OrderLineNo > 0 {
				if o, err = o.Receive(l.OrderLineNo, l.Quantity); err != nil {
					return err
				}
			}
		}
		if !rc.OrderID.IsZero() {
			if err := r.UpdateOrder(ctx, o); err != nil {
				return err
			}
		}
		for _, e := range expenses {
			if err := r.InsertAllocations(ctx, p.OrganizationID, e); err != nil {
				return err
			}
		}
		if err := r.SaveReceiptLines(ctx, rc); err != nil {
			return err
		}
		if err := r.UpdateReceipt(ctx, rc); err != nil {
			return err
		}
		if out, err = s.receiptView(ctx, r, rc); err != nil {
			return err
		}
		out.Movements = moves
		return nil
	})
	return out, err
}

// CancelReceipt descarta un borrador.
func (s *Service) CancelReceipt(ctx context.Context, p identity.Principal, ref string) (ReceiptView, error) {
	var out ReceiptView
	err := s.store.InTx(ctx, func(r Repository) error {
		rc, _, err := findReceipt(ctx, r, p, ref, identity.PermPurchaseManage, true)
		if err != nil {
			return err
		}
		if rc, err = rc.Cancel(p.UserID, s.now()); err != nil {
			return err
		}
		if err := r.UpdateReceipt(ctx, rc); err != nil {
			return err
		}
		out, err = s.receiptView(ctx, r, rc)
		return err
	})
	return out, err
}

func (s *Service) ShowReceipt(ctx context.Context, p identity.Principal, ref string) (ReceiptView, error) {
	var out ReceiptView
	err := s.store.InTx(ctx, func(r Repository) error {
		rc, _, err := findReceipt(ctx, r, p, ref, identity.PermPurchaseRead, false)
		if err != nil {
			return err
		}
		out, err = s.receiptView(ctx, r, rc)
		return err
	})
	return out, err
}

func (s *Service) Receipts(ctx context.Context, p identity.Principal, status purchase.ReceiptStatus) ([]ReceiptView, error) {
	if !p.HasAnywhere(identity.PermPurchaseRead) {
		return nil, fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermPurchaseRead)
	}
	var out []ReceiptView
	err := s.store.InTx(ctx, func(r Repository) error {
		list, err := r.Receipts(ctx, p.OrganizationID, status)
		if err != nil {
			return err
		}
		for _, rc := range list {
			if !p.Can(identity.PermPurchaseRead, rc.Scope) {
				continue
			}
			v, err := s.receiptView(ctx, r, rc)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

// ---------- Costos ----------

// CostView reúne los costos de un producto en una empresa, sin mezclarlos:
// el CPP por almacén (valoración), el último costo de compra y el costo de
// reposición estimado (comercial).
type CostView struct {
	Company     org.Company
	Product     catalog.Product
	Unit        catalog.Unit
	Balances    []stock.BalanceView
	Last        *LastPurchase
	Replacement []purchase.ReplacementCost // del más reciente al más antiguo
}

// SetReplacementCost registra una nueva estimación del costo de reposición.
// No cambia el CPP ni ningún precio.
// currency vacía es la de valoración de la empresa.
func (s *Service) SetReplacementCost(ctx context.Context, p identity.Principal, companyCode, product, unitCost, currency, note string) (CostView, error) {
	var out CostView
	err := s.store.InTx(ctx, func(r Repository) error {
		c, prod, err := companyProduct(ctx, r, p, companyCode, product)
		if err != nil {
			return err
		}
		if err := p.Authorize(identity.PermReplacementCost, c.Scope(), "la empresa "+c.Code.String()); err != nil {
			return err
		}
		cur, err := currencyOr(currency, c.Currency)
		if err != nil {
			return err
		}
		rc, err := purchase.NewReplacementCost(p.OrganizationID, c.ID, prod.ID, unitCost, cur, note, p.UserID, s.now())
		if err != nil {
			return err
		}
		if err := r.InsertReplacementCost(ctx, rc); err != nil {
			return err
		}
		out, err = s.costView(ctx, r, p, c, prod)
		return err
	})
	return out, err
}

// Costs muestra los costos de un producto en una empresa.
func (s *Service) Costs(ctx context.Context, p identity.Principal, companyCode, product string) (CostView, error) {
	var out CostView
	err := s.store.InTx(ctx, func(r Repository) error {
		c, prod, err := companyProduct(ctx, r, p, companyCode, product)
		if err != nil {
			return err
		}
		if !p.Sees(identity.PermPurchaseRead, c.Scope()) && !p.Sees(identity.PermStockRead, c.Scope()) {
			return fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermPurchaseRead)
		}
		out, err = s.costView(ctx, r, p, c, prod)
		return err
	})
	return out, err
}

func companyProduct(ctx context.Context, r Repository, p identity.Principal, companyCode, product string) (org.Company, catalog.Product, error) {
	cc, err := org.ParseCode(companyCode)
	if err != nil {
		return org.Company{}, catalog.Product{}, err
	}
	c, err := r.CompanyByCode(ctx, p.OrganizationID, cc)
	if err != nil {
		return c, catalog.Product{}, err
	}
	if !p.Sees(identity.PermOrgRead, c.Scope()) {
		return c, catalog.Product{}, fmt.Errorf("%w: empresa %s", apporg.ErrNotFound, cc)
	}
	prod, err := appcatalog.Resolve(ctx, r, p.OrganizationID, product)
	return c, prod, err
}

func (s *Service) costView(ctx context.Context, r Repository, p identity.Principal, c org.Company, prod catalog.Product) (CostView, error) {
	v := CostView{Company: c, Product: prod}
	var err error
	if v.Unit, err = r.Unit(ctx, p.OrganizationID, prod.UnitID); err != nil {
		return v, err
	}
	balances, err := r.Balances(ctx, p.OrganizationID, id.ID{}, prod.ID)
	if err != nil {
		return v, err
	}
	for _, b := range balances {
		if b.Scope.Company() != c.ID || !p.Can(identity.PermStockRead, b.Scope) {
			continue
		}
		loc, err := stock.LocationOf(ctx, r, b.Scope)
		if err != nil {
			return v, err
		}
		v.Balances = append(v.Balances, stock.BalanceView{Balance: b, Product: prod, Unit: v.Unit, Location: loc})
	}
	if last, err := r.LastPurchase(ctx, p.OrganizationID, c.ID, prod.ID); err == nil {
		v.Last = &last
	} else if !errors.Is(err, apporg.ErrNotFound) {
		return v, err
	}
	if v.Replacement, err = r.ReplacementCosts(ctx, p.OrganizationID, c.ID, prod.ID); err != nil {
		return v, err
	}
	slices.Reverse(v.Replacement)
	return v, nil
}

// ExpenseInput es un gasto atribuible a una recepción.
type ExpenseInput struct {
	Concept   string
	Amount    string
	Currency  string // vacía es la de la recepción
	Date      string // del comprobante, AAAA-MM-DD; vacía: ver AddExpense
	Criterion string // value o quantity
}

// rateFor busca la tasa que convierte cur a la moneda de valoración de c el
// día on; nil si no hace falta conversión.
func rateFor(ctx context.Context, r Repository, c org.Company, cur money.Currency, on time.Time) (*exchange.Rate, error) {
	if cur == c.Currency {
		return nil, nil
	}
	rate, err := r.EffectiveRate(ctx, c.OrganizationID, c.ID, exchange.Pair(cur, c.Currency), on)
	if err != nil {
		return nil, fmt.Errorf("convertir %s a %s el %s: %w", cur, c.Currency, on.Format(timezone.DateLayout), err)
	}
	return &rate, nil
}

// currencyOr lee una moneda habilitada; vacía es def.
func currencyOr(s string, def money.Currency) (money.Currency, error) {
	if strings.TrimSpace(s) == "" {
		return def, nil
	}
	return rates.ParseCurrency(s)
}

// dateOr lee una fecha comercial; vacía es la de hoy en Caracas.
func dateOr(s string, now time.Time) (time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return timezone.DateOf(now), nil
	}
	return rates.ParseDate(s)
}

func digest(docID id.ID, n int) string {
	return stock.Digest("purchase", docID.String(), fmt.Sprint(n))
}

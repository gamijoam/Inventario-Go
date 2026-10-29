package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/gamijoam/inventario-go/internal/application/purchasing"
	"github.com/gamijoam/inventario-go/internal/application/rates"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/exchange"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/purchase"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

// Monedas que acepta un documento; vacía es la de valoración (o la de la
// orden).
var (
	currencyCodes  = []string{"", "USD", "VES"}
	currencyLabels = []string{"la de valoración", "USD", "VES"}
)

func day(t time.Time) string { return t.Format(timezone.DateLayout) }

// ---------- Proveedores ----------

func newSuppliers(a *app) screen {
	r := &recordsScreen[purchase.Supplier]{app: a, title: "Proveedores",
		info:  "Los proveedores son de toda la organización: crearlos exige permiso con ese ámbito.",
		empty: "No hay proveedores.",
		list: newList([]table.Column{{Title: "Código", Width: 16}, {Title: "Nombre", Width: 40}, {Title: "RIF", Width: 14},
			{Title: "Estado", Width: 8}}, 1),
		fetch: func(bool) ([]purchase.Supplier, error) { return a.opts.Backend.Suppliers(a.ctx) },
		row: func(s purchase.Supplier) table.Row {
			state := "activo"
			if s.Status != org.StatusActive {
				state = "inactivo"
			}
			return table.Row{s.Code.String(), s.Name.String(), s.RIF.String(), state}
		}}
	r.actions = []action[purchase.Supplier]{{key: "f2", label: "F2 Nuevo",
		ok:  func(*purchase.Supplier) bool { return a.can(identity.PermPurchaseManage) },
		run: func(*purchase.Supplier) tea.Cmd { return push(newSupplierForm(a)) }}}
	return r
}

func newSupplierForm(a *app) screen {
	f := &formScreen{app: a, title: "Nuevo proveedor",
		info: "Código corto (DIST, POLAR). El RIF es opcional: V-12345678-9 para personas, J-12345678-9 para empresas; " +
			"si omite el dígito final, se calcula.",
		fields: []*field{textF("code", "Código", "", 16), textF("name", "Nombre", "", 120),
			textF("rif", "RIF", "", 16).placeholder("opcional: V-12345678 o J-12345678-9")}}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.CreateSupplier(a.ctx, v["code"], v["name"], v["rif"])
	}
	f.done = func(v any) tea.Cmd { return back("proveedor creado: " + v.(purchase.Supplier).Code.String()) }
	return f
}

// supplierChoice carga los proveedores activos en el campo "supplier";
// first es la opción vacía, si la hay.
func supplierChoice(f *formScreen, list []purchase.Supplier, first, firstLabel string) {
	fl := f.field("supplier")
	fl.options, fl.labels = nil, nil
	if firstLabel != "" {
		fl.options, fl.labels = []string{first}, []string{firstLabel}
	}
	for _, s := range list {
		if s.Status == org.StatusActive {
			fl.options = append(fl.options, s.Code.String())
			fl.labels = append(fl.labels, s.Code.String()+" · "+s.Name.String())
		}
	}
	if len(fl.options) == 0 {
		fl.options, fl.labels = []string{""}, []string{"no hay proveedores: créelos en Compras → Proveedores"}
	}
}

// ---------- Recepciones ----------

var receiptText = map[purchase.ReceiptStatus]string{purchase.ReceiptDraft: "borrador", purchase.ReceiptConfirmed: "confirmada",
	purchase.ReceiptCancelled: "anulada"}

func newReceipts(a *app) screen {
	r := &recordsScreen[purchasing.ReceiptView]{app: a, title: "Recepciones", toggle: true, subset: "borradores", wide: true,
		info: "Una recepción registra la mercancía que llega de un proveedor: se arma en borrador (líneas y gastos) y, " +
			"al confirmarla, entra al almacén y recalcula el costo promedio.",
		empty: "No hay recepciones.",
		list: newList([]table.Column{{Title: "Número", Width: 10}, {Title: "Proveedor", Width: 30}, {Title: "Almacén", Width: 12},
			{Title: "Documento", Width: 16}, {Title: "Fecha", Width: 10}, {Title: "Moneda", Width: 6}, {Title: "Estado", Width: 10},
			{Title: "Líneas", Width: 6}}, 1),
		fetch: func(all bool) ([]purchasing.ReceiptView, error) {
			st := purchase.ReceiptDraft
			if all {
				st = ""
			}
			return a.opts.Backend.Receipts(a.ctx, st)
		},
		row: func(v purchasing.ReceiptView) table.Row {
			return table.Row{v.Number, v.Supplier.Name.String(), v.Location.Warehouse.Code.String(), v.SupplierDocument,
				day(v.DocumentDate), v.Currency.Code(), receiptText[v.Status], itoa(len(v.Lines))}
		}}
	r.actions = []action[purchasing.ReceiptView]{
		{key: "f2", label: "F2 Nueva", ok: func(*purchasing.ReceiptView) bool { return a.can(identity.PermPurchaseManage) },
			run: func(*purchasing.ReceiptView) tea.Cmd { return push(newReceiptForm(a, nil)) }},
		{key: "enter", label: "Enter Abrir", needsItem: true,
			run: func(v *purchasing.ReceiptView) tea.Cmd { return push(newReceipt(a, v.Number)) }},
	}
	return r
}

type receiptPrep struct {
	suppliers []purchase.Supplier
	orders    []purchasing.OrderView
}

// newReceiptForm abre una recepción, directa o contra una orden.
func newReceiptForm(a *app, order *purchasing.OrderView) screen {
	f := &formScreen{app: a, title: "Nueva recepción",
		info: "La fecha es la de la factura: decide la tasa de cambio si el documento no está en la moneda de valoración. " +
			"Contra una orden, el proveedor y la moneda son los de la orden.",
		fields: []*field{warehouseF("warehouse", "Almacén"), choiceF("supplier", "Proveedor", nil, ""),
			choiceF("order", "Orden de compra", []string{""}, "").labeled("ninguna (compra directa)"),
			choiceF("currency", "Moneda", currencyCodes, "").labeled(currencyLabels...),
			textF("date", "Fecha de la factura", "", 10).placeholder("AAAA-MM-DD · vacía: hoy"),
			textF("document", "Factura o guía", "", 60), textF("note", "Nota", "", 500)}}
	f.prepare = func() (any, error) {
		var p receiptPrep
		var err error
		if p.suppliers, err = a.opts.Backend.Suppliers(a.ctx); err != nil {
			return nil, err
		}
		p.orders, _ = a.opts.Backend.Orders(a.ctx, purchase.OrderOpen)
		return p, nil
	}
	f.ready = func(f *formScreen, v any) {
		p := v.(receiptPrep)
		supplierChoice(f, p.suppliers, "", "el de la orden")
		o := f.field("order")
		for _, x := range p.orders {
			o.options = append(o.options, x.Number)
			o.labels = append(o.labels, x.Number+" · "+x.Supplier.Name.String())
			if order != nil && x.Number == order.Number {
				o.choice = len(o.options) - 1
			}
		}
		if order == nil {
			f.field("supplier").choice = min(1, len(f.field("supplier").options)-1) // el primer proveedor real
		}
	}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.CreateReceipt(a.ctx, purchasing.ReceiptInput{Where: stock.WarehouseRef{Warehouse: v["warehouse"]}, Supplier: v["supplier"],
			Order: v["order"], Currency: v["currency"], Date: v["date"], Document: v["document"], Note: v["note"]})
	}
	f.done = func(v any) tea.Cmd {
		r := v.(purchasing.ReceiptView)
		return replace(newReceipt(a, r.Number), "recepción "+r.Number+" creada en borrador")
	}
	return withWarehouses(a, f)
}

// receiptData es una recepción con la tasa que le corresponde.
type receiptData struct {
	v       purchasing.ReceiptView
	rate    *exchange.Rate
	rateErr error
}

// receiptScreen es el documento de una recepción: encabezado, tasa, líneas
// y gastos.
type receiptScreen struct {
	app    *app
	id     int
	number string
	d      *receiptData
	list   listView
}

func newReceipt(a *app, number string) screen {
	return &receiptScreen{app: a, number: number, list: newList([]table.Column{{Title: "#", Width: 3}, {Title: "SKU", Width: 16},
		{Title: "Producto", Width: 30}, {Title: "Cantidad", Width: 12}, {Title: "Costo unit.", Width: 14}, {Title: "Compra", Width: 14},
		{Title: "Gastos", Width: 12}, {Title: "Total", Width: 14}}, 2)}
}

func (r *receiptScreen) Title() string {
	if r.d == nil {
		return "Recepción " + r.number
	}
	return "Recepción " + r.number + " · " + receiptText[r.d.v.Status]
}
func (r *receiptScreen) Wide() bool      { return true }
func (r *receiptScreen) Capturing() bool { return r.list.capturing() }

func (r *receiptScreen) draft() bool {
	return r.d != nil && r.d.v.Status == purchase.ReceiptDraft && r.app.can(identity.PermPurchaseManage)
}

func (r *receiptScreen) missingRate() bool {
	return r.d != nil && r.d.v.Status == purchase.ReceiptDraft && r.d.rate == nil && r.d.v.Currency != r.d.v.Valuation
}

func (r *receiptScreen) Help() string {
	if r.list.capturing() {
		return "Escriba para filtrar  Enter Fijar  Esc Borrar"
	}
	parts := []string{"↑↓ Elegir"}
	switch {
	case r.draft():
		parts = append(parts, "F2 Línea")
		if r.selected() != nil {
			parts = append(parts, "Enter Editar", "F8 Quitar")
		}
		parts = append(parts, "F3 Gasto")
		if r.app.can(identity.PermReceiptConfirm) && len(r.d.v.Lines) > 0 {
			parts = append(parts, "F9 Confirmar")
		}
		parts = append(parts, "F4 Anular")
	case r.d != nil && r.d.v.Status == purchase.ReceiptConfirmed && r.app.can(identity.PermPurchaseManage):
		parts = append(parts, "F3 Gasto tardío")
	}
	if r.missingRate() && r.app.can(identity.PermRateManage) {
		parts = append(parts, "F6 Registrar tasa")
	}
	return strings.Join(append(parts, "R Recargar", "Esc Volver"), "  ")
}

func (r *receiptScreen) Init(id int) tea.Cmd {
	r.id = id
	return r.Refresh()
}

func (r *receiptScreen) Refresh() tea.Cmd {
	a, n := r.app, r.number
	return load(r.id, func() (any, error) {
		v, err := a.opts.Backend.Receipt(a.ctx, n)
		if err != nil {
			return nil, err
		}
		d := receiptData{v: v}
		switch {
		case v.Currency == v.Valuation:
		case !v.RateID.IsZero():
			rt := v.Rates[v.RateID]
			d.rate = &rt
		default:
			rv, err := a.opts.Backend.EffectiveRate(a.ctx, v.Currency.Code(), v.Valuation.Code(), day(v.DocumentDate))
			if err == nil {
				d.rate = &rv.Rate
			} else {
				d.rateErr = err
			}
		}
		return d, nil
	})
}

func (r *receiptScreen) selected() *purchase.ReceiptLine {
	if i := r.list.selected(); r.d != nil && i >= 0 && i < len(r.d.v.Lines) {
		return &r.d.v.Lines[i]
	}
	return nil
}

func (r *receiptScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.list.resize(msg.Width, msg.Height-6)
	case loaded:
		if msg.err != nil {
			return setStatus("no se pudo cargar la recepción", msg.err)
		}
		d := msg.v.(receiptData)
		r.d = &d
		v := d.v
		val := v.Valuation.Code()
		rows := make([]table.Row, len(v.Lines))
		for i, l := range v.Lines {
			p := v.Products[l.ProductID]
			total := "—"
			if v.Status == purchase.ReceiptConfirmed {
				total = l.TotalCost().StringFixed(2) + " " + val
			}
			rows[i] = table.Row{itoa(l.LineNo), p.Product.SKU.String(), p.Product.Name.String(), l.Quantity.StringFixed(p.Unit.Scale) + " " + p.Unit.Code,
				l.UnitCost.String(), l.BaseCost().StringFixed(2) + " " + v.Currency.Code(), l.Expenses.StringFixed(2), total}
		}
		r.list.setRows(rows)
	case tea.KeyPressMsg:
		if cmd, done := r.list.update(msg); done {
			return cmd
		}
		if r.d == nil {
			return nil
		}
		a, v := r.app, r.d.v
		switch msg.String() {
		case "r":
			return r.Refresh()
		case "f2":
			if r.draft() {
				return push(newReceiptLine(a, v, nil))
			}
		case "enter":
			if l := r.selected(); l != nil && r.draft() {
				return push(newReceiptLine(a, v, l))
			}
		case "f8":
			if l := r.selected(); l != nil && r.draft() {
				p := v.Products[l.ProductID].Product
				return push(&confirmScreen{app: a, title: "Quitar línea", message: "Quitar " + p.SKU.String() + " " + p.Name.String() + " de " + v.Number + ".",
					ok: "línea quitada", run: func() (any, error) { return a.opts.Backend.RemoveReceiptLine(a.ctx, v.Number, p.ID.String()) }})
			}
		case "f3":
			if r.app.can(identity.PermPurchaseManage) && v.Status != purchase.ReceiptCancelled {
				return push(newExpense(a, v))
			}
		case "f9":
			if r.draft() && a.can(identity.PermReceiptConfirm) && len(v.Lines) > 0 {
				return push(newConfirmReceipt(a, *r.d))
			}
		case "f4":
			if r.draft() {
				return push(&confirmScreen{app: a, title: "Anular " + v.Number,
					message: "Anular la recepción en borrador. No mueve existencia; queda en el historial como anulada.",
					ok:      "recepción " + v.Number + " anulada",
					run:     func() (any, error) { return a.opts.Backend.CancelReceipt(a.ctx, v.Number) }})
			}
		case "f6":
			if r.missingRate() && a.can(identity.PermRateManage) {
				return push(newRateForm(a, "USD", "VES", day(v.DocumentDate)))
			}
		}
	default:
		cmd, _ := r.list.update(msg)
		return cmd
	}
	return nil
}

func rateText(rt exchange.Rate) string {
	return fmt.Sprintf("1 %s = %s %s (%s %s, desde %s, versión %d)", rt.Base, rt.Value, rt.Quote, sourceText[rt.Source.Type],
		rt.Source.Label, day(rt.ValidOn), rt.Version)
}

var sourceText = map[money.RateSourceType]string{money.SourceOfficial: "oficial", money.SourceManual: "manual", money.SourceProvider: "proveedor"}

// purchaseTotal es la suma de las líneas en la moneda del documento.
func purchaseTotal(v purchasing.ReceiptView) decimal.Decimal {
	t := decimal.Zero()
	for _, l := range v.Lines {
		t = t.Add(l.BaseCost())
	}
	return t
}

func (r *receiptScreen) View(width, _ int) string {
	if r.d == nil {
		return "Cargando…"
	}
	v := r.d.v
	var b strings.Builder
	head := fmt.Sprintf("%s · %s · %s", v.Supplier.Code, v.Supplier.Name, v.Location)
	if v.Order != "" {
		head += " · orden " + v.Order
	}
	b.WriteString(head + "\n")
	doc := "Factura del " + day(v.DocumentDate) + " en " + v.Currency.Code()
	if v.SupplierDocument != "" {
		doc = "Factura " + v.SupplierDocument + " del " + day(v.DocumentDate) + " en " + v.Currency.Code()
	}
	b.WriteString(doc + "\n")
	switch {
	case v.Currency == v.Valuation:
		b.WriteString(hintStyle.Render("Sin conversión: el documento está en la moneda de valoración.") + "\n")
	case r.d.rate != nil && v.Status == purchase.ReceiptDraft:
		b.WriteString("Tasa: " + rateText(*r.d.rate) + "; se aplica al confirmar\n")
	case r.d.rate != nil:
		b.WriteString("Tasa aplicada: " + rateText(*r.d.rate) + "\n")
	case v.Status == purchase.ReceiptDraft:
		line := fmt.Sprintf("Falta la tasa %s/%s vigente al %s: sin ella no se puede confirmar.", v.Currency, v.Valuation, day(v.DocumentDate))
		if r.app.can(identity.PermRateManage) {
			line += " F6 la registra."
		}
		b.WriteString(warnStyle.Render(ansi.Wordwrap(line, width, " ")) + "\n")
	}
	b.WriteString(r.list.view("Sin líneas: pulse F2 para agregar la primera.") + "\n")
	total := purchaseTotal(v)
	sum := fmt.Sprintf("Total compra: %s %s", total.StringFixed(2), v.Currency)
	if r.d.rate != nil && v.Currency != v.Valuation {
		if c, err := r.d.rate.Convert(total, v.Currency, 2); err == nil {
			sum += fmt.Sprintf(" ≈ %s %s", c.StringFixed(2), v.Valuation)
		}
	}
	b.WriteString(sum)
	for _, e := range v.Expenses {
		state := "se prorratea al confirmar"
		if len(e.Allocations) > 0 {
			capd := decimal.Zero()
			for _, al := range e.Allocations {
				capd = capd.Add(al.Capitalized)
			}
			state = "capitalizado " + capd.StringFixed(2) + " " + v.Valuation.Code()
		}
		crit := "por valor"
		if e.Criterion == purchase.ByQuantity {
			crit = "por cantidad"
		}
		late := ""
		if e.Late {
			late = " (tardío)"
		}
		b.WriteString(fmt.Sprintf("\nGasto%s: %s %s %s, %s; %s", late, e.Concept, e.Amount, e.Currency, crit, state))
	}
	return b.String()
}

func newReceiptLine(a *app, v purchasing.ReceiptView, l *purchase.ReceiptLine) screen {
	product, qty, cost := productF("product", "Producto"), textF("qty", "Cantidad", "", 32), textF("cost", "Costo unitario", "", 32)
	title := "Agregar línea a " + v.Number
	if l != nil {
		p := v.Products[l.ProductID]
		title = "Editar línea " + itoa(l.LineNo) + " de " + v.Number
		product.input.SetValue(p.Product.SKU.String())
		qty.input.SetValue(l.Quantity.StringFixed(p.Unit.Scale))
		cost.input.SetValue(l.UnitCost.String())
	}
	cost.placeholder("en " + v.Currency.Code() + ", sin gastos")
	f := &formScreen{app: a, title: title,
		info: "El costo es el de la factura, en la moneda del documento y sin gastos (flete, seguro): estos van aparte y se " +
			"prorratean al confirmar. Un producto ya cargado se reemplaza.",
		fields: []*field{product, qty, cost}}
	f.save = func(m map[string]string) (any, error) {
		return a.opts.Backend.SetReceiptLine(a.ctx, v.Number, purchasing.LineInput{Product: m["product"], Quantity: m["qty"], UnitCost: m["cost"]})
	}
	f.done = func(any) tea.Cmd { return back("línea guardada en " + v.Number) }
	return f
}

func newExpense(a *app, v purchasing.ReceiptView) screen {
	title, info := "Gasto de "+v.Number, "Flete, seguro u otro gasto para traer la mercancía. Se reparte entre las líneas por "+
		"valor o por cantidad y entra al costo. Vacía, la fecha es la de la factura."
	if v.Status == purchase.ReceiptConfirmed {
		title, info = "Gasto tardío de "+v.Number, "La recepción ya está confirmada: el gasto revalora lo que queda en el almacén; "+
			"la parte de unidades ya vendidas o salidas queda como diferencia. Vacía, la fecha es hoy."
	}
	f := &formScreen{app: a, title: title, info: info, fields: []*field{textF("concept", "Concepto", "", 120).placeholder("p. ej. Flete"),
		textF("amount", "Monto", "", 32), choiceF("currency", "Moneda", currencyCodes, "").labeled("la del documento", "USD", "VES"),
		textF("date", "Fecha del comprobante", "", 10).placeholder("AAAA-MM-DD"),
		choiceF("criterion", "Reparto", []string{"value", "quantity"}, "value").labeled("Por valor", "Por cantidad")}}
	f.save = func(m map[string]string) (any, error) {
		return a.opts.Backend.AddExpense(a.ctx, v.Number, purchasing.ExpenseInput{Concept: m["concept"], Amount: m["amount"],
			Currency: m["currency"], Date: m["date"], Criterion: m["criterion"]})
	}
	f.done = func(any) tea.Cmd { return back("gasto registrado en " + v.Number) }
	return f
}

func newConfirmReceipt(a *app, d receiptData) screen {
	v := d.v
	msg := fmt.Sprintf("Confirmar %s de %s: %d líneas por %s %s", v.Number, v.Supplier.Name, len(v.Lines), purchaseTotal(v).StringFixed(2), v.Currency)
	if len(v.Expenses) > 0 {
		msg += fmt.Sprintf(" y %d gastos", len(v.Expenses))
	}
	msg += ". La mercancía entra a " + v.Location.String() + ", se recalcula el costo promedio y pueden generarse propuestas " +
		"de precio. Es definitivo."
	return &confirmScreen{app: a, title: "Confirmar " + v.Number, message: msg,
		run: func() (any, error) { return a.opts.Backend.ConfirmReceipt(a.ctx, v.Number) },
		result: func(x any) string {
			c := x.(purchasing.ReceiptView)
			out := "recepción " + c.Number + " confirmada: mercancía en " + c.Location.Warehouse.Code.String()
			if n := len(c.Suggestions); n > 0 {
				out += fmt.Sprintf("; %d propuestas de precio para aprobar", n)
			}
			return out
		}}
}

// ---------- Órdenes de compra ----------

var orderText = map[purchase.OrderStatus]string{purchase.OrderOpen: "abierta", purchase.OrderReceived: "recibida", purchase.OrderCancelled: "anulada"}

func pendingLines(o purchasing.OrderView) int {
	n := 0
	for _, l := range o.Lines {
		if l.Pending().Sign() > 0 {
			n++
		}
	}
	return n
}

func newOrders(a *app) screen {
	r := &recordsScreen[purchasing.OrderView]{app: a, title: "Órdenes de compra", toggle: true, subset: "abiertas", wide: true,
		info: "Una orden registra lo pedido a un proveedor con su costo pactado. No mueve existencia: la mercancía entra " +
			"con las recepciones contra la orden.",
		empty: "No hay órdenes.",
		list: newList([]table.Column{{Title: "Número", Width: 10}, {Title: "Proveedor", Width: 30}, {Title: "Moneda", Width: 6},
			{Title: "Estado", Width: 10}, {Title: "Líneas", Width: 6}, {Title: "Pendientes", Width: 10}, {Title: "Creada", Width: 16}}, 1),
		fetch: func(all bool) ([]purchasing.OrderView, error) {
			st := purchase.OrderOpen
			if all {
				st = ""
			}
			return a.opts.Backend.Orders(a.ctx, st)
		},
		row: func(o purchasing.OrderView) table.Row {
			return table.Row{o.Number, o.Supplier.Name.String(), o.Currency.Code(), orderText[o.Status], itoa(len(o.Lines)),
				itoa(pendingLines(o)), when(o.CreatedAt)}
		}}
	r.actions = []action[purchasing.OrderView]{
		{key: "f2", label: "F2 Nueva", ok: func(*purchasing.OrderView) bool { return a.can(identity.PermPurchaseManage) },
			run: func(*purchasing.OrderView) tea.Cmd { return push(newOrderForm(a)) }},
		{key: "enter", label: "Enter Abrir", needsItem: true, run: func(o *purchasing.OrderView) tea.Cmd { return push(newOrder(a, o.Number)) }},
	}
	return r
}

// orderHeader son los datos de una orden antes de cargar sus líneas.
type orderHeader struct{ supplier, currency, note string }

func newOrderForm(a *app) screen {
	f := &formScreen{app: a, title: "Nueva orden de compra",
		info: "Elija el proveedor y la moneda de los costos pactados; después se cargan las líneas.",
		fields: []*field{choiceF("supplier", "Proveedor", nil, ""), choiceF("currency", "Moneda", currencyCodes, "").labeled(currencyLabels...),
			textF("note", "Nota", "", 500)}}
	f.prepare = func() (any, error) { return a.opts.Backend.Suppliers(a.ctx) }
	f.ready = func(f *formScreen, v any) { supplierChoice(f, v.([]purchase.Supplier), "", "") }
	f.save = func(m map[string]string) (any, error) {
		if m["supplier"] == "" {
			return nil, errors.New("elija un proveedor")
		}
		return orderHeader{m["supplier"], m["currency"], m["note"]}, nil
	}
	f.done = func(v any) tea.Cmd { return replace(newOrderLines(a, v.(orderHeader)), "") }
	return f
}

// orderLinesScreen carga las líneas de una orden nueva: producto, Enter,
// cantidad, Enter, costo, Enter. F10 crea la orden.
type orderLinesScreen struct {
	app    *app
	id     int
	hdr    orderHeader
	lines  []purchasing.LineInput
	list   listView
	inputs [3]textinput.Model
	focus  int
	found  string
	looked string
	busy   bool
	errMsg string
}

func newOrderLines(a *app, h orderHeader) screen {
	o := &orderLinesScreen{app: a, hdr: h, list: newList([]table.Column{{Title: "Producto", Width: 30}, {Title: "Cantidad", Width: 12},
		{Title: "Costo unit.", Width: 14}}, 0)}
	o.inputs[0] = newInput("Producto: ", 200)
	o.inputs[0].Placeholder = "SKU o código"
	o.inputs[0].SetWidth(24)
	o.inputs[1] = newInput("Cantidad: ", 32)
	o.inputs[1].SetWidth(10)
	o.inputs[2] = newInput("Costo: ", 32)
	o.inputs[2].SetWidth(12)
	return o
}

func (o *orderLinesScreen) Title() string   { return "Nueva orden · " + o.hdr.supplier }
func (o *orderLinesScreen) Capturing() bool { return true }
func (o *orderLinesScreen) Info() string {
	return "Escriba o escanee el producto, Enter, la cantidad, Enter, el costo pactado, Enter. Repetir un producto lo " +
		"reemplaza. F8 quita la última línea y F10 crea la orden."
}
func (o *orderLinesScreen) Help() string {
	return "Enter Siguiente/Agregar  F8 Quitar última  F10 Crear orden  Esc Cancelar"
}

func (o *orderLinesScreen) Init(id int) tea.Cmd {
	o.id = id
	return o.inputs[0].Focus()
}

func (o *orderLinesScreen) setFocus(i int) tea.Cmd {
	for j := range o.inputs {
		o.inputs[j].Blur()
	}
	o.focus = i
	return o.inputs[i].Focus()
}

func (o *orderLinesScreen) rows() {
	rows := make([]table.Row, len(o.lines))
	for i, l := range o.lines {
		rows[i] = table.Row{l.Product, l.Quantity, l.UnitCost}
	}
	o.list.setRows(rows)
}

func (o *orderLinesScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		o.list.resize(msg.Width, msg.Height-3)
	case loaded:
		if v, ok := msg.v.(productInfo); ok {
			if v.query == o.looked {
				o.found = v.text
			}
			return nil
		}
		o.busy = false
		if msg.err != nil {
			o.errMsg = describe(msg.err)
			return nil
		}
		return back("orden " + msg.v.(purchasing.OrderView).Number + " creada")
	case tea.KeyPressMsg:
		if o.busy {
			return nil
		}
		switch msg.String() {
		case "esc":
			return back("cancelado; no se guardó nada")
		case "f8":
			if n := len(o.lines); n > 0 {
				o.lines = o.lines[:n-1]
				o.rows()
			}
			return nil
		case "f10", "ctrl+s":
			if len(o.lines) == 0 {
				o.errMsg = "agregue al menos una línea"
				return nil
			}
			o.busy, o.errMsg = true, ""
			a, in := o.app, purchasing.OrderInput{Supplier: o.hdr.supplier, Currency: o.hdr.currency, Note: o.hdr.note, Lines: o.lines}
			return load(o.id, func() (any, error) { return a.opts.Backend.CreateOrder(a.ctx, in) })
		case "tab":
			return o.setFocus((o.focus + 1) % 3)
		case "shift+tab":
			return o.setFocus((o.focus + 2) % 3)
		case "enter":
			switch o.focus {
			case 0:
				q := strings.TrimSpace(o.inputs[0].Value())
				if q == "" {
					return nil
				}
				var cmd tea.Cmd
				if q != o.looked {
					o.looked, o.found = q, ""
					cmd = lookupProduct(o.app, o.id, "product", q, "")
				}
				return tea.Batch(cmd, o.setFocus(1))
			case 1:
				return o.setFocus(2)
			}
			l := purchasing.LineInput{Product: strings.ToUpper(strings.TrimSpace(o.inputs[0].Value())),
				Quantity: strings.TrimSpace(o.inputs[1].Value()), UnitCost: strings.TrimSpace(o.inputs[2].Value())}
			if l.Product == "" || l.Quantity == "" || l.UnitCost == "" {
				o.errMsg = "indique producto, cantidad y costo"
				return nil
			}
			o.errMsg = ""
			replaced := false
			for i := range o.lines {
				if o.lines[i].Product == l.Product {
					o.lines[i], replaced = l, true
				}
			}
			if !replaced {
				o.lines = append(o.lines, l)
			}
			o.rows()
			for i := range o.inputs {
				o.inputs[i].SetValue("")
			}
			o.found, o.looked = "", ""
			return o.setFocus(0)
		}
		var cmd tea.Cmd
		o.inputs[o.focus], cmd = o.inputs[o.focus].Update(msg)
		return cmd
	}
	return nil
}

func (o *orderLinesScreen) View(int, int) string {
	var b strings.Builder
	cur := o.hdr.currency
	if cur == "" {
		cur = "moneda de valoración"
	}
	b.WriteString("Proveedor " + o.hdr.supplier + " · costos en " + cur + "\n")
	b.WriteString(o.inputs[0].View() + "  " + o.inputs[1].View() + "  " + o.inputs[2].View() + "\n")
	if o.found != "" {
		b.WriteString("  " + okStyle.Render("→ "+o.found))
	}
	b.WriteString("\n")
	switch {
	case o.busy:
		b.WriteString("Guardando…\n")
	case o.errMsg != "":
		b.WriteString(failStyle.Render(o.errMsg) + "\n")
	}
	b.WriteString(o.list.view("Sin líneas todavía."))
	return b.String()
}

// orderScreen muestra una orden con lo pedido, lo recibido y lo pendiente.
type orderScreen struct {
	app    *app
	id     int
	number string
	v      *purchasing.OrderView
	list   listView
}

func newOrder(a *app, number string) screen {
	return &orderScreen{app: a, number: number, list: newList([]table.Column{{Title: "#", Width: 3}, {Title: "SKU", Width: 16},
		{Title: "Producto", Width: 30}, {Title: "Pedido", Width: 12}, {Title: "Recibido", Width: 12}, {Title: "Pendiente", Width: 12},
		{Title: "Costo pactado", Width: 14}}, 2)}
}

func (o *orderScreen) Title() string {
	if o.v == nil {
		return "Orden " + o.number
	}
	return "Orden " + o.number + " · " + orderText[o.v.Status]
}
func (o *orderScreen) Wide() bool      { return true }
func (o *orderScreen) Capturing() bool { return o.list.capturing() }

func (o *orderScreen) open() bool {
	return o.v != nil && o.v.Status == purchase.OrderOpen && o.app.can(identity.PermPurchaseManage)
}

func (o *orderScreen) Help() string {
	if o.open() {
		return "↑↓ Elegir  F5 Recibir  F4 Anular  R Recargar  Esc Volver"
	}
	return "↑↓ Elegir  R Recargar  Esc Volver"
}

func (o *orderScreen) Init(id int) tea.Cmd {
	o.id = id
	return o.Refresh()
}

func (o *orderScreen) Refresh() tea.Cmd {
	a, n := o.app, o.number
	return load(o.id, func() (any, error) { return a.opts.Backend.Order(a.ctx, n) })
}

func (o *orderScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		o.list.resize(msg.Width, msg.Height-2)
	case loaded:
		if msg.err != nil {
			return setStatus("no se pudo cargar la orden", msg.err)
		}
		v := msg.v.(purchasing.OrderView)
		o.v = &v
		rows := make([]table.Row, len(v.Lines))
		for i, l := range v.Lines {
			p := v.Products[l.ProductID]
			sc := p.Unit.Scale
			rows[i] = table.Row{itoa(l.LineNo), p.Product.SKU.String(), p.Product.Name.String(), l.Quantity.StringFixed(sc),
				l.Received.StringFixed(sc), l.Pending().StringFixed(sc), l.UnitCost.String() + " " + v.Currency.Code()}
		}
		o.list.setRows(rows)
	case tea.KeyPressMsg:
		if cmd, done := o.list.update(msg); done {
			return cmd
		}
		switch msg.String() {
		case "r":
			return o.Refresh()
		case "f5":
			if o.open() {
				return push(newReceiptForm(o.app, o.v))
			}
		case "f4":
			if o.open() {
				a, n := o.app, o.v.Number
				return push(&confirmScreen{app: a, title: "Anular " + n,
					message: "Anular la orden: lo pendiente ya no se espera. Las recepciones ya registradas no cambian.",
					ok:      "orden " + n + " anulada", run: func() (any, error) { return a.opts.Backend.CancelOrder(a.ctx, n) }})
			}
		}
	default:
		cmd, _ := o.list.update(msg)
		return cmd
	}
	return nil
}

func (o *orderScreen) View(int, int) string {
	if o.v == nil {
		return "Cargando…"
	}
	head := fmt.Sprintf("%s · %s · costos en %s", o.v.Supplier.Code, o.v.Supplier.Name, o.v.Currency)
	if o.v.Note != "" {
		head += "\n" + hintStyle.Render(o.v.Note)
	}
	return head + "\n" + o.list.view("Sin líneas.")
}

// ---------- Costos de un producto ----------

type costsScreen struct {
	app   *app
	id    int
	input textinput.Model
	v     *purchasing.CostView
	err   string
	busy  bool
}

func newCosts(a *app) screen {
	c := &costsScreen{app: a, input: newInput("Producto: ", 200)}
	c.input.Placeholder = "SKU o código de barras, Enter"
	return c
}

func (c *costsScreen) Title() string   { return "Costos de un producto" }
func (c *costsScreen) Capturing() bool { return true }
func (c *costsScreen) Info() string {
	return "Costo promedio por almacén, última compra y costo de reposición. El de reposición es una estimación comercial " +
		"para fijar precios: no cambia el costo promedio."
}
func (c *costsScreen) Help() string {
	if c.v != nil && c.app.can(identity.PermReplacementCost) {
		return "Enter Consultar  F2 Costo de reposición  Esc Volver"
	}
	return "Enter Consultar  Esc Volver"
}

func (c *costsScreen) Init(id int) tea.Cmd {
	c.id = id
	return c.input.Focus()
}

func (c *costsScreen) Refresh() tea.Cmd {
	if c.v == nil {
		return nil
	}
	return c.fetch(c.v.Product.ID.String())
}

func (c *costsScreen) fetch(ref string) tea.Cmd {
	c.busy = true
	a := c.app
	return load(c.id, func() (any, error) { return a.opts.Backend.Costs(a.ctx, ref) })
}

func (c *costsScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case loaded:
		c.busy = false
		if msg.err != nil {
			c.err, c.v = describe(msg.err), nil
			return nil
		}
		v := msg.v.(purchasing.CostView)
		c.v, c.err = &v, ""
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			return back("")
		case "enter":
			if q := strings.TrimSpace(c.input.Value()); q != "" {
				return c.fetch(q)
			}
			return nil
		case "f2":
			if c.v != nil && c.app.can(identity.PermReplacementCost) {
				return push(newReplacementCost(c.app, *c.v))
			}
			return nil
		}
		var cmd tea.Cmd
		c.input, cmd = c.input.Update(msg)
		return cmd
	}
	return nil
}

func (c *costsScreen) View(int, int) string {
	var b strings.Builder
	b.WriteString(c.input.View() + "\n\n")
	switch {
	case c.busy:
		return b.String() + "Consultando…"
	case c.err != "":
		return b.String() + failStyle.Render(c.err)
	case c.v == nil:
		return b.String() + hintStyle.Render("Indique un producto.")
	}
	v := c.v
	cur := v.Company.Currency.Code()
	b.WriteString(sectionStyle.Render(v.Product.SKU.String()+" "+v.Product.Name.String()) + "\n")
	for _, bal := range v.Balances {
		cpp := "sin costo"
		if u := bal.UnitCost(); u.Known {
			cpp = u.Value.StringFixed(6) + " " + cur
		}
		b.WriteString(fmt.Sprintf("Costo promedio en %s: %s (existencia %s %s)\n", bal.Location.Warehouse.Code, cpp,
			bal.OnHand.StringFixed(v.Unit.Scale), v.Unit.Code))
	}
	if len(v.Balances) == 0 {
		b.WriteString("Sin existencias registradas.\n")
	}
	if l := v.Last; l != nil {
		b.WriteString(fmt.Sprintf("Última compra: %s %s (%s, %s); con gastos %s %s\n", l.UnitCost, l.Currency, l.Receipt,
			day(timezone.InCaracas(l.ReceivedAt)), l.TotalUnit.StringFixed(6), cur))
	} else {
		b.WriteString("Sin compras confirmadas.\n")
	}
	if len(v.Replacement) == 0 {
		b.WriteString("Sin costo de reposición.")
	}
	for i, r := range v.Replacement {
		label := "Reposición vigente"
		if i > 0 {
			label = "  anterior"
		}
		note := ""
		if r.Note != "" {
			note = " · " + r.Note
		}
		b.WriteString(fmt.Sprintf("%s: %s %s (%s)%s\n", label, r.UnitCost, r.Currency, when(r.OccurredAt), note))
	}
	return b.String()
}

func newReplacementCost(a *app, v purchasing.CostView) screen {
	f := &formScreen{app: a, title: "Costo de reposición de " + v.Product.SKU.String(),
		info: "Lo que costaría volver a comprarlo hoy. Sirve de base a los márgenes de las listas de precios; no cambia el " +
			"costo promedio ni ningún precio por sí solo.",
		fields: []*field{textF("cost", "Costo unitario", "", 32), choiceF("currency", "Moneda", currencyCodes, "").labeled(currencyLabels...),
			textF("note", "Nota", "", 500).placeholder("p. ej. lista de precios del proveedor")}}
	f.save = func(m map[string]string) (any, error) {
		return a.opts.Backend.SetReplacementCost(a.ctx, v.Product.ID.String(), m["cost"], m["currency"], m["note"])
	}
	f.done = func(any) tea.Cmd { return back("costo de reposición registrado") }
	return f
}

// ---------- Tasas de cambio ----------

func newRates(a *app) screen {
	r := &recordsScreen[rates.RateView]{app: a, title: "Tasas de cambio",
		info: "Cada tasa rige desde su fecha hasta que otra la reemplace. Corregir es registrar una versión nueva: los " +
			"documentos que ya usaron la anterior la conservan.",
		empty: "No hay tasas registradas.",
		list: newList([]table.Column{{Title: "Vigente desde", Width: 13}, {Title: "Tasa", Width: 26}, {Title: "Fuente", Width: 20},
			{Title: "Versión", Width: 7}, {Title: "Referencia", Width: 20}, {Title: "Nota", Width: 30}}, 5),
		fetch: func(bool) ([]rates.RateView, error) { return a.opts.Backend.Rates(a.ctx) },
		row: func(v rates.RateView) table.Row {
			return table.Row{day(v.ValidOn), fmt.Sprintf("1 %s = %s %s", v.Base, v.Value, v.Quote),
				sourceText[v.Source.Type] + " " + v.Source.Label, itoa(v.Version), v.Reference, v.Note}
		}}
	r.actions = []action[rates.RateView]{{key: "f2", label: "F2 Registrar",
		ok:  func(*rates.RateView) bool { return a.can(identity.PermRateManage) },
		run: func(*rates.RateView) tea.Cmd { return push(newRateForm(a, "USD", "VES", "")) }}}
	return r
}

func newRateForm(a *app, base, quote, date string) screen {
	f := &formScreen{app: a, title: "Registrar tasa",
		info: "1 moneda base = tasa en la cotizada (1 USD = 45.725 VES). Declare la fuente: oficial (BCV), manual o un " +
			"proveedor, con su nombre. Vacía, la fecha es hoy en Caracas.",
		fields: []*field{choiceF("base", "Base", []string{"USD", "VES"}, base), choiceF("quote", "Cotizada", []string{"VES", "USD"}, quote),
			textF("rate", "Tasa", "", 32), choiceF("source", "Fuente", []string{"official", "manual", "provider"}, "official").labeled("Oficial", "Manual", "Proveedor"),
			textF("label", "Nombre de la fuente", "BCV", 60), textF("date", "Vigente desde", date, 10).placeholder("AAAA-MM-DD · vacía: hoy"),
			textF("reference", "Referencia", "", 120).placeholder("opcional: publicación o enlace"), textF("note", "Nota", "", 500)}}
	f.save = func(m map[string]string) (any, error) {
		return a.opts.Backend.SetRate(a.ctx, rates.SetInput{Base: m["base"], Quote: m["quote"], Rate: m["rate"], Source: m["source"],
			Label: m["label"], Date: m["date"], Reference: m["reference"], Note: m["note"]})
	}
	f.done = func(v any) tea.Cmd { return back("tasa registrada: " + rateText(v.(rates.RateView).Rate)) }
	return f
}

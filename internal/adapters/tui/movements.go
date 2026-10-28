package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/org"
)

// ---------- Producto: reconocimiento y lista ----------

// matchProduct elige el producto que corresponde a lo escrito: el de ese
// SKU o código de barras, o el único resultado de la búsqueda.
func matchProduct(list []appcatalog.ProductView, q string) (appcatalog.ProductView, bool) {
	for _, v := range list {
		if strings.EqualFold(v.SKU.String(), q) {
			return v, true
		}
		for _, b := range v.Barcodes {
			if b.String() == q {
				return v, true
			}
		}
	}
	if len(list) == 1 {
		return list[0], true
	}
	return appcatalog.ProductView{}, false
}

// lookupProduct reconoce q y describe el producto con su disponible en la
// sucursal de la sesión (o en warehouse, si se indica).
func lookupProduct(a *app, to int, key, q, warehouse string) tea.Cmd {
	return load(to, func() (any, error) {
		info := productInfo{key: key, query: q}
		list, err := a.opts.Backend.Products(a.ctx, q)
		if err != nil {
			info.text = describe(err)
			return info, nil
		}
		v, ok := matchProduct(list, q)
		if !ok {
			info.text = "no se reconoce; pulse F5 para elegirlo de la lista"
			if len(list) > 1 {
				info.text = fmt.Sprintf("%d productos coinciden; pulse F5 para elegir", len(list))
			}
			return info, nil
		}
		info.ok = true
		info.text = fmt.Sprintf("%s %s (%s)", v.SKU, v.Name, v.Unit.Code)
		if v.Status != catalog.StatusActive {
			info.text += " · inactivo"
		}
		if a.session == nil || !a.session.Can(identity.PermStockRead) {
			return info, nil
		}
		bals, err := a.opts.Backend.Stock(a.ctx, v.ID.String())
		if err != nil {
			return info, nil // el producto se reconoció; el saldo es solo informativo
		}
		var parts []string
		for _, b := range bals {
			if b.Location.Branch.Code.String() != a.session.Branch || (warehouse != "" && b.Location.Warehouse.Code.String() != warehouse) {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %s", b.Location.Warehouse.Code, b.Available().StringFixed(v.Unit.Scale)))
		}
		if len(parts) == 0 {
			info.text += " · sin existencia aquí"
		} else {
			info.text += " · disponible " + strings.Join(parts, ", ")
		}
		return info, nil
	})
}

// productPicker es la lista para elegir un producto (F5 en un formulario).
type productPicker struct {
	app     *app
	id      int
	to      int    // formulario que lo pidió
	key     string // campo del formulario
	list    listView
	items   []appcatalog.ProductView
	loading bool
}

func newProductPicker(a *app, to int, key string) screen {
	return &productPicker{app: a, to: to, key: key, list: newList([]table.Column{
		{Title: "SKU", Width: 16}, {Title: "Nombre", Width: 50}, {Title: "Unidad", Width: 6}}, 1)}
}

func (p *productPicker) Title() string   { return "Elegir producto" }
func (p *productPicker) Capturing() bool { return p.list.capturing() }
func (p *productPicker) Info() string {
	return "Solo productos activos. / filtra por nombre, SKU o código; Enter elige."
}
func (p *productPicker) Help() string {
	if p.list.capturing() {
		return "Escriba para filtrar  Enter Elegir  Esc Borrar"
	}
	return "↑↓ Elegir  / Filtrar  Enter Elegir  Esc Volver"
}

func (p *productPicker) Init(id int) tea.Cmd {
	p.id, p.loading = id, true
	a := p.app
	// Abre con el filtro listo para escribir.
	p.list.filtering = true
	return tea.Batch(p.list.filter.Focus(), load(id, func() (any, error) { return a.opts.Backend.Products(a.ctx, "") }))
}

func (p *productPicker) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.list.resize(msg.Width, msg.Height)
	case loaded:
		p.loading = false
		if msg.err != nil {
			return setStatus("no se pudieron cargar los productos", msg.err)
		}
		p.items = p.items[:0]
		var rows []table.Row
		for _, v := range msg.v.([]appcatalog.ProductView) {
			if v.Status != catalog.StatusActive {
				continue
			}
			p.items = append(p.items, v)
			rows = append(rows, table.Row{v.SKU.String(), v.Name.String(), v.Unit.Code})
		}
		p.list.setRows(rows)
	case tea.KeyPressMsg:
		if p.list.capturing() && msg.String() == "enter" {
			p.list.update(msg) // fija el filtro y elige de una vez
			return p.pick()
		}
		if cmd, done := p.list.update(msg); done {
			return cmd
		}
		if msg.String() == "enter" {
			return p.pick()
		}
	default:
		cmd, _ := p.list.update(msg)
		return cmd
	}
	return nil
}

func (p *productPicker) pick() tea.Cmd {
	i := p.list.selected()
	if i < 0 {
		return nil
	}
	v := pickedProduct{key: p.key, sku: p.items[i].SKU.String()}
	to := p.to
	return tea.Batch(back(""), load(to, func() (any, error) { return v, nil }))
}

func (p *productPicker) View(int, int) string {
	if p.loading && len(p.items) == 0 {
		return "Cargando…"
	}
	return p.list.view("No hay productos activos.")
}

// ---------- Almacenes ----------

// warehouseF es la selección de almacén de la sucursal; se llena con
// withWarehouses. Sin opciones, el backend usa el único almacén activo.
func warehouseF(key, label string) *field {
	return choiceF(key, label, nil, "")
}

// withWarehouses carga los almacenes activos de la sucursal en los campos
// de almacén del formulario. Si el usuario no puede listarlos, queda el
// almacén de la sucursal.
func withWarehouses(a *app, f *formScreen) *formScreen {
	f.prepare = func() (any, error) {
		list, err := a.opts.Backend.Warehouses(a.ctx)
		if err != nil {
			return []org.Warehouse(nil), nil
		}
		return list, nil
	}
	f.ready = func(f *formScreen, v any) {
		var codes, labels []string
		for _, w := range v.([]org.Warehouse) {
			if w.Status != org.StatusActive {
				continue
			}
			codes = append(codes, w.Code.String())
			labels = append(labels, w.Code.String()+" · "+w.Name.String())
		}
		if len(codes) == 0 {
			codes, labels = []string{""}, []string{"el de la sucursal"}
		}
		for _, fl := range f.fields {
			if fl.kind != choiceField || (fl.key != "warehouse" && fl.key != "to") {
				continue
			}
			fl.options, fl.labels = codes, labels
			// El destino empieza en otro almacén que el origen.
			if fl.key == "to" && len(codes) > 1 {
				fl.choice = 1
			}
		}
	}
	return f
}

// ---------- Entradas y salidas ----------

func reasonChoice(d inventory.Direction) *field {
	var codes, labels []string
	for _, r := range inventory.Reasons() {
		if (d == inventory.In && r.In) || (d == inventory.Out && r.Out) {
			codes = append(codes, string(r.Reason))
			labels = append(labels, r.Description)
		}
	}
	return choiceF("reason", "Motivo", codes, "").labeled(labels...)
}

func newAdjust(a *app, d inventory.Direction) screen {
	title, info := "Entrada de mercancía", "Suma existencia en el almacén. Con costo unitario, el costo promedio se recalcula; "+
		"vacío entra al costo promedio vigente (debe existir). Para compras a proveedores use Compras."
	fields := []*field{warehouseF("warehouse", "Almacén"), productF("product", "Producto"), textF("qty", "Cantidad", "", 32)}
	if d == inventory.In {
		fields = append(fields, textF("cost", "Costo unitario", "", 32).placeholder("vacío: costo promedio vigente"))
	} else {
		title, info = "Salida de mercancía", "Resta existencia al costo promedio vigente. No puede dejar el disponible en negativo "+
			"salvo que la política lo permita y usted tenga el permiso extraordinario."
	}
	fields = append(fields, reasonChoice(d), textF("note", "Nota", "", 500).placeholder("obligatoria con el motivo Otro"))
	negative := d == inventory.Out && a.session != nil && a.session.Can(identity.PermNegativeOverride)
	if negative {
		fields = append(fields, choiceF("negative", "Permitir negativo", []string{"no", "si"}, "no").labeled("No", "Sí, dentro de la política"))
	}
	f := &formScreen{app: a, title: title, info: info, fields: fields, key: newKey()}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.Adjust(a.ctx, stock.AdjustInput{Where: stock.WarehouseRef{Warehouse: v["warehouse"]}, Product: v["product"],
			Direction: d, Quantity: v["qty"], UnitCost: v["cost"], Reason: v["reason"], Note: v["note"], IdempotencyKey: f.key,
			AllowNegative: v["negative"] == "si"})
	}
	f.done = func(v any) tea.Cmd { return back(describeMovement(v.(stock.MovementView))) }
	return withWarehouses(a, f)
}

func describeMovement(m stock.MovementView) string {
	sc := m.Unit.Scale
	verb := "entrada"
	if m.Quantity.IsNegative() {
		verb = "salida"
	}
	if m.Kind == inventory.KindReversal {
		verb = "reverso"
	}
	out := fmt.Sprintf("%s registrada: %s %s %s en %s; existencia %s", verb, m.Quantity.StringFixed(sc), m.Unit.Code, m.Product.SKU,
		m.Location, m.OnHandAfter.StringFixed(sc))
	if m.Replayed {
		out += " (ya estaba registrada)"
	}
	return out
}

// ---------- Transferencias ----------

func newTransfer(a *app) screen {
	f := &formScreen{app: a, title: "Transferencia entre almacenes",
		info: "Mueve existencia entre dos almacenes de la sucursal al costo promedio del origen, en una sola operación. " +
			"Exige el permiso de transferir en ambos almacenes.",
		fields: []*field{warehouseF("warehouse", "Origen"), warehouseF("to", "Destino"), productF("product", "Producto"),
			textF("qty", "Cantidad", "", 32), textF("note", "Nota", "", 500)},
		key: newKey()}
	f.save = func(v map[string]string) (any, error) {
		if v["warehouse"] == v["to"] {
			return nil, fmt.Errorf("el origen y el destino deben ser almacenes distintos")
		}
		return a.opts.Backend.Transfer(a.ctx, stock.TransferInput{From: v["warehouse"], To: v["to"], Product: v["product"],
			Quantity: v["qty"], Note: v["note"], IdempotencyKey: f.key})
	}
	f.done = func(v any) tea.Cmd {
		t := v.(stock.TransferView)
		return back(fmt.Sprintf("transferidos %s %s de %s: %s → %s", t.In.Quantity.StringFixed(t.Unit.Scale), t.Unit.Code, t.Product.SKU,
			t.Origin.Warehouse.Code, t.Destination.Warehouse.Code))
	}
	return withWarehouses(a, f)
}

// ---------- Reverso ----------

// reversible indica si un movimiento se puede revertir desde el kardex.
func reversible(m inventory.Movement) bool {
	return m.Kind != inventory.KindReversal && m.TransferID.IsZero()
}

func newReverse(a *app, b stock.BalanceView, m inventory.Movement) screen {
	sc := b.Unit.Scale
	f := &formScreen{app: a, title: fmt.Sprintf("Revertir movimiento #%d", m.Sequence),
		intro: fmt.Sprintf("%s de %s %s %s (%s). El reverso registra el movimiento contrario; el original queda en el kardex.",
			kindText[m.Kind], m.Quantity.StringFixed(sc), b.Unit.Code, b.Product.SKU, m.Reason),
		info:   "Cada movimiento se revierte una sola vez. Una transferencia no se revierte por partes: registre otra en sentido contrario.",
		fields: []*field{textF("note", "Motivo del reverso", "", 500)},
		key:    newKey()}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.Reverse(a.ctx, m.ID.String(), v["note"], f.key)
	}
	f.done = func(v any) tea.Cmd { return back(describeMovement(v.(stock.MovementView))) }
	return f
}

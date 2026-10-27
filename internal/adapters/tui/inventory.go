package tui

import (
	"fmt"
	"strconv"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

func itoa(n int) string { return strconv.Itoa(n) }

// ---------- Productos ----------

type productsScreen struct {
	app     *app
	id      int
	list    listView
	items   []appcatalog.ProductView
	units   []catalog.Unit
	loading bool
}

type productsData struct {
	products []appcatalog.ProductView
	units    []catalog.Unit
}

func newProducts(a *app) screen {
	return &productsScreen{app: a, list: newList([]table.Column{
		{Title: "SKU", Width: 14}, {Title: "Nombre", Width: 40}, {Title: "Unidad", Width: 8}, {Title: "Estado", Width: 9},
	}, 1)}
}

func (p *productsScreen) Title() string   { return "Productos" }
func (p *productsScreen) Capturing() bool { return p.list.capturing() }
func (p *productsScreen) canManage() bool {
	return p.app.session != nil && p.app.session.Can(identity.PermProductManage)
}
func (p *productsScreen) Info() string {
	s := "Enter abre las existencias del producto y P sus precios. / filtra por nombre, SKU o código."
	if p.canManage() {
		s += " F2 crea un producto, F3 lo edita (nombre, SKU, códigos de barras) y F4 lo activa o inactiva."
	}
	return s
}
func (p *productsScreen) Help() string {
	if p.list.capturing() {
		return "Escriba para filtrar  Enter Fijar  Esc Borrar"
	}
	if p.canManage() {
		return "↑↓ Elegir  / Filtrar  Enter Existencias  P Precios  F2 Nuevo  F3 Editar  F4 Activar/Inactivar  Esc Volver"
	}
	return "↑↓ Elegir  / Filtrar  Enter Existencias  P Precios  Esc Volver"
}

func (p *productsScreen) Init(id int) tea.Cmd {
	p.id = id
	return p.Refresh()
}

func (p *productsScreen) Refresh() tea.Cmd {
	p.loading = true
	a := p.app
	return load(p.id, func() (any, error) {
		prods, err := a.opts.Backend.Products(a.ctx, "")
		if err != nil {
			return nil, err
		}
		units, err := a.opts.Backend.Units(a.ctx)
		return productsData{prods, units}, err
	})
}

func (p *productsScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.list.resize(msg.Width, msg.Height)
	case loaded:
		p.loading = false
		if msg.err != nil {
			return setStatus("no se pudieron cargar los productos", msg.err)
		}
		d := msg.v.(productsData)
		p.items, p.units = d.products, d.units
		rows := make([]table.Row, len(p.items))
		for i, v := range p.items {
			state := "activo"
			if v.Status != catalog.StatusActive {
				state = "inactivo"
			}
			rows[i] = table.Row{v.SKU.String(), v.Name.String(), v.Unit.Code, state}
		}
		p.list.setRows(rows)
	case tea.KeyPressMsg:
		if cmd, done := p.list.update(msg); done {
			return cmd
		}
		if msg.String() == "f2" && p.canManage() {
			return push(newProductForm(p.app, p.units))
		}
		i := p.list.selected()
		if i < 0 {
			return nil
		}
		switch msg.String() {
		case "f3":
			if p.canManage() {
				return push(newProductEdit(p.app, p.items[i]))
			}
		case "f4":
			if p.canManage() {
				return push(newProductToggle(p.app, p.items[i]))
			}
		case "enter":
			return push(newStock(p.app, p.items[i].SKU.String()))
		case "p":
			return push(newPricesOf(p.app, p.items[i].SKU.String()))
		}
	default:
		cmd, _ := p.list.update(msg)
		return cmd
	}
	return nil
}

func (p *productsScreen) View(int, int) string {
	if p.loading && len(p.items) == 0 {
		return "Cargando…"
	}
	empty := "No hay productos."
	if p.canManage() {
		empty += " Pulse F2 para crear el primero."
	}
	return p.list.view(empty)
}

// ---------- Existencias ----------

type stockScreen struct {
	app     *app
	id      int
	product string // vacío: todos
	list    listView
	items   []stock.BalanceView
	loading bool
}

func newStock(a *app, product string) screen {
	return &stockScreen{app: a, product: product, list: newList([]table.Column{
		{Title: "Sucursal/Almacén", Width: 30}, {Title: "SKU", Width: 16}, {Title: "Producto", Width: 40}, {Title: "Física", Width: 14},
		{Title: "Reserv.", Width: 14}, {Title: "Bloq.", Width: 14}, {Title: "Disponible", Width: 14}, {Title: "Unidad", Width: 6},
		{Title: "CPP", Width: 16}, {Title: "Valor", Width: 20},
	}, 2)}
}

func (s *stockScreen) Title() string {
	if s.product != "" {
		return "Existencias de " + s.product
	}
	return "Existencias"
}
func (s *stockScreen) Capturing() bool { return s.list.capturing() }
func (s *stockScreen) Wide() bool      { return true }

func (s *stockScreen) Help() string {
	if s.list.capturing() {
		return "Escriba para filtrar  Enter Fijar  Esc Borrar"
	}
	return "↑↓ Elegir  / Filtrar  Enter Kardex  R Recargar  Esc Volver"
}

func (s *stockScreen) Init(id int) tea.Cmd {
	s.id = id
	return s.Refresh()
}

func (s *stockScreen) Refresh() tea.Cmd {
	s.loading = true
	a, product := s.app, s.product
	return load(s.id, func() (any, error) { return a.opts.Backend.Stock(a.ctx, product) })
}

func (s *stockScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.list.resize(msg.Width, msg.Height)
	case loaded:
		s.loading = false
		if msg.err != nil {
			return setStatus("no se pudieron cargar las existencias", msg.err)
		}
		s.items = msg.v.([]stock.BalanceView)
		rows := make([]table.Row, len(s.items))
		for i, v := range s.items {
			sc := v.Unit.Scale
			cpp, value := "—", "—"
			if c := v.UnitCost(); c.Known {
				cpp = c.Value.StringFixed(6)
			}
			if v.Value.Known {
				value = v.Value.Value.StringFixed(2) + " " + v.Location.Company.Currency.Code()
			}
			rows[i] = table.Row{v.Location.Branch.Code.String() + "/" + v.Location.Warehouse.Code.String(), v.Product.SKU.String(), v.Product.Name.String(), v.OnHand.StringFixed(sc),
				v.Reserved.StringFixed(sc), v.Blocked.StringFixed(sc), v.Available().StringFixed(sc), v.Unit.Code, cpp, value}
		}
		s.list.setRows(rows)
	case tea.KeyPressMsg:
		if cmd, done := s.list.update(msg); done {
			return cmd
		}
		switch msg.String() {
		case "r":
			return s.Refresh()
		case "enter":
			if i := s.list.selected(); i >= 0 {
				return push(newKardex(s.app, s.items[i]))
			}
		}
	default:
		cmd, _ := s.list.update(msg)
		return cmd
	}
	return nil
}

func (s *stockScreen) View(int, int) string {
	if s.loading && len(s.items) == 0 {
		return "Cargando…"
	}
	return s.list.view("Sin existencias registradas en los almacenes que puede ver.")
}

// ---------- Kardex ----------

type kardexScreen struct {
	app     *app
	id      int
	bal     stock.BalanceView
	list    listView
	loading bool
}

func newKardex(a *app, b stock.BalanceView) screen {
	return &kardexScreen{app: a, bal: b, list: newList([]table.Column{
		{Title: "#", Width: 5}, {Title: "Fecha (VET)", Width: 16}, {Title: "Tipo", Width: 15}, {Title: "Motivo", Width: 14},
		{Title: "Cantidad", Width: 10}, {Title: "Saldo", Width: 10}, {Title: "Costo", Width: 12}, {Title: "Valor", Width: 12}, {Title: "Nota", Width: 20},
	}, 8)}
}

func (k *kardexScreen) Title() string {
	return "Kardex de " + k.bal.Product.SKU.String() + " en " + k.bal.Location.String()
}
func (k *kardexScreen) Capturing() bool { return k.list.capturing() }
func (k *kardexScreen) Wide() bool      { return true }

func (k *kardexScreen) Help() string {
	if k.list.capturing() {
		return "Escriba para filtrar  Enter Fijar  Esc Borrar"
	}
	return "↑↓ Elegir  / Filtrar  R Recargar  Esc Volver"
}

func (k *kardexScreen) Init(id int) tea.Cmd {
	k.id = id
	return k.Refresh()
}

func (k *kardexScreen) Refresh() tea.Cmd {
	k.loading = true
	a, b := k.app, k.bal
	where := stock.WarehouseRef{Company: b.Location.Company.Code.String(), Branch: b.Location.Branch.Code.String(), Warehouse: b.Location.Warehouse.Code.String()}
	return load(k.id, func() (any, error) {
		bv, moves, err := a.opts.Backend.Kardex(a.ctx, where, b.Product.ID.String())
		return kardexData{bv, moves}, err
	})
}

type kardexData struct {
	bal   stock.BalanceView
	moves []inventory.Movement
}

// kindText nombra cada tipo de movimiento, como en la CLI.
var kindText = map[inventory.Kind]string{
	inventory.KindAdjustmentIn:   "entrada",
	inventory.KindAdjustmentOut:  "salida",
	inventory.KindReversal:       "reverso",
	inventory.KindTransferOut:    "transf. salida",
	inventory.KindTransferIn:     "transf. entrada",
	inventory.KindReservationOut: "despacho",
	inventory.KindCountIn:        "conteo +",
	inventory.KindCountOut:       "conteo −",
	inventory.KindPurchaseIn:     "compra",
	inventory.KindRevaluation:    "revaloración",
}

func (k *kardexScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		k.list.resize(msg.Width, msg.Height-2)
	case loaded:
		k.loading = false
		if msg.err != nil {
			return setStatus("no se pudo cargar el kardex", msg.err)
		}
		d := msg.v.(kardexData)
		k.bal = d.bal
		sc := d.bal.Unit.Scale
		rows := make([]table.Row, len(d.moves))
		for i, m := range d.moves {
			cost, value := "—", "—"
			if m.Cost.Known {
				cost = m.Cost.Value.StringFixed(2)
			}
			if m.ValueAfter.Known {
				value = m.ValueAfter.Value.StringFixed(2)
			}
			rows[i] = table.Row{itoa(int(m.Sequence)), timezone.InCaracas(m.OccurredAt).Format("2006-01-02 15:04"), kindText[m.Kind], string(m.Reason),
				m.Quantity.StringFixed(sc), m.OnHandAfter.StringFixed(sc), cost, value, m.Note}
		}
		k.list.setRows(rows)
		k.list.t.GotoBottom() // lo más reciente
	case tea.KeyPressMsg:
		if cmd, done := k.list.update(msg); done {
			return cmd
		}
		if msg.String() == "r" {
			return k.Refresh()
		}
	default:
		cmd, _ := k.list.update(msg)
		return cmd
	}
	return nil
}

func (k *kardexScreen) View(int, int) string {
	if k.loading && len(k.list.rows) == 0 {
		return "Cargando…"
	}
	b := k.bal
	sc := b.Unit.Scale
	head := fmt.Sprintf("%s %s · existencia %s %s", b.Product.SKU, b.Product.Name, b.OnHand.StringFixed(sc), b.Unit.Code)
	if c := b.UnitCost(); c.Known {
		head += fmt.Sprintf(" · CPP %s · valor %s %s", c.Value.StringFixed(6), b.EffectiveValue().Value.StringFixed(2), b.Location.Company.Currency)
	}
	return head + "\n" + k.list.view("Sin movimientos.")
}

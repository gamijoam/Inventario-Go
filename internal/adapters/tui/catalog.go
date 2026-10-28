package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
)

// ---------- Asistente de primer uso ----------

func newSetupWizard(a *app) screen {
	f := &formScreen{app: a, title: "Configuración inicial",
		intro: "Bienvenido a BalanzaPro. Indique los datos de su negocio y del usuario administrador.",
		info: "Se crea la base de datos con la organización, la empresa, la sucursal y el almacén PRINCIPAL, y el usuario " +
			"administrador, que queda con la sesión iniciada. La moneda de valoración del inventario no se puede cambiar después. " +
			"El RIF es opcional (J-12345678-9).",
		fields: []*field{
			textF("name", "Nombre del negocio", "", 120).placeholder("Bodega Ana"),
			textF("legal", "Razón social", "", 120).placeholder("opcional: Bodega Ana, C.A."),
			textF("rif", "RIF", "", 16).placeholder("opcional: J-12345678-9"),
			choiceF("currency", "Moneda de valoración", []string{"USD", "VES"}, "USD"),
			textF("user", "Usuario administrador", "admin", 64),
			textF("display", "Nombre visible", "Administrador", 120),
			passwordF("password", "Contraseña"),
			passwordF("again", "Repita la contraseña"),
		},
	}
	f.cancel = tea.Quit
	f.save = func(v map[string]string) (any, error) {
		if v["name"] == "" {
			return nil, errors.New("indique el nombre del negocio")
		}
		if v["password"] != v["again"] {
			return nil, errors.New("las contraseñas no coinciden")
		}
		b, err := a.opts.Setup(a.ctx, SetupInput{BusinessName: v["name"], LegalName: v["legal"], RIF: v["rif"], Currency: v["currency"],
			AdminUser: v["user"], AdminName: v["display"], Password: v["password"]})
		if err != nil {
			return nil, err
		}
		s, err := b.Resume(a.ctx)
		if err != nil {
			return nil, err
		}
		return setupDone{b, s}, nil
	}
	f.done = func(v any) tea.Cmd {
		d := v.(setupDone)
		a.opts.Backend, a.opts.Setup = d.backend, nil
		return func() tea.Msg { return sessionMsg{s: d.session} }
	}
	return f
}

type setupDone struct {
	backend Backend
	session Session
}

// ---------- Confirmación ----------

// confirmScreen pide confirmar una acción: Enter la ejecuta, Esc vuelve.
type confirmScreen struct {
	app     *app
	id      int
	title   string
	message string
	run     func() (any, error)
	ok      string // mensaje al volver tras ejecutarla
	// result, si existe, arma el mensaje con el resultado.
	result func(v any) string
	busy   bool
	errMsg string
}

func (c *confirmScreen) Title() string { return c.title }
func (c *confirmScreen) Help() string  { return "Enter Confirmar  Esc Cancelar" }
func (c *confirmScreen) Info() string {
	return "Enter (o S) confirma. Esc (o N) vuelve sin cambiar nada."
}
func (c *confirmScreen) Capturing() bool { return true }

func (c *confirmScreen) Init(id int) tea.Cmd {
	c.id = id
	return nil
}

func (c *confirmScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case loaded:
		c.busy = false
		if msg.err != nil {
			c.errMsg = describe(msg.err)
			return nil
		}
		if c.result != nil {
			return back(c.result(msg.v))
		}
		return back(c.ok)
	case tea.KeyPressMsg:
		if c.busy {
			return nil
		}
		switch msg.String() {
		case "esc", "n":
			return back("cancelado")
		case "enter", "s":
			c.busy, c.errMsg = true, ""
			return load(c.id, c.run)
		}
	}
	return nil
}

func (c *confirmScreen) View(width, _ int) string {
	out := ansi.Wordwrap(c.message, width, " ") + "\n\n¿Confirma? Enter sí · Esc no"
	switch {
	case c.busy:
		out += "\n\nProcesando…"
	case c.errMsg != "":
		out += "\n\n" + failStyle.Render(c.errMsg)
	}
	return out
}

// ---------- Productos: formularios ----------

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		out = append(out, p)
	}
	return out
}

func newProductForm(a *app, units []catalog.Unit) screen {
	codes := make([]string, len(units))
	for i, u := range units {
		codes[i] = u.Code
	}
	return &formScreen{app: a, title: "Nuevo producto",
		info: "El SKU es el código interno; vacío se asigna automático (P-000001…). La unidad fija los decimales de las " +
			"cantidades y no se cambia después. Separe varios códigos de barras con comas.",
		fields: []*field{
			textF("name", "Nombre", "", 120),
			textF("sku", "SKU", "", 32).placeholder("vacío: automático"),
			choiceF("unit", "Unidad", codes, "UND"),
			textF("barcodes", "Códigos de barras", "", 200).placeholder("opcional, separados por comas"),
		},
		save: func(v map[string]string) (any, error) {
			return a.opts.Backend.CreateProduct(a.ctx, appcatalog.NewProduct{Name: v["name"], SKU: v["sku"], UnitCode: v["unit"],
				Barcodes: splitList(v["barcodes"])})
		},
		done: func(v any) tea.Cmd {
			p := v.(appcatalog.ProductView)
			return back("producto creado: " + p.SKU.String() + " " + p.Name.String())
		},
	}
}

func barcodes(p catalog.Product) string {
	out := make([]string, len(p.Barcodes))
	for i, b := range p.Barcodes {
		out[i] = b.String()
	}
	return strings.Join(out, ", ")
}

func newProductEdit(a *app, p appcatalog.ProductView) screen {
	current := barcodes(p.Product)
	if current == "" {
		current = "ninguno"
	}
	return &formScreen{app: a, title: "Editar " + p.SKU.String(),
		intro: fmt.Sprintf("Unidad: %s (no se cambia). Códigos de barras actuales: %s.", p.Unit.Code, current),
		info: "Cambie nombre o SKU, agregue o quite códigos de barras (separados por comas). Si otra persona editó el " +
			"producto mientras tanto, no se guarda: vuelva a abrirlo.",
		fields: []*field{
			textF("name", "Nombre", p.Name.String(), 120),
			textF("sku", "SKU", p.SKU.String(), 32),
			textF("add", "Agregar códigos", "", 200),
			textF("remove", "Quitar códigos", "", 200),
		},
		save: func(v map[string]string) (any, error) {
			c := appcatalog.Changes{AddBarcodes: splitList(v["add"]), RemoveBarcodes: splitList(v["remove"]), ExpectedRevision: p.Revision}
			if name := v["name"]; name != p.Name.String() {
				c.Name = &name
			}
			if sku := v["sku"]; sku != p.SKU.String() {
				c.SKU = &sku
			}
			return a.opts.Backend.UpdateProduct(a.ctx, p.ID.String(), c)
		},
		done: func(v any) tea.Cmd {
			return back("producto actualizado: " + v.(appcatalog.ProductView).SKU.String())
		},
	}
}

func newProductToggle(a *app, p appcatalog.ProductView) screen {
	active := p.Status != catalog.StatusActive
	verb, ok := "inactivar", "inactivado"
	note := "Un producto inactivo no se puede comprar ni vender; conserva su historial y sus existencias."
	if active {
		verb, ok, note = "activar", "activado", "Vuelve a estar disponible para compras y ventas."
	}
	return &confirmScreen{app: a, title: strings.ToUpper(verb[:1]) + verb[1:] + " " + p.SKU.String(),
		message: fmt.Sprintf("¿%s %s %s?\n%s", strings.ToUpper(verb[:1])+verb[1:], p.SKU, p.Name, note),
		ok:      "producto " + p.SKU.String() + " " + ok,
		run: func() (any, error) {
			return a.opts.Backend.UpdateProduct(a.ctx, p.ID.String(), appcatalog.Changes{Active: &active, ExpectedRevision: p.Revision})
		}}
}

// ---------- Unidades ----------

type unitsScreen struct {
	app     *app
	id      int
	list    listView
	items   []catalog.Unit
	loading bool
}

func newUnits(a *app) screen {
	return &unitsScreen{app: a, list: newList([]table.Column{{Title: "Código", Width: 10}, {Title: "Nombre", Width: 40}, {Title: "Decimales", Width: 9}}, 1)}
}

func (u *unitsScreen) Title() string   { return "Unidades de medida" }
func (u *unitsScreen) Capturing() bool { return u.list.capturing() }
func (u *unitsScreen) Info() string {
	return "Los decimales limitan las cantidades: 0 para unidades enteras, 3 para kilos con gramos."
}

func (u *unitsScreen) Help() string {
	if u.list.capturing() {
		return "Escriba para filtrar  Enter Fijar  Esc Borrar"
	}
	if u.canManage() {
		return "↑↓ Elegir  / Filtrar  F2 Nueva  Esc Volver"
	}
	return "↑↓ Elegir  / Filtrar  Esc Volver"
}

func (u *unitsScreen) canManage() bool {
	return u.app.session != nil && u.app.session.Can(identity.PermUnitManage)
}

func (u *unitsScreen) Init(id int) tea.Cmd {
	u.id = id
	return u.Refresh()
}

func (u *unitsScreen) Refresh() tea.Cmd {
	u.loading = true
	a := u.app
	return load(u.id, func() (any, error) { return a.opts.Backend.Units(a.ctx) })
}

func (u *unitsScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		u.list.resize(msg.Width, msg.Height)
	case loaded:
		u.loading = false
		if msg.err != nil {
			return setStatus("no se pudieron cargar las unidades", msg.err)
		}
		u.items = msg.v.([]catalog.Unit)
		rows := make([]table.Row, len(u.items))
		for i, x := range u.items {
			rows[i] = table.Row{x.Code, x.Name.String(), strconv.Itoa(int(x.Scale))}
		}
		u.list.setRows(rows)
	case tea.KeyPressMsg:
		if cmd, done := u.list.update(msg); done {
			return cmd
		}
		if msg.String() == "f2" && u.canManage() {
			return push(newUnitForm(u.app))
		}
	default:
		cmd, _ := u.list.update(msg)
		return cmd
	}
	return nil
}

func (u *unitsScreen) View(int, int) string {
	if u.loading && len(u.items) == 0 {
		return "Cargando…"
	}
	return u.list.view("No hay unidades.")
}

func newUnitForm(a *app) screen {
	return &formScreen{app: a, title: "Nueva unidad",
		info: "Código corto en mayúsculas (KG, LT, CAJA12). Los decimales no se cambian después.",
		fields: []*field{
			textF("code", "Código", "", 16),
			textF("name", "Nombre", "", 120),
			choiceF("scale", "Decimales", []string{"0", "1", "2", "3", "4", "5", "6"}, "0"),
		},
		save: func(v map[string]string) (any, error) {
			scale, _ := strconv.Atoi(v["scale"])
			return a.opts.Backend.CreateUnit(a.ctx, v["code"], v["name"], int32(scale))
		},
		done: func(v any) tea.Cmd { return back("unidad creada: " + v.(catalog.Unit).Code) },
	}
}

package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

var holdText = map[inventory.HoldStatus]string{
	inventory.HoldActive:     "activa",
	inventory.HoldConsumed:   "despachada",
	inventory.HoldReleased:   "liberada",
	inventory.HoldExpired:    "vencida",
	inventory.HoldWrittenOff: "dada de baja",
}

func (a *app) can(perm identity.Permission) bool { return a.session != nil && a.session.Can(perm) }

func when(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return timezone.InCaracas(t).Format("2006-01-02 15:04")
}

// parseWhen lee una fecha y hora de Caracas; vacío es sin fecha.
func parseWhen(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", s, timezone.Caracas())
	if err != nil {
		return time.Time{}, fmt.Errorf("vencimiento %q: use AAAA-MM-DD HH:MM (hora de Caracas)", s)
	}
	return t, nil
}

// ---------- Reservas ----------

func newReservations(a *app) screen {
	r := &recordsScreen[stock.ReservationView]{app: a, title: "Reservas", toggle: true, wide: true,
		info: "Una reserva aparta existencia para un pedido: baja el disponible sin sacarla del almacén. " +
			"Despachar la saca (total o parcial); liberar la devuelve al disponible.",
		empty: "No hay reservas.",
		list: newList([]table.Column{{Title: "Referencia", Width: 20}, {Title: "Almacén", Width: 12}, {Title: "SKU", Width: 16},
			{Title: "Producto", Width: 36}, {Title: "Cantidad", Width: 12}, {Title: "Despachado", Width: 12}, {Title: "Estado", Width: 12},
			{Title: "Vence", Width: 16}}, 3),
		fetch: func(all bool) ([]stock.ReservationView, error) {
			st := inventory.HoldActive
			if all {
				st = ""
			}
			return a.opts.Backend.Reservations(a.ctx, st)
		},
		row: func(v stock.ReservationView) table.Row {
			sc := v.Unit.Scale
			return table.Row{v.Reference, v.Location.Warehouse.Code.String(), v.Product.SKU.String(), v.Product.Name.String(),
				v.Quantity.StringFixed(sc), v.Consumed.StringFixed(sc), holdText[v.Status], when(v.ExpiresAt)}
		}}
	manage := func(*stock.ReservationView) bool { return a.can(identity.PermReservation) }
	active := func(v *stock.ReservationView) bool { return manage(v) && v.Status == inventory.HoldActive }
	r.actions = []action[stock.ReservationView]{
		{key: "f2", label: "F2 Nueva", ok: manage, run: func(*stock.ReservationView) tea.Cmd { return push(newReserve(a)) }},
		{key: "f3", label: "F3 Despachar", needsItem: true, ok: active, run: func(v *stock.ReservationView) tea.Cmd { return push(newConsume(a, *v)) }},
		{key: "f4", label: "F4 Liberar", needsItem: true, ok: active, run: func(v *stock.ReservationView) tea.Cmd { return push(newRelease(a, *v)) }},
	}
	return r
}

func newReserve(a *app) screen {
	f := &formScreen{app: a, title: "Nueva reserva",
		info: "Nunca reserva más que el disponible. La referencia identifica el pedido. El vencimiento es opcional: " +
			"al cumplirse, la reserva se libera sola.",
		fields: []*field{warehouseF("warehouse", "Almacén"), productF("product", "Producto"), textF("qty", "Cantidad", "", 32),
			textF("ref", "Referencia", "", 100).placeholder("p. ej. PED-1024"),
			textF("expires", "Vence", "", 16).placeholder("opcional: AAAA-MM-DD HH:MM"), textF("note", "Nota", "", 500)},
		key: newKey()}
	f.save = func(v map[string]string) (any, error) {
		exp, err := parseWhen(v["expires"])
		if err != nil {
			return nil, err
		}
		return a.opts.Backend.Reserve(a.ctx, stock.ReserveInput{Where: stock.WarehouseRef{Warehouse: v["warehouse"]}, Product: v["product"],
			Quantity: v["qty"], Reference: v["ref"], ExpiresAt: exp, Note: v["note"], IdempotencyKey: f.key})
	}
	f.done = func(v any) tea.Cmd {
		r := v.(stock.ReservationView)
		return back(fmt.Sprintf("reservados %s %s de %s para %s", r.Quantity.StringFixed(r.Unit.Scale), r.Unit.Code, r.Product.SKU, r.Reference))
	}
	return withWarehouses(a, f)
}

func newConsume(a *app, r stock.ReservationView) screen {
	sc := r.Unit.Scale
	pending := r.Quantity.Sub(r.Consumed)
	f := &formScreen{app: a, title: "Despachar " + r.Reference,
		intro: fmt.Sprintf("%s %s · pendiente %s %s en %s.", r.Product.SKU, r.Product.Name, pending.StringFixed(sc), r.Unit.Code, r.Location),
		info:  "Saca la mercancía reservada al costo promedio vigente. Vacío despacha todo lo pendiente; menos deja el resto reservado.",
		fields: []*field{textF("qty", "Cantidad", "", 32).placeholder("vacío: todo lo pendiente"),
			textF("note", "Nota", "", 500)},
		key: newKey()}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.ConsumeReservation(a.ctx, r.ID.String(), v["qty"], v["note"], f.key)
	}
	f.done = func(v any) tea.Cmd {
		x := v.(stock.ReservationView)
		return back(fmt.Sprintf("despachado %s: %s de %s %s", x.Reference, x.Consumed.StringFixed(sc), x.Quantity.StringFixed(sc), x.Unit.Code))
	}
	return f
}

func newRelease(a *app, r stock.ReservationView) screen {
	f := &formScreen{app: a, title: "Liberar " + r.Reference,
		intro: fmt.Sprintf("%s %s · %s %s vuelven al disponible.", r.Product.SKU, r.Product.Name,
			r.Quantity.Sub(r.Consumed).StringFixed(r.Unit.Scale), r.Unit.Code),
		info:   "Liberar no mueve existencia: solo deja de apartarla.",
		fields: []*field{textF("note", "Motivo", "", 500)}}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.ReleaseReservation(a.ctx, r.ID.String(), v["note"])
	}
	f.done = func(any) tea.Cmd { return back("reserva " + r.Reference + " liberada") }
	return f
}

// ---------- Bloqueos ----------

func blockReasonText(r inventory.BlockReason) string {
	for _, i := range inventory.BlockReasons() {
		if i.Reason == r {
			return i.Description
		}
	}
	return string(r)
}

func newBlocks(a *app) screen {
	r := &recordsScreen[stock.BlockView]{app: a, title: "Bloqueos", toggle: true, wide: true,
		info: "Un bloqueo retiene mercancía no vendible (dañada, en cuarentena…): baja el disponible sin sacarla. " +
			"Liberar la devuelve; dar de baja la saca como merma al costo promedio.",
		empty: "No hay bloqueos.",
		list: newList([]table.Column{{Title: "Almacén", Width: 12}, {Title: "SKU", Width: 16}, {Title: "Producto", Width: 36},
			{Title: "Cantidad", Width: 12}, {Title: "Motivo", Width: 24}, {Title: "Estado", Width: 12}, {Title: "Desde", Width: 16},
			{Title: "Nota", Width: 30}}, 2),
		fetch: func(all bool) ([]stock.BlockView, error) {
			st := inventory.HoldActive
			if all {
				st = ""
			}
			return a.opts.Backend.Blocks(a.ctx, st)
		},
		row: func(v stock.BlockView) table.Row {
			return table.Row{v.Location.Warehouse.Code.String(), v.Product.SKU.String(), v.Product.Name.String(),
				v.Quantity.StringFixed(v.Unit.Scale), blockReasonText(v.Reason), holdText[v.Status], when(v.CreatedAt), v.Note}
		}}
	manage := func(*stock.BlockView) bool { return a.can(identity.PermBlock) }
	active := func(v *stock.BlockView) bool { return manage(v) && v.Status == inventory.HoldActive }
	r.actions = []action[stock.BlockView]{
		{key: "f2", label: "F2 Nuevo", ok: manage, run: func(*stock.BlockView) tea.Cmd { return push(newBlock(a)) }},
		{key: "f3", label: "F3 Liberar", needsItem: true, ok: active, run: func(v *stock.BlockView) tea.Cmd { return push(newCloseBlock(a, *v, false)) }},
		{key: "f4", label: "F4 Dar de baja", needsItem: true, ok: active, run: func(v *stock.BlockView) tea.Cmd { return push(newCloseBlock(a, *v, true)) }},
	}
	return r
}

func newBlock(a *app) screen {
	var codes, labels []string
	for _, i := range inventory.BlockReasons() {
		codes, labels = append(codes, string(i.Reason)), append(labels, i.Description)
	}
	f := &formScreen{app: a, title: "Nuevo bloqueo",
		info: "Retiene unidades del disponible sin sacarlas: la existencia y el costo promedio no cambian.",
		fields: []*field{warehouseF("warehouse", "Almacén"), productF("product", "Producto"), textF("qty", "Cantidad", "", 32),
			choiceF("reason", "Motivo", codes, "").labeled(labels...), textF("note", "Nota", "", 500)},
		key: newKey()}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.Block(a.ctx, stock.BlockInput{Where: stock.WarehouseRef{Warehouse: v["warehouse"]}, Product: v["product"],
			Quantity: v["qty"], Reason: v["reason"], Note: v["note"], IdempotencyKey: f.key})
	}
	f.done = func(v any) tea.Cmd {
		b := v.(stock.BlockView)
		return back(fmt.Sprintf("bloqueados %s %s de %s", b.Quantity.StringFixed(b.Unit.Scale), b.Unit.Code, b.Product.SKU))
	}
	return withWarehouses(a, f)
}

func newCloseBlock(a *app, b stock.BlockView, writeOff bool) screen {
	qty := b.Quantity.StringFixed(b.Unit.Scale) + " " + b.Unit.Code + " de " + b.Product.SKU.String()
	title, intro, ok := "Liberar bloqueo", qty+" vuelven al disponible.", "bloqueo liberado: "+qty
	if writeOff {
		title, intro, ok = "Dar de baja", qty+" salen del almacén como merma, al costo promedio vigente. No se deshace.", "dados de baja "+qty
	}
	f := &formScreen{app: a, title: title, intro: intro,
		info:   "La nota queda en el historial del bloqueo y, si se da de baja, en el kardex.",
		fields: []*field{textF("note", "Nota", "", 500)}}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.CloseBlock(a.ctx, b.ID.String(), writeOff, v["note"])
	}
	f.done = func(any) tea.Cmd { return back(ok) }
	return f
}

// ---------- Conteos físicos ----------

var countText = map[inventory.CountStatus]string{
	inventory.CountDraft:    "borrador",
	inventory.CountInReview: "en revisión",
	inventory.CountApplied:  "aplicado",
	inventory.CountRejected: "rechazado",
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[len(s)-8:]
	}
	return s
}

func newCounts(a *app) screen {
	r := &recordsScreen[stock.CountView]{app: a, title: "Conteos físicos", wide: true,
		info: "Un conteo compara lo contado con lo registrado. Se captura en borrador, se envía a revisión y otra persona " +
			"lo aprueba (aplica los ajustes) o lo rechaza.",
		empty: "No hay conteos.",
		list: newList([]table.Column{{Title: "Conteo", Width: 8}, {Title: "Almacén", Width: 24}, {Title: "Estado", Width: 12},
			{Title: "Líneas", Width: 6}, {Title: "Creado", Width: 16}, {Title: "Nota", Width: 40}}, 5),
		fetch: func(bool) ([]stock.CountView, error) { return a.opts.Backend.Counts(a.ctx, "") },
		row: func(v stock.CountView) table.Row {
			return table.Row{shortID(v.ID.String()), v.Location.Branch.Code.String() + "/" + v.Location.Warehouse.Code.String(),
				countText[v.Status], itoa(len(v.Lines)), when(v.CreatedAt), v.Note}
		}}
	r.actions = []action[stock.CountView]{
		{key: "f2", label: "F2 Nuevo", ok: func(*stock.CountView) bool { return a.can(identity.PermCountManage) },
			run: func(*stock.CountView) tea.Cmd { return push(newCountForm(a)) }},
		{key: "enter", label: "Enter Abrir", needsItem: true, run: func(v *stock.CountView) tea.Cmd { return push(newCount(a, v.ID.String())) }},
	}
	return r
}

func newCountForm(a *app) screen {
	f := &formScreen{app: a, title: "Nuevo conteo",
		info:   "Crea el conteo en borrador y abre la captura: escanee o escriba cada producto con la cantidad contada.",
		fields: []*field{warehouseF("warehouse", "Almacén"), textF("note", "Nota", "", 500).placeholder("p. ej. conteo de pasillo 3")}}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.CreateCount(a.ctx, v["warehouse"], v["note"])
	}
	f.done = func(v any) tea.Cmd {
		c := v.(stock.CountView)
		return replace(newCount(a, c.ID.String()), "conteo creado en "+c.Location.String())
	}
	return withWarehouses(a, f)
}

// countScreen muestra un conteo. En borrador, con permiso, es una captura
// rápida: producto (o código de barras), Enter, cantidad, Enter.
type countScreen struct {
	app     *app
	id      int
	ref     string
	v       *stock.CountView
	list    listView
	inputs  [3]textinput.Model // producto, contado, costo
	focus   int
	found   string
	looked  string
	busy    bool
	msg     string
	msgErr  bool
	loading bool
}

func newCount(a *app, ref string) screen {
	c := &countScreen{app: a, ref: ref, list: newList([]table.Column{{Title: "SKU", Width: 16}, {Title: "Producto", Width: 36},
		{Title: "Registrado", Width: 12}, {Title: "Contado", Width: 12}, {Title: "Diferencia", Width: 12}, {Title: "Unidad", Width: 6}}, 1)}
	c.inputs[0] = newInput("Producto: ", 200)
	c.inputs[0].Placeholder = "escanee o escriba SKU / código"
	c.inputs[0].SetWidth(32)
	c.inputs[1] = newInput("Contado: ", 32)
	c.inputs[1].SetWidth(12)
	c.inputs[2] = newInput("Costo: ", 32)
	c.inputs[2].Placeholder = "opcional"
	c.inputs[2].SetWidth(12)
	return c
}

func (c *countScreen) Title() string {
	if c.v == nil {
		return "Conteo"
	}
	return "Conteo " + shortID(c.v.ID.String()) + " · " + c.v.Location.String() + " · " + countText[c.v.Status]
}
func (c *countScreen) Wide() bool { return true }

func (c *countScreen) capturing() bool {
	return c.v != nil && c.v.Status == inventory.CountDraft && c.app.can(identity.PermCountManage)
}
func (c *countScreen) deciding() bool {
	return c.v != nil && c.v.Status == inventory.CountInReview && c.app.can(identity.PermCountApprove)
}
func (c *countScreen) Capturing() bool { return c.capturing() }

func (c *countScreen) Help() string {
	switch {
	case c.capturing():
		return "Enter Siguiente/Registrar  Tab Costo  F9 Enviar a revisión  Esc Volver"
	case c.deciding():
		return "↑↓ Elegir  A Aprobar  X Rechazar  R Recargar  Esc Volver"
	}
	return "↑↓ Elegir  R Recargar  Esc Volver"
}

func (c *countScreen) Init(id int) tea.Cmd {
	c.id = id
	return tea.Batch(c.inputs[0].Focus(), c.Refresh())
}

func (c *countScreen) Refresh() tea.Cmd {
	c.loading = true
	a, ref := c.app, c.ref
	return load(c.id, func() (any, error) { return a.opts.Backend.Count(a.ctx, ref) })
}

func (c *countScreen) setFocus(i int) tea.Cmd {
	for j := range c.inputs {
		c.inputs[j].Blur()
	}
	c.focus = i
	return c.inputs[i].Focus()
}

func (c *countScreen) record() tea.Cmd {
	product, counted, cost := strings.TrimSpace(c.inputs[0].Value()), strings.TrimSpace(c.inputs[1].Value()), strings.TrimSpace(c.inputs[2].Value())
	if product == "" || counted == "" {
		c.msg, c.msgErr = "indique producto y cantidad contada", true
		return nil
	}
	c.busy = true
	a, ref := c.app, c.ref
	return load(c.id, func() (any, error) { return a.opts.Backend.RecordCount(a.ctx, ref, product, counted, cost) })
}

func (c *countScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		c.list.resize(msg.Width, msg.Height-4)
	case loaded:
		switch v := msg.v.(type) {
		case productInfo:
			if v.query == c.looked {
				c.found = v.text
			}
			return nil
		case stock.CountLineView:
			c.busy = false
			if msg.err != nil {
				c.msg, c.msgErr = describe(msg.err), true
				return nil
			}
			sc := v.Unit.Scale
			c.msg, c.msgErr = fmt.Sprintf("%s contado %s (registrado %s, diferencia %s)", v.Product.SKU, v.Counted.StringFixed(sc),
				v.Expected.StringFixed(sc), signed(v.Difference().StringFixed(sc))), false
			for i := range c.inputs {
				c.inputs[i].SetValue("")
			}
			c.found, c.looked = "", ""
			return tea.Batch(c.setFocus(0), c.Refresh())
		case stock.CountView:
			c.loading = false
			c.v = &v
			sc := func(l stock.CountLineView) int32 { return l.Unit.Scale }
			rows := make([]table.Row, len(v.Lines))
			for i, l := range v.Lines {
				rows[i] = table.Row{l.Product.SKU.String(), l.Product.Name.String(), l.Expected.StringFixed(sc(l)), l.Counted.StringFixed(sc(l)),
					signed(l.Difference().StringFixed(sc(l))), l.Unit.Code}
			}
			c.list.setRows(rows)
			return nil
		}
		c.loading, c.busy = false, false
		if msg.err != nil {
			c.msg, c.msgErr = describe(msg.err), true
		}
	case tea.KeyPressMsg:
		if c.busy {
			return nil
		}
		k := msg.String()
		if c.capturing() {
			switch k {
			case "esc":
				return back("")
			case "f9":
				return push(newSubmitCount(c.app, *c.v))
			case "tab", "shift+tab":
				if c.focus == 1 && k == "tab" {
					return c.setFocus(2)
				}
				return c.setFocus((c.focus + 1) % 3)
			case "enter":
				if c.focus == 0 {
					q := strings.TrimSpace(c.inputs[0].Value())
					if q == "" {
						return nil
					}
					var cmd tea.Cmd
					if q != c.looked {
						c.looked, c.found = q, ""
						cmd = lookupProduct(c.app, c.id, "product", q, c.v.Location.Warehouse.Code.String())
					}
					return tea.Batch(cmd, c.setFocus(1))
				}
				return c.record()
			}
			var cmd tea.Cmd
			c.inputs[c.focus], cmd = c.inputs[c.focus].Update(msg)
			return cmd
		}
		if cmd, done := c.list.update(msg); done {
			return cmd
		}
		switch k {
		case "r":
			return c.Refresh()
		case "a":
			if c.deciding() {
				return push(newDecideCount(c.app, *c.v, true))
			}
		case "x":
			if c.deciding() {
				return push(newDecideCount(c.app, *c.v, false))
			}
		}
	default:
		cmd, _ := c.list.update(msg)
		return cmd
	}
	return nil
}

func signed(s string) string {
	if !strings.HasPrefix(s, "-") && strings.Trim(s, "0.") != "" {
		return "+" + s
	}
	return s
}

func (c *countScreen) View(int, int) string {
	if c.v == nil {
		return "Cargando…"
	}
	var b strings.Builder
	if c.v.Note != "" {
		b.WriteString(hintStyle.Render(c.v.Note) + "\n")
	}
	if c.capturing() {
		b.WriteString(c.inputs[0].View() + "  " + c.inputs[1].View() + "  " + c.inputs[2].View() + "\n")
		if c.found != "" {
			b.WriteString("  " + okStyle.Render("→ "+c.found))
		}
		b.WriteString("\n")
	}
	switch {
	case c.busy:
		b.WriteString("Guardando…\n")
	case c.msg != "" && c.msgErr:
		b.WriteString(failStyle.Render(c.msg) + "\n")
	case c.msg != "":
		b.WriteString(okStyle.Render(c.msg) + "\n")
	}
	if c.v.DecisionNote != "" {
		b.WriteString("Decisión: " + c.v.DecisionNote + "\n")
	}
	b.WriteString(c.list.view("Sin líneas: escanee o escriba el primer producto."))
	return b.String()
}

func newSubmitCount(a *app, v stock.CountView) screen {
	return &confirmScreen{app: a, title: "Enviar a revisión",
		message: fmt.Sprintf("Enviar el conteo de %s con %d líneas a revisión. Después ya no se capturan líneas; "+
			"lo aprueba o rechaza otra persona.", v.Location, len(v.Lines)),
		ok:  "conteo enviado a revisión",
		run: func() (any, error) { return a.opts.Backend.SubmitCount(a.ctx, v.ID.String()) }}
}

func newDecideCount(a *app, v stock.CountView, approve bool) screen {
	title, intro, ok := "Rechazar conteo", "Rechazar no cambia la existencia.", "conteo rechazado"
	if approve {
		title, intro, ok = "Aprobar conteo", "Aprobar aplica las diferencias como ajustes sobre la existencia actual.", "conteo aprobado; diferencias aplicadas"
	}
	f := &formScreen{app: a, title: title, intro: intro,
		info:   "Quien capturó o envió el conteo no puede decidirlo.",
		fields: []*field{textF("note", "Nota", "", 500)}}
	f.save = func(m map[string]string) (any, error) {
		return a.opts.Backend.DecideCount(a.ctx, v.ID.String(), approve, m["note"])
	}
	f.done = func(any) tea.Cmd { return back(ok) }
	return f
}

// ---------- Política de existencia negativa ----------

type policyScreen struct {
	app     *app
	id      int
	v       *stock.PolicyView
	list    listView
	loading bool
}

var modeText = map[inventory.NegativeMode]string{
	"":                        "hereda",
	inventory.NegativeInherit: "hereda",
	inventory.NegativeDeny:    "prohíbe",
	inventory.NegativeAllow:   "permite",
}

func newPolicy(a *app) screen {
	return &policyScreen{app: a, list: newList([]table.Column{{Title: "SKU", Width: 16}, {Title: "Producto", Width: 36},
		{Title: "Modo", Width: 8}, {Title: "Límite", Width: 12}, {Title: "Efecto", Width: 24}}, 1)}
}

func (p *policyScreen) Title() string   { return "Política de existencia negativa" }
func (p *policyScreen) Capturing() bool { return p.list.capturing() }
func (p *policyScreen) Info() string {
	return "Por defecto ninguna salida deja el disponible en negativo. La empresa puede habilitarlo con un límite " +
		"por producto y almacén; cada producto hereda, lo prohíbe o fija su propio límite. Aun así, cada salida en " +
		"negativo exige el permiso extraordinario."
}
func (p *policyScreen) Help() string {
	if p.app.can(identity.PermPolicyManage) {
		return "↑↓ Elegir  F2 Empresa  F3 Producto  R Recargar  Esc Volver"
	}
	return "↑↓ Elegir  R Recargar  Esc Volver"
}

func (p *policyScreen) Init(id int) tea.Cmd {
	p.id = id
	return p.Refresh()
}

func (p *policyScreen) Refresh() tea.Cmd {
	p.loading = true
	a := p.app
	return load(p.id, func() (any, error) { return a.opts.Backend.Policy(a.ctx) })
}

func (p *policyScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.list.resize(msg.Width, msg.Height-3)
	case loaded:
		p.loading = false
		if msg.err != nil {
			return setStatus("no se pudo cargar la política", msg.err)
		}
		v := msg.v.(stock.PolicyView)
		p.v = &v
		rows := make([]table.Row, len(v.Products))
		for i, pp := range v.Products {
			limit, effect := "—", "sin negativo"
			if pp.PolicyChange.Product.Mode == inventory.NegativeAllow {
				limit = pp.PolicyChange.Product.Limit.StringFixed(pp.Unit.Scale)
			}
			if pp.Allowed {
				effect = "hasta " + pp.Limit.StringFixed(pp.Unit.Scale) + " " + pp.Unit.Code
			}
			rows[i] = table.Row{pp.Product.SKU.String(), pp.Product.Name.String(), modeText[pp.PolicyChange.Product.Mode], limit, effect}
		}
		p.list.setRows(rows)
	case tea.KeyPressMsg:
		if cmd, done := p.list.update(msg); done {
			return cmd
		}
		switch msg.String() {
		case "r":
			return p.Refresh()
		case "f2":
			if p.app.can(identity.PermPolicyManage) && p.v != nil {
				return push(newCompanyPolicy(p.app, p.v.Current.Company))
			}
		case "f3":
			if p.app.can(identity.PermPolicyManage) {
				return push(newProductPolicy(p.app))
			}
		}
	default:
		cmd, _ := p.list.update(msg)
		return cmd
	}
	return nil
}

func (p *policyScreen) View(int, int) string {
	if p.v == nil {
		return "Cargando…"
	}
	c := p.v.Current.Company
	head := "Empresa " + p.v.Company.Code.String() + ": existencia negativa "
	if c.NegativeEnabled {
		head += okStyle.Render("habilitada") + ", hasta " + c.Limit.String() + " por producto y almacén"
	} else {
		head += warnStyle.Render("deshabilitada")
	}
	return head + "\n\nProductos con política propia:" + p.list.view("Ninguno: todos siguen a la empresa.")
}

func newCompanyPolicy(a *app, cur inventory.CompanyPolicy) screen {
	state, limit := "no", ""
	if cur.NegativeEnabled {
		state, limit = "si", cur.Limit.String()
	}
	f := &formScreen{app: a, title: "Política de la empresa",
		info: "Habilitada exige un límite: el déficit máximo por producto y almacén, en la unidad de cada producto. " +
			"Deshabilitarla deja el límite vacío. Cada cambio queda versionado.",
		fields: []*field{choiceF("enabled", "Existencia negativa", []string{"no", "si"}, state).labeled("Deshabilitada", "Habilitada"),
			textF("limit", "Límite", limit, 32)}}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.SetCompanyPolicy(a.ctx, v["enabled"] == "si", v["limit"])
	}
	f.done = func(any) tea.Cmd { return back("política de la empresa actualizada") }
	return f
}

func newProductPolicy(a *app) screen {
	f := &formScreen{app: a, title: "Política de un producto",
		info: "Hereda sigue a la empresa; prohíbe nunca lo deja en negativo; permite fija un límite propio (solo si la " +
			"empresa lo habilitó). El límite usa los decimales de la unidad.",
		fields: []*field{productF("product", "Producto"),
			choiceF("mode", "Modo", []string{"inherit", "deny", "allow"}, "inherit").labeled("Hereda de la empresa", "Prohíbe", "Permite con límite"),
			textF("limit", "Límite", "", 32).placeholder("solo con Permite")}}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.SetProductPolicy(a.ctx, v["product"], v["mode"], v["limit"])
	}
	f.done = func(any) tea.Cmd { return back("política del producto actualizada") }
	return f
}

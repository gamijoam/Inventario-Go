package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/pricing"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

var proposalText = map[pricing.ProposalStatus]string{pricing.ProposalPending: "pendiente", pricing.ProposalApproved: "aprobada",
	pricing.ProposalRejected: "rechazada", pricing.ProposalSuperseded: "sustituida"}

var basisText = map[pricing.CostBasis]string{pricing.BasisReplacement: "costo de reposición", pricing.BasisLastPurchase: "último costo de compra"}

// change describe "10 → 12 USD" o "— → 12 USD".
func change(current *decimal.Decimal, price decimal.Decimal, currency string) string {
	from := "—"
	if current != nil {
		from = current.String()
	}
	return fmt.Sprintf("%s → %s %s", from, price, currency)
}

func vet(p interface{ Format(string) string }) string { return p.Format("2006-01-02 15:04") }

// ---------- Precios ----------

type pricesScreen struct {
	app     *app
	id      int
	product string
	list    listView
	items   []apppricing.PriceView
	loading bool
}

func newPrices(a *app) screen { return newPricesOf(a, "") }

func newPricesOf(a *app, product string) screen {
	return &pricesScreen{app: a, product: product, list: newList([]table.Column{
		{Title: "Lista", Width: 10}, {Title: "SKU", Width: 12}, {Title: "Producto", Width: 26}, {Title: "Precio", Width: 10},
		{Title: "Moneda", Width: 6}, {Title: "Programado", Width: 26}, {Title: "Propuesta", Width: 20}, {Title: "Margen obj.", Width: 11},
	}, 2)}
}

func (p *pricesScreen) Title() string {
	if p.product != "" {
		return "Precios de " + p.product
	}
	return "Precios"
}
func (p *pricesScreen) Capturing() bool { return p.list.capturing() }
func (p *pricesScreen) Help() string {
	if p.list.capturing() {
		return "Escriba para filtrar  Enter Fijar  Esc Borrar"
	}
	return "↑↓ Elegir  / Filtrar  Enter Propuesta  R Recargar  Esc Volver"
}

func (p *pricesScreen) Init(id int) tea.Cmd {
	p.id = id
	return p.Refresh()
}

func (p *pricesScreen) Refresh() tea.Cmd {
	p.loading = true
	a, product := p.app, p.product
	return load(p.id, func() (any, error) { return a.opts.Backend.Prices(a.ctx, product) })
}

func (p *pricesScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.list.resize(msg.Width, msg.Height)
	case loaded:
		p.loading = false
		if msg.err != nil {
			return setStatus("no se pudieron cargar los precios", msg.err)
		}
		p.items = msg.v.([]apppricing.PriceView)
		rows := make([]table.Row, len(p.items))
		for i, v := range p.items {
			price, scheduled, pending, margin := "—", "—", "—", "—"
			if v.Current != nil {
				price = v.Current.Price.String()
			}
			if len(v.Scheduled) > 0 {
				scheduled = fmt.Sprintf("%s desde %s", v.Scheduled[0].Price, vet(timezone.InCaracas(v.Scheduled[0].EffectiveAt)))
			}
			if v.Pending != nil {
				pending = fmt.Sprintf("%s (%s)", v.Pending.Price, v.Pending.Number)
			}
			if v.Margin != nil {
				margin = v.Margin.String() + " %"
			}
			rows[i] = table.Row{v.List.Code.String(), v.Product.SKU.String(), v.Product.Name.String(), price, v.List.Currency.Code(), scheduled, pending, margin}
		}
		p.list.setRows(rows)
	case tea.KeyPressMsg:
		if cmd, done := p.list.update(msg); done {
			return cmd
		}
		switch msg.String() {
		case "r":
			return p.Refresh()
		case "enter":
			if i := p.list.selected(); i >= 0 {
				if pend := p.items[i].Pending; pend != nil {
					return push(newProposal(p.app, pend.Number))
				}
				return setStatus("sin propuesta pendiente para este producto en esta lista", nil)
			}
		}
	default:
		cmd, _ := p.list.update(msg)
		return cmd
	}
	return nil
}

func (p *pricesScreen) View(int, int) string {
	if p.loading && len(p.items) == 0 {
		return "Cargando…"
	}
	return p.list.view("Sin precios. Cree una lista con 'balanzapro price-list create' y proponga precios.")
}

// ---------- Propuestas ----------

type proposalsScreen struct {
	app     *app
	id      int
	all     bool // false: solo pendientes
	list    listView
	items   []apppricing.ProposalView
	loading bool
}

func newProposals(a *app) screen {
	return &proposalsScreen{app: a, list: newList([]table.Column{
		{Title: "Número", Width: 10}, {Title: "Lista", Width: 10}, {Title: "SKU", Width: 12}, {Title: "Cambio", Width: 24},
		{Title: "Origen", Width: 9}, {Title: "Estado", Width: 10},
	}, 3)}
}

func (p *proposalsScreen) Title() string {
	if p.all {
		return "Propuestas de precio (todas)"
	}
	return "Propuestas de precio pendientes"
}
func (p *proposalsScreen) Capturing() bool { return p.list.capturing() }
func (p *proposalsScreen) Help() string {
	if p.list.capturing() {
		return "Escriba para filtrar  Enter Fijar  Esc Borrar"
	}
	return "↑↓ Elegir  / Filtrar  Enter Abrir  T Pendientes/Todas  R Recargar  Esc Volver"
}

func (p *proposalsScreen) Init(id int) tea.Cmd {
	p.id = id
	return p.Refresh()
}

func (p *proposalsScreen) Refresh() tea.Cmd {
	p.loading = true
	a, status := p.app, pricing.ProposalPending
	if p.all {
		status = ""
	}
	return load(p.id, func() (any, error) { return a.opts.Backend.Proposals(a.ctx, status) })
}

func (p *proposalsScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.list.resize(msg.Width, msg.Height)
	case loaded:
		p.loading = false
		if msg.err != nil {
			return setStatus("no se pudieron cargar las propuestas", msg.err)
		}
		p.items = msg.v.([]apppricing.ProposalView)
		rows := make([]table.Row, len(p.items))
		for i, v := range p.items {
			origin := "manual"
			if v.Kind == pricing.KindSuggested {
				origin = "recepción"
			}
			rows[i] = table.Row{v.Number, v.List.Code.String(), v.Product.SKU.String(), change(v.Current, v.Price, v.List.Currency.Code()), origin, proposalText[v.Status]}
		}
		p.list.setRows(rows)
	case tea.KeyPressMsg:
		if cmd, done := p.list.update(msg); done {
			return cmd
		}
		switch msg.String() {
		case "r":
			return p.Refresh()
		case "t":
			p.all = !p.all
			return p.Refresh()
		case "enter":
			if i := p.list.selected(); i >= 0 {
				return push(newProposal(p.app, p.items[i].Number))
			}
		}
	default:
		cmd, _ := p.list.update(msg)
		return cmd
	}
	return nil
}

func (p *proposalsScreen) View(int, int) string {
	if p.loading && len(p.items) == 0 {
		return "Cargando…"
	}
	empty := "No hay propuestas pendientes."
	if p.all {
		empty = "No hay propuestas."
	}
	return p.list.view(empty)
}

// ---------- Una propuesta ----------

type proposalScreen struct {
	app     *app
	id      int
	number  string
	v       *apppricing.ProposalView
	loading bool
}

func newProposal(a *app, number string) screen { return &proposalScreen{app: a, number: number} }

func (p *proposalScreen) Title() string   { return "Propuesta " + p.number }
func (p *proposalScreen) Capturing() bool { return false }

func (p *proposalScreen) canDecide() bool {
	return p.v != nil && p.v.Status == pricing.ProposalPending && p.app.session != nil && p.app.session.Can(identity.PermPriceApprove)
}

func (p *proposalScreen) Help() string {
	if p.canDecide() {
		return "A Aprobar  X Rechazar  R Recargar  Esc Volver"
	}
	return "R Recargar  Esc Volver"
}

func (p *proposalScreen) Init(id int) tea.Cmd {
	p.id = id
	return p.Refresh()
}

func (p *proposalScreen) Refresh() tea.Cmd {
	p.loading = true
	a, n := p.app, p.number
	return load(p.id, func() (any, error) { return a.opts.Backend.Proposal(a.ctx, n) })
}

func (p *proposalScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case loaded:
		p.loading = false
		if msg.err != nil {
			return setStatus("no se pudo cargar la propuesta", msg.err)
		}
		v := msg.v.(apppricing.ProposalView)
		p.v = &v
	case tea.KeyPressMsg:
		switch msg.String() {
		case "r":
			return p.Refresh()
		case "a":
			if p.canDecide() {
				return push(newApprove(p.app, *p.v))
			}
		case "x":
			if p.canDecide() {
				return push(newReject(p.app, *p.v))
			}
		}
	}
	return nil
}

func (p *proposalScreen) View(int, int) string {
	if p.v == nil {
		return "Cargando…"
	}
	return describeProposal(*p.v)
}

// describeProposal resume una propuesta: cambio, referencia de costo,
// margen y, si se publicó, la revisión.
func describeProposal(v apppricing.ProposalView) string {
	cur := v.List.Currency.Code()
	field := func(label, value string) string { return labelStyle.Render(fmt.Sprintf("%-16s", label)) + value + "\n" }
	var b strings.Builder
	origin := "manual"
	if v.Kind == pricing.KindSuggested {
		origin = "sugerida al recibir mercancía"
	}
	b.WriteString(field("Producto", v.Product.SKU.String()+" "+v.Product.Name.String()))
	b.WriteString(field("Lista", v.List.Code.String()+" "+v.List.Name))
	b.WriteString(field("Cambio", change(v.Current, v.Price, cur)))
	b.WriteString(field("Estado", proposalText[v.Status]))
	b.WriteString(field("Origen", origin))
	if ref := v.Reference; ref != nil {
		line := fmt.Sprintf("%s %s %s", basisText[ref.Basis], ref.Cost, ref.Currency)
		if v.Rate != nil {
			line += fmt.Sprintf(" = %s %s (%s)", ref.ListCost, cur, v.Rate)
		}
		b.WriteString(field("Costo ref.", line))
		if m, ok := pricing.MarginOf(v.Price, ref.ListCost); ok {
			b.WriteString(field("Margen", m.String()+" %"))
		}
		if v.BelowCost(v.Price) {
			b.WriteString(failStyle.Render("Bajo el costo de referencia: aprobarla exige un permiso extraordinario.") + "\n")
		}
	} else {
		b.WriteString(field("Costo ref.", "sin costo de referencia"))
	}
	if v.Margin != nil {
		b.WriteString(field("Margen objetivo", v.Margin.String()+" %"))
	}
	if v.Note != "" {
		b.WriteString(field("Nota", v.Note))
	}
	b.WriteString(field("Propuesta", vet(timezone.InCaracas(v.CreatedAt))))
	if v.Revision != nil {
		when := "rige desde " + vet(timezone.InCaracas(v.Revision.EffectiveAt))
		b.WriteString(field("Publicada", fmt.Sprintf("revisión %d, %s", v.Revision.Version, when)))
	}
	if v.DecisionNote != "" {
		b.WriteString(field("Motivo", v.DecisionNote))
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---------- Aprobar ----------

type approveScreen struct {
	app    *app
	id     int
	v      apppricing.ProposalView
	price  textinput.Model
	at     textinput.Model
	focus  int
	busy   bool
	errMsg string
}

func newApprove(a *app, v apppricing.ProposalView) screen {
	price := newInput("Precio ("+v.List.Currency.Code()+"):  ", 24)
	price.SetValue(v.Price.String())
	at := newInput("Vigencia:      ", 16)
	at.Placeholder = "vacío = ya; o AAAA-MM-DD HH:MM (Caracas)"
	return &approveScreen{app: a, v: v, price: price, at: at}
}

func (s *approveScreen) Title() string { return "Aprobar " + s.v.Number }
func (s *approveScreen) Help() string {
	return "Tab Campo  Enter Confirmar  Esc Cancelar"
}
func (s *approveScreen) Capturing() bool { return true }

func (s *approveScreen) Init(id int) tea.Cmd {
	s.id = id
	return s.price.Focus()
}

func (s *approveScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case loaded:
		s.busy = false
		if msg.err != nil {
			s.errMsg = describe(msg.err)
			return nil
		}
		return back(s.v.Number + " aprobada")
	case tea.KeyPressMsg:
		if s.busy {
			return nil
		}
		switch msg.String() {
		case "esc":
			return back("aprobación cancelada")
		case "tab", "shift+tab", "up", "down":
			return s.toggle()
		case "enter":
			return s.submit()
		}
	}
	var cmd tea.Cmd
	if s.focus == 0 {
		s.price, cmd = s.price.Update(msg)
	} else {
		s.at, cmd = s.at.Update(msg)
	}
	return cmd
}

func (s *approveScreen) toggle() tea.Cmd {
	s.focus = 1 - s.focus
	if s.focus == 0 {
		s.at.Blur()
		return s.price.Focus()
	}
	s.price.Blur()
	return s.at.Focus()
}

func (s *approveScreen) submit() tea.Cmd {
	in := apppricing.ApproveInput{At: strings.TrimSpace(s.at.Value())}
	if p := strings.TrimSpace(s.price.Value()); p != s.v.Price.String() {
		in.Price = p
	}
	s.busy, s.errMsg = true, ""
	a, n := s.app, s.v.Number
	return load(s.id, func() (any, error) { return a.opts.Backend.Approve(a.ctx, n, in) })
}

func (s *approveScreen) View(int, int) string {
	var b strings.Builder
	b.WriteString(describeProposal(s.v) + "\n\n")
	b.WriteString(sectionStyle.Render("Confirme la aprobación") + "\n")
	b.WriteString(s.price.View() + "\n" + s.at.View() + "\n\n")
	b.WriteString(hintStyle.Render("El precio rige para las ventas futuras de la lista; las ya hechas no cambian.") + "\n")
	switch {
	case s.busy:
		b.WriteString("Aprobando…")
	case s.errMsg != "":
		b.WriteString(failStyle.Render(s.errMsg))
	}
	return b.String()
}

// ---------- Rechazar ----------

type rejectScreen struct {
	app    *app
	id     int
	v      apppricing.ProposalView
	note   textinput.Model
	busy   bool
	errMsg string
}

func newReject(a *app, v apppricing.ProposalView) screen {
	n := newInput("Motivo: ", 200)
	return &rejectScreen{app: a, v: v, note: n}
}

func (s *rejectScreen) Title() string   { return "Rechazar " + s.v.Number }
func (s *rejectScreen) Help() string    { return "Enter Confirmar  Esc Cancelar" }
func (s *rejectScreen) Capturing() bool { return true }
func (s *rejectScreen) Init(id int) tea.Cmd {
	s.id = id
	return s.note.Focus()
}

func (s *rejectScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case loaded:
		s.busy = false
		if msg.err != nil {
			s.errMsg = describe(msg.err)
			return nil
		}
		return back(s.v.Number + " rechazada; el precio vigente no cambia")
	case tea.KeyPressMsg:
		if s.busy {
			return nil
		}
		switch msg.String() {
		case "esc":
			return back("rechazo cancelado")
		case "enter":
			s.busy, s.errMsg = true, ""
			a, n, note := s.app, s.v.Number, s.note.Value()
			return load(s.id, func() (any, error) { return a.opts.Backend.Reject(a.ctx, n, note) })
		}
	}
	var cmd tea.Cmd
	s.note, cmd = s.note.Update(msg)
	return cmd
}

func (s *rejectScreen) View(int, int) string {
	var b strings.Builder
	b.WriteString(describeProposal(s.v) + "\n\n")
	b.WriteString(sectionStyle.Render("Confirme el rechazo") + "\n" + s.note.View() + "\n\n")
	switch {
	case s.busy:
		b.WriteString("Rechazando…")
	case s.errMsg != "":
		b.WriteString(failStyle.Render(s.errMsg))
	}
	return b.String()
}

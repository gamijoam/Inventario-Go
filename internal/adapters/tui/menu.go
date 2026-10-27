package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/gamijoam/inventario-go/internal/application/admin"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
)

// ---------- Menú principal ----------

type menuItem struct {
	label string
	info  string              // texto del panel de ayuda
	perm  identity.Permission // vacío: siempre visible; "session": con sesión
	open  func(*app) screen
	run   func(*app) tea.Cmd
}

type menuTab struct {
	name  string
	items []menuItem
}

// menuTabs son los menús de la barra superior. Una opción aparece con
// sesión y con el permiso de lectura de su módulo; un menú sin opciones no
// aparece.
var menuTabs = []menuTab{
	{"Inventario", []menuItem{
		{label: "Productos", perm: identity.PermProductRead, open: newProducts,
			info: "Catálogo de productos: alta (F2), edición y códigos de barras (F3), activar o inactivar (F4). Enter abre sus existencias."},
		{label: "Unidades de medida", perm: identity.PermProductRead, open: newUnits,
			info: "Unidades con sus decimales (UND, KG, LT…). F2 crea una nueva."},
		{label: "Existencias y kardex", perm: identity.PermStockRead, open: func(a *app) screen { return newStock(a, "") },
			info: "Física, reservada, bloqueada y disponible por almacén, con costo promedio y valor. Enter abre el kardex."},
	}},
	{"Precios", []menuItem{
		{label: "Precios vigentes", perm: identity.PermPriceRead, open: newPrices,
			info: "Precio vigente por lista, el programado y la propuesta pendiente."},
		{label: "Propuestas de precio", perm: identity.PermPriceRead, open: newProposals,
			info: "Propuestas pendientes de aprobación: las que deja cada compra y las manuales. Aprobar exige permiso."},
	}},
	{"Sistema", []menuItem{
		{label: "Diagnóstico", open: newDoctor, info: "Verifica configuración, base de datos, zona horaria y aritmética exacta."},
		{label: "Versión", open: newVersion, info: "Versión, commit y fecha de compilación."},
		{label: "Cerrar sesión", perm: "session", run: logout, info: "Cierra la sesión en este equipo, también para los comandos."},
		{label: "Salir", run: func(*app) tea.Cmd { return tea.Quit }, info: "Cierra BalanzaPro."},
	}},
}

type menuScreen struct {
	app    *app
	tabs   []menuTab
	tab    int
	cursor int
}

func newMenu(a *app) screen {
	m := &menuScreen{app: a}
	for _, t := range menuTabs {
		var items []menuItem
		for _, it := range t.items {
			switch {
			case it.perm == "":
			case a.session == nil:
				continue
			case it.perm == "session":
			case !a.session.Can(it.perm):
				continue
			}
			items = append(items, it)
		}
		if len(items) > 0 {
			m.tabs = append(m.tabs, menuTab{t.name, items})
		}
	}
	return m
}

func (m *menuScreen) tabNames() []string {
	out := make([]string, len(m.tabs))
	for i, t := range m.tabs {
		out[i] = t.name
	}
	return out
}

func (m *menuScreen) items() []menuItem { return m.tabs[m.tab].items }

func logout(a *app) tea.Cmd {
	return func() tea.Msg { return loggedOutMsg{a.opts.Backend.Logout(a.ctx)} }
}

func (m *menuScreen) Title() string    { return m.tabs[m.tab].name }
func (m *menuScreen) Info() string     { return m.items()[m.cursor].info }
func (m *menuScreen) Help() string     { return "←→ Menú  ↑↓ Elegir  Enter Abrir  Esc/q Salir" }
func (m *menuScreen) Init(int) tea.Cmd { return nil }
func (m *menuScreen) Capturing() bool  { return false }

func (m *menuScreen) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	n := len(m.items())
	switch key.String() {
	case "left", "h", "shift+tab":
		m.tab, m.cursor = (m.tab-1+len(m.tabs))%len(m.tabs), 0
	case "right", "l", "tab":
		m.tab, m.cursor = (m.tab+1)%len(m.tabs), 0
	case "up", "k":
		m.cursor = (m.cursor - 1 + n) % n
	case "down", "j":
		m.cursor = (m.cursor + 1) % n
	case "enter":
		it := m.items()[m.cursor]
		if it.run != nil {
			return it.run(m.app)
		}
		return push(it.open(m.app))
	}
	return nil
}

func (m *menuScreen) View(int, int) string {
	var b strings.Builder
	if s := m.app.session; s != nil && m.tab == 0 {
		b.WriteString("Hola, " + s.DisplayName + ".\n\n")
	}
	for i, it := range m.items() {
		if i == m.cursor {
			b.WriteString(pickedStyle.Render(" ► "+it.label+" ") + "\n")
		} else {
			b.WriteString("   " + it.label + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---------- Inicio de sesión ----------

type loginScreen struct {
	app      *app
	user     textinput.Model
	password textinput.Model
	focus    int
	busy     bool
	err      string
}

func newLogin(a *app) screen {
	u := newInput("Usuario:     ", 64)
	p := newInput("Contraseña:  ", 256)
	p.EchoMode = textinput.EchoPassword
	p.EchoCharacter = '•'
	return &loginScreen{app: a, user: u, password: p}
}

func (l *loginScreen) Title() string   { return "Iniciar sesión" }
func (l *loginScreen) Help() string    { return "Tab Campo  Enter Entrar  Esc Salir" }
func (l *loginScreen) Capturing() bool { return true }

func (l *loginScreen) Init(int) tea.Cmd {
	l.password.Blur()
	return l.user.Focus()
}

func (l *loginScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case sessionMsg: // intento fallido; el éxito lo atiende la raíz
		l.busy = false
		l.err = describe(msg.err)
		l.password.SetValue("")
		return nil
	case tea.KeyPressMsg:
		if l.busy {
			return nil
		}
		switch msg.String() {
		case "tab", "shift+tab", "up", "down":
			return l.toggle()
		case "enter":
			if l.focus == 0 {
				return l.toggle()
			}
			return l.submit()
		case "esc":
			return tea.Quit
		}
	}
	var cmd tea.Cmd
	if l.focus == 0 {
		l.user, cmd = l.user.Update(msg)
	} else {
		l.password, cmd = l.password.Update(msg)
	}
	return cmd
}

func (l *loginScreen) toggle() tea.Cmd {
	l.focus = 1 - l.focus
	if l.focus == 0 {
		l.password.Blur()
		return l.user.Focus()
	}
	l.user.Blur()
	return l.password.Focus()
}

func (l *loginScreen) submit() tea.Cmd {
	user, pw := strings.TrimSpace(l.user.Value()), l.password.Value()
	if user == "" || pw == "" {
		l.err = "indique usuario y contraseña"
		return nil
	}
	l.busy, l.err = true, ""
	a := l.app
	return func() tea.Msg {
		s, err := a.opts.Backend.Login(a.ctx, user, pw)
		return sessionMsg{s: s, err: err, login: true}
	}
}

func (l *loginScreen) View(int, int) string {
	var b strings.Builder
	b.WriteString("Inicie sesión con su usuario de BalanzaPro.\n\n")
	b.WriteString(l.user.View() + "\n" + l.password.View() + "\n\n")
	switch {
	case l.busy:
		b.WriteString("Verificando…")
	case l.err != "":
		b.WriteString(failStyle.Render(l.err))
	}
	return b.String()
}

// ---------- Diagnóstico y versión ----------

type doctorScreen struct {
	app     *app
	id      int
	report  *admin.Report
	running bool
}

func newDoctor(a *app) screen { return &doctorScreen{app: a} }

func (d *doctorScreen) Title() string   { return "Diagnóstico" }
func (d *doctorScreen) Help() string    { return "R Repetir  Esc Volver  Q Salir" }
func (d *doctorScreen) Capturing() bool { return false }

func (d *doctorScreen) Init(id int) tea.Cmd {
	d.id = id
	return d.run()
}

func (d *doctorScreen) run() tea.Cmd {
	d.running = true
	a := d.app
	return load(d.id, func() (any, error) {
		if a.opts.Doctor == nil {
			return admin.Report{}, nil
		}
		return a.opts.Doctor(a.ctx), nil
	})
}

func (d *doctorScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case loaded:
		r := msg.v.(admin.Report)
		d.report, d.running = &r, false
	case tea.KeyPressMsg:
		if msg.String() == "r" && !d.running {
			return d.run()
		}
	}
	return nil
}

func (d *doctorScreen) View(int, int) string {
	if d.running || d.report == nil {
		return "Ejecutando verificaciones…"
	}
	var b strings.Builder
	for _, r := range d.report.Checks {
		b.WriteString(fmt.Sprintf("%s %-10s %s\n", statusLabel(r.Status), r.Name, r.Detail))
	}
	return strings.TrimRight(b.String(), "\n")
}

// statusLabel no depende solo del color: el texto distingue el estado aun
// sin colores.
func statusLabel(s admin.Status) string {
	switch s {
	case admin.StatusOK:
		return okStyle.Render("[ OK ]")
	case admin.StatusWarn:
		return warnStyle.Render("[WARN]")
	default:
		return failStyle.Render("[FAIL]")
	}
}

type versionScreen struct{ app *app }

func newVersion(a *app) screen { return &versionScreen{app: a} }

func (v *versionScreen) Title() string          { return "Versión" }
func (v *versionScreen) Help() string           { return "Esc Volver  Q Salir" }
func (v *versionScreen) Init(int) tea.Cmd       { return nil }
func (v *versionScreen) Update(tea.Msg) tea.Cmd { return nil }
func (v *versionScreen) Capturing() bool        { return false }

func (v *versionScreen) View(int, int) string {
	bi := v.app.opts.Build
	return fmt.Sprintf("Versión:    %s\nCommit:     %s\nCompilado:  %s\nGo:         %s %s/%s",
		bi.Version, orUnknown(bi.Commit), orUnknown(bi.Date), bi.GoVersion, bi.OS, bi.Arch)
}

func orUnknown(s string) string {
	if s == "" {
		return "desconocido"
	}
	return s
}

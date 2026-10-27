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
	perm  identity.Permission // vacío: siempre visible
	open  func(*app) screen
	run   func(*app) tea.Cmd
}

// menuItems son las opciones del menú; las de módulos solo aparecen con
// sesión y con el permiso de lectura correspondiente.
var menuItems = []menuItem{
	{label: "Productos", perm: identity.PermProductRead, open: newProducts},
	{label: "Existencias", perm: identity.PermStockRead, open: func(a *app) screen { return newStock(a, "") }},
	{label: "Precios", perm: identity.PermPriceRead, open: newPrices},
	{label: "Propuestas de precio", perm: identity.PermPriceRead, open: newProposals},
	{label: "Diagnóstico", open: newDoctor},
	{label: "Versión", open: newVersion},
	{label: "Cerrar sesión", perm: "session", run: logout},
	{label: "Salir", run: func(*app) tea.Cmd { return tea.Quit }},
}

type menuScreen struct {
	app    *app
	items  []menuItem
	cursor int
}

func newMenu(a *app) screen {
	m := &menuScreen{app: a}
	for _, it := range menuItems {
		switch {
		case it.perm == "":
		case a.session == nil:
			continue
		case it.perm == "session":
		case !a.session.Can(it.perm):
			continue
		}
		m.items = append(m.items, it)
	}
	return m
}

func logout(a *app) tea.Cmd {
	return func() tea.Msg { return loggedOutMsg{a.opts.Backend.Logout(a.ctx)} }
}

func (m *menuScreen) Title() string { return "" }
func (m *menuScreen) Help() string {
	return "↑/↓ mover · enter abrir · 1-9 ir directo · q salir"
}
func (m *menuScreen) Init(int) tea.Cmd     { return nil }
func (m *menuScreen) Capturing() bool      { return false }
func (m *menuScreen) View(int, int) string { return m.render() }

func (m *menuScreen) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch k := key.String(); k {
	case "up", "k":
		m.cursor = (m.cursor - 1 + len(m.items)) % len(m.items)
	case "down", "j", "tab":
		m.cursor = (m.cursor + 1) % len(m.items)
	case "enter", "right", "l":
		return m.choose(m.cursor)
	default:
		if len(k) == 1 && k[0] >= '1' && k[0] <= '9' && int(k[0]-'1') < len(m.items) {
			m.cursor = int(k[0] - '1')
			return m.choose(m.cursor)
		}
	}
	return nil
}

func (m *menuScreen) choose(i int) tea.Cmd {
	it := m.items[i]
	if it.run != nil {
		return it.run(m.app)
	}
	return push(it.open(m.app))
}

func (m *menuScreen) render() string {
	var b strings.Builder
	if s := m.app.session; s != nil {
		b.WriteString(fmt.Sprintf("Hola, %s.\n\n", s.DisplayName))
	}
	for i, it := range m.items {
		line := fmt.Sprintf("%d. %s", i+1, it.label)
		if i == m.cursor {
			b.WriteString(selectedStyle.Render("> "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
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
func (l *loginScreen) Help() string    { return "tab cambiar de campo · enter entrar · ctrl+c salir" }
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
func (d *doctorScreen) Help() string    { return "r repetir · esc volver · q salir" }
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
func (v *versionScreen) Help() string           { return "esc volver · q salir" }
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

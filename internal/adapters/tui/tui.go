// Package tui implementa la interfaz interactiva de terminal (Bubble Tea v2).
//
// La TUI no contiene reglas de negocio: solo presenta y dispara casos de uso
// de la capa application a través de Backend, igual que la CLI. Cada
// pantalla ocupa la terminal completa; Esc vuelve a la anterior.
package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/gamijoam/inventario-go/internal/application/admin"
	"github.com/gamijoam/inventario-go/internal/platform/buildinfo"
)

// Tamaño mínimo utilizable; por debajo se muestra un aviso en lugar del menú.
const (
	MinWidth  = 40
	MinHeight = 12
)

// Options configura la TUI.
type Options struct {
	NoColor bool
	Build   buildinfo.Info
	// Doctor ejecuta el diagnóstico; la TUI lo llama de forma asíncrona.
	Doctor func(context.Context) admin.Report
	// Backend da acceso a los módulos. Nil si la base no está disponible:
	// entonces solo hay diagnóstico y versión, y Unavailable explica por qué.
	Backend     Backend
	Unavailable string
}

// Run inicia la TUI y bloquea hasta que el usuario sale o ctx se cancela.
func Run(ctx context.Context, in io.Reader, out io.Writer, opts Options) error {
	progOpts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out)}
	if opts.NoColor {
		// Conserva negrita e inversión pero elimina los colores.
		progOpts = append(progOpts, tea.WithColorProfile(colorprofile.Ascii))
	}
	_, err := tea.NewProgram(New(ctx, opts), progOpts...).Run()
	if err != nil && ctx.Err() != nil {
		return nil // salida por Ctrl+C o señal: no es un error
	}
	return err
}

// screen es una pantalla completa. Las pantallas son punteros: Update las
// modifica y devuelve el comando siguiente.
type screen interface {
	Title() string
	// Help resume los atajos de la pantalla para la barra inferior.
	Help() string
	// Init recibe el identificador con que la raíz le entregará sus
	// resultados asíncronos (loaded.to) y devuelve la carga inicial.
	Init(id int) tea.Cmd
	Update(msg tea.Msg) tea.Cmd
	View(width, height int) string
	// Capturing indica que el usuario está escribiendo: q y Esc dejan de
	// ser atajos globales y llegan a la pantalla.
	Capturing() bool
}

// refresher es una pantalla que recarga sus datos al volver a ella.
type refresher interface{ Refresh() tea.Cmd }

// app es el contexto compartido por las pantallas.
type app struct {
	ctx     context.Context
	opts    Options
	session *Session
}

type (
	pushMsg   struct{ s screen }
	popMsg    struct{ status string }
	statusMsg struct {
		text string
		err  error
	}
	// sessionMsg trae el resultado de retomar o iniciar sesión.
	sessionMsg struct {
		s     Session
		err   error
		login bool // viene del formulario, no de retomar la sesión guardada
	}
	loggedOutMsg struct{ err error }
	// loaded entrega un resultado asíncrono a la pantalla to.
	loaded struct {
		to  int
		v   any
		err error
	}
)

func push(s screen) tea.Cmd { return func() tea.Msg { return pushMsg{s} } }

// back vuelve a la pantalla anterior, que se recarga si sabe hacerlo.
func back(status string) tea.Cmd { return func() tea.Msg { return popMsg{status} } }

func setStatus(text string, err error) tea.Cmd {
	return func() tea.Msg { return statusMsg{text, err} }
}

// load ejecuta f fuera del ciclo de la interfaz y entrega su resultado a la
// pantalla to.
func load(to int, f func() (any, error)) tea.Cmd {
	return func() tea.Msg {
		v, err := f()
		return loaded{to, v, err}
	}
}

// Model es el estado de la TUI. Se exporta para poder probarlo sin terminal.
type Model struct {
	app           *app
	stack         []screen
	ids           []int
	seq           int
	width, height int
	status        string
	statusErr     bool
	checking      bool // retomando la sesión guardada
}

// New crea el modelo inicial. Con Backend, empieza retomando la sesión de
// este equipo; si no hay, pide iniciarla.
func New(ctx context.Context, opts Options) Model {
	m := Model{app: &app{ctx: ctx, opts: opts}}
	if opts.Backend == nil {
		m.reset(newMenu(m.app))
		m.status, m.statusErr = opts.Unavailable, opts.Unavailable != ""
		return m
	}
	m.checking = true
	return m
}

func (m Model) Init() tea.Cmd {
	if !m.checking {
		return nil
	}
	a := m.app
	return func() tea.Msg {
		s, err := a.opts.Backend.Resume(a.ctx)
		return sessionMsg{s: s, err: err}
	}
}

// reset deja s como única pantalla.
func (m *Model) reset(s screen) tea.Cmd {
	m.stack, m.ids = nil, nil
	return m.open(s)
}

func (m *Model) open(s screen) tea.Cmd {
	m.seq++
	m.stack = append(m.stack, s)
	m.ids = append(m.ids, m.seq)
	cmd := s.Init(m.seq)
	if m.width > 0 {
		s.Update(tea.WindowSizeMsg{Width: m.width, Height: m.bodyHeight()})
	}
	return cmd
}

func (m Model) top() screen {
	if len(m.stack) == 0 {
		return nil
	}
	return m.stack[len(m.stack)-1]
}

// bodyHeight descuenta la cabecera (2 líneas) y el pie (2 líneas).
func (m Model) bodyHeight() int { return max(m.height-4, 1) }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		body := tea.WindowSizeMsg{Width: msg.Width, Height: m.bodyHeight()}
		for _, s := range m.stack {
			s.Update(body)
		}
		return m, nil
	case sessionMsg:
		m.checking = false
		if msg.err != nil {
			if msg.login {
				// El formulario muestra el error y sigue abierto.
				return m, m.top().Update(msg)
			}
			return m, m.reset(newLogin(m.app))
		}
		s := msg.s
		m.app.session = &s
		m.status, m.statusErr = "", false
		return m, m.reset(newMenu(m.app))
	case loggedOutMsg:
		m.app.session = nil
		m.status, m.statusErr = "sesión cerrada", false
		if msg.err != nil {
			m.status, m.statusErr = "no se pudo cerrar la sesión: "+msg.err.Error(), true
		}
		return m, m.reset(newLogin(m.app))
	case pushMsg:
		m.status, m.statusErr = "", false
		return m, m.open(msg.s)
	case popMsg:
		if len(m.stack) > 1 {
			m.stack, m.ids = m.stack[:len(m.stack)-1], m.ids[:len(m.ids)-1]
		}
		m.status, m.statusErr = msg.status, false
		if r, ok := m.top().(refresher); ok {
			return m, r.Refresh()
		}
		return m, nil
	case statusMsg:
		m.status, m.statusErr = msg.text, msg.err != nil
		if msg.err != nil {
			m.status = describe(msg.err)
			if msg.text != "" {
				m.status = msg.text + ": " + m.status
			}
		}
		return m, nil
	case loaded:
		for i, sid := range m.ids {
			if sid == msg.to {
				return m, m.stack[i].Update(msg)
			}
		}
		return m, nil // la pantalla ya se cerró
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	if t := m.top(); t != nil {
		return m, t.Update(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	t := m.top()
	if t == nil {
		return m, nil
	}
	if t.Capturing() {
		return m, t.Update(msg)
	}
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "esc":
		if len(m.stack) > 1 {
			return m.Update(popMsg{})
		}
		if _, isMenu := t.(*menuScreen); isMenu {
			return m, tea.Quit
		}
	}
	return m, t.Update(msg)
}

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "BalanzaPro"
	return v
}

func (m Model) render() string {
	if m.width > 0 && (m.width < MinWidth || m.height < MinHeight) {
		return fmt.Sprintf("Terminal demasiado pequeña (%dx%d).\nMínimo %dx%d. Agrande la ventana o pulse q para salir.",
			m.width, m.height, MinWidth, MinHeight)
	}
	width := m.width
	if width == 0 {
		width = 80
	}
	t := m.top()
	title := ""
	if t != nil {
		title = t.Title()
	}
	var b strings.Builder
	b.WriteString(m.header(title, width) + "\n\n")
	body := ""
	switch {
	case m.checking:
		body = "Comprobando la sesión…"
	case t != nil:
		body = t.View(width, m.bodyHeight())
	}
	b.WriteString(body)
	// El pie va al final de la pantalla.
	used := 2 + strings.Count(body, "\n") + 1
	if m.height > 0 {
		for ; used < m.height-2; used++ {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n" + m.statusLine(width))
	help := "q salir"
	if t != nil {
		help = t.Help()
	}
	b.WriteString("\n" + hintStyle.Render(truncate(help, width)))
	return b.String()
}

// header muestra el producto, la pantalla y, a la derecha, quién y dónde.
func (m Model) header(title string, width int) string {
	left := titleStyle.Render("BalanzaPro") + " " + hintStyle.Render(m.app.opts.Build.Version)
	if title != "" {
		left += "  " + sectionStyle.Render(title)
	}
	right := ""
	if s := m.app.session; s != nil {
		right = fmt.Sprintf("%s · %s/%s", s.Username, s.Company, s.Branch)
	}
	gap := width - visibleLen(left) - visibleLen(right)
	if right == "" || gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + hintStyle.Render(right)
}

func (m Model) statusLine(width int) string {
	if m.status == "" {
		return ""
	}
	if m.statusErr {
		return failStyle.Render(truncate(m.status, width))
	}
	return okStyle.Render(truncate(m.status, width))
}

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
	// Setup, si no es nil, es que la instalación no está configurada: la TUI
	// abre el asistente de primer uso.
	Setup Setup
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
	if opts.Backend == nil && opts.Setup != nil {
		m.reset(newSetupWizard(m.app))
		return m
	}
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
		s.Update(m.bodySize(s))
	}
	return cmd
}

func (m Model) top() screen {
	if len(m.stack) == 0 {
		return nil
	}
	return m.stack[len(m.stack)-1]
}

// bodySize es el área de trabajo de una pantalla dentro del marco: sin la
// barra de menús, el título, el estado, las teclas ni el panel de ayuda.
func (m Model) bodySize(s screen) tea.WindowSizeMsg {
	l := newLayout(m.width, m.height, s)
	return tea.WindowSizeMsg{Width: l.contentW, Height: max(l.contentH-2, 1)}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		for _, s := range m.stack {
			s.Update(m.bodySize(s))
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
	w, h := m.width, m.height
	if w == 0 {
		w, h = 80, 24
	}
	f := frame{l: newLayout(w, h, m.top()), keys: "q Salir"}
	if s := m.app.session; s != nil {
		f.right = fmt.Sprintf("%s · %s/%s", s.Username, s.Company, s.Branch)
	}
	if len(m.stack) > 0 {
		if menu, ok := m.stack[0].(*menuScreen); ok {
			f.tabs, f.active = menu.tabNames(), menu.tab
		}
	}
	switch t := m.top(); {
	case m.checking:
		f.title, f.content = "Inicio", "Comprobando la sesión…"
	case t != nil:
		bs := m.bodySize(t)
		f.title, f.content, f.keys = t.Title(), t.View(bs.Width, bs.Height), t.Help()
		if i, ok := t.(infoer); ok {
			f.info = i.Info()
		}
	}
	if m.status != "" {
		if m.statusErr {
			f.status = failStyle.Render(m.status)
		} else {
			f.status = okStyle.Render(m.status)
		}
	}
	return f.render()
}

// infoer es una pantalla con texto para el panel de ayuda.
type infoer interface{ Info() string }

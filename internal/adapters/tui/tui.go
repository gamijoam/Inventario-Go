// Package tui implementa la interfaz interactiva de terminal (Bubble Tea v2).
//
// La TUI no contiene reglas de negocio: solo presenta y dispara casos de uso
// de la capa application, igual que la CLI.
package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

type screen int

const (
	screenMenu screen = iota
	screenDoctor
	screenVersion
)

type menuItem struct {
	label  string
	target screen
	quit   bool
}

var menu = []menuItem{
	{label: "Diagnóstico", target: screenDoctor},
	{label: "Versión", target: screenVersion},
	{label: "Salir", quit: true},
}

type doctorDoneMsg admin.Report

// Model es el estado de la TUI. Se exporta para poder probarlo sin terminal.
type Model struct {
	ctx           context.Context
	opts          Options
	screen        screen
	cursor        int
	width, height int
	report        *admin.Report
	running       bool
}

// New crea el modelo inicial.
func New(ctx context.Context, opts Options) Model {
	return Model{ctx: ctx, opts: opts}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case doctorDoneMsg:
		report := admin.Report(msg)
		m.report, m.running = &report, false
	case tea.KeyPressMsg:
		return m.handleKey(msg.String())
	}
	return m, nil
}

func (m Model) handleKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	}
	if m.screen != screenMenu {
		switch key {
		case "esc", "backspace", "left", "h":
			m.screen = screenMenu
		case "r":
			if m.screen == screenDoctor && !m.running {
				return m.startDoctor()
			}
		}
		return m, nil
	}
	switch key {
	case "up", "k":
		m.cursor = (m.cursor - 1 + len(menu)) % len(menu)
	case "down", "j", "tab":
		m.cursor = (m.cursor + 1) % len(menu)
	case "enter", "right", "l":
		item := menu[m.cursor]
		if item.quit {
			return m, tea.Quit
		}
		m.screen = item.target
		if item.target == screenDoctor && m.report == nil && !m.running {
			return m.startDoctor()
		}
	case "esc":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) startDoctor() (tea.Model, tea.Cmd) {
	m.running = true
	ctx, run := m.ctx, m.opts.Doctor
	return m, func() tea.Msg {
		if run == nil {
			return doctorDoneMsg{}
		}
		return doctorDoneMsg(run(ctx))
	}
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#5FAFFF"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Reverse(true)
	hintStyle     = lipgloss.NewStyle().Faint(true)
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FD75F"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD75F"))
	failStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF5F5F"))
)

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
	var b strings.Builder
	b.WriteString(titleStyle.Render("BalanzaPro") + "  " + hintStyle.Render(m.opts.Build.Version) + "\n\n")
	switch m.screen {
	case screenMenu:
		for i, item := range menu {
			line := "  " + item.label
			if i == m.cursor {
				line = selectedStyle.Render("> " + item.label)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n" + hintStyle.Render("↑/↓ mover · enter abrir · q salir"))
	case screenDoctor:
		b.WriteString("Diagnóstico\n\n")
		switch {
		case m.running:
			b.WriteString("Ejecutando verificaciones…\n")
		case m.report != nil:
			for _, r := range m.report.Checks {
				b.WriteString(fmt.Sprintf("%s %-10s %s\n", statusLabel(r.Status), r.Name, r.Detail))
			}
		}
		b.WriteString("\n" + hintStyle.Render("r repetir · esc volver · q salir"))
	case screenVersion:
		bi := m.opts.Build
		b.WriteString("Versión\n\n")
		b.WriteString(fmt.Sprintf("Versión:    %s\nCommit:     %s\nCompilado:  %s\nGo:         %s %s/%s\n",
			bi.Version, orUnknown(bi.Commit), orUnknown(bi.Date), bi.GoVersion, bi.OS, bi.Arch))
		b.WriteString("\n" + hintStyle.Render("esc volver · q salir"))
	}
	return b.String()
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

func orUnknown(s string) string {
	if s == "" {
		return "desconocido"
	}
	return s
}

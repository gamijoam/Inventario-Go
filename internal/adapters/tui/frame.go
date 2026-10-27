package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Marco tipo BIOS: barra de menús arriba, contenido al centro, panel de
// ayuda a la derecha y teclas abajo, todo sobre fondo azul. Sin colores
// (--no-color) quedan los marcos, la negrita y la inversión.
var (
	biosBlue  = lipgloss.Color("#0000AA")
	biosGrey  = lipgloss.Color("#AAAAAA")
	biosWhite = lipgloss.Color("#FFFFFF")
	biosCyan  = lipgloss.Color("#55FFFF")

	baseStyle  = lipgloss.NewStyle().Background(biosBlue).Foreground(biosWhite)
	barStyle   = lipgloss.NewStyle().Background(biosGrey).Foreground(lipgloss.Color("#000000"))
	tabStyle   = lipgloss.NewStyle().Background(biosGrey).Foreground(biosBlue).Bold(true)
	tabOnStyle = lipgloss.NewStyle().Background(biosBlue).Foreground(biosWhite).Bold(true).Underline(true)
	infoStyle  = lipgloss.NewStyle().Background(biosBlue).Foreground(biosCyan)
	ruleStyle  = lipgloss.NewStyle().Background(biosBlue).Foreground(biosCyan)
	// La selección invierte el estilo base: azul sobre blanco con colores,
	// inversión sin ellos.
	pickedStyle = lipgloss.NewStyle().Reverse(true).Bold(true)
)

// helpWidth es el ancho del panel de ayuda; con menos de minWithHelp
// columnas el panel se oculta.
const (
	helpWidth   = 30
	minWithHelp = 90
)

// wide es una pantalla de tablas anchas: ocupa también el lugar del panel de
// ayuda.
type wide interface{ Wide() bool }

// layout son las medidas del área de contenido para un tamaño de terminal.
type layout struct {
	width, height int // terminal
	contentW      int
	contentH      int // incluye el título de la pantalla (2 líneas)
	help          bool
	helpW         int
}

func newLayout(width, height int, s screen) layout {
	l := layout{width: width, height: height, help: width >= minWithHelp}
	if w, ok := s.(wide); ok && w.Wide() {
		l.help = false
	}
	if l.help {
		l.helpW = helpWidth
	}
	// Márgenes de 1, y el separador del panel de ayuda.
	l.contentW = width - 2
	if l.help {
		l.contentW -= l.helpW + 3
	}
	// Barra de menús, regla, estado y teclas.
	l.contentH = max(height-4, 1)
	return l
}

// seq es la secuencia de inicio de un estilo, para reponerla después de
// cada reinicio de atributos dentro de una línea.
func seq(s lipgloss.Style) string {
	r := s.Render("x")
	if i := strings.Index(r, "x"); i > 0 {
		return r[:i]
	}
	return ""
}

// paint pinta una línea de width columnas con el estilo base, también tras
// los estilos internos que reinician los atributos.
func paint(line string, width int, base lipgloss.Style) string {
	line = ansi.Truncate(line, width, "…")
	if pad := width - ansi.StringWidth(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	start := seq(base)
	if start == "" {
		return line
	}
	line = strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+start)
	line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+start)
	return start + line + "\x1b[m"
}

// frame compone la pantalla completa.
type frame struct {
	l       layout
	tabs    []string // barra de menús; vacía muestra solo el título
	active  int
	title   string
	right   string // usuario y ámbito
	content string
	info    string
	status  string
	keys    string
}

func (f frame) render() string {
	w := f.l.width
	var b strings.Builder

	// Barra de menús.
	bar := " " + tabStyle.Render("BalanzaPro") + barStyle.Render("  ")
	for i, t := range f.tabs {
		label := " " + t + " "
		if i == f.active {
			bar += tabOnStyle.Render(label)
		} else {
			bar += barStyle.Render(label)
		}
	}
	if gap := w - ansi.StringWidth(bar) - ansi.StringWidth(f.right) - 1; gap > 0 && f.right != "" {
		bar += barStyle.Render(strings.Repeat(" ", gap) + f.right + " ")
	}
	b.WriteString(paint(bar, w, barStyle) + "\n")

	// Contenido y ayuda.
	sep := "─"
	b.WriteString(paint(ruleStyle.Render(strings.Repeat(sep, w)), w, baseStyle) + "\n")
	body := append([]string{sectionStyle.Render(f.title), ""}, strings.Split(strings.TrimRight(f.content, "\n"), "\n")...)
	var info []string
	if f.l.help {
		text := f.info
		if text == "" {
			text = "Use ↑/↓ y Enter. Esc vuelve a la pantalla anterior."
		}
		info = strings.Split(ansi.Wordwrap(text, f.l.helpW, " "), "\n")
	}
	for i := range f.l.contentH {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		row := " " + paint(line, f.l.contentW, baseStyle)
		if f.l.help {
			h := ""
			if i == 0 {
				h = sectionStyle.Render("Ayuda")
			} else if i-2 >= 0 && i-2 < len(info) {
				h = infoStyle.Render(info[i-2])
			}
			row += baseStyle.Render(" ") + ruleStyle.Render("│") + baseStyle.Render(" ") + paint(h, f.l.helpW, baseStyle)
		}
		b.WriteString(paint(row, w, baseStyle) + "\n")
	}

	// Estado y teclas.
	b.WriteString(paint(" "+f.status, w, baseStyle) + "\n")
	b.WriteString(paint(" "+f.keys, w, barStyle))
	return b.String()
}

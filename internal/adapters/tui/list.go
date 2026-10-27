package tui

import (
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// listView es una tabla con filtro: "/" abre el filtro, que deja las filas
// que contienen el texto (sin distinguir mayúsculas); Enter lo fija y Esc
// lo borra.
type listView struct {
	cols      []table.Column
	flex      int // columna que se encoge si no cabe
	rows      []table.Row
	visible   []int // índices de rows que pasan el filtro
	t         table.Model
	filter    textinput.Model
	filtering bool
	width     int
	height    int
}

func newList(cols []table.Column, flex int) listView {
	f := newInput("Filtrar: ", 64)
	t := table.New(table.WithColumns(cols), table.WithFocused(true), table.WithStyles(tableStyles()))
	return listView{cols: cols, flex: flex, t: t, filter: f, width: 80, height: 10}
}

func (l *listView) setRows(rows []table.Row) {
	l.rows = rows
	l.apply()
	l.resize(l.width, l.height)
}

// columns ajusta cada columna a su contenido, sin pasar del ancho
// configurado, que es el máximo.
func (l *listView) columns() []table.Column {
	out := append([]table.Column(nil), l.cols...)
	for i, c := range out {
		w := visibleLen(c.Title)
		for _, r := range l.rows {
			if i < len(r) {
				w = max(w, visibleLen(r[i]))
			}
		}
		out[i].Width = min(w, c.Width)
	}
	return out
}

func (l *listView) apply() {
	q := strings.ToLower(strings.TrimSpace(l.filter.Value()))
	l.visible = l.visible[:0]
	shown := make([]table.Row, 0, len(l.rows))
	for i, r := range l.rows {
		if q == "" || strings.Contains(strings.ToLower(strings.Join(r, " ")), q) {
			l.visible = append(l.visible, i)
			shown = append(shown, r)
		}
	}
	l.t.SetRows(shown)
	if l.t.Cursor() >= len(shown) {
		l.t.SetCursor(max(len(shown)-1, 0))
	}
}

func (l *listView) resize(width, height int) {
	l.width, l.height = width, height
	l.t.SetColumns(fitColumns(l.columns(), l.flex, width))
	l.t.SetWidth(width)
	// Quedan la línea del filtro y la del total.
	l.t.SetHeight(max(height-3, 3))
}

// selected es el índice en rows de la fila elegida, o -1.
func (l *listView) selected() int {
	c := l.t.Cursor()
	if c < 0 || c >= len(l.visible) {
		return -1
	}
	return l.visible[c]
}

func (l *listView) capturing() bool { return l.filtering }

// update atiende teclas de la tabla y del filtro. Devuelve si las consumió.
func (l *listView) update(msg tea.Msg) (tea.Cmd, bool) {
	key, ok := msg.(tea.KeyPressMsg)
	if l.filtering {
		if ok {
			switch key.String() {
			case "enter":
				l.filtering = false
				l.filter.Blur()
				return nil, true
			case "esc":
				l.filtering = false
				l.filter.Blur()
				l.filter.SetValue("")
				l.apply()
				return nil, true
			}
		}
		var cmd tea.Cmd
		l.filter, cmd = l.filter.Update(msg)
		l.apply()
		return cmd, true
	}
	if !ok {
		return nil, false
	}
	switch key.String() {
	case "/":
		l.filtering = true
		return l.filter.Focus(), true
	case "up", "k", "down", "j", "pgup", "pgdown", "home", "end", "g", "G":
		var cmd tea.Cmd
		l.t, cmd = l.t.Update(msg)
		return cmd, true
	}
	return nil, false
}

func (l *listView) view(empty string) string {
	var b strings.Builder
	if l.filtering || l.filter.Value() != "" {
		b.WriteString(l.filter.View())
	}
	b.WriteString("\n")
	if len(l.rows) == 0 {
		b.WriteString(hintStyle.Render(empty))
		return b.String()
	}
	b.WriteString(l.t.View() + "\n")
	total := len(l.rows)
	if len(l.visible) != total {
		b.WriteString(hintStyle.Render(itoa(len(l.visible)) + " de " + itoa(total)))
	} else {
		b.WriteString(hintStyle.Render(itoa(total) + " en total"))
	}
	return b.String()
}

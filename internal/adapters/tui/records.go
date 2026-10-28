package tui

import (
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
)

// action es una tecla de una lista. Sin needsItem funciona con la lista
// vacía; ok decide si se ofrece para la fila elegida (nil si no hay).
type action[T any] struct {
	key, label string
	needsItem  bool
	ok         func(item *T) bool
	run        func(item *T) tea.Cmd
}

// recordsScreen es una lista con filtro, recarga, alternar entre los
// activos y todos (T) y acciones por tecla.
type recordsScreen[T any] struct {
	app         *app
	id          int
	title, info string
	empty       string
	list        listView
	items       []T
	loading     bool
	wide        bool
	// showAll alterna con T; fetch recibe si se piden todos.
	toggle  bool
	showAll bool
	// subset nombra lo que se ve sin T; vacío, "activos".
	subset  string
	fetch   func(all bool) ([]T, error)
	row     func(T) table.Row
	actions []action[T]
}

func (r *recordsScreen[T]) Title() string {
	if r.toggle && !r.showAll {
		if r.subset != "" {
			return r.title + " (" + r.subset + ")"
		}
		return r.title + " (activos)"
	}
	return r.title
}
func (r *recordsScreen[T]) Info() string    { return r.info }
func (r *recordsScreen[T]) Wide() bool      { return r.wide }
func (r *recordsScreen[T]) Capturing() bool { return r.list.capturing() }

func (r *recordsScreen[T]) current() *T {
	if i := r.list.selected(); i >= 0 {
		return &r.items[i]
	}
	return nil
}

func (r *recordsScreen[T]) available() []action[T] {
	cur := r.current()
	var out []action[T]
	for _, a := range r.actions {
		if a.needsItem && cur == nil {
			continue
		}
		if a.ok != nil && !a.ok(cur) {
			continue
		}
		out = append(out, a)
	}
	return out
}

func (r *recordsScreen[T]) Help() string {
	if r.list.capturing() {
		return "Escriba para filtrar  Enter Fijar  Esc Borrar"
	}
	parts := []string{"↑↓ Elegir", "/ Filtrar"}
	for _, a := range r.available() {
		parts = append(parts, a.label)
	}
	if r.toggle {
		if r.showAll {
			parts = append(parts, "T Solo "+cmpOr(r.subset, "activos"))
		} else {
			parts = append(parts, "T Todos")
		}
	}
	return strings.Join(append(parts, "R Recargar", "Esc Volver"), "  ")
}

func (r *recordsScreen[T]) Init(id int) tea.Cmd {
	r.id = id
	return r.Refresh()
}

func (r *recordsScreen[T]) Refresh() tea.Cmd {
	r.loading = true
	fetch, all := r.fetch, r.showAll
	return load(r.id, func() (any, error) { return fetch(all) })
}

func (r *recordsScreen[T]) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.list.resize(msg.Width, msg.Height)
	case loaded:
		r.loading = false
		if msg.err != nil {
			return setStatus("no se pudo cargar "+strings.ToLower(r.title), msg.err)
		}
		r.items = msg.v.([]T)
		rows := make([]table.Row, len(r.items))
		for i, it := range r.items {
			rows[i] = r.row(it)
		}
		r.list.setRows(rows)
	case tea.KeyPressMsg:
		if cmd, done := r.list.update(msg); done {
			return cmd
		}
		k := msg.String()
		switch {
		case k == "r":
			return r.Refresh()
		case k == "t" && r.toggle:
			r.showAll = !r.showAll
			return r.Refresh()
		}
		for _, a := range r.available() {
			if a.key == k {
				return a.run(r.current())
			}
		}
	default:
		cmd, _ := r.list.update(msg)
		return cmd
	}
	return nil
}

func (r *recordsScreen[T]) View(int, int) string {
	if r.loading && len(r.items) == 0 {
		return "Cargando…"
	}
	return r.list.view(r.empty)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

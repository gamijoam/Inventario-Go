package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/gamijoam/inventario-go/internal/domain/id"
)

// Formularios: una columna de campos. ↑/↓ o Tab cambian de campo, ←/→
// cambian la opción de una selección, Enter pasa al siguiente y en el
// último guarda; F10 guarda desde cualquiera y Esc cancela. Si el backend
// rechaza los datos, el formulario sigue abierto con el motivo.

type fieldKind int

const (
	textField fieldKind = iota
	passwordField
	choiceField
	// productField es un texto (SKU, nombre o código de barras, también
	// con lector) que se reconoce al salir del campo; F5 abre la lista.
	productField
)

type field struct {
	key, label string
	kind       fieldKind
	input      textinput.Model
	options    []string
	labels     []string // texto de cada opción; vacío, la opción misma
	choice     int
	// Producto reconocido: lo que se buscó y qué se encontró.
	looked string
	found  string
	ok     bool
	// sugg son las coincidencias mientras se escribe el producto.
	sugg suggester
}

func textF(key, label, value string, limit int) *field {
	in := newInput("", limit)
	in.SetValue(value)
	return &field{key: key, label: label, kind: textField, input: in}
}

func passwordF(key, label string) *field {
	in := newInput("", 256)
	in.EchoMode = textinput.EchoPassword
	in.EchoCharacter = '•'
	return &field{key: key, label: label, kind: passwordField, input: in}
}

func choiceF(key, label string, options []string, selected string) *field {
	f := &field{key: key, label: label, kind: choiceField, options: options}
	for i, o := range options {
		if o == selected {
			f.choice = i
		}
	}
	return f
}

// productF es el campo de producto con buscador.
func productF(key, label string) *field {
	in := newInput("", 200)
	in.Placeholder = "SKU, nombre o código de barras · F5 lista"
	return &field{key: key, label: label, kind: productField, input: in}
}

// labeled pone el texto que se muestra para cada opción.
func (f *field) labeled(labels ...string) *field {
	f.labels = labels
	return f
}

func (f *field) shown() string {
	if len(f.options) == 0 {
		return "—"
	}
	if f.choice < len(f.labels) {
		return f.labels[f.choice]
	}
	return f.options[f.choice]
}

func (f *field) value() string {
	if f.kind == choiceField {
		if len(f.options) == 0 {
			return ""
		}
		return f.options[f.choice]
	}
	return f.input.Value()
}

// placeholder da una pista dentro de un campo vacío.
func (f *field) placeholder(p string) *field {
	f.input.Placeholder = p
	return f
}

type formScreen struct {
	app    *app
	id     int
	title  string
	info   string
	intro  string // texto arriba de los campos
	fields []*field
	focus  int
	busy   bool
	errMsg string
	// save corre fuera del ciclo de la interfaz con los valores por clave.
	save func(values map[string]string) (any, error)
	// done decide qué hacer con el resultado: normalmente volver con un
	// mensaje.
	done func(v any) tea.Cmd
	// cancel es lo que hace Esc; por defecto, volver sin guardar.
	cancel tea.Cmd
	// prepare, si existe, carga datos antes de mostrar los campos (p. ej.
	// los almacenes) y ready los aplica.
	prepare   func() (any, error)
	ready     func(f *formScreen, v any)
	preparing bool
	// key es la clave de idempotencia del formulario: un reintento tras
	// perder la respuesta no aplica la operación dos veces.
	key string
	// changed, si existe, se llama al cambiar una selección (p. ej. para
	// recalcular las opciones de los campos que dependen de ella).
	changed func(f *formScreen, key string)
}

// prepared es el resultado de prepare.
type prepared struct{ v any }

// productInfo es el resultado de reconocer un producto escrito.
type productInfo struct {
	key, query string
	text       string
	ok         bool
	sku, name  string // del producto reconocido
}

// pickedProduct es el producto elegido en la lista (F5).
type pickedProduct struct{ key, sku string }

func newKey() string { return id.MustNew().String() }

func (f *formScreen) field(key string) *field {
	for _, fl := range f.fields {
		if fl.key == key {
			return fl
		}
	}
	return nil
}

func (f *formScreen) Title() string   { return f.title }
func (f *formScreen) Info() string    { return f.info }
func (f *formScreen) Capturing() bool { return true }
func (f *formScreen) Help() string {
	if cur := f.fields[f.focus]; cur.kind == productField {
		if cur.sugg.active() {
			return "↑↓ Elegir producto  Enter Tomar  Esc Cerrar lista  F5 Lista completa"
		}
		return "Escriba para buscar  F5 Lista de productos  Enter Siguiente  F10 Guardar  Esc Cancelar"
	}
	return "↑↓ Campo  ←→ Opción  Enter Siguiente  F10 Guardar  Esc Cancelar"
}

func (f *formScreen) Init(id int) tea.Cmd {
	f.id = id
	if f.prepare != nil {
		f.preparing = true
		prep := f.prepare
		return tea.Batch(f.focusField(0), load(id, func() (any, error) {
			v, err := prep()
			return prepared{v}, err
		}))
	}
	return f.focusField(0)
}

func (f *formScreen) focusField(i int) tea.Cmd {
	cmd := f.lookup(f.fields[f.focus])
	for _, fl := range f.fields {
		fl.input.Blur()
		fl.sugg.open = false
	}
	f.focus = (i + len(f.fields)) % len(f.fields)
	if fl := f.fields[f.focus]; fl.kind != choiceField {
		return tea.Batch(cmd, fl.input.Focus())
	}
	return cmd
}

// lookup reconoce el producto escrito en fl si cambió desde la última vez.
func (f *formScreen) lookup(fl *field) tea.Cmd {
	q := strings.TrimSpace(fl.input.Value())
	if fl.kind != productField || q == fl.looked {
		return nil
	}
	fl.looked, fl.found, fl.ok = q, "", false
	if q == "" {
		return nil
	}
	warehouse := ""
	if w := f.field("warehouse"); w != nil {
		warehouse = w.value()
	}
	return lookupProduct(f.app, f.id, fl.key, q, warehouse)
}

func (f *formScreen) values() map[string]string {
	out := make(map[string]string, len(f.fields))
	for _, fl := range f.fields {
		out[fl.key] = strings.TrimSpace(fl.value())
		if fl.kind == passwordField {
			out[fl.key] = fl.value() // las contraseñas no se recortan
		}
	}
	return out
}

func (f *formScreen) submit() tea.Cmd {
	f.busy, f.errMsg = true, ""
	vals, save := f.values(), f.save
	return load(f.id, func() (any, error) { return save(vals) })
}

func (f *formScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case loaded:
		switch v := msg.v.(type) {
		case productInfo:
			if fl := f.field(v.key); fl != nil && fl.looked == v.query {
				fl.found, fl.ok = v.text, v.ok
			}
			return nil
		case pickedProduct:
			if fl := f.field(v.key); fl != nil {
				fl.input.SetValue(v.sku)
				fl.input.CursorEnd()
				fl.sugg.close()
				fl.sugg.query = v.sku
				return f.lookup(fl)
			}
			return nil
		case productMatches:
			if fl := f.field(v.key); fl != nil && fl == f.fields[f.focus] {
				fl.sugg.receive(v)
			}
			return nil
		case prepared:
			f.preparing = false
			if msg.err != nil {
				f.errMsg = describe(msg.err)
				return nil
			}
			if f.ready != nil {
				f.ready(f, v.v)
			}
			return f.focusField(f.focus)
		}
		f.busy = false
		if msg.err != nil {
			f.errMsg = describe(msg.err)
			return nil
		}
		return f.done(msg.v)
	case tea.KeyPressMsg:
		if f.busy || f.preparing {
			if msg.String() == "esc" && !f.busy {
				return back("cancelado; no se guardó nada")
			}
			return nil
		}
		cur := f.fields[f.focus]
		if cur.kind == productField && cur.sugg.active() {
			switch msg.String() {
			case "up", "shift+tab":
				cur.sugg.move(-1)
				return nil
			case "down":
				cur.sugg.move(1)
				return nil
			case "esc":
				cur.sugg.open = false
				return nil
			case "enter", "tab":
				v, _ := cur.sugg.choose()
				cur.input.SetValue(v.SKU.String())
				cur.input.CursorEnd()
				if f.focus == len(f.fields)-1 {
					return f.lookup(cur)
				}
				return f.focusField(f.focus + 1)
			}
		}
		switch msg.String() {
		case "f5":
			if cur.kind == productField {
				return push(newProductPicker(f.app, f.id, cur.key))
			}
		case "esc":
			if f.cancel != nil {
				return f.cancel
			}
			return back("cancelado; no se guardó nada")
		case "f10", "ctrl+s":
			return tea.Batch(f.lookup(cur), f.submit())
		case "up", "shift+tab":
			return f.focusField(f.focus - 1)
		case "down", "tab":
			return f.focusField(f.focus + 1)
		case "enter":
			if f.focus == len(f.fields)-1 {
				return tea.Batch(f.lookup(cur), f.submit())
			}
			return f.focusField(f.focus + 1)
		case "left", "right", " ":
			if cur.kind == choiceField && len(cur.options) > 0 {
				d := 1
				if msg.String() == "left" {
					d = -1
				}
				cur.choice = (cur.choice + d + len(cur.options)) % len(cur.options)
				if f.changed != nil {
					f.changed(f, cur.key)
				}
				return nil
			}
		}
		if cur.kind != choiceField {
			var cmd tea.Cmd
			cur.input, cmd = cur.input.Update(msg)
			if cur.kind == productField {
				return tea.Batch(cmd, cur.sugg.changed(f.app, f.id, cur.key, cur.input.Value()))
			}
			return cmd
		}
	}
	return nil
}

func (f *formScreen) View(width, _ int) string {
	var b strings.Builder
	if f.intro != "" {
		b.WriteString(ansi.Wordwrap(f.intro, width, " ") + "\n\n")
	}
	labelW := 0
	for _, fl := range f.fields {
		labelW = max(labelW, len([]rune(fl.label)))
	}
	for i, fl := range f.fields {
		label := fmt.Sprintf("%-*s", labelW, fl.label)
		var val string
		switch fl.kind {
		case choiceField:
			val = "‹ " + fl.shown() + " ›"
		default:
			val = fl.input.View()
		}
		if i == f.focus {
			b.WriteString(pickedStyle.Render(" "+label+" ") + " " + val + "\n")
		} else {
			b.WriteString(" " + labelStyle.Render(label) + "  " + val + "\n")
		}
		pad := strings.Repeat(" ", labelW+3)
		if fl.kind == productField && i == f.focus && fl.sugg.open {
			b.WriteString(fl.sugg.view(pad))
			continue
		}
		if fl.kind == productField && fl.found != "" {
			if fl.ok {
				b.WriteString(pad + okStyle.Render("→ "+fl.found) + "\n")
			} else {
				b.WriteString(pad + warnStyle.Render(fl.found) + "\n")
			}
		}
	}
	b.WriteString("\n")
	switch {
	case f.preparing:
		b.WriteString("Cargando…")
	case f.busy:
		b.WriteString("Guardando…")
	case f.errMsg != "":
		b.WriteString(failStyle.Render(ansi.Wordwrap(f.errMsg, width, " ")))
	}
	return b.String()
}

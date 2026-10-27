package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
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
)

type field struct {
	key, label string
	kind       fieldKind
	input      textinput.Model
	options    []string
	choice     int
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
}

func (f *formScreen) Title() string   { return f.title }
func (f *formScreen) Info() string    { return f.info }
func (f *formScreen) Capturing() bool { return true }
func (f *formScreen) Help() string {
	return "↑↓ Campo  ←→ Opción  Enter Siguiente  F10 Guardar  Esc Cancelar"
}

func (f *formScreen) Init(id int) tea.Cmd {
	f.id = id
	return f.focusField(0)
}

func (f *formScreen) focusField(i int) tea.Cmd {
	for _, fl := range f.fields {
		fl.input.Blur()
	}
	f.focus = (i + len(f.fields)) % len(f.fields)
	if fl := f.fields[f.focus]; fl.kind != choiceField {
		return fl.input.Focus()
	}
	return nil
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
		f.busy = false
		if msg.err != nil {
			f.errMsg = describe(msg.err)
			return nil
		}
		return f.done(msg.v)
	case tea.KeyPressMsg:
		if f.busy {
			return nil
		}
		cur := f.fields[f.focus]
		switch msg.String() {
		case "esc":
			if f.cancel != nil {
				return f.cancel
			}
			return back("cancelado; no se guardó nada")
		case "f10", "ctrl+s":
			return f.submit()
		case "up", "shift+tab":
			return f.focusField(f.focus - 1)
		case "down", "tab":
			return f.focusField(f.focus + 1)
		case "enter":
			if f.focus == len(f.fields)-1 {
				return f.submit()
			}
			return f.focusField(f.focus + 1)
		case "left", "right", " ":
			if cur.kind == choiceField && len(cur.options) > 0 {
				d := 1
				if msg.String() == "left" {
					d = -1
				}
				cur.choice = (cur.choice + d + len(cur.options)) % len(cur.options)
				return nil
			}
		}
		if cur.kind != choiceField {
			var cmd tea.Cmd
			cur.input, cmd = cur.input.Update(msg)
			return cmd
		}
	}
	return nil
}

func (f *formScreen) View(width, _ int) string {
	var b strings.Builder
	if f.intro != "" {
		b.WriteString(f.intro + "\n\n")
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
			val = "‹ " + fl.value() + " ›"
		default:
			val = fl.input.View()
		}
		if i == f.focus {
			b.WriteString(pickedStyle.Render(" "+label+" ") + " " + val + "\n")
		} else {
			b.WriteString(" " + labelStyle.Render(label) + "  " + val + "\n")
		}
	}
	b.WriteString("\n")
	switch {
	case f.busy:
		b.WriteString("Guardando…")
	case f.errMsg != "":
		b.WriteString(failStyle.Render(f.errMsg))
	}
	return b.String()
}

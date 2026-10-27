package tui

import (
	"errors"
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"

	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#5FAFFF"))
	sectionStyle  = lipgloss.NewStyle().Bold(true)
	selectedStyle = lipgloss.NewStyle().Bold(true).Reverse(true)
	hintStyle     = lipgloss.NewStyle().Faint(true)
	labelStyle    = lipgloss.NewStyle().Faint(true)
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FD75F"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD75F"))
	failStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF5F5F"))
)

// tableStyles marca la fila elegida con inversión: se distingue también
// sin colores.
func tableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.Bold(true).Padding(0, 1)
	s.Cell = s.Cell.Padding(0, 1)
	s.Selected = lipgloss.NewStyle().Bold(true).Reverse(true)
	return s
}

// visibleLen es el ancho en pantalla sin secuencias de estilo.
func visibleLen(s string) int { return lipgloss.Width(s) }

// truncate corta s a width columnas con "…".
func truncate(s string, width int) string {
	if width <= 0 || visibleLen(s) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && visibleLen(string(r))+1 > width {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// describe traduce un error del backend a un mensaje para la barra de
// estado.
func describe(err error) string {
	switch {
	case errors.Is(err, identity.ErrSessionInvalid):
		return "la sesión venció o fue revocada; vuelva a iniciarla"
	case errors.Is(err, identity.ErrDenied):
		return "permiso denegado: " + strings.TrimPrefix(err.Error(), identity.ErrDenied.Error()+": ")
	case errors.Is(err, apporg.ErrNotFound):
		return err.Error()
	}
	return err.Error()
}

// fitColumns ajusta los anchos para que la tabla quepa en width (cada celda
// lleva 2 de relleno): primero encoge la columna flex hasta 12 y luego, de
// la más ancha a la más angosta, las demás hasta el ancho de su título.
func fitColumns(cols []table.Column, flex, width int) []table.Column {
	out := append([]table.Column(nil), cols...)
	over := -width
	for _, c := range out {
		over += c.Width + 2
	}
	if over <= 0 {
		return out
	}
	if cut := min(over, out[flex].Width-12); cut > 0 {
		out[flex].Width -= cut
		over -= cut
	}
	for over > 0 {
		widest, room := -1, 0
		for i, c := range out {
			if r := c.Width - max(visibleLen(c.Title), 4); r > room {
				widest, room = i, r
			}
		}
		if widest < 0 {
			break // no cabe ni al mínimo: la tabla se corta a la derecha
		}
		out[widest].Width--
		over--
	}
	return out
}

// newInput crea un campo de texto con cursor fijo: sin parpadeo no hay
// temporizadores corriendo mientras se escribe.
func newInput(prompt string, limit int) textinput.Model {
	in := textinput.New()
	in.Prompt = prompt
	in.CharLimit = limit
	in.SetWidth(48)
	st := in.Styles()
	st.Cursor.Blink = false
	in.SetStyles(st)
	return in
}

package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
)

// maxSuggestions es cuántas coincidencias se muestran a la vez.
const maxSuggestions = 6

// productMatches son los productos activos que coinciden con lo escrito.
type productMatches struct {
	key, query string
	items      []appcatalog.ProductView
}

// searchProducts busca q (nombre, SKU o código de barras) y entrega las
// coincidencias a la pantalla to.
func searchProducts(a *app, to int, key, q string) tea.Cmd {
	return load(to, func() (any, error) {
		m := productMatches{key: key, query: q}
		list, err := a.opts.Backend.Products(a.ctx, q)
		if err != nil {
			return m, nil // sin sugerencias; al salir del campo se informa
		}
		for _, v := range list {
			if v.Status == catalog.StatusActive {
				m.items = append(m.items, v)
			}
		}
		// La coincidencia exacta (SKU o código de barras) va primero.
		if v, ok := matchProduct(m.items, q); ok && len(m.items) > 1 {
			for i := range m.items {
				if m.items[i].ID == v.ID {
					m.items[0], m.items[i] = m.items[i], m.items[0]
				}
			}
		}
		return m, nil
	})
}

// suggester muestra, mientras se escribe un producto, la lista de los que
// coinciden: ↑/↓ eligen y Enter toma el elegido.
type suggester struct {
	query string // última búsqueda pedida
	items []appcatalog.ProductView
	sel   int
	open  bool
}

// changed busca de nuevo si el texto cambió.
func (s *suggester) changed(a *app, to int, key, text string) tea.Cmd {
	q := strings.TrimSpace(text)
	if q == s.query {
		return nil
	}
	s.query = q
	if q == "" {
		s.close()
		return nil
	}
	return searchProducts(a, to, key, q)
}

// receive aplica un resultado si sigue siendo el de la última búsqueda.
func (s *suggester) receive(m productMatches) {
	if m.query != s.query {
		return
	}
	s.items, s.sel, s.open = m.items, 0, true
}

func (s *suggester) close() { s.items, s.sel, s.open = nil, 0, false }

// active indica que hay sugerencias a la vista para elegir.
func (s *suggester) active() bool { return s.open && len(s.items) > 0 }

func (s *suggester) move(d int) {
	if n := len(s.items); n > 0 {
		s.sel = (s.sel + d + n) % n
	}
}

// choose devuelve el elegido y cierra la lista; deja recordado su SKU para
// no volver a buscarlo.
func (s *suggester) choose() (appcatalog.ProductView, bool) {
	if !s.active() {
		return appcatalog.ProductView{}, false
	}
	v := s.items[s.sel]
	s.close()
	s.query = v.SKU.String()
	return v, true
}

// view dibuja las sugerencias con sangría pad.
func (s *suggester) view(pad string) string {
	if !s.open {
		return ""
	}
	if len(s.items) == 0 {
		return pad + warnStyle.Render("ningún producto activo coincide; pruebe con otra parte del nombre o F5") + "\n"
	}
	var b strings.Builder
	first := max(0, min(s.sel-maxSuggestions+1, len(s.items)-maxSuggestions))
	for i := first; i < len(s.items) && i < first+maxSuggestions; i++ {
		v := s.items[i]
		line := fmt.Sprintf(" %s %s (%s) ", v.SKU, v.Name, v.Unit.Code)
		if i == s.sel {
			b.WriteString(pad + pickedStyle.Render("►"+line) + "\n")
		} else {
			b.WriteString(pad + " " + line + "\n")
		}
	}
	if n := len(s.items); n > maxSuggestions {
		b.WriteString(pad + hintStyle.Render(fmt.Sprintf("%d coincidencias: ↑↓ para ver más, o siga escribiendo", n)) + "\n")
	}
	return b.String()
}

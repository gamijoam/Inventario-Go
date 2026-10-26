package tui_test

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/gamijoam/inventario-go/internal/adapters/tui"
	"github.com/gamijoam/inventario-go/internal/application/admin"
	"github.com/gamijoam/inventario-go/internal/platform/buildinfo"
)

var (
	keyUp    = tea.KeyPressMsg{Code: tea.KeyUp}
	keyDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	keyQ     = tea.KeyPressMsg{Code: 'q', Text: "q"}
	keyR     = tea.KeyPressMsg{Code: 'r', Text: "r"}
)

func newModel(doctorCalls *int) tea.Model {
	m := tui.New(context.Background(), tui.Options{
		Build: buildinfo.Info{Version: "v0.0.0-test", GoVersion: "go1.27.3", OS: "linux", Arch: "amd64"},
		Doctor: func(context.Context) admin.Report {
			*doctorCalls++
			return admin.Report{Checks: []admin.Result{{Name: "timezone", Status: admin.StatusOK, Detail: "America/Caracas"}}}
		},
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next
}

// send aplica msg y ejecuta sincrónicamente el comando resultante, como
// haría el runtime de Bubble Tea; devuelve el mensaje final producido.
func send(t *testing.T, m tea.Model, msg tea.Msg) (tea.Model, tea.Msg) {
	t.Helper()
	next, cmd := m.Update(msg)
	if cmd == nil {
		return next, nil
	}
	out := cmd()
	if _, quit := out.(tea.QuitMsg); quit {
		return next, out
	}
	next, _ = next.Update(out)
	return next, out
}

func content(m tea.Model) string { return m.View().Content }

func TestMenuNavigationWrapsAround(t *testing.T) {
	calls := 0
	m := newModel(&calls)
	if !strings.Contains(content(m), "> Diagnóstico") {
		t.Fatalf("el cursor debe empezar en Diagnóstico:\n%s", content(m))
	}
	m, _ = send(t, m, keyUp)
	if !strings.Contains(content(m), "> Salir") {
		t.Fatalf("↑ desde el primero debe ir al último:\n%s", content(m))
	}
	m, _ = send(t, m, keyDown)
	m, _ = send(t, m, keyDown)
	if !strings.Contains(content(m), "> Versión") {
		t.Fatalf("↓ debe avanzar:\n%s", content(m))
	}
}

func TestDoctorScreenRunsDiagnosticsOnceAndCanRepeat(t *testing.T) {
	calls := 0
	m := newModel(&calls)
	m, _ = send(t, m, keyEnter)
	if calls != 1 || !strings.Contains(content(m), "timezone") || !strings.Contains(content(m), "[ OK ]") {
		t.Fatalf("diagnóstico no mostrado (llamadas=%d):\n%s", calls, content(m))
	}
	m, _ = send(t, m, keyEsc)
	m, _ = send(t, m, keyEnter)
	if calls != 1 {
		t.Fatalf("volver a entrar no debe repetir el diagnóstico (llamadas=%d)", calls)
	}
	_, _ = send(t, m, keyR)
	if calls != 2 {
		t.Fatalf("r debe repetir el diagnóstico (llamadas=%d)", calls)
	}
}

func TestVersionScreen(t *testing.T) {
	calls := 0
	m := newModel(&calls)
	m, _ = send(t, m, keyDown)
	m, _ = send(t, m, keyEnter)
	if v := content(m); !strings.Contains(v, "v0.0.0-test") || !strings.Contains(v, "desconocido") {
		t.Fatalf("pantalla de versión incompleta:\n%s", v)
	}
	m, _ = send(t, m, keyEsc)
	if !strings.Contains(content(m), "> Versión") {
		t.Fatalf("esc debe volver al menú conservando el cursor:\n%s", content(m))
	}
}

func TestQuitKeys(t *testing.T) {
	cases := map[string][]tea.Msg{
		"q":              {keyQ},
		"ctrl+c":         {tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}},
		"esc en el menú": {keyEsc},
		"opción Salir":   {keyUp, keyEnter},
	}
	for name, keys := range cases {
		calls := 0
		m := newModel(&calls)
		var last tea.Msg
		for _, k := range keys {
			m, last = send(t, m, k)
		}
		if _, ok := last.(tea.QuitMsg); !ok {
			t.Errorf("%s: se esperaba QuitMsg, obtuvo %T", name, last)
		}
	}
}

func TestSmallTerminalShowsWarning(t *testing.T) {
	calls := 0
	m := newModel(&calls)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: tui.MinWidth - 1, Height: 24})
	if v := content(m); !strings.Contains(v, "demasiado pequeña") || strings.Contains(v, "Diagnóstico") {
		t.Fatalf("se esperaba aviso de terminal pequeña:\n%s", v)
	}
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(content(m), "Diagnóstico") {
		t.Fatalf("al agrandar debe volver el menú")
	}
}

func TestViewUsesAltScreen(t *testing.T) {
	calls := 0
	if v := newModel(&calls).View(); !v.AltScreen {
		t.Fatalf("la TUI debe usar la pantalla alternativa para no ensuciar el historial")
	}
}

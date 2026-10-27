package cli_test

import (
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
)

func TestPricesFromTheCLI(t *testing.T) {
	admin := setupSQLite(t)
	mustRun(t, admin, "product", "create", "--name", "Harina", "--sku", "HARINA")
	mustRun(t, admin, "purchase", "supplier", "create", "--code", "dist", "--name", "Distribuidora")
	r := run(t, admin, "price-list", "create", "--code", "general", "--name", "General", "--margin", "20")
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "lista creada: GENERAL General en USD (margen objetivo 20 %)") {
		t.Fatalf("price-list create: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, admin, "price-list", "create", "--code", "x", "--name", "X", "--margin", "100"); r.code != cli.ExitUsage {
		t.Errorf("margen 100: %d", r.code)
	}
	mustRun(t, admin, "price-list", "margin", "--list", "general", "--product", "HARINA", "--margin", "25")
	if r := run(t, admin, "price-list", "margin", "--list", "general"); r.code != cli.ExitUsage {
		t.Errorf("sin --margin ni --clear: %d", r.code)
	}

	// Precio inicial a mano.
	p := decode(t, mustRun(t, admin, "price", "propose", "--list", "general", "--product", "HARINA", "--price", "10", "--json").stdout)
	if p["schema"] != "balanzapro.price-proposal/v1" || p["status"] != "pending" || p["number"] != "PP-000001" {
		t.Fatalf("propose: %v", p)
	}
	if r := run(t, admin, "price", "approve", "PP-000001"); !strings.Contains(r.stdout, "revisión 1 publicada: rige desde ya") {
		t.Fatalf("approve: %s %s", r.stdout, r.stderr)
	}

	// La recepción deja una propuesta, no cambia el precio.
	rc := decode(t, mustRun(t, admin, "purchase", "receipt", "create", "--supplier", "dist", "--json").stdout)["number"].(string)
	mustRun(t, admin, "purchase", "receipt", "line", rc, "--product", "HARINA", "--qty", "10", "--unit-cost", "9")
	r = run(t, admin, "purchase", "receipt", "confirm", rc)
	if !strings.Contains(r.stdout, "precio sugerido PP-000002 en GENERAL para HARINA: 10 → 12 USD; pendiente de aprobación") { // 9 ÷ 0.75
		t.Fatalf("confirmar: %s", r.stdout)
	}
	if r := run(t, admin, "price", "show", "--product", "HARINA"); !strings.Contains(r.stdout, "GENERAL  HARINA  10") || !strings.Contains(r.stdout, "12 (PP-000002)") {
		t.Errorf("show:\n%s", r.stdout)
	}
	r = run(t, admin, "price", "proposal", "PP-000002")
	if !strings.Contains(r.stdout, "referencia: último costo de compra 9 USD; margen objetivo 25 %; margen resultante 25 %") {
		t.Errorf("proposal:\n%s", r.stdout)
	}

	// El almacenista propone pero no aprueba.
	alm := user(t, admin, "alm1", "ALMACENISTA")
	if r := run(t, alm, "price", "approve", "PP-000002"); r.code != cli.ExitForbidden {
		t.Errorf("almacenista aprueba: %d", r.code)
	}
	r = run(t, admin, "price", "approve", "PP-000002", "--at", "2099-01-01 08:00", "--json")
	a := decode(t, r.stdout)
	if a["status"] != "approved" || a["revision"].(map[string]any)["effective_at"] != "2099-01-01T12:00:00Z" {
		t.Fatalf("approve programado: %v", a)
	}
	s := decode(t, mustRun(t, admin, "price", "show", "--list", "general", "--json").stdout)["items"].([]any)[0].(map[string]any)
	if s["current"].(map[string]any)["price"] != "10" || len(s["scheduled"].([]any)) != 1 {
		t.Fatalf("show json: %v", s)
	}
	rev := a["revision"].(map[string]any)["id"].(string)
	if r := run(t, admin, "price", "cancel", rev); r.code != cli.ExitOK || !strings.Contains(r.stdout, "revisión 2 anulada") {
		t.Errorf("cancel: %d %s %s", r.code, r.stdout, r.stderr)
	}

	// Bajo el costo exige el permiso extraordinario.
	mustRun(t, admin, "price", "propose", "--list", "general", "--product", "HARINA", "--price", "5")
	if r := run(t, admin, "price", "approve", "PP-000003"); r.code != cli.ExitForbidden || !strings.Contains(r.stderr, "pricing.override.approve_below_cost") {
		t.Errorf("bajo costo: %d %s", r.code, r.stderr)
	}
	mustRun(t, admin, "price", "reject", "PP-000003", "--note", "bajo costo")
	if l := decode(t, mustRun(t, admin, "price", "proposals", "--status", "rejected", "--json").stdout); len(l["items"].([]any)) != 1 {
		t.Errorf("proposals: %v", l)
	}
}

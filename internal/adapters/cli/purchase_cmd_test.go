package cli_test

import (
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
)

func TestPurchasingFromTheCLI(t *testing.T) {
	admin := setupSQLite(t)
	mustRun(t, admin, "product", "create", "--name", "Harina", "--sku", "HARINA")
	mustRun(t, admin, "product", "create", "--name", "Arroz", "--sku", "ARROZ")
	s := decode(t, mustRun(t, admin, "purchase", "supplier", "create", "--code", "dist", "--name", "Distribuidora, C.A.", "--rif", "J-12345678-9", "--json").stdout)
	if s["schema"] != "balanzapro.supplier/v1" || s["code"] != "DIST" {
		t.Fatalf("proveedor: %v", s)
	}
	if r := run(t, admin, "purchase", "supplier", "create", "--code", "dist", "--name", "Otra"); r.code != cli.ExitConflict {
		t.Errorf("código repetido: %d", r.code)
	}
	if r := run(t, admin, "purchase", "order", "create", "--supplier", "dist", "--line", "HARINA:10"); r.code != cli.ExitUsage {
		t.Errorf("línea mal formada: %d", r.code)
	}
	o := decode(t, mustRun(t, admin, "purchase", "order", "create", "--supplier", "dist", "--line", "HARINA:10:80", "--line", "ARROZ:20:1", "--json").stdout)
	if o["number"] != "OC-000001" || o["status"] != "open" || len(o["lines"].([]any)) != 2 {
		t.Fatalf("orden: %v", o)
	}
	rc := decode(t, mustRun(t, admin, "purchase", "receipt", "create", "--order", "OC-000001", "--document", "FAC-77", "--json").stdout)
	number := rc["number"].(string)
	if number != "RC-000001" || rc["status"] != "draft" || len(rc["lines"].([]any)) != 2 || rc["supplier_code"] != "DIST" {
		t.Fatalf("recepción: %v", rc)
	}
	mustRun(t, admin, "purchase", "receipt", "line", number, "--product", "ARROZ", "--qty", "20", "--unit-cost", "1")
	mustRun(t, admin, "purchase", "receipt", "expense", number, "--concept", "Flete", "--amount", "10")
	mustRun(t, admin, "purchase", "receipt", "expense", number, "--concept", "Seguro", "--amount", "3", "--by", "quantity")
	if r := run(t, admin, "purchase", "receipt", "expense", number, "--concept", "X", "--amount", "1", "--by", "peso"); r.code != cli.ExitUsage {
		t.Errorf("criterio inválido: %d", r.code)
	}
	r := run(t, admin, "purchase", "receipt", "confirm", number)
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "confirmada") || !strings.Contains(r.stdout, "2 movimientos registrados") {
		t.Fatalf("confirmar: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, admin, "purchase", "receipt", "confirm", number); !strings.Contains(r.stdout, "ya lo estaba") {
		t.Errorf("confirmar dos veces: %s", r.stdout)
	}
	// Harina: 800 + flete por valor 10 × 800/820 = 9.756098 + seguro por
	// cantidad 3 × 10/30 = 1 → 810.756098 por 10.
	st := decode(t, mustRun(t, admin, "inventory", "stock", "--product", "HARINA", "--json").stdout)["items"].([]any)[0].(map[string]any)
	if st["value"] != "810.756098" || st["unit_cost"] != "81.0756098" {
		t.Fatalf("costo capitalizado: %v", st)
	}
	if o := decode(t, mustRun(t, admin, "purchase", "order", "show", "OC-000001", "--json").stdout); o["status"] != "received" {
		t.Errorf("orden recibida: %v", o["status"])
	}
	if r := run(t, admin, "inventory", "kardex", "--product", "HARINA", "--json"); !strings.Contains(r.stdout, `"kind": "purchase_in"`) {
		t.Errorf("kardex: %s", r.stdout)
	}

	// Gasto tardío con parte ya consumida.
	mustRun(t, admin, "inventory", "out", "--product", "HARINA", "--qty", "5", "--reason", "internal_use")
	r = run(t, admin, "purchase", "receipt", "expense", number, "--concept", "Aduana", "--amount", "20", "--json")
	late := decode(t, r.stdout)["expenses"].([]any)[2].(map[string]any)
	if late["late"] != true || len(decode(t, r.stdout)["movements"].([]any)) != 2 {
		t.Fatalf("gasto tardío: %s", r.stdout)
	}
	if r := run(t, admin, "purchase", "receipt", "show", number); !strings.Contains(r.stdout, "Aduana (tardío)") || !strings.Contains(r.stdout, "diferencia") {
		t.Errorf("show: %s", r.stdout)
	}

	// Costos separados.
	mustRun(t, admin, "purchase", "cost", "set-replacement", "--product", "HARINA", "--unit-cost", "85", "--note", "cotización")
	r = run(t, admin, "purchase", "cost", "show", "--product", "HARINA")
	for _, want := range []string{"último costo de compra: 80", "costo de reposición estimado: 85", "costo promedio en PRINCIPAL/PRINCIPAL/PRINCIPAL"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("cost show sin %q:\n%s", want, r.stdout)
		}
	}
	c := decode(t, mustRun(t, admin, "purchase", "cost", "show", "--product", "HARINA", "--json").stdout)
	if c["schema"] != "balanzapro.product-costs/v1" || c["last_purchase"].(map[string]any)["receipt_number"] != number || len(c["replacement_costs"].([]any)) != 1 {
		t.Fatalf("cost show json: %v", c)
	}
	if r := run(t, admin, "purchase", "receipt", "list", "--json"); len(decode(t, r.stdout)["items"].([]any)) != 1 {
		t.Errorf("list: %s", r.stdout)
	}
	if r := run(t, admin, "inventory", "verify"); r.code != cli.ExitOK {
		t.Errorf("verify: %d %s", r.code, r.stdout)
	}

	// Permisos: el almacenista prepara pero no confirma.
	alm := user(t, admin, "alm1", "ALMACENISTA")
	rc2 := decode(t, mustRun(t, alm, "purchase", "receipt", "create", "--supplier", "dist", "--json").stdout)["number"].(string)
	mustRun(t, alm, "purchase", "receipt", "line", rc2, "--product", "ARROZ", "--qty", "1", "--unit-cost", "1")
	if r := run(t, alm, "purchase", "receipt", "confirm", rc2); r.code != cli.ExitForbidden {
		t.Errorf("almacenista confirma: %d", r.code)
	}
	if r := run(t, alm, "purchase", "supplier", "create", "--code", "x", "--name", "X"); r.code != cli.ExitForbidden {
		t.Errorf("almacenista crea proveedor: %d", r.code)
	}
	mustRun(t, admin, "purchase", "receipt", "cancel", rc2)
}

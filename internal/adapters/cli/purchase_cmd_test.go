package cli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
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
	if r := run(t, admin, "inventory", "kardex", "--product", "HARINA"); !strings.Contains(r.stdout, "compra") || !strings.Contains(r.stdout, "revaloración") {
		t.Errorf("kardex sin el tipo de compra o revaloración:\n%s", r.stdout)
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
	if c["schema"] != "balanzapro.product-costs/v2" || c["last_purchase"].(map[string]any)["receipt_number"] != number || len(c["replacement_costs"].([]any)) != 1 {
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

func TestRatesAndForeignPurchasesFromTheCLI(t *testing.T) {
	admin := setupSQLite(t)
	mustRun(t, admin, "product", "create", "--name", "Harina", "--sku", "HARINA")
	mustRun(t, admin, "purchase", "supplier", "create", "--code", "dist", "--name", "Distribuidora")
	// Tasas ficticias: una de ayer y otra de hoy.
	today := timezone.DateOf(time.Now())
	yesterday := today.AddDate(0, 0, -1).Format(timezone.DateLayout)
	r := run(t, admin, "rate", "set", "--base", "USD", "--quote", "VES", "--rate", "40", "--source", "official", "--label", "BCV", "--date", yesterday)
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "1 USD = 40 VES (oficial BCV), vigente desde "+yesterday+", versión 1") {
		t.Fatalf("rate set: %d %s %s", r.code, r.stdout, r.stderr)
	}
	x := decode(t, mustRun(t, admin, "rate", "set", "--base", "USD", "--quote", "VES", "--rate", "50", "--source", "manual", "--label", "Gerencia", "--json").stdout)
	if x["schema"] != "balanzapro.exchange-rate/v1" || x["rate"] != "50" || x["version"] != float64(2) || x["source_type"] != "manually_entered" {
		t.Fatalf("rate set --json: %v", x)
	}
	if r := run(t, admin, "rate", "set", "--base", "USD", "--quote", "USD", "--rate", "1", "--source", "official", "--label", "X"); r.code != cli.ExitUsage {
		t.Errorf("par inválido: %d", r.code)
	}
	if r := run(t, admin, "rate", "show", "--date", yesterday); !strings.Contains(r.stdout, "1 USD = 40 VES") {
		t.Errorf("rate show ayer: %s", r.stdout)
	}
	if l := decode(t, mustRun(t, admin, "rate", "list", "--pair", "USD/VES", "--json").stdout); len(l["items"].([]any)) != 2 {
		t.Errorf("rate list: %v", l)
	}
	if r := run(t, admin, "rate", "show", "--date", "2000-01-01"); r.code != cli.ExitConflict {
		t.Errorf("sin tasa: %d %s", r.code, r.stderr)
	}

	// Factura de ayer en VES: la tasa de ayer, aunque hoy rija otra.
	rc := decode(t, mustRun(t, admin, "purchase", "receipt", "create", "--supplier", "dist", "--currency", "VES", "--date", yesterday, "--json").stdout)
	number := rc["number"].(string)
	if rc["schema"] != "balanzapro.purchase-receipt/v2" || rc["currency"] != "VES" || rc["valuation_currency"] != "USD" || rc["document_date"] != yesterday {
		t.Fatalf("recepción: %v", rc)
	}
	mustRun(t, admin, "purchase", "receipt", "line", number, "--product", "HARINA", "--qty", "10", "--unit-cost", "400")
	mustRun(t, admin, "purchase", "receipt", "expense", number, "--concept", "Flete", "--amount", "2", "--currency", "USD")
	c := decode(t, mustRun(t, admin, "purchase", "receipt", "confirm", number, "--json").stdout)
	line := c["lines"].([]any)[0].(map[string]any)
	if c["rate"].(map[string]any)["rate"] != "40" || line["valued_cost"] != "100" || line["total_cost"] != "102" {
		t.Fatalf("confirmada: %v", c)
	}
	r = run(t, admin, "purchase", "receipt", "show", number)
	for _, want := range []string{"documento del " + yesterday + " en VES; tasa aplicada 1 USD = 40 VES (BCV", "gasto Flete del " + yesterday + ": 2 USD"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("show sin %q:\n%s", want, r.stdout)
		}
	}
	r = run(t, admin, "purchase", "cost", "show", "--product", "HARINA")
	if !strings.Contains(r.stdout, "último costo de compra: 400 VES = 10 (10.2 con gastos)") {
		t.Errorf("cost show:\n%s", r.stdout)
	}

	// El cajero consulta tasas, pero no las registra.
	caja := user(t, admin, "caja1", "CAJERO")
	mustRun(t, caja, "rate", "show")
	if r := run(t, caja, "rate", "set", "--base", "USD", "--quote", "VES", "--rate", "1", "--source", "official", "--label", "X"); r.code != cli.ExitForbidden {
		t.Errorf("cajero registra: %d", r.code)
	}
}

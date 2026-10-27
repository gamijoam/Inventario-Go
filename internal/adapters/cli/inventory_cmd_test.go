package cli_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
)

func TestInventoryFromTheCLI(t *testing.T) {
	o := setupSQLite(t)
	for _, args := range [][]string{
		{"product", "create", "--name", "Queso blanco", "--sku", "QUESO", "--unit", "KG"},
		{"product", "create", "--name", "Harina", "--sku", "HARINA", "--barcode", "7591234567894"},
	} {
		if r := run(t, o, args...); r.code != cli.ExitOK {
			t.Fatal(r.stderr)
		}
	}
	// Sin --company/--branch/--warehouse: el único almacén de esta sucursal.
	r := run(t, o, "inventory", "in", "--product", "queso", "--qty", "12.5", "--reason", "initial_load", "--json")
	v := decode(t, r.stdout)
	if r.code != cli.ExitOK || v["schema"] != "balanzapro.movement/v1" || v["quantity"] != "12.500" || v["on_hand_after"] != "12.500" ||
		v["sequence"] != float64(1) || v["warehouse_code"] != "PRINCIPAL" || v["replayed"] != false {
		t.Fatalf("in: %d %s %s", r.code, r.stdout, r.stderr)
	}
	first := v["id"].(string)
	if r := run(t, o, "inventory", "out", "--product", "QUESO", "--qty", "0.125", "--reason", "shrinkage", "--note", "se partió"); r.code != cli.ExitOK ||
		!strings.Contains(r.stdout, "existencia 12.375 KG") {
		t.Fatalf("out: %d %s %s", r.code, r.stdout, r.stderr)
	}
	r = run(t, o, "inventory", "out", "--product", "QUESO", "--qty", "20", "--reason", "correction", "--json")
	if r.code != cli.ExitInsufficientStock || decode(t, r.stderr)["error"].(map[string]any)["kind"] != "insufficient_stock" {
		t.Fatalf("salida sin stock: %d %s", r.code, r.stderr)
	}

	// Reintento seguro con --key.
	args := []string{"inventory", "in", "--product", "7591234567894", "--qty", "30", "--reason", "initial_load", "--key", "carga-harina"}
	if r := run(t, o, args...); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	if r := run(t, o, args...); r.code != cli.ExitOK || !strings.Contains(r.stdout, "ya registrado antes") {
		t.Fatalf("reintento: %d %s", r.code, r.stdout)
	}
	if r := run(t, o, "inventory", "in", "--product", "HARINA", "--qty", "31", "--reason", "initial_load", "--key", "carga-harina"); r.code != cli.ExitConflict {
		t.Errorf("misma clave, otra cantidad: %d", r.code)
	}

	// Reverso.
	r = run(t, o, "inventory", "reverse", first, "--note", "cargué el kilo equivocado", "--json")
	// 12.375 en existencia: revertir la entrada de 12.5 no alcanza.
	if r.code != cli.ExitInsufficientStock {
		t.Fatalf("reverso de una entrada ya consumida: %d %s", r.code, r.stderr)
	}
	if r := run(t, o, "inventory", "in", "--product", "QUESO", "--qty", "0.125", "--reason", "correction"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	if r := run(t, o, "inventory", "reverse", first, "--note", "cargué el kilo equivocado"); r.code != cli.ExitOK || !strings.Contains(r.stdout, "reverso registrado") {
		t.Fatalf("reverso: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, o, "inventory", "reverse", first); r.code != cli.ExitConflict {
		t.Errorf("doble reverso: %d", r.code)
	}

	// Consultas.
	items := decode(t, run(t, o, "inventory", "stock", "--json").stdout)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("stock = %v", items)
	}
	for _, it := range items {
		b := it.(map[string]any)
		want := map[string]string{"QUESO": "0.000", "HARINA": "30"}[b["sku"].(string)]
		if b["on_hand"] != want || b["available"] != want || b["reserved"] == nil {
			t.Errorf("saldo %v", b)
		}
	}
	k := decode(t, run(t, o, "inventory", "kardex", "--product", "QUESO", "--json").stdout)
	if len(k["items"].([]any)) != 4 || k["balance"].(map[string]any)["version"] != float64(4) {
		t.Fatalf("kardex = %v", k)
	}
	text := run(t, o, "inventory", "kardex", "--product", "QUESO").stdout
	for _, want := range []string{"reverso", "shrinkage", "se partió", "-12.500"} {
		if !strings.Contains(text, want) {
			t.Errorf("kardex sin %q:\n%s", want, text)
		}
	}
	if r := run(t, o, "inventory", "verify"); r.code != cli.ExitOK || !strings.Contains(r.stdout, "2 saldos y 5 movimientos verificados, 0 con diferencias") {
		t.Errorf("verify: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, o, "inventory", "reasons"); r.code != cli.ExitOK || !strings.Contains(r.stdout, "internal_use") {
		t.Errorf("reasons: %s", r.stdout)
	}

	// Validaciones de uso.
	for name, args := range map[string][]string{
		"decimales de más":    {"inventory", "in", "--product", "HARINA", "--qty", "1.5", "--reason", "correction"},
		"motivo inválido":     {"inventory", "in", "--product", "HARINA", "--qty", "1", "--reason", "shrinkage"},
		"cantidad negativa":   {"inventory", "in", "--product", "HARINA", "--qty", "-1", "--reason", "correction"},
		"falta el producto":   {"inventory", "in", "--qty", "1", "--reason", "correction"},
		"kardex sin producto": {"inventory", "kardex"},
	} {
		if r := run(t, o, args...); r.code != cli.ExitUsage {
			t.Errorf("%s: %d %s", name, r.code, r.stderr)
		}
	}
}

func TestCashierSeesButDoesNotAdjustStock(t *testing.T) {
	admin := setupSQLite(t)
	run(t, admin, "product", "create", "--name", "Arroz", "--sku", "ARR")
	run(t, admin, "inventory", "in", "--product", "ARR", "--qty", "10", "--reason", "initial_load")
	run(t, withStdin(admin, userPassword), "user", "create", "--username", "caja1", "--name", "Caja", "--password-stdin")
	if r := run(t, admin, "user", "assign", "--username", "caja1", "--role", "CAJERO", "--company", "principal", "--branch", "principal"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	caja := asUser(t, admin, "caja1", userPassword)
	if r := run(t, caja, "inventory", "stock"); r.code != cli.ExitOK || !strings.Contains(r.stdout, "ARR") {
		t.Errorf("stock: %d %s %s", r.code, r.stdout, r.stderr)
	}
	r := run(t, caja, "inventory", "out", "--product", "ARR", "--qty", "1", "--reason", "internal_use")
	if r.code != cli.ExitForbidden || !strings.Contains(r.stderr, "inventory.stock.adjust") {
		t.Errorf("cajero ajusta: %d %s", r.code, r.stderr)
	}
	if r := run(t, caja, "inventory", "verify"); r.code != cli.ExitForbidden {
		t.Errorf("verify sin ámbito de organización: %d", r.code)
	}
}

func TestInventoryVerifyDetectsTampering(t *testing.T) {
	o, path := sqliteEnv(t)
	if r := run(t, withStdin(o, adminPassword), "setup", "--name", "Ana", "--password-stdin"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	run(t, o, "product", "create", "--name", "Arroz", "--sku", "ARR")
	run(t, o, "inventory", "in", "--product", "ARR", "--qty", "10", "--reason", "initial_load")
	// Alguien con acceso al archivo cambia el saldo sin pasar por el kardex.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE inventory_balances SET on_hand = '99', version = version + 1"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	r := run(t, o, "inventory", "verify")
	if r.code != cli.ExitCheckFailed || !strings.Contains(r.stdout, "[FAIL] PRINCIPAL/PRINCIPAL/PRINCIPAL ARR") || !strings.Contains(r.stdout, "1 con diferencias") {
		t.Fatalf("verify: %d %s %s", r.code, r.stdout, r.stderr)
	}
	v := decode(t, run(t, o, "inventory", "verify", "--json").stdout)
	if len(v["problems"].([]any)) != 1 {
		t.Fatalf("verify JSON = %v", v)
	}
}

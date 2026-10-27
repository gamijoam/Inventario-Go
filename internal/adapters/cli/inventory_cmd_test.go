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
	r := run(t, o, "inventory", "in", "--product", "queso", "--qty", "12.5", "--unit-cost", "4", "--reason", "initial_load", "--json")
	v := decode(t, r.stdout)
	if r.code != cli.ExitOK || v["schema"] != "balanzapro.movement/v1" || v["quantity"] != "12.500" || v["on_hand_after"] != "12.500" ||
		v["sequence"] != float64(1) || v["warehouse_code"] != "PRINCIPAL" || v["replayed"] != false ||
		v["cost"] != "50" || v["unit_cost"] != "4" || v["value_after"] != "50" {
		t.Fatalf("in: %d %s %s", r.code, r.stdout, r.stderr)
	}
	first := v["id"].(string)
	if r := run(t, o, "inventory", "out", "--product", "QUESO", "--qty", "0.125", "--reason", "shrinkage", "--note", "se partió"); r.code != cli.ExitOK ||
		!strings.Contains(r.stdout, "existencia 12.375 KG, valor 49.50 USD") || !strings.Contains(r.stdout, "costo -0.50 USD") {
		t.Fatalf("out: %d %s %s", r.code, r.stdout, r.stderr)
	}
	r = run(t, o, "inventory", "out", "--product", "QUESO", "--qty", "20", "--reason", "correction", "--json")
	if r.code != cli.ExitInsufficientStock || decode(t, r.stderr)["error"].(map[string]any)["kind"] != "insufficient_stock" {
		t.Fatalf("salida sin stock: %d %s", r.code, r.stderr)
	}

	// Reintento seguro con --key.
	args := []string{"inventory", "in", "--product", "7591234567894", "--qty", "30", "--unit-cost", "1.25", "--reason", "initial_load", "--key", "carga-harina"}
	if r := run(t, o, args...); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	if r := run(t, o, args...); r.code != cli.ExitOK || !strings.Contains(r.stdout, "ya registrado antes") {
		t.Fatalf("reintento: %d %s", r.code, r.stdout)
	}
	if r := run(t, o, "inventory", "in", "--product", "HARINA", "--qty", "31", "--unit-cost", "1.25", "--reason", "initial_load", "--key", "carga-harina"); r.code != cli.ExitConflict {
		t.Errorf("misma clave, otra cantidad: %d", r.code)
	}
	if r := run(t, o, "inventory", "in", "--product", "HARINA", "--qty", "30", "--unit-cost", "1.5", "--reason", "initial_load", "--key", "carga-harina"); r.code != cli.ExitConflict {
		t.Errorf("misma clave, otro costo: %d", r.code)
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
		value := map[string]string{"QUESO": "0", "HARINA": "37.5"}[b["sku"].(string)]
		if b["on_hand"] != want || b["available"] != want || b["reserved"] == nil || b["value"] != value || b["currency"] != "USD" {
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
	if r := run(t, o, "inventory", "verify"); r.code != cli.ExitOK || !strings.Contains(r.stdout, "2 saldos, 5 movimientos y 0 transferencias verificados, 0 con diferencias, 0 sin valorar") {
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
		"costo negativo":      {"inventory", "in", "--product", "HARINA", "--qty", "1", "--unit-cost", "-1", "--reason", "correction"},
		"salida con costo":    {"inventory", "out", "--product", "HARINA", "--qty", "1", "--unit-cost", "1", "--reason", "correction"},
	} {
		if r := run(t, o, args...); r.code != cli.ExitUsage {
			t.Errorf("%s: %d %s", name, r.code, r.stderr)
		}
	}
}

func TestCashierSeesButDoesNotAdjustStock(t *testing.T) {
	admin := setupSQLite(t)
	run(t, admin, "product", "create", "--name", "Arroz", "--sku", "ARR")
	run(t, admin, "inventory", "in", "--product", "ARR", "--qty", "10", "--unit-cost", "1", "--reason", "initial_load")
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
	run(t, o, "inventory", "in", "--product", "ARR", "--qty", "10", "--unit-cost", "1", "--reason", "initial_load")
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

func TestTransferAndValuationFromTheCLI(t *testing.T) {
	o, _ := sqliteEnv(t)
	if r := run(t, withStdin(o, adminPassword), "setup", "--name", "Bodega Ana", "--currency", "ves", "--password-stdin"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	if r := run(t, o, "warehouse", "create", "--code", "deposito", "--name", "Depósito"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	run(t, o, "product", "create", "--name", "Harina", "--sku", "HARINA")
	for _, args := range [][]string{
		{"inventory", "in", "--warehouse", "principal", "--product", "HARINA", "--qty", "10", "--unit-cost", "80", "--reason", "initial_load"},
		{"inventory", "in", "--warehouse", "principal", "--product", "HARINA", "--qty", "5", "--unit-cost", "95", "--reason", "correction"},
		{"inventory", "in", "--warehouse", "deposito", "--product", "HARINA", "--qty", "6", "--unit-cost", "100", "--reason", "initial_load"},
	} {
		if r := run(t, o, args...); r.code != cli.ExitOK {
			t.Fatalf("%v: %s", args, r.stderr)
		}
	}
	// Sin costo y sin CPP: código 2 con un mensaje claro.
	run(t, o, "product", "create", "--name", "Nuevo", "--sku", "NUEVO")
	if r := run(t, o, "inventory", "in", "--warehouse", "principal", "--product", "NUEVO", "--qty", "1", "--reason", "initial_load"); r.code != cli.ExitUsage ||
		!strings.Contains(r.stderr, "indique el costo unitario") {
		t.Errorf("entrada sin costo: %d %s", r.code, r.stderr)
	}

	r := run(t, o, "inventory", "transfer", "--from", "principal", "--to", "deposito", "--product", "HARINA", "--qty", "4", "--key", "t1", "--json")
	v := decode(t, r.stdout)
	if r.code != cli.ExitOK || v["schema"] != "balanzapro.transfer/v1" || v["value"] != "340" || v["currency"] != "VES" ||
		v["out"].(map[string]any)["value_after"] != "935" || v["in"].(map[string]any)["value_after"] != "940" || v["replayed"] != false {
		t.Fatalf("transfer: %d %s %s", r.code, r.stdout, r.stderr)
	}
	r = run(t, o, "inventory", "transfer", "--from", "principal", "--to", "deposito", "--product", "HARINA", "--qty", "4", "--key", "t1")
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "ya registrada antes") {
		t.Errorf("reintento: %d %s", r.code, r.stdout)
	}
	if r := run(t, o, "inventory", "transfer", "--from", "principal", "--to", "deposito", "--product", "HARINA", "--qty", "1"); r.code != cli.ExitOK ||
		!strings.Contains(r.stdout, "transferencia registrada: 1 UND HARINA de PRINCIPAL a DEPOSITO por 85.00 VES") {
		t.Errorf("texto: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, o, "inventory", "transfer", "--from", "principal", "--to", "deposito", "--product", "HARINA", "--qty", "99"); r.code != cli.ExitInsufficientStock {
		t.Errorf("de más: %d", r.code)
	}
	if r := run(t, o, "inventory", "transfer", "--from", "principal", "--to", "principal", "--product", "HARINA", "--qty", "1"); r.code != cli.ExitUsage {
		t.Errorf("mismo almacén: %d", r.code)
	}
	if r := run(t, o, "inventory", "transfer", "--to", "deposito", "--product", "HARINA", "--qty", "1"); r.code != cli.ExitUsage {
		t.Errorf("sin --from: %d", r.code)
	}

	text := run(t, o, "inventory", "stock").stdout
	for _, want := range []string{"CPP", "VALOR", "MONEDA", "85.000000", "850.00", "93.181818", "1025.00", "VES"} {
		if !strings.Contains(text, want) {
			t.Errorf("stock sin %q:\n%s", want, text)
		}
	}
	k := run(t, o, "inventory", "kardex", "--warehouse", "deposito", "--product", "HARINA").stdout
	for _, want := range []string{"valor 1025.00 VES, CPP 93.181818", "transf. entrada", "transfer", "340.00", "940.00"} {
		if !strings.Contains(k, want) {
			t.Errorf("kardex sin %q:\n%s", want, k)
		}
	}
	// Un tramo de transferencia no se revierte solo.
	in := decode(t, run(t, o, "inventory", "kardex", "--warehouse", "deposito", "--product", "HARINA", "--json").stdout)["items"].([]any)[1].(map[string]any)
	if in["transfer_id"] == nil || in["kind"] != "transfer_in" {
		t.Fatalf("tramo = %v", in)
	}
	if r := run(t, o, "inventory", "reverse", in["id"].(string)); r.code != cli.ExitUsage {
		t.Errorf("revertir un tramo: %d %s", r.code, r.stderr)
	}
	if r := run(t, o, "inventory", "verify"); r.code != cli.ExitOK || !strings.Contains(r.stdout, "2 saldos, 7 movimientos y 2 transferencias verificados, 0 con diferencias") {
		t.Errorf("verify: %d %s", r.code, r.stdout)
	}

	// La moneda de la empresa se ve y se fija al crearla.
	if r := run(t, o, "company", "list"); !strings.Contains(r.stdout, "VES") {
		t.Errorf("company list: %s", r.stdout)
	}
	if r := run(t, o, "company", "create", "--code", "otra", "--legal-name", "Otra, C.A.", "--currency", "EUR"); r.code != cli.ExitUsage {
		t.Errorf("moneda inválida: %d", r.code)
	}
	r = run(t, o, "company", "create", "--code", "otra", "--legal-name", "Otra, C.A.", "--json")
	if r.code != cli.ExitOK || decode(t, r.stdout)["valuation_currency"] != "USD" {
		t.Errorf("moneda por defecto: %d %s %s", r.code, r.stdout, r.stderr)
	}
}

func TestStorekeeperTransfersOnlyWithinTheirScope(t *testing.T) {
	admin := setupSQLite(t)
	run(t, admin, "warehouse", "create", "--code", "deposito", "--name", "Depósito")
	run(t, admin, "product", "create", "--name", "Arroz", "--sku", "ARR")
	run(t, admin, "inventory", "in", "--warehouse", "principal", "--product", "ARR", "--qty", "10", "--unit-cost", "1", "--reason", "initial_load")
	run(t, withStdin(admin, userPassword), "user", "create", "--username", "alm1", "--name", "Alm", "--password-stdin")
	if r := run(t, admin, "user", "assign", "--username", "alm1", "--role", "ALMACENISTA", "--company", "principal", "--branch", "principal", "--warehouse", "principal"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	alm := asUser(t, admin, "alm1", userPassword)
	if r := run(t, alm, "inventory", "transfer", "--from", "principal", "--to", "deposito", "--product", "ARR", "--qty", "1"); r.code != cli.ExitNotFound {
		t.Errorf("almacenista hacia un almacén que no ve: %d %s", r.code, r.stderr)
	}
	if r := run(t, admin, "user", "assign", "--username", "alm1", "--role", "ALMACENISTA", "--company", "principal", "--branch", "principal", "--warehouse", "deposito"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	if r := run(t, alm, "inventory", "transfer", "--from", "principal", "--to", "deposito", "--product", "ARR", "--qty", "1"); r.code != cli.ExitOK {
		t.Errorf("con ambos almacenes: %d %s", r.code, r.stderr)
	}
}

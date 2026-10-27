package cli_test

import (
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
)

// user crea un usuario con un rol en la sucursal principal y abre su sesión.
func user(t *testing.T, admin runOpts, name, role string) runOpts {
	t.Helper()
	if r := run(t, withStdin(admin, userPassword), "user", "create", "--username", name, "--name", name, "--password-stdin"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	if r := run(t, admin, "user", "assign", "--username", name, "--role", role, "--company", "principal", "--branch", "principal"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	return asUser(t, admin, name, userPassword)
}

func mustRun(t *testing.T, o runOpts, args ...string) result {
	t.Helper()
	r := run(t, o, args...)
	if r.code != cli.ExitOK {
		t.Fatalf("%v: %d %s", args, r.code, r.stderr)
	}
	return r
}

func TestNegativeStockFromTheCLI(t *testing.T) {
	admin := setupSQLite(t)
	mustRun(t, admin, "product", "create", "--name", "Arroz", "--sku", "ARR")
	mustRun(t, admin, "inventory", "in", "--product", "ARR", "--qty", "2", "--unit-cost", "8", "--reason", "initial_load")
	mustRun(t, admin, "role", "create", "--code", "NEGATIVOS", "--name", "Negativos", "--permission", "inventory.negative_stock.override")
	alm := user(t, admin, "alm1", "ALMACENISTA")
	mustRun(t, admin, "user", "assign", "--username", "alm1", "--role", "NEGATIVOS", "--company", "principal", "--branch", "principal")

	out := []string{"inventory", "out", "--product", "ARR", "--qty", "5", "--reason", "correction", "--allow-negative"}
	if r := run(t, admin, out...); r.code != cli.ExitForbidden || !strings.Contains(r.stderr, "inventory.negative_stock.override") {
		t.Errorf("sin el permiso extraordinario: %d %s", r.code, r.stderr)
	}
	if r := run(t, alm, out...); r.code != cli.ExitInsufficientStock || !strings.Contains(r.stderr, "no permite existencia negativa") {
		t.Errorf("política por defecto: %d %s", r.code, r.stderr)
	}
	if r := run(t, admin, "inventory", "policy", "show"); !strings.Contains(r.stdout, "deshabilitada") {
		t.Errorf("show: %s", r.stdout)
	}
	if r := run(t, admin, "inventory", "policy", "set", "--negative", "on"); r.code != cli.ExitUsage {
		t.Errorf("sin límite: %d", r.code)
	}
	if r := run(t, admin, "inventory", "policy", "set", "--negative", "on", "--mode", "deny"); r.code != cli.ExitUsage {
		t.Errorf("mezcla de opciones: %d", r.code)
	}
	if r := run(t, alm, "inventory", "policy", "set", "--negative", "on", "--limit", "5"); r.code != cli.ExitForbidden {
		t.Errorf("almacenista configura: %d", r.code)
	}
	mustRun(t, admin, "inventory", "policy", "set", "--negative", "on", "--limit", "5")
	v := decode(t, mustRun(t, admin, "inventory", "policy", "set", "--product", "ARR", "--mode", "allow", "--limit", "4", "--json").stdout)
	p := v["products"].([]any)[0].(map[string]any)
	if v["schema"] != "balanzapro.inventory-policy/v1" || v["negative_enabled"] != true || p["mode"] != "allow" || p["effective_limit"] != "4" {
		t.Fatalf("policy set: %v", v)
	}
	r := run(t, alm, append(out, "--json")...)
	m := decode(t, r.stdout)
	if r.code != cli.ExitOK || m["on_hand_after"] != "-3" || m["cost"] != "-40" || m["value_after"] != "-24" {
		t.Fatalf("salida en déficit: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, admin, "inventory", "stock", "--negative"); !strings.Contains(r.stdout, "ARR") || !strings.Contains(r.stdout, "-3") {
		t.Errorf("stock --negative: %s", r.stdout)
	}
	if r := run(t, admin, "inventory", "verify"); r.code != cli.ExitOK || !strings.Contains(r.stdout, "existencia negativa -3 UND pendiente de regularizar") {
		t.Errorf("verify: %d %s", r.code, r.stdout)
	}
	in := decode(t, mustRun(t, admin, "inventory", "in", "--product", "ARR", "--qty", "5", "--unit-cost", "10", "--reason", "correction", "--json").stdout)
	if in["value_adjustment"] != "-6" || in["value_after"] != "20" {
		t.Fatalf("liquidación: %v", in)
	}
}

func TestReservationsAndBlocksFromTheCLI(t *testing.T) {
	admin := setupSQLite(t)
	mustRun(t, admin, "product", "create", "--name", "Arroz", "--sku", "ARR")
	mustRun(t, admin, "inventory", "in", "--product", "ARR", "--qty", "100", "--unit-cost", "2", "--reason", "initial_load")

	r := run(t, admin, "inventory", "reservation", "create", "--product", "ARR", "--qty", "20", "--reference", "PED-1", "--key", "r1", "--json")
	v := decode(t, r.stdout)
	if r.code != cli.ExitOK || v["schema"] != "balanzapro.reservation/v1" || v["remaining"] != "20" || v["status"] != "active" || v["expires_at"] != nil {
		t.Fatalf("reservar: %d %s %s", r.code, r.stdout, r.stderr)
	}
	id := v["id"].(string)
	if r := run(t, admin, "inventory", "reservation", "create", "--product", "ARR", "--qty", "20", "--reference", "PED-1", "--key", "r1"); !strings.Contains(r.stdout, "ya registrada antes") {
		t.Errorf("reintento: %s", r.stdout)
	}
	if r := run(t, admin, "inventory", "reservation", "create", "--product", "ARR", "--qty", "81", "--reference", "PED-2"); r.code != cli.ExitInsufficientStock {
		t.Errorf("más que el disponible: %d", r.code)
	}
	if r := run(t, admin, "inventory", "reservation", "create", "--product", "ARR", "--qty", "1", "--reference", "PED-2", "--expires", "mañana"); r.code != cli.ExitUsage {
		t.Errorf("vencimiento ilegible: %d", r.code)
	}
	exp := decode(t, mustRun(t, admin, "inventory", "reservation", "create", "--product", "ARR", "--qty", "5", "--reference", "PED-3", "--expires", "2099-01-01 08:00", "--json").stdout)
	if exp["expires_at"] != "2099-01-01T12:00:00Z" {
		t.Errorf("vencimiento en hora de Caracas: %v", exp["expires_at"])
	}
	b := decode(t, mustRun(t, admin, "inventory", "block", "create", "--product", "ARR", "--qty", "5", "--reason", "quarantine", "--json").stdout)
	st := decode(t, mustRun(t, admin, "inventory", "stock", "--json").stdout)["items"].([]any)[0].(map[string]any)
	if st["on_hand"] != "100" || st["reserved"] != "25" || st["blocked"] != "5" || st["available"] != "70" {
		t.Fatalf("ST-01: %v", st)
	}
	d := decode(t, mustRun(t, admin, "inventory", "reservation", "consume", id, "--qty", "8", "--json").stdout)
	if d["remaining"] != "12" || d["movement"].(map[string]any)["kind"] != "reservation_out" || d["movement"].(map[string]any)["cost"] != "-16" {
		t.Fatalf("despacho: %v", d)
	}
	if r := run(t, admin, "inventory", "reservation", "release", id, "--note", "desistió"); r.code != cli.ExitOK || !strings.Contains(r.stdout, "liberada") {
		t.Errorf("liberar: %d %s", r.code, r.stdout)
	}
	if r := run(t, admin, "inventory", "reservation", "release", id); r.code != cli.ExitConflict {
		t.Errorf("liberar dos veces: %d", r.code)
	}
	if r := run(t, admin, "inventory", "reservation", "list", "--status", "active"); !strings.Contains(r.stdout, "PED-3") || strings.Contains(r.stdout, "PED-1") {
		t.Errorf("list: %s", r.stdout)
	}
	if r := run(t, admin, "inventory", "reservation", "expire", "--json"); decode(t, r.stdout)["expired"] != float64(0) {
		t.Errorf("expire: %s", r.stdout)
	}
	if r := run(t, admin, "inventory", "kardex", "--product", "ARR"); !strings.Contains(r.stdout, "despacho") {
		t.Errorf("kardex: %s", r.stdout)
	}
	if r := run(t, admin, "inventory", "block", "write-off", b["id"].(string), "--note", "vencido"); r.code != cli.ExitOK || !strings.Contains(r.stdout, "merma registrada: costo -10.00 USD") {
		t.Errorf("write-off: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, admin, "inventory", "block", "list", "--json"); len(decode(t, r.stdout)["items"].([]any)) != 1 {
		t.Errorf("block list: %s", r.stdout)
	}
	if r := run(t, admin, "inventory", "block", "create", "--product", "ARR", "--qty", "1", "--reason", "robo"); r.code != cli.ExitUsage {
		t.Errorf("motivo inválido: %d", r.code)
	}
	caja := user(t, admin, "caja1", "CAJERO")
	if r := run(t, caja, "inventory", "reservation", "create", "--product", "ARR", "--qty", "1", "--reference", "PED-9"); r.code != cli.ExitOK {
		t.Errorf("cajero reserva: %d %s", r.code, r.stderr)
	}
	if r := run(t, caja, "inventory", "block", "create", "--product", "ARR", "--qty", "1", "--reason", "damage"); r.code != cli.ExitForbidden {
		t.Errorf("cajero bloquea: %d", r.code)
	}
	if r := run(t, admin, "inventory", "verify"); r.code != cli.ExitOK {
		t.Errorf("verify: %d %s", r.code, r.stdout)
	}
}

func TestPhysicalCountFromTheCLI(t *testing.T) {
	admin := setupSQLite(t)
	mustRun(t, admin, "product", "create", "--name", "Arroz", "--sku", "ARR")
	mustRun(t, admin, "inventory", "in", "--product", "ARR", "--qty", "100", "--unit-cost", "2", "--reason", "initial_load")
	alm := user(t, admin, "alm1", "ALMACENISTA")
	enc := user(t, admin, "enc1", "ENCARGADO")

	c := decode(t, mustRun(t, alm, "inventory", "count", "create", "--note", "cierre", "--json").stdout)
	if c["schema"] != "balanzapro.count/v1" || c["status"] != "draft" {
		t.Fatalf("create: %v", c)
	}
	id := c["id"].(string)
	l := decode(t, mustRun(t, alm, "inventory", "count", "record", id, "--product", "ARR", "--qty", "96", "--json").stdout)
	if l["difference"] != "-4" || l["expected"] != "100" {
		t.Fatalf("record: %v", l)
	}
	if r := run(t, alm, "inventory", "count", "record", id, "--product", "ARR", "--qty", "-1"); r.code != cli.ExitUsage {
		t.Errorf("negativo: %d", r.code)
	}
	mustRun(t, alm, "inventory", "count", "submit", id)
	if r := run(t, alm, "inventory", "count", "approve", id); r.code != cli.ExitForbidden {
		t.Errorf("almacenista aprueba: %d", r.code)
	}
	r := run(t, enc, "inventory", "count", "approve", id, "--note", "conforme")
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "aplicado") || !strings.Contains(r.stdout, "1 ajustes registrados") {
		t.Fatalf("approve: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, enc, "inventory", "count", "approve", id); r.code != cli.ExitUsage {
		t.Errorf("aprobar dos veces: %d", r.code)
	}
	own := decode(t, mustRun(t, enc, "inventory", "count", "create", "--json").stdout)["id"].(string)
	mustRun(t, enc, "inventory", "count", "record", own, "--product", "ARR", "--qty", "1")
	mustRun(t, enc, "inventory", "count", "submit", own)
	if r := run(t, enc, "inventory", "count", "approve", own); r.code != cli.ExitUsage || !strings.Contains(r.stderr, "distinto de quien contó") {
		t.Errorf("aprobar lo propio: %d %s", r.code, r.stderr)
	}
	if r := run(t, admin, "inventory", "count", "reject", own); r.code != cli.ExitOK || !strings.Contains(r.stdout, "rechazado") {
		t.Errorf("reject: %d %s", r.code, r.stdout)
	}
	if r := run(t, admin, "inventory", "count", "list"); !strings.Contains(r.stdout, "aplicado") || !strings.Contains(r.stdout, "rechazado") {
		t.Errorf("list: %s", r.stdout)
	}
	if r := run(t, admin, "inventory", "kardex", "--product", "ARR"); !strings.Contains(r.stdout, "conteo −") || !strings.Contains(r.stdout, "-4") {
		t.Errorf("kardex: %s", r.stdout)
	}
	if r := run(t, admin, "inventory", "verify"); r.code != cli.ExitOK {
		t.Errorf("verify: %d %s", r.code, r.stdout)
	}
}

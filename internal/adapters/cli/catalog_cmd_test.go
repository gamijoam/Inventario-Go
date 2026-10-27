package cli_test

import (
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
)

func TestProductLifecycleFromTheCLI(t *testing.T) {
	o := setupSQLite(t)

	r := run(t, o, "product", "create", "--name", "Harina de maíz 1 kg", "--barcode", "7591234567894", "--json")
	v := decode(t, r.stdout)
	if r.code != cli.ExitOK || v["schema"] != "balanzapro.product/v1" || v["sku"] != "P-000001" || v["unit"] != "UND" ||
		v["revision"] != float64(1) || v["barcodes"].([]any)[0] != "7591234567894" {
		t.Fatalf("create: code=%d %v %s", r.code, v, r.stderr)
	}
	id := v["id"].(string)
	if r := run(t, o, "product", "create", "--name", "Queso", "--sku", "que-01", "--unit", "kg"); r.code != cli.ExitOK || !strings.Contains(r.stdout, "QUE-01") {
		t.Fatalf("create con SKU y unidad: %d %s", r.code, r.stderr)
	}

	// La referencia comercial cambia; el ID no.
	r = run(t, o, "product", "update", "P-000001", "--sku", "HAR-1", "--revision", "1", "--json")
	if v := decode(t, r.stdout); r.code != cli.ExitOK || v["id"] != id || v["sku"] != "HAR-1" || v["revision"] != float64(2) {
		t.Fatalf("update: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, o, "product", "update", "HAR-1", "--name", "Otra", "--revision", "1"); r.code != cli.ExitConflict {
		t.Errorf("revisión vieja: code=%d %s", r.code, r.stderr)
	}
	for _, ref := range []string{"har-1", "07591234567894", id} {
		r := run(t, o, "product", "show", ref)
		if r.code != cli.ExitOK || !strings.Contains(r.stdout, "HAR-1") || !strings.Contains(r.stdout, "UND (Unidad, 0 decimales)") {
			t.Errorf("show %s: %d %s %s", ref, r.code, r.stdout, r.stderr)
		}
	}
	if r := run(t, o, "product", "show", "no-existe"); r.code != cli.ExitNotFound {
		t.Errorf("show inexistente: %d", r.code)
	}
	if r := run(t, o, "product", "show"); r.code != cli.ExitUsage {
		t.Errorf("show sin referencia: %d", r.code)
	}

	// Validaciones.
	for name, args := range map[string][]string{
		"dígito verificador": {"product", "create", "--name", "X", "--barcode", "7591234567893"},
		"SKU inválido":       {"product", "create", "--name", "X", "--sku", "con espacio"},
		"sin nombre":         {"product", "create"},
		"sin cambios":        {"product", "update", "HAR-1"},
		"active e inactive":  {"product", "update", "HAR-1", "--active", "--inactive"},
	} {
		if r := run(t, o, args...); r.code != cli.ExitUsage {
			t.Errorf("%s: code=%d %s", name, r.code, r.stderr)
		}
	}
	if r := run(t, o, "product", "create", "--name", "X", "--barcode", "07591234567894"); r.code != cli.ExitConflict {
		t.Errorf("código de barras repetido (misma clave GTIN): %d", r.code)
	}
	if r := run(t, o, "product", "create", "--name", "X", "--unit", "lb"); r.code != cli.ExitNotFound {
		t.Errorf("unidad inexistente: %d", r.code)
	}

	// Inactivar en lugar de borrar.
	if r := run(t, o, "product", "update", "HAR-1", "--inactive"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	if items := decode(t, run(t, o, "product", "list", "--json").stdout)["items"].([]any); len(items) != 1 {
		t.Errorf("activos = %v", items)
	}
	if items := decode(t, run(t, o, "product", "list", "--all", "--search", "harina", "--json").stdout)["items"].([]any); len(items) != 1 {
		t.Errorf("búsqueda con inactivos = %v", items)
	}
	if text := run(t, o, "product", "list", "--all").stdout; !strings.Contains(text, "inactivo") || !strings.Contains(text, "QUE-01") {
		t.Errorf("list:\n%s", text)
	}
	if r := run(t, o, "product", "list", "--limit", "0"); r.code != cli.ExitUsage {
		t.Errorf("límite 0: %d", r.code)
	}
}

func TestUnitsFromTheCLI(t *testing.T) {
	o := setupSQLite(t)
	units := decode(t, run(t, o, "unit", "list", "--json").stdout)["items"].([]any)
	if len(units) != 8 {
		t.Fatalf("unidades base = %v", units)
	}
	r := run(t, o, "unit", "create", "--code", "lb", "--name", "Libra", "--decimals", "3", "--json")
	if v := decode(t, r.stdout); r.code != cli.ExitOK || v["code"] != "LB" || v["scale"] != float64(3) {
		t.Fatalf("unit create: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, o, "unit", "create", "--code", "x", "--name", "X", "--decimals", "7"); r.code != cli.ExitUsage {
		t.Errorf("7 decimales: %d", r.code)
	}
	if r := run(t, o, "unit", "create", "--code", "LB", "--name", "Otra"); r.code != cli.ExitConflict {
		t.Errorf("repetida: %d", r.code)
	}
	if r := run(t, o, "product", "create", "--name", "Carne", "--unit", "LB"); r.code != cli.ExitOK {
		t.Errorf("producto con unidad nueva: %s", r.stderr)
	}
}

func TestCashierReadsButDoesNotEditTheCatalog(t *testing.T) {
	admin := setupSQLite(t)
	if r := run(t, admin, "product", "create", "--name", "Arroz", "--sku", "ARR"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	if r := run(t, withStdin(admin, userPassword), "user", "create", "--username", "caja1", "--name", "Caja 1", "--password-stdin"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	if r := run(t, admin, "user", "assign", "--username", "caja1", "--role", "CAJERO", "--company", "principal", "--branch", "principal"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	caja := asUser(t, admin, "caja1", userPassword)
	if r := run(t, caja, "product", "show", "ARR"); r.code != cli.ExitOK {
		t.Errorf("el cajero consulta: %d %s", r.code, r.stderr)
	}
	for _, args := range [][]string{
		{"product", "create", "--name", "X"},
		{"product", "update", "ARR", "--name", "Y"},
		{"unit", "create", "--code", "LB", "--name", "Libra"},
	} {
		r := run(t, caja, args...)
		if r.code != cli.ExitForbidden || !strings.Contains(r.stderr, "catalog.") {
			t.Errorf("%v: code=%d %s", args, r.code, r.stderr)
		}
	}
}

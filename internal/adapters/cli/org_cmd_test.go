package cli_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
	"github.com/gamijoam/inventario-go/internal/testutil/pgtest"

	_ "modernc.org/sqlite"
)

func setupSQLite(t *testing.T) runOpts {
	t.Helper()
	o, _ := sqliteEnv(t)
	r := run(t, o, "setup", "--name", "Panadería Ñandú", "--legal-name", "Panadería Ñandú, C.A.", "--rif", "j123456789")
	if r.code != cli.ExitOK {
		t.Fatalf("setup: code=%d stderr=%s", r.code, r.stderr)
	}
	return o
}

func TestSetupCreatesTheWholeStructure(t *testing.T) {
	o, _ := sqliteEnv(t)
	r := run(t, o, "setup", "--name", "Panadería Ñandú", "--rif", "j123456789", "--json")
	if r.code != cli.ExitOK {
		t.Fatalf("code=%d stderr=%s", r.code, r.stderr)
	}
	v := decode(t, r.stdout)
	orgv := v["organization"].(map[string]any)
	company := v["company"].(map[string]any)
	if v["schema"] != "balanzapro.setup/v1" || orgv["code"] != "PANADERIA-NANDU" || company["rif"] != "J-12345678-9" ||
		company["legal_name"] != "Panadería Ñandú" || v["warehouse"].(map[string]any)["code"] != "PRINCIPAL" {
		t.Fatalf("payload = %v", v)
	}

	r = run(t, o, "setup", "--name", "Otra")
	if r.code != cli.ExitConflict || !strings.Contains(r.stderr, "ya tiene una organización") {
		t.Fatalf("segundo setup: code=%d stderr=%s", r.code, r.stderr)
	}
}

func TestSetupValidatesInput(t *testing.T) {
	o, _ := sqliteEnv(t)
	for _, args := range [][]string{
		{"setup"},
		{"setup", "--name", "  "},
		{"setup", "--name", "Ana", "--rif", "123"},
		{"setup", "--name", "Ana", "--branch-code", "con espacio"},
	} {
		if r := run(t, o, args...); r.code != cli.ExitUsage {
			t.Errorf("%v: code=%d stderr=%s", args, r.code, r.stderr)
		}
	}
}

func TestCommandsBeforeSetupReportNotFound(t *testing.T) {
	o, _ := sqliteEnv(t)
	for _, args := range [][]string{{"org", "list"}, {"company", "list"}, {"branch", "list"}, {"warehouse", "list"}} {
		r := run(t, o, args...)
		if r.code != cli.ExitNotFound || !strings.Contains(r.stderr, "balanzapro setup") {
			t.Errorf("%v: code=%d stderr=%s", args, r.code, r.stderr)
		}
	}
	// Con la base migrada pero sin setup, igual.
	if run(t, o, "db", "migrate").code != cli.ExitOK {
		t.Fatal("db migrate")
	}
	if r := run(t, o, "company", "list"); r.code != cli.ExitNotFound {
		t.Errorf("tras migrar: code=%d", r.code)
	}
}

func TestCompanyBranchWarehouseLifecycle(t *testing.T) {
	o := setupSQLite(t)

	r := run(t, o, "company", "create", "--code", "norte", "--legal-name", "Ñandú Norte, C.A.", "--json")
	if v := decode(t, r.stdout); r.code != cli.ExitOK || v["schema"] != "balanzapro.company/v1" || v["code"] != "NORTE" || v["rif"] != "" {
		t.Fatalf("company create: code=%d %v %s", r.code, v, r.stderr)
	}
	if r := run(t, o, "company", "create", "--code", "NORTE", "--legal-name", "Duplicada"); r.code != cli.ExitConflict {
		t.Errorf("empresa duplicada: code=%d", r.code)
	}
	if r := run(t, o, "company", "create", "--code", "x", "--legal-name", "X", "--rif", "J-12345678-9"); r.code != cli.ExitConflict {
		t.Errorf("RIF duplicado: code=%d", r.code)
	}

	if r := run(t, o, "branch", "create", "--company", "norte", "--code", "maracay", "--name", "Maracay"); r.code != cli.ExitOK {
		t.Fatalf("branch create: code=%d stderr=%s", r.code, r.stderr)
	}
	if r := run(t, o, "branch", "create", "--code", "centro", "--name", "Centro"); r.code != cli.ExitOK {
		t.Fatalf("branch create sin --company (usa la empresa de la instalación): code=%d stderr=%s", r.code, r.stderr)
	}
	if r := run(t, o, "branch", "create", "--company", "no-existe", "--code", "x", "--name", "X"); r.code != cli.ExitNotFound {
		t.Errorf("empresa inexistente: code=%d", r.code)
	}

	if r := run(t, o, "warehouse", "create", "--code", "danados", "--name", "Mercancía dañada"); r.code != cli.ExitOK {
		t.Fatalf("warehouse create en la sucursal actual: code=%d stderr=%s", r.code, r.stderr)
	}
	if r := run(t, o, "warehouse", "create", "--company", "norte", "--branch", "maracay", "--code", "principal", "--name", "Principal"); r.code != cli.ExitOK {
		t.Fatalf("warehouse create en Maracay: code=%d stderr=%s", r.code, r.stderr)
	}
	if r := run(t, o, "warehouse", "create", "--company", "norte", "--code", "x", "--name", "X"); r.code != cli.ExitUsage {
		t.Errorf("--company sin --branch: code=%d", r.code)
	}

	companies := decode(t, run(t, o, "company", "list", "--json").stdout)["items"].([]any)
	branches := decode(t, run(t, o, "branch", "list", "--json").stdout)["items"].([]any)
	norte := decode(t, run(t, o, "branch", "list", "--company", "norte", "--json").stdout)["items"].([]any)
	whs := decode(t, run(t, o, "warehouse", "list", "--json").stdout)["items"].([]any)
	whsMaracay := decode(t, run(t, o, "warehouse", "list", "--company", "norte", "--branch", "maracay", "--json").stdout)["items"].([]any)
	if len(companies) != 2 || len(branches) != 3 || len(norte) != 1 || len(whs) != 3 || len(whsMaracay) != 1 {
		t.Fatalf("listados: %d empresas, %d sucursales, %d de norte, %d almacenes, %d en Maracay",
			len(companies), len(branches), len(norte), len(whs), len(whsMaracay))
	}
	if norte[0].(map[string]any)["company_code"] != "NORTE" {
		t.Errorf("branch list debe incluir el código de empresa: %v", norte[0])
	}

	text := run(t, o, "branch", "list").stdout
	if !strings.Contains(text, "(esta instalación)") || !strings.Contains(text, "MARACAY") {
		t.Errorf("branch list en texto:\n%s", text)
	}
	orgText := run(t, o, "org", "list").stdout
	if !strings.Contains(orgText, "PANADERIA-NANDU") || !strings.Contains(orgText, "activa: empresa PRINCIPAL, sucursal PRINCIPAL") {
		t.Errorf("org list:\n%s", orgText)
	}
}

func TestRequiredFlagsAreUsageErrors(t *testing.T) {
	o := setupSQLite(t)
	for _, args := range [][]string{
		{"company", "create", "--code", "x"},
		{"branch", "create", "--name", "X"},
		{"warehouse", "create"},
	} {
		if r := run(t, o, args...); r.code != cli.ExitUsage {
			t.Errorf("%v: code=%d stderr=%s", args, r.code, r.stderr)
		}
	}
}

func TestOrgCommandsRefusePendingMigrations(t *testing.T) {
	o, path := sqliteEnv(t)
	if r := run(t, o, "setup", "--name", "Ana"); r.code != cli.ExitOK {
		t.Fatalf("setup: %s", r.stderr)
	}
	// Simula un ejecutable más nuevo que la base: la última migración
	// queda como pendiente.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version = (SELECT MAX(version) FROM schema_migrations)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	r := run(t, o, "company", "list")
	if r.code == cli.ExitOK || !strings.Contains(r.stderr, "migraciones pendientes") {
		t.Fatalf("code=%d stderr=%s", r.code, r.stderr)
	}
}

func TestSetupOnPostgres(t *testing.T) {
	o := runOpts{env: map[string]string{
		"BALANZAPRO_DB_DRIVER":    "postgres",
		"BALANZAPRO_POSTGRES_URL": pgtest.NewDatabase(t),
	}}
	if r := run(t, o, "setup", "--name", "Bodega Ana", "--rif", "J-12345678-9"); r.code != cli.ExitOK {
		t.Fatalf("setup: code=%d stderr=%s", r.code, r.stderr)
	}
	if r := run(t, o, "branch", "create", "--code", "centro", "--name", "Centro"); r.code != cli.ExitOK {
		t.Fatalf("branch create: code=%d stderr=%s", r.code, r.stderr)
	}
	if items := decode(t, run(t, o, "branch", "list", "--json").stdout)["items"].([]any); len(items) != 2 {
		t.Fatalf("branch list = %v", items)
	}
}

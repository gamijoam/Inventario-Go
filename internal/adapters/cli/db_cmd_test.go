package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
	"github.com/gamijoam/inventario-go/internal/platform/migration"
	"github.com/gamijoam/inventario-go/internal/testutil/pgtest"
	"github.com/gamijoam/inventario-go/migrations"
)

// migrationCount es el número de migraciones embebidas del motor SQLite.
func migrationCount(t *testing.T) int {
	t.Helper()
	migs, err := migration.Load(migrations.FS, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	return len(migs)
}

func sqliteEnv(t *testing.T) (runOpts, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "datos", "bp.db")
	return runOpts{env: map[string]string{"BALANZAPRO_SQLITE_PATH": path}}, path
}

func TestDBStatusBeforeMigrateDoesNotCreateTheDatabase(t *testing.T) {
	o, path := sqliteEnv(t)
	r := run(t, o, "db", "status")
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "no inicializada") || !strings.Contains(r.stdout, "[pendiente] 0001_installation") {
		t.Fatalf("code=%d stdout=\n%s stderr=%s", r.code, r.stdout, r.stderr)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("db status no debe crear la base: %v", err)
	}
	v := decode(t, run(t, o, "db", "status", "--json").stdout)
	if v["schema"] != "balanzapro.db-status/v1" || v["initialized"] != false || len(v["pending"].([]any)) != migrationCount(t) {
		t.Fatalf("payload = %v", v)
	}
}

func TestDBMigrateIsIdempotentAndKeepsInstallationID(t *testing.T) {
	o, path := sqliteEnv(t)
	r := run(t, o, "db", "migrate", "--json")
	if r.code != cli.ExitOK {
		t.Fatalf("code=%d stderr=%s", r.code, r.stderr)
	}
	first := decode(t, r.stdout)
	if first["schema"] != "balanzapro.db-migrate/v1" || first["installation_created"] != true || len(first["applied"].([]any)) != migrationCount(t) || first["backup"] != "" {
		t.Fatalf("primera migración = %v", first)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("la base debía crearse: %v", err)
	}

	r = run(t, o, "db", "migrate")
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "ya estaba al día") || !strings.Contains(r.stdout, first["installation_id"].(string)) {
		t.Fatalf("segunda migración: code=%d stdout=\n%s", r.code, r.stdout)
	}

	st := decode(t, run(t, o, "db", "status", "--json").stdout)
	if st["initialized"] != true || st["installation_id"] != first["installation_id"] || len(st["applied"].([]any)) != migrationCount(t) || len(st["pending"].([]any)) != 0 {
		t.Fatalf("status = %v", st)
	}
	if !strings.Contains(st["server"].(string), "journal wal") {
		t.Fatalf("server = %v", st["server"])
	}
	text := run(t, o, "db", "status").stdout
	if !strings.Contains(text, "[aplicada]  0001_installation") || !strings.Contains(text, "(VET)") {
		t.Fatalf("status en texto:\n%s", text)
	}
}

func TestDoctorReportsDatabaseState(t *testing.T) {
	o, _ := sqliteEnv(t)
	r := run(t, o, "doctor")
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "[WARN] database") || !strings.Contains(r.stdout, "db migrate") {
		t.Fatalf("antes de migrar: code=%d\n%s", r.code, r.stdout)
	}
	if run(t, o, "db", "migrate").code != cli.ExitOK {
		t.Fatal("db migrate falló")
	}
	r = run(t, o, "doctor")
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "[ OK ] database") || !strings.Contains(r.stdout, "esquema al día") {
		t.Fatalf("después de migrar: code=%d\n%s", r.code, r.stdout)
	}
}

func TestUnreachablePostgresExitsWith5WithoutSecrets(t *testing.T) {
	o := runOpts{env: map[string]string{
		"BALANZAPRO_DB_DRIVER":    "postgres",
		"BALANZAPRO_POSTGRES_URL": "postgres://bp:" + pw + "@127.0.0.1:1/bp?connect_timeout=2",
	}}
	for _, args := range [][]string{{"db", "status"}, {"db", "migrate"}} {
		r := run(t, o, args...)
		if r.code != cli.ExitUnavailable || strings.Contains(r.stdout+r.stderr, pw) || !strings.Contains(r.stderr, "127.0.0.1") {
			t.Errorf("%v: code=%d stderr=%q", args, r.code, r.stderr)
		}
	}
	r := run(t, o, "db", "status", "--json")
	if v := decode(t, r.stderr); v["error"].(map[string]any)["kind"] != "unavailable" {
		t.Errorf("error JSON = %v", v)
	}
	if r := run(t, o, "doctor"); r.code != cli.ExitCheckFailed || !strings.Contains(r.stdout, "[FAIL] database") || strings.Contains(r.stdout, pw) {
		t.Errorf("doctor: code=%d\n%s", r.code, r.stdout)
	}
}

func TestDBMigrateOnPostgres(t *testing.T) {
	o := runOpts{env: map[string]string{
		"BALANZAPRO_DB_DRIVER":    "postgres",
		"BALANZAPRO_POSTGRES_URL": pgtest.NewDatabase(t),
	}}
	r := run(t, o, "db", "migrate", "--json")
	if r.code != cli.ExitOK {
		t.Fatalf("code=%d stderr=%s", r.code, r.stderr)
	}
	v := decode(t, r.stdout)
	if v["engine"] != "postgres" || v["installation_created"] != true || strings.Contains(v["location"].(string), "pgtest") {
		t.Fatalf("payload = %v", v)
	}
	if r := run(t, o, "doctor"); !strings.Contains(r.stdout, "[ OK ] database") || !strings.Contains(r.stdout, "PostgreSQL") {
		t.Fatalf("doctor:\n%s", r.stdout)
	}
}

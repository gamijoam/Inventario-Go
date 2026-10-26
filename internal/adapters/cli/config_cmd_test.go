package cli_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
)

const pw = "s3cr3t-P4ss"

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("los permisos POSIX no aplican en Windows")
	}
}

func writeConfig(t *testing.T, content string, perm os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigShowDefaults(t *testing.T) {
	home := t.TempDir()
	r := run(t, runOpts{home: home}, "config", "show")
	if r.code != cli.ExitOK {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	for _, want := range []string{
		filepath.Join(home, ".config", "balanzapro", "config.toml"),
		"no existe; valores por defecto",
		`database.driver        = "sqlite"`,
		"[default]",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("falta %q en:\n%s", want, r.stdout)
		}
	}
}

func TestConfigShowJSONReportsSourcesAndRedactsSecrets(t *testing.T) {
	r := run(t, runOpts{env: map[string]string{
		"BALANZAPRO_DB_DRIVER":    "postgres",
		"BALANZAPRO_POSTGRES_URL": "postgres://bp:" + pw + "@db:5432/bp",
	}}, "config", "show", "--json")
	if r.code != cli.ExitOK || r.stderr != "" {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	if strings.Contains(r.stdout, pw) {
		t.Fatalf("config show revela el secreto:\n%s", r.stdout)
	}
	v := decode(t, r.stdout)
	if v["schema"] != "balanzapro.config/v1" || v["file_found"] != false {
		t.Fatalf("payload = %v", v)
	}
	found := false
	for _, item := range v["values"].([]any) {
		e := item.(map[string]any)
		if e["key"] == "database.postgres.url" {
			found = true
			if e["source"] != "env" || e["env_var"] != "BALANZAPRO_POSTGRES_URL" || !strings.Contains(e["value"].(string), "bp:xxxxx@db:5432") {
				t.Errorf("entrada postgres = %v", e)
			}
		}
	}
	if !found {
		t.Fatalf("falta database.postgres.url")
	}
}

func TestInvalidConfigExitsWith4(t *testing.T) {
	path := writeConfig(t, "[database]\ndriver = \"postgres\"\n[database.postgres]\nurl = \"mysql://u:"+pw+"@h/d\"\n", 0o600)

	r := run(t, runOpts{}, "config", "show", "--config", path)
	if r.code != cli.ExitConfig || r.stdout != "" || !strings.Contains(r.stderr, "esquema postgres://") {
		t.Fatalf("code=%d stdout=%q stderr=%q", r.code, r.stdout, r.stderr)
	}
	if strings.Contains(r.stderr, pw) {
		t.Fatalf("el error revela el secreto: %s", r.stderr)
	}

	r = run(t, runOpts{}, "config", "show", "--config", path, "--json")
	v := decode(t, r.stderr)
	if r.code != cli.ExitConfig || v["error"].(map[string]any)["kind"] != "config" {
		t.Fatalf("code=%d error=%v", r.code, v)
	}
}

func TestMissingExplicitConfigExitsWith4(t *testing.T) {
	r := run(t, runOpts{}, "config", "show", "--config", filepath.Join(t.TempDir(), "nada.toml"))
	if r.code != cli.ExitConfig {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
}

func TestDoctorReportsInvalidConfigWithoutSecrets(t *testing.T) {
	path := writeConfig(t, "[log]\nlevel = \"verbose\"\n", 0o600)
	r := run(t, runOpts{}, "doctor", "--config", path)
	if r.code != cli.ExitCheckFailed || !strings.Contains(r.stdout, "[FAIL] config") {
		t.Fatalf("code=%d stdout=\n%s", r.code, r.stdout)
	}

	r = run(t, runOpts{env: map[string]string{"BALANZAPRO_DB_DRIVER": "postgres", "BALANZAPRO_POSTGRES_URL": "x://u:" + pw + "@h"}}, "doctor", "--json")
	if r.code != cli.ExitCheckFailed || strings.Contains(r.stdout, pw) {
		t.Fatalf("code=%d stdout=%s", r.code, r.stdout)
	}
}

func TestDoctorWarnsAboutWorldReadableSecretsButStaysHealthy(t *testing.T) {
	skipOnWindows(t)
	path := writeConfig(t, "[database]\ndriver = \"postgres\"\n[database.postgres]\nurl = \"postgres://u:"+pw+"@h/d\"\n", 0o644)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, runOpts{}, "doctor", "--config", path)
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "[WARN] config") || strings.Contains(r.stdout, pw) {
		t.Fatalf("code=%d stdout=\n%s", r.code, r.stdout)
	}
}

func TestLogsGoToStderrNeverStdout(t *testing.T) {
	path := writeConfig(t, "[database]\ndriver = \"postgres\"\n[database.postgres]\nurl = \"postgres://u:"+pw+"@h/d\"\n", 0o600)
	r := run(t, runOpts{}, "config", "show", "--config", path, "--json", "--log-level", "debug")
	if r.code != cli.ExitOK {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	decode(t, r.stdout) // stdout sigue siendo un único JSON válido
	if !strings.Contains(r.stderr, "configuración cargada") || strings.Contains(r.stderr, pw) {
		t.Fatalf("stderr = %q", r.stderr)
	}
	r = run(t, runOpts{}, "config", "show", "--config", path, "--json")
	if r.stderr != "" {
		t.Fatalf("con el nivel por defecto (warn) no debe haber logs de depuración: %q", r.stderr)
	}
}

func TestWarningsArePartOfConfigShowOutput(t *testing.T) {
	skipOnWindows(t)
	path := writeConfig(t, "[database]\ndriver = \"postgres\"\n[database.postgres]\nurl = \"postgres://u:"+pw+"@h/d\"\n", 0o644)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, runOpts{}, "config", "show", "--config", path, "--json")
	v := decode(t, r.stdout)
	warnings := v["warnings"].([]any)
	if r.code != cli.ExitOK || len(warnings) != 1 || !strings.Contains(warnings[0].(string), "chmod 600") {
		t.Fatalf("code=%d warnings=%v", r.code, warnings)
	}
	r = run(t, runOpts{}, "config", "show", "--config", path)
	if strings.Count(r.stdout+r.stderr, "chmod 600") != 1 {
		t.Fatalf("la advertencia debe aparecer una sola vez:\n%s%s", r.stdout, r.stderr)
	}
}

func TestInvalidLogLevelFlagIsUsageError(t *testing.T) {
	r := run(t, runOpts{}, "config", "show", "--log-level", "verbose")
	if r.code != cli.ExitUsage {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
}

func TestConfigPath(t *testing.T) {
	home := t.TempDir()
	r := run(t, runOpts{home: home}, "config", "path")
	if r.code != cli.ExitOK || strings.TrimSpace(r.stdout) != filepath.Join(home, ".config", "balanzapro", "config.toml") {
		t.Fatalf("code=%d stdout=%q", r.code, r.stdout)
	}
	r = run(t, runOpts{env: map[string]string{"BALANZAPRO_CONFIG": "/etc/bp.toml"}}, "config", "path")
	if strings.TrimSpace(r.stdout) != "/etc/bp.toml" {
		t.Fatalf("BALANZAPRO_CONFIG no respetado: %q", r.stdout)
	}
	r = run(t, runOpts{}, "config", "path", "--config", "/x.toml", "--json")
	if v := decode(t, r.stdout); v["path"] != "/x.toml" {
		t.Fatalf("payload = %v", v)
	}
}

func TestTUIDoesNotOpenWithInvalidConfig(t *testing.T) {
	path := writeConfig(t, "[log]\nformat = \"xml\"\n", 0o600)
	r := run(t, runOpts{interactive: true}, "--config", path)
	if r.code != cli.ExitConfig || r.tuiOpened {
		t.Fatalf("code=%d tui=%v", r.code, r.tuiOpened)
	}
}

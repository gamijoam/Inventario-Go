package config_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/platform/config"
)

const secretPassword = "s3cr3t-P4ss"

type fixture struct {
	t    *testing.T
	home string
	env  map[string]string
}

func newFixture(t *testing.T) *fixture {
	return &fixture{t: t, home: t.TempDir(), env: map[string]string{}}
}

func (f *fixture) opts(explicit string) config.LoadOptions {
	return config.LoadOptions{
		ExplicitPath: explicit,
		Getenv:       func(k string) string { return f.env[k] },
		GOOS:         "linux",
		HomeDir:      f.home,
	}
}

func (f *fixture) write(name, content string) string {
	f.t.Helper()
	path := filepath.Join(f.home, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func TestDefaultsWithoutFile(t *testing.T) {
	f := newFixture(t)
	l, err := config.Load(f.opts(""))
	if err != nil {
		t.Fatal(err)
	}
	if l.FileFound {
		t.Fatalf("no debía encontrar archivo")
	}
	if want := filepath.Join(f.home, ".config", "balanzapro", "config.toml"); l.Path != want {
		t.Errorf("Path = %q, se esperaba %q", l.Path, want)
	}
	c := l.Config
	if c.Database.Driver != config.DriverSQLite || c.Log.Level != "warn" || c.Log.Format != "text" || c.Log.File != "" {
		t.Errorf("valores por defecto incorrectos: %+v", c)
	}
	if want := filepath.Join(f.home, ".local", "share", "balanzapro", "balanzapro.db"); c.Database.SQLite.Path != want {
		t.Errorf("SQLite.Path = %q, se esperaba %q", c.Database.SQLite.Path, want)
	}
	if l.Sources["database.driver"] != config.SourceDefault {
		t.Errorf("fuente = %q", l.Sources["database.driver"])
	}
}

func TestDefaultPathsFollowPlatformConventions(t *testing.T) {
	f := newFixture(t)
	f.env["XDG_CONFIG_HOME"] = "/xdg/config"
	f.env["XDG_DATA_HOME"] = "/xdg/data"
	f.env["XDG_STATE_HOME"] = "/xdg/state"
	l, err := config.Load(f.opts(""))
	if err != nil {
		t.Fatal(err)
	}
	if l.Config.Session.File != filepath.Join("/xdg/state", "balanzapro", "session") {
		t.Errorf("sesión: %q", l.Config.Session.File)
	}
	if l.Path != filepath.Join("/xdg/config", "balanzapro", "config.toml") ||
		l.Config.Database.SQLite.Path != filepath.Join("/xdg/data", "balanzapro", "balanzapro.db") {
		t.Errorf("XDG no respetado: %q %q", l.Path, l.Config.Database.SQLite.Path)
	}

	w := newFixture(t)
	w.env["APPDATA"] = `C:\Users\ana\AppData\Roaming`
	w.env["LOCALAPPDATA"] = `C:\Users\ana\AppData\Local`
	opts := w.opts("")
	opts.GOOS = "windows"
	paths := config.DefaultPaths(opts)
	if paths.ConfigFile != filepath.Join(`C:\Users\ana\AppData\Roaming`, "BalanzaPro", "config.toml") ||
		paths.SQLiteFile != filepath.Join(`C:\Users\ana\AppData\Local`, "BalanzaPro", "balanzapro.db") ||
		paths.SessionFile != filepath.Join(`C:\Users\ana\AppData\Local`, "BalanzaPro", "session") {
		t.Errorf("rutas Windows: %+v", paths)
	}
}

func TestFileOverridesDefaultsAndEnvOverridesFile(t *testing.T) {
	f := newFixture(t)
	path := f.write("bp.toml", `
[database]
driver = "postgres"

[database.postgres]
url = "postgres://bp:`+secretPassword+`@localhost:5432/balanzapro"

[log]
level = "info"
format = "json"
`)
	f.env["BALANZAPRO_LOG_LEVEL"] = "debug"

	l, err := config.Load(f.opts(path))
	if err != nil {
		t.Fatal(err)
	}
	c := l.Config
	if !l.FileFound || l.Path != path {
		t.Fatalf("archivo no cargado: %+v", l)
	}
	if c.Database.Driver != config.DriverPostgres || c.Log.Format != "json" {
		t.Errorf("valores del archivo no aplicados: %+v", c)
	}
	if c.Log.Level != "debug" || l.Sources["log.level"] != config.SourceEnv {
		t.Errorf("el entorno debe prevalecer sobre el archivo: %q (%s)", c.Log.Level, l.Sources["log.level"])
	}
	if l.Sources["log.format"] != config.SourceFile || l.Sources["log.file"] != config.SourceDefault {
		t.Errorf("fuentes incorrectas: %v", l.Sources)
	}
	if got := c.Database.Postgres.URL.Reveal(); !strings.Contains(got, secretPassword) {
		t.Errorf("Reveal debe devolver el valor real")
	}
}

func TestConfigPathFromEnvironment(t *testing.T) {
	f := newFixture(t)
	path := f.write("otro.toml", "[log]\nlevel = \"error\"\n")
	f.env["BALANZAPRO_CONFIG"] = path
	l, err := config.Load(f.opts(""))
	if err != nil || l.Path != path || l.Config.Log.Level != "error" {
		t.Fatalf("BALANZAPRO_CONFIG no respetado: %+v, %v", l, err)
	}
	// Un archivo explícito (flag) prevalece sobre la variable.
	explicit := f.write("flag.toml", "[log]\nlevel = \"info\"\n")
	l, err = config.Load(f.opts(explicit))
	if err != nil || l.Path != explicit || l.Config.Log.Level != "info" {
		t.Fatalf("--config no prevaleció: %+v, %v", l, err)
	}
}

func TestExplicitMissingFileIsAnError(t *testing.T) {
	f := newFixture(t)
	_, err := config.Load(f.opts(filepath.Join(f.home, "no-existe.toml")))
	if !errors.Is(err, config.ErrInvalid) {
		t.Fatalf("se esperaba ErrInvalid, obtuvo %v", err)
	}
}

func TestEveryEnvironmentVariable(t *testing.T) {
	f := newFixture(t)
	f.env = map[string]string{
		"BALANZAPRO_DB_DRIVER":    "postgres",
		"BALANZAPRO_SQLITE_PATH":  "/datos/bp.db",
		"BALANZAPRO_POSTGRES_URL": "postgresql://bp@db.local/bp",
		"BALANZAPRO_LOG_LEVEL":    "error",
		"BALANZAPRO_LOG_FORMAT":   "json",
		"BALANZAPRO_LOG_FILE":     "/var/log/bp.log",
		"BALANZAPRO_SESSION_FILE": "/run/bp/session",
	}
	l, err := config.Load(f.opts(""))
	if err != nil {
		t.Fatal(err)
	}
	c := l.Config
	if c.Database.Driver != "postgres" || c.Database.SQLite.Path != "/datos/bp.db" ||
		c.Database.Postgres.URL.Reveal() != "postgresql://bp@db.local/bp" ||
		c.Log.Level != "error" || c.Log.Format != "json" || c.Log.File != "/var/log/bp.log" || c.Session.File != "/run/bp/session" {
		t.Fatalf("variables no aplicadas: %+v", c)
	}
	for key, src := range l.Sources {
		if src != config.SourceEnv {
			t.Errorf("%s: fuente %s, se esperaba env", key, src)
		}
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]string{
		"driver desconocido":       "[database]\ndriver = \"mysql\"\n",
		"postgres sin url":         "[database]\ndriver = \"postgres\"\n",
		"url con esquema inválido": "[database]\ndriver = \"postgres\"\n[database.postgres]\nurl = \"mysql://x@y/z\"\n",
		"url sin host":             "[database]\ndriver = \"postgres\"\n[database.postgres]\nurl = \"postgres:///bp\"\n",
		"nivel de log inválido":    "[log]\nlevel = \"verbose\"\n",
		"formato de log inválido":  "[log]\nformat = \"xml\"\n",
		"sqlite sin ruta":          "[database.sqlite]\npath = \"\"\n",
		"clave desconocida":        "[database]\ndriverr = \"sqlite\"\n",
		"sección desconocida":      "[servidor]\npuerto = 1\n",
		"tipo incorrecto":          "[log]\nlevel = 5\n",
		"toml mal formado":         "[log\nlevel = \"info\"\n",
	}
	for name, content := range cases {
		f := newFixture(t)
		_, err := config.Load(f.opts(f.write("c.toml", content)))
		if !errors.Is(err, config.ErrInvalid) {
			t.Errorf("%s: se esperaba ErrInvalid, obtuvo %v", name, err)
		}
	}
}

func TestErrorsNeverRevealSecrets(t *testing.T) {
	cases := map[string]string{
		"esquema inválido": "[database]\ndriver = \"postgres\"\n[database.postgres]\nurl = \"mysql://u:" + secretPassword + "@h/d\"\n",
		"url ilegible":     "[database]\ndriver = \"postgres\"\n[database.postgres]\nurl = \"postgres://u:" + secretPassword + "@h:puerto/d\"\n",
		"toml roto":        "[database.postgres]\nurl = \"postgres://u:" + secretPassword + "@h/d\n",
		"clave vecina":     "[database.postgres]\nurl = \"postgres://u:" + secretPassword + "@h/d\"\nclave = 1\n",
	}
	for name, content := range cases {
		f := newFixture(t)
		_, err := config.Load(f.opts(f.write("c.toml", content)))
		if err == nil {
			t.Errorf("%s: se esperaba error", name)
			continue
		}
		if strings.Contains(err.Error(), secretPassword) {
			t.Errorf("%s: el error revela el secreto: %v", name, err)
		}
	}

	f := newFixture(t)
	f.env["BALANZAPRO_DB_DRIVER"] = "postgres"
	f.env["BALANZAPRO_POSTGRES_URL"] = "mysql://u:" + secretPassword + "@h/d"
	if _, err := config.Load(f.opts("")); err == nil || strings.Contains(err.Error(), secretPassword) {
		t.Errorf("entorno: error = %v", err)
	}
}

func TestSecretIsRedactedEverywhere(t *testing.T) {
	s := config.NewSecret("postgres://u:" + secretPassword + "@h/d")
	for name, got := range map[string]string{
		"String":   s.String(),
		"%v":       fmt.Sprintf("%v", s),
		"%+v":      fmt.Sprintf("%+v", struct{ S config.Secret }{s}),
		"%#v":      fmt.Sprintf("%#v", s),
		"GoString": s.GoString(),
	} {
		if strings.Contains(got, secretPassword) {
			t.Errorf("%s revela el secreto: %s", name, got)
		}
	}
	b, err := json.Marshal(struct{ S config.Secret }{s})
	if err != nil || strings.Contains(string(b), secretPassword) {
		t.Errorf("JSON revela el secreto: %s (%v)", b, err)
	}
	if (config.Secret{}).String() != "" || !config.NewSecret("").IsZero() {
		t.Errorf("un secreto vacío debe mostrarse vacío")
	}
}

func TestDisplayValuesRedactURLPassword(t *testing.T) {
	f := newFixture(t)
	f.env["BALANZAPRO_DB_DRIVER"] = "postgres"
	f.env["BALANZAPRO_POSTGRES_URL"] = "postgres://bp:" + secretPassword + "@db:5432/bp?sslmode=require&password=" + secretPassword
	l, err := config.Load(f.opts(""))
	if err != nil {
		t.Fatal(err)
	}
	shown := l.DisplayValues()["database.postgres.url"]
	if strings.Contains(shown, secretPassword) {
		t.Fatalf("DisplayValues revela el secreto: %s", shown)
	}
	if !strings.Contains(shown, "bp:xxxxx@db:5432") || !strings.Contains(shown, "sslmode=require") {
		t.Fatalf("la versión redactada debe conservar lo útil para soporte: %s", shown)
	}
}

func TestWorldReadableFileWithSecretWarns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("los permisos POSIX no aplican en Windows")
	}
	f := newFixture(t)
	path := f.write("c.toml", "[database]\ndriver = \"postgres\"\n[database.postgres]\nurl = \"postgres://u:"+secretPassword+"@h/d\"\n")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := config.Load(f.opts(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Warnings) == 0 {
		t.Fatalf("se esperaba advertencia por permisos 0644 con secreto")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if l, _ = config.Load(f.opts(path)); len(l.Warnings) != 0 {
		t.Fatalf("0600 no debe advertir: %v", l.Warnings)
	}
}

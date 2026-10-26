// Package config carga la configuración local de balanzapro.
//
// Prioridad, de menor a mayor: valores por defecto < archivo TOML <
// variables de entorno BALANZAPRO_*. Los secretos se aceptan en el archivo o
// en el entorno, nunca como flags de línea de comandos (sección 28.3).
// Ningún error ni representación de la configuración revela secretos.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

var ErrInvalid = errors.New("configuración inválida")

const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
)

// Source indica de dónde proviene un valor efectivo.
type Source string

const (
	SourceDefault Source = "default"
	SourceFile    Source = "file"
	SourceEnv     Source = "env"
)

type Config struct {
	Database Database `toml:"database"`
	Log      Log      `toml:"log"`
	Session  Session  `toml:"session"`
}

// Session indica dónde guarda la CLI el token de la sesión iniciada.
type Session struct {
	File string `toml:"file"`
}

type Database struct {
	Driver   string   `toml:"driver"`
	SQLite   SQLite   `toml:"sqlite"`
	Postgres Postgres `toml:"postgres"`
}

type SQLite struct {
	Path string `toml:"path"`
}

type Postgres struct {
	URL Secret `toml:"url"`
}

type Log struct {
	Level  string `toml:"level"`
	Format string `toml:"format"`
	// File vacío significa stderr.
	File string `toml:"file"`
}

// LoadOptions permite inyectar el entorno en pruebas.
type LoadOptions struct {
	// ExplicitPath proviene de --config; prevalece sobre BALANZAPRO_CONFIG.
	ExplicitPath string
	Getenv       func(string) string
	GOOS         string
	HomeDir      string
}

// SystemOptions usa el entorno real del proceso.
func SystemOptions(explicitPath string) LoadOptions {
	home, _ := os.UserHomeDir()
	return LoadOptions{ExplicitPath: explicitPath, Getenv: os.Getenv, GOOS: runtime.GOOS, HomeDir: home}
}

// Loaded es la configuración efectiva y su procedencia.
type Loaded struct {
	Config    Config
	Path      string
	FileFound bool
	Sources   map[string]Source
	Warnings  []string
}

// Paths son las ubicaciones por defecto según el sistema operativo.
type Paths struct {
	ConfigFile  string
	SQLiteFile  string
	SessionFile string
}

// DefaultPaths sigue las convenciones de cada plataforma: XDG en Linux y
// %APPDATA% / %LOCALAPPDATA% en Windows.
func DefaultPaths(o LoadOptions) Paths {
	if o.GOOS == "windows" {
		local := firstNonEmpty(o.Getenv("LOCALAPPDATA"), filepath.Join(o.HomeDir, "AppData", "Local"))
		return Paths{
			ConfigFile:  filepath.Join(firstNonEmpty(o.Getenv("APPDATA"), filepath.Join(o.HomeDir, "AppData", "Roaming")), "BalanzaPro", "config.toml"),
			SQLiteFile:  filepath.Join(local, "BalanzaPro", "balanzapro.db"),
			SessionFile: filepath.Join(local, "BalanzaPro", "session"),
		}
	}
	return Paths{
		ConfigFile:  filepath.Join(firstNonEmpty(o.Getenv("XDG_CONFIG_HOME"), filepath.Join(o.HomeDir, ".config")), "balanzapro", "config.toml"),
		SQLiteFile:  filepath.Join(firstNonEmpty(o.Getenv("XDG_DATA_HOME"), filepath.Join(o.HomeDir, ".local", "share")), "balanzapro", "balanzapro.db"),
		SessionFile: filepath.Join(firstNonEmpty(o.Getenv("XDG_STATE_HOME"), filepath.Join(o.HomeDir, ".local", "state")), "balanzapro", "session"),
	}
}

// field enlaza una clave pública con su variable de entorno y su destino.
type field struct {
	key string
	env string
	get func(*Config) string
	set func(*Config, string)
}

var fields = []field{
	{"database.driver", "BALANZAPRO_DB_DRIVER", func(c *Config) string { return c.Database.Driver }, func(c *Config, v string) { c.Database.Driver = v }},
	{"database.sqlite.path", "BALANZAPRO_SQLITE_PATH", func(c *Config) string { return c.Database.SQLite.Path }, func(c *Config, v string) { c.Database.SQLite.Path = v }},
	{"database.postgres.url", "BALANZAPRO_POSTGRES_URL", func(c *Config) string { return c.Database.Postgres.URL.Reveal() }, func(c *Config, v string) { c.Database.Postgres.URL = NewSecret(v) }},
	{"log.level", "BALANZAPRO_LOG_LEVEL", func(c *Config) string { return c.Log.Level }, func(c *Config, v string) { c.Log.Level = v }},
	{"log.format", "BALANZAPRO_LOG_FORMAT", func(c *Config) string { return c.Log.Format }, func(c *Config, v string) { c.Log.Format = v }},
	{"log.file", "BALANZAPRO_LOG_FILE", func(c *Config) string { return c.Log.File }, func(c *Config, v string) { c.Log.File = v }},
	{"session.file", "BALANZAPRO_SESSION_FILE", func(c *Config) string { return c.Session.File }, func(c *Config, v string) { c.Session.File = v }},
}

// Keys devuelve las claves de configuración en orden estable.
func Keys() []string {
	keys := make([]string, len(fields))
	for i, f := range fields {
		keys[i] = f.key
	}
	return keys
}

// EnvVar devuelve la variable de entorno asociada a una clave.
func EnvVar(key string) string {
	for _, f := range fields {
		if f.key == key {
			return f.env
		}
	}
	return ""
}

// Load construye la configuración efectiva y la valida.
func Load(o LoadOptions) (Loaded, error) {
	paths := DefaultPaths(o)
	l := Loaded{
		Config: Config{
			Database: Database{Driver: DriverSQLite, SQLite: SQLite{Path: paths.SQLiteFile}},
			Log:      Log{Level: "warn", Format: "text"},
			Session:  Session{File: paths.SessionFile},
		},
		Sources: map[string]Source{},
	}
	for _, f := range fields {
		l.Sources[f.key] = SourceDefault
	}

	l.Path = firstNonEmpty(o.ExplicitPath, o.Getenv("BALANZAPRO_CONFIG"))
	explicit := l.Path != ""
	if !explicit {
		l.Path = paths.ConfigFile
	}

	if err := l.loadFile(explicit); err != nil {
		return l, err
	}
	for _, f := range fields {
		if v, ok := lookupEnv(o.Getenv, f.env); ok {
			f.set(&l.Config, v)
			l.Sources[f.key] = SourceEnv
		}
	}
	return l, validate(l.Config)
}

func (l *Loaded) loadFile(explicit bool) error {
	data, err := os.ReadFile(l.Path)
	if errors.Is(err, fs.ErrNotExist) && !explicit {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: no se puede leer %s: %v", ErrInvalid, l.Path, errors.Unwrap(err))
	}
	l.FileFound = true

	md, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&l.Config)
	if err != nil {
		return fmt.Errorf("%w: %s: %s", ErrInvalid, l.Path, describeTOMLError(err))
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return fmt.Errorf("%w: %s: claves desconocidas: %s", ErrInvalid, l.Path, strings.Join(keys, ", "))
	}
	for _, f := range fields {
		if md.IsDefined(strings.Split(f.key, ".")...) {
			l.Sources[f.key] = SourceFile
		}
	}
	if md.IsDefined("database", "postgres", "url") && !l.Config.Database.Postgres.URL.IsZero() {
		l.Warnings = append(l.Warnings, permissionWarnings(l.Path)...)
	}
	return nil
}

// describeTOMLError informa línea, columna y clave sin copiar el contenido
// del archivo, que podría incluir secretos.
func describeTOMLError(err error) string {
	var pe toml.ParseError
	if errors.As(err, &pe) {
		msg := fmt.Sprintf("TOML inválido en la línea %d, columna %d", pe.Position.Line, pe.Position.Col)
		if pe.LastKey != "" {
			msg += fmt.Sprintf(" (clave %q)", pe.LastKey)
		}
		return msg
	}
	return "TOML inválido"
}

func validate(c Config) error {
	var problems []string
	if !slices.Contains([]string{DriverSQLite, DriverPostgres}, c.Database.Driver) {
		problems = append(problems, fmt.Sprintf("database.driver debe ser %q o %q, es %q", DriverSQLite, DriverPostgres, c.Database.Driver))
	}
	if c.Database.Driver == DriverSQLite && c.Database.SQLite.Path == "" {
		problems = append(problems, "database.sqlite.path es obligatorio con el driver sqlite")
	}
	if c.Database.Driver == DriverPostgres {
		if c.Database.Postgres.URL.IsZero() {
			problems = append(problems, "database.postgres.url es obligatorio con el driver postgres (archivo o BALANZAPRO_POSTGRES_URL)")
		} else if msg := checkPostgresURL(c.Database.Postgres.URL.Reveal()); msg != "" {
			problems = append(problems, "database.postgres.url "+msg)
		}
	}
	if !slices.Contains([]string{"debug", "info", "warn", "error"}, c.Log.Level) {
		problems = append(problems, fmt.Sprintf("log.level debe ser debug, info, warn o error, es %q", c.Log.Level))
	}
	if !slices.Contains([]string{"text", "json"}, c.Log.Format) {
		problems = append(problems, fmt.Sprintf("log.format debe ser text o json, es %q", c.Log.Format))
	}
	if c.Session.File == "" {
		problems = append(problems, "session.file no puede estar vacío")
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(problems, "; "))
	}
	return nil
}

// checkPostgresURL valida sin incluir la URL en el mensaje: url.Parse
// devuelve errores que contienen la cadena completa, credenciales incluidas.
func checkPostgresURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "no es una URL válida"
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "debe usar el esquema postgres:// o postgresql://"
	}
	if u.Host == "" {
		return "debe indicar el host"
	}
	return ""
}

// DisplayValues devuelve los valores efectivos aptos para mostrar: las URLs
// con credenciales se redactan conservando host, base y opciones.
func (l Loaded) DisplayValues() map[string]string {
	out := make(map[string]string, len(fields))
	for _, f := range fields {
		out[f.key] = f.get(&l.Config)
	}
	out["database.postgres.url"] = RedactURL(l.Config.Database.Postgres.URL.Reveal())
	return out
}

var sensitiveQueryKeys = []string{"password", "sslpassword", "passfile", "sslkey"}

// RedactURL oculta la contraseña y los parámetros sensibles de una URL. Si
// no puede analizarla, la oculta por completo.
func RedactURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return redacted
	}
	q := u.Query()
	changed := false
	for key := range q {
		if slices.Contains(sensitiveQueryKeys, strings.ToLower(key)) {
			q.Set(key, "xxxxx")
			changed = true
		}
	}
	if changed {
		u.RawQuery = q.Encode()
	}
	return u.Redacted()
}

func permissionWarnings(path string) []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if info.Mode().Perm()&0o077 != 0 {
		return []string{fmt.Sprintf("%s contiene credenciales y otros usuarios pueden leerlo (permisos %#o); use chmod 600", path, info.Mode().Perm())}
	}
	return nil
}

func lookupEnv(getenv func(string) string, key string) (string, bool) {
	v := getenv(key)
	return v, v != ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

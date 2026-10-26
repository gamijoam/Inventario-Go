// Package sqlite conecta con SQLite mediante modernc.org/sqlite (Go puro,
// sin CGO; ADR-0006) para la instalación de una sola PC.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registra el driver "sqlite"

	"github.com/gamijoam/inventario-go/internal/platform/migration"
)

var ErrNotInitialized = errors.New("la base de datos SQLite no existe todavía")

// Mode indica si Open puede crear el archivo.
type Mode int

const (
	// OpenExisting falla con ErrNotInitialized si el archivo no existe; lo
	// usan doctor y status para no crear bases vacías por accidente.
	OpenExisting Mode = iota
	// OpenOrCreate crea el archivo y su directorio (permisos 0700).
	OpenOrCreate
)

// Pragmas aplicados a cada conexión (sección 10.4, a validar en pruebas):
//   - foreign_keys: SQLite no aplica FKs por defecto.
//   - journal_mode=WAL: lectores concurrentes con un escritor.
//   - synchronous=FULL: durabilidad ante cortes de energía, a costa de velocidad.
//   - busy_timeout: espera al escritor en lugar de fallar con SQLITE_BUSY.
var pragmas = []string{"foreign_keys(1)", "journal_mode(WAL)", "synchronous(FULL)", "busy_timeout(5000)"}

// Open abre la base en path y verifica que los pragmas quedaron activos.
func Open(ctx context.Context, path string, mode Mode) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("ruta de SQLite vacía")
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		if mode == OpenExisting {
			return nil, fmt.Errorf("%w: %s", ErrNotInitialized, path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("crear el directorio de la base: %w", err)
		}
	}
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	// Las transacciones de escritura toman el bloqueo al empezar (BEGIN
	// IMMEDIATE) y así evitan interbloqueos al pasar de lectura a escritura.
	q.Set("_txlock", "immediate")

	db, err := sql.Open("sqlite", path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if err := verify(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if mode == OpenOrCreate {
		// La base puede contener datos comerciales: solo el usuario la lee.
		_ = os.Chmod(path, 0o600)
	}
	return db, nil
}

func verify(ctx context.Context, db *sql.DB) error {
	var fk int
	var journal string
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		return fmt.Errorf("abrir SQLite: %w", err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
		return fmt.Errorf("abrir SQLite: %w", err)
	}
	if fk != 1 || journal != "wal" {
		return fmt.Errorf("SQLite no aplicó la configuración requerida (foreign_keys=%d, journal_mode=%s)", fk, journal)
	}
	return nil
}

// Info describe el motor para diagnóstico.
func Info(ctx context.Context, db *sql.DB) (string, error) {
	var version, journal, sync string
	if err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		return "", err
	}
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
		return "", err
	}
	if err := db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync); err != nil {
		return "", err
	}
	return fmt.Sprintf("SQLite %s, journal %s, synchronous %s", version, journal, syncName(sync)), nil
}

func syncName(v string) string {
	switch v {
	case "0":
		return "off"
	case "1":
		return "normal"
	case "2":
		return "full"
	case "3":
		return "extra"
	default:
		return v
	}
}

// Backup copia la base de forma consistente con VACUUM INTO, incluso con
// conexiones abiertas. dest no debe existir.
func Backup(ctx context.Context, db *sql.DB, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("el respaldo %s ya existe", dest)
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		return fmt.Errorf("respaldar SQLite: %w", err)
	}
	return os.Chmod(dest, 0o600)
}

// Dialect describe SQLite para el migrador.
var Dialect = migration.Dialect{
	Name:        "sqlite",
	Placeholder: func(int) string { return "?" },
	CreateTable: `CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER NOT NULL PRIMARY KEY,
    name       TEXT    NOT NULL,
    checksum   TEXT    NOT NULL,
    applied_at TEXT    NOT NULL
) STRICT`,
	TableExists: "SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = 'schema_migrations'",
	Timestamp:   func(t time.Time) any { return FormatTime(t) },
}

// FormatTime es la representación canónica de instantes en SQLite: UTC,
// RFC 3339 con microsegundos y sufijo Z; ordenable como texto.
func FormatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000Z")
}

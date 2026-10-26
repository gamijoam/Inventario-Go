// Package persistence elige el motor según la configuración (DEC-004) y
// expone la conexión, el migrador y la identidad de la instalación.
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gamijoam/inventario-go/internal/adapters/persistence/postgres"
	"github.com/gamijoam/inventario-go/internal/adapters/persistence/sqlite"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/platform/config"
	"github.com/gamijoam/inventario-go/internal/platform/migration"
	"github.com/gamijoam/inventario-go/migrations"
)

// ErrNotInitialized indica que la base local todavía no existe.
var ErrNotInitialized = sqlite.ErrNotInitialized

// Store es una base abierta de cualquiera de los dos motores.
type Store struct {
	DB      *sql.DB
	Engine  string
	Dialect migration.Dialect
	// Location identifica la base sin secretos: ruta o URL redactada.
	Location string
}

// Open conecta según cfg. Con create=false no crea archivos SQLite.
func Open(ctx context.Context, cfg config.Database, create bool) (*Store, error) {
	switch cfg.Driver {
	case config.DriverSQLite:
		mode := sqlite.OpenExisting
		if create {
			mode = sqlite.OpenOrCreate
		}
		db, err := sqlite.Open(ctx, cfg.SQLite.Path, mode)
		if err != nil {
			return nil, err
		}
		return &Store{DB: db, Engine: config.DriverSQLite, Dialect: sqlite.Dialect, Location: cfg.SQLite.Path}, nil
	case config.DriverPostgres:
		db, err := postgres.Open(ctx, cfg.Postgres.URL)
		if err != nil {
			return nil, err
		}
		return &Store{DB: db, Engine: config.DriverPostgres, Dialect: postgres.Dialect, Location: config.RedactURL(cfg.Postgres.URL.Reveal())}, nil
	default:
		return nil, fmt.Errorf("driver de base de datos desconocido %q", cfg.Driver)
	}
}

func (s *Store) Close() error { return s.DB.Close() }

// Migrations devuelve las migraciones embebidas del motor.
func (s *Store) Migrations() ([]migration.Migration, error) {
	return migration.Load(migrations.FS, s.Engine)
}

// Runner devuelve el migrador del motor.
func (s *Store) Runner() (migration.Runner, error) {
	migs, err := s.Migrations()
	if err != nil {
		return migration.Runner{}, err
	}
	return migration.Runner{DB: s.DB, Dialect: s.Dialect, Migrations: migs}, nil
}

// Info describe el motor para diagnóstico.
func (s *Store) Info(ctx context.Context) (string, error) {
	if s.Engine == config.DriverSQLite {
		return sqlite.Info(ctx, s.DB)
	}
	return postgres.Info(ctx, s.DB)
}

// BackupSQLite crea una copia consistente de la base SQLite en dest antes
// de una migración. PostgreSQL se respalda con sus propias herramientas.
func (s *Store) BackupSQLite(ctx context.Context, dest string) error {
	if s.Engine != config.DriverSQLite {
		return errors.New("el respaldo integrado solo aplica a SQLite")
	}
	return sqlite.Backup(ctx, s.DB, dest)
}

// Installation es la identidad de esta base (nodo).
type Installation struct {
	ID        id.ID
	Engine    string
	CreatedAt time.Time
}

// EnsureInstallation registra la identidad de la instalación si no existe
// y devuelve la vigente; created indica si se creó ahora. Requiere la
// migración 0001.
func (s *Store) EnsureInstallation(ctx context.Context) (inst Installation, created bool, err error) {
	newID, err := id.New()
	if err != nil {
		return Installation{}, false, err
	}
	p := s.Dialect.Placeholder
	q := fmt.Sprintf("INSERT INTO installation (id, engine, created_at_utc) VALUES (%s, %s, %s) ON CONFLICT (singleton) DO NOTHING", p(1), p(2), p(3))
	res, err := s.DB.ExecContext(ctx, q, newID.String(), s.Engine, s.Dialect.Timestamp(time.Now().UTC()))
	if err != nil {
		return Installation{}, false, fmt.Errorf("registrar la instalación: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Installation{}, false, err
	}
	inst, err = s.Installation(ctx)
	return inst, n == 1, err
}

// Installation lee la identidad registrada; sql.ErrNoRows si no hay.
func (s *Store) Installation(ctx context.Context) (Installation, error) {
	var rawID, engine string
	var at any
	err := s.DB.QueryRowContext(ctx, "SELECT CAST(id AS TEXT), engine, created_at_utc FROM installation").Scan(&rawID, &engine, &at)
	if err != nil {
		return Installation{}, err
	}
	parsed, err := id.Parse(rawID)
	if err != nil {
		return Installation{}, fmt.Errorf("ID de instalación corrupto: %w", err)
	}
	created, err := parseTime(at)
	if err != nil {
		return Installation{}, err
	}
	return Installation{ID: parsed, Engine: engine, CreatedAt: created}, nil
}

func parseTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t.UTC(), nil
	case string:
		return time.Parse(time.RFC3339Nano, t)
	default:
		return time.Time{}, fmt.Errorf("instante con tipo inesperado %T", v)
	}
}

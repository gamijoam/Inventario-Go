// Package postgres conecta con PostgreSQL mediante pgx v5 (database/sql)
// para el servidor de sucursal multicaja y la nube.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/gamijoam/inventario-go/internal/platform/config"
	"github.com/gamijoam/inventario-go/internal/platform/logging"
	"github.com/gamijoam/inventario-go/internal/platform/migration"
)

var ErrInvalidURL = errors.New("URL de PostgreSQL inválida")

const defaultConnectTimeout = 5 * time.Second

// Open conecta con url y comprueba la conexión. Los errores nunca incluyen
// la contraseña.
func Open(ctx context.Context, url config.Secret) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(url.Reveal())
	if err != nil {
		// El error de pgx puede citar la cadena de conexión: no se propaga.
		return nil, ErrInvalidURL
	}
	// Los instantes viajan y se guardan en UTC; la presentación en
	// America/Caracas es responsabilidad de las interfaces (DEC-006.TZ).
	cfg.RuntimeParams["timezone"] = "UTC"
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	db := stdlib.OpenDB(*cfg)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("conectar a PostgreSQL en %s:%d/%s: %s",
			cfg.Host, cfg.Port, cfg.Database, oneLine(logging.RedactText(err.Error())))
	}
	return db, nil
}

// oneLine une los errores multilínea de pgx para que quepan en una línea
// de diagnóstico o de log.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " | ")), " ")
}

// Info describe el servidor para diagnóstico.
func Info(ctx context.Context, db *sql.DB) (string, error) {
	var version, tz string
	if err := db.QueryRowContext(ctx, "SELECT current_setting('server_version'), current_setting('TimeZone')").Scan(&version, &tz); err != nil {
		return "", err
	}
	return fmt.Sprintf("PostgreSQL %s, zona de sesión %s", version, tz), nil
}

// migrationLockKey identifica el bloqueo consultivo del migrador.
const migrationLockKey = 7_263_915_001

// Dialect describe PostgreSQL para el migrador.
var Dialect = migration.Dialect{
	Name:        "postgres",
	Placeholder: func(n int) string { return fmt.Sprintf("$%d", n) },
	CreateTable: `CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER     NOT NULL PRIMARY KEY,
    name       TEXT        NOT NULL,
    checksum   TEXT        NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL
)`,
	TableExists: "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'schema_migrations'",
	Lock:        fmt.Sprintf("SELECT pg_advisory_xact_lock(%d)", migrationLockKey),
	Timestamp:   func(t time.Time) any { return t.UTC() },
}

// Package migration aplica migraciones SQL versionadas con database/sql.
//
// Cada motor tiene su propio directorio de migraciones (SQL nativo) con la
// misma numeración, y un Dialect que aporta lo que difiere entre motores:
// marcadores de parámetros, tipos de la tabla de control y bloqueo.
//
// Garantías:
//   - Cada migración se aplica en su propia transacción junto con su
//     registro en schema_migrations: se aplica completa o no se aplica.
//   - Ejecuciones concurrentes no aplican dos veces la misma migración: se
//     toma un bloqueo y se vuelve a comprobar dentro de la transacción.
//   - Se rechaza una base con migraciones modificadas (checksum distinto) o
//     más nueva que el ejecutable.
package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"time"
)

var (
	ErrInvalidSet       = errors.New("conjunto de migraciones inválido")
	ErrChecksumMismatch = errors.New("una migración aplicada fue modificada")
	ErrDatabaseNewer    = errors.New("la base de datos tiene migraciones más nuevas que este ejecutable")
	ErrIrreversible     = errors.New("la migración no tiene script de reversión")
)

// Migration es una versión del esquema. Down vacío significa irreversible.
type Migration struct {
	Version  int
	Name     string
	Up       string
	Down     string
	Checksum string
}

var fileName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.(up|down)\.sql$`)

// Load lee dir dentro de fsys. Los archivos se llaman NNNN_nombre.up.sql y,
// opcionalmente, NNNN_nombre.down.sql; las versiones empiezan en 1 y no
// tienen huecos.
func Load(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSet, err)
	}
	byVersion := map[int]*Migration{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := fileName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("%w: nombre de archivo inesperado %q", ErrInvalidSet, e.Name())
		}
		version, _ := strconv.Atoi(m[1])
		body, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidSet, err)
		}
		mig := byVersion[version]
		if mig == nil {
			mig = &Migration{Version: version, Name: m[2]}
			byVersion[version] = mig
		}
		if mig.Name != m[2] {
			return nil, fmt.Errorf("%w: la versión %04d tiene nombres distintos (%s, %s)", ErrInvalidSet, version, mig.Name, m[2])
		}
		if m[3] == "up" {
			mig.Up = string(body)
		} else {
			mig.Down = string(body)
		}
	}
	out := make([]Migration, 0, len(byVersion))
	for _, m := range byVersion {
		if m.Up == "" {
			return nil, fmt.Errorf("%w: la versión %04d no tiene .up.sql", ErrInvalidSet, m.Version)
		}
		sum := sha256.Sum256([]byte(m.Up))
		m.Checksum = hex.EncodeToString(sum[:])
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("%w: se esperaba la versión %04d y se encontró %04d", ErrInvalidSet, i+1, m.Version)
		}
	}
	return out, nil
}

// Dialect contiene lo que cambia entre motores.
type Dialect struct {
	Name string
	// Placeholder devuelve el marcador del parámetro n (desde 1).
	Placeholder func(n int) string
	// CreateTable crea schema_migrations si no existe.
	CreateTable string
	// TableExists devuelve una fila con un entero > 0 si existe schema_migrations.
	TableExists string
	// Lock, si no está vacío, se ejecuta al inicio de cada transacción para
	// serializar migradores concurrentes.
	Lock string
	// Timestamp convierte un instante UTC al valor que guarda el motor.
	Timestamp func(time.Time) any
}

// Applied es una migración registrada en la base.
type Applied struct {
	Version   int
	Name      string
	Checksum  string
	AppliedAt string
}

// Status compara la base con las migraciones del ejecutable.
type Status struct {
	Applied []Applied
	Pending []Migration
	// Unknown son versiones aplicadas que este ejecutable no conoce.
	Unknown []int
	// Modified son versiones aplicadas cuyo checksum no coincide.
	Modified []int
}

// Problem devuelve el error de integridad del estado, si lo hay.
func (s Status) Problem() error {
	if len(s.Modified) > 0 {
		return fmt.Errorf("%w: versiones %v", ErrChecksumMismatch, s.Modified)
	}
	if len(s.Unknown) > 0 {
		return fmt.Errorf("%w: versiones %v; actualice balanzapro", ErrDatabaseNewer, s.Unknown)
	}
	return nil
}

// Runner aplica Migrations sobre DB.
type Runner struct {
	DB         *sql.DB
	Dialect    Dialect
	Migrations []Migration
}

// Status lee el estado sin modificar la base.
func (r Runner) Status(ctx context.Context) (Status, error) {
	var exists int
	if err := r.DB.QueryRowContext(ctx, r.Dialect.TableExists).Scan(&exists); err != nil {
		return Status{}, fmt.Errorf("consultar schema_migrations: %w", err)
	}
	var applied []Applied
	if exists > 0 {
		var err error
		if applied, err = r.applied(ctx, r.DB); err != nil {
			return Status{}, err
		}
	}
	return r.compare(applied), nil
}

func (r Runner) compare(applied []Applied) Status {
	st := Status{Applied: applied}
	known := map[int]Migration{}
	for _, m := range r.Migrations {
		known[m.Version] = m
	}
	done := map[int]bool{}
	for _, a := range applied {
		done[a.Version] = true
		m, ok := known[a.Version]
		switch {
		case !ok:
			st.Unknown = append(st.Unknown, a.Version)
		case m.Checksum != a.Checksum:
			st.Modified = append(st.Modified, a.Version)
		}
	}
	for _, m := range r.Migrations {
		if !done[m.Version] {
			st.Pending = append(st.Pending, m)
		}
	}
	return st
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (r Runner) applied(ctx context.Context, q querier) ([]Applied, error) {
	rows, err := q.QueryContext(ctx, "SELECT version, name, checksum, applied_at FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("leer schema_migrations: %w", err)
	}
	defer rows.Close()
	var out []Applied
	for rows.Next() {
		var a Applied
		var at any
		if err := rows.Scan(&a.Version, &a.Name, &a.Checksum, &at); err != nil {
			return nil, fmt.Errorf("leer schema_migrations: %w", err)
		}
		a.AppliedAt = formatTime(at)
		out = append(out, a)
	}
	return out, rows.Err()
}

func formatTime(v any) string {
	switch t := v.(type) {
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprint(v)
	}
}

// Up aplica todas las migraciones pendientes y devuelve las aplicadas.
func (r Runner) Up(ctx context.Context) ([]Migration, error) {
	if err := r.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, r.Dialect.CreateTable)
		return err
	}); err != nil {
		return nil, fmt.Errorf("crear schema_migrations: %w", err)
	}
	st, err := r.Status(ctx)
	if err != nil {
		return nil, err
	}
	if err := st.Problem(); err != nil {
		return nil, err
	}
	var done []Migration
	for _, m := range st.Pending {
		applied, err := r.apply(ctx, m)
		if err != nil {
			return done, err
		}
		if applied {
			done = append(done, m)
		}
	}
	return done, nil
}

// apply ejecuta una migración si nadie la aplicó mientras tanto.
func (r Runner) apply(ctx context.Context, m Migration) (bool, error) {
	applied := false
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		var n int
		q := "SELECT COUNT(*) FROM schema_migrations WHERE version = " + r.Dialect.Placeholder(1)
		if err := tx.QueryRowContext(ctx, q, m.Version).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, m.Up); err != nil {
			return err
		}
		p := r.Dialect.Placeholder
		ins := fmt.Sprintf("INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (%s, %s, %s, %s)", p(1), p(2), p(3), p(4))
		if _, err := tx.ExecContext(ctx, ins, m.Version, m.Name, m.Checksum, r.Dialect.Timestamp(time.Now().UTC())); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("migración %04d_%s: %w", m.Version, m.Name, err)
	}
	return applied, nil
}

// Down revierte las últimas steps migraciones aplicadas, de la más nueva a
// la más antigua. Falla sin cambios si alguna es irreversible.
func (r Runner) Down(ctx context.Context, steps int) ([]Migration, error) {
	st, err := r.Status(ctx)
	if err != nil {
		return nil, err
	}
	if err := st.Problem(); err != nil {
		return nil, err
	}
	byVersion := map[int]Migration{}
	for _, m := range r.Migrations {
		byVersion[m.Version] = m
	}
	var targets []Migration
	for i := len(st.Applied) - 1; i >= 0 && len(targets) < steps; i-- {
		m := byVersion[st.Applied[i].Version]
		if m.Down == "" {
			return nil, fmt.Errorf("%w: %04d_%s", ErrIrreversible, m.Version, m.Name)
		}
		targets = append(targets, m)
	}
	var done []Migration
	for _, m := range targets {
		err := r.inTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.Down); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = "+r.Dialect.Placeholder(1), m.Version)
			return err
		})
		if err != nil {
			return done, fmt.Errorf("revertir %04d_%s: %w", m.Version, m.Name, err)
		}
		done = append(done, m)
	}
	return done, nil
}

func (r Runner) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if r.Dialect.Lock != "" {
		if _, err := tx.ExecContext(ctx, r.Dialect.Lock); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

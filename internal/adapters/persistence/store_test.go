package persistence_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/adapters/persistence"
	"github.com/gamijoam/inventario-go/internal/adapters/persistence/sqlite"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/platform/config"
	"github.com/gamijoam/inventario-go/internal/platform/migration"
	"github.com/gamijoam/inventario-go/internal/testutil/pgtest"
)

// engine prepara una base vacía de un motor y devuelve su configuración.
type engine struct {
	name  string
	setup func(t *testing.T) config.Database
}

var engines = []engine{
	{"sqlite", func(t *testing.T) config.Database {
		return config.Database{Driver: config.DriverSQLite, SQLite: config.SQLite{Path: filepath.Join(t.TempDir(), "bp.db")}}
	}},
	{"postgres", func(t *testing.T) config.Database {
		return config.Database{Driver: config.DriverPostgres, Postgres: config.Postgres{URL: config.NewSecret(pgtest.NewDatabase(t))}}
	}},
}

// forEachEngine ejecuta la misma prueba de comportamiento en ambos motores.
func forEachEngine(t *testing.T, fn func(t *testing.T, cfg config.Database)) {
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) { fn(t, e.setup(t)) })
	}
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func open(t *testing.T, cfg config.Database) *persistence.Store {
	t.Helper()
	s, err := persistence.Open(ctx(t), cfg, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func migrated(t *testing.T, cfg config.Database) (*persistence.Store, migration.Runner) {
	t.Helper()
	s := open(t, cfg)
	r, err := s.Runner()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Up(ctx(t)); err != nil {
		t.Fatalf("Up: %v", err)
	}
	return s, r
}

func TestUpAppliesOnceAndStatusReflectsIt(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		s := open(t, cfg)
		r, err := s.Runner()
		if err != nil {
			t.Fatal(err)
		}
		st, err := r.Status(ctx(t))
		if err != nil || len(st.Applied) != 0 || len(st.Pending) != len(r.Migrations) {
			t.Fatalf("estado inicial = %+v, %v", st, err)
		}
		done, err := r.Up(ctx(t))
		if err != nil || len(done) != len(r.Migrations) {
			t.Fatalf("Up = %d migraciones, %v", len(done), err)
		}
		again, err := r.Up(ctx(t))
		if err != nil || len(again) != 0 {
			t.Fatalf("segunda ejecución aplicó %d, %v", len(again), err)
		}
		st, err = r.Status(ctx(t))
		if err != nil || len(st.Pending) != 0 || len(st.Applied) != len(r.Migrations) || st.Problem() != nil {
			t.Fatalf("estado final = %+v, %v", st, err)
		}
		if _, err := time.Parse(time.RFC3339Nano, st.Applied[0].AppliedAt); err != nil {
			t.Errorf("applied_at no es RFC 3339: %q", st.Applied[0].AppliedAt)
		}
	})
}

func TestConcurrentMigratorsApplyEachMigrationOnce(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		const workers = 4
		var wg sync.WaitGroup
		applied := make(chan int, workers)
		errs := make(chan error, workers)
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, err := persistence.Open(context.Background(), cfg, true)
				if err != nil {
					errs <- err
					return
				}
				defer s.Close()
				r, err := s.Runner()
				if err != nil {
					errs <- err
					return
				}
				done, err := r.Up(context.Background())
				if err != nil {
					errs <- err
					return
				}
				applied <- len(done)
			}()
		}
		wg.Wait()
		close(applied)
		close(errs)
		for err := range errs {
			t.Errorf("migrador concurrente falló: %v", err)
		}
		total := 0
		for n := range applied {
			total += n
		}
		_, r := migrated(t, cfg)
		if total != len(r.Migrations) {
			t.Fatalf("se aplicaron %d migraciones en total, se esperaban %d", total, len(r.Migrations))
		}
	})
}

func TestEnsureInstallationIsStable(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		s, _ := migrated(t, cfg)
		first, created, err := s.EnsureInstallation(ctx(t))
		if err != nil || !created {
			t.Fatalf("primera vez: created=%v err=%v", created, err)
		}
		if b := first.ID.Bytes(); b[6]>>4 != 7 || first.Engine != cfg.Driver {
			t.Fatalf("instalación = %+v", first)
		}
		if time.Since(first.CreatedAt) > time.Minute || first.CreatedAt.Location() != time.UTC {
			t.Fatalf("CreatedAt = %v", first.CreatedAt)
		}
		second, created, err := s.EnsureInstallation(ctx(t))
		if err != nil || created || second.ID != first.ID {
			t.Fatalf("segunda vez: %+v created=%v err=%v", second, created, err)
		}
	})
}

func TestSchemaConstraintsAreEnforced(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		s, _ := migrated(t, cfg)
		inst, _, err := s.EnsureInstallation(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		p := s.Dialect.Placeholder
		now := s.Dialect.Timestamp(time.Now())
		insInst := "INSERT INTO installation (id, engine, created_at_utc) VALUES (" + p(1) + ", " + p(2) + ", " + p(3) + ")"
		insSetting := "INSERT INTO installation_settings (installation_id, key, value, updated_at_utc) VALUES (" + p(1) + ", " + p(2) + ", " + p(3) + ", " + p(4) + ")"

		rejected := map[string]func() error{
			"segunda instalación (singleton)": func() error {
				_, err := s.DB.ExecContext(ctx(t), insInst, id.MustNew().String(), cfg.Driver, now)
				return err
			},
			"FK a instalación inexistente": func() error {
				_, err := s.DB.ExecContext(ctx(t), insSetting, id.MustNew().String(), "k", "v", now)
				return err
			},
			"clave vacía": func() error {
				_, err := s.DB.ExecContext(ctx(t), insSetting, inst.ID.String(), "", "v", now)
				return err
			},
			"borrar instalación referenciada": func() error {
				if _, err := s.DB.ExecContext(ctx(t), insSetting, inst.ID.String(), "ref", "v", now); err != nil {
					return nil // no debería fallar aquí; el caso se reporta abajo
				}
				_, err := s.DB.ExecContext(ctx(t), "DELETE FROM installation")
				return err
			},
		}
		for name, fn := range rejected {
			if err := fn(); err == nil {
				t.Errorf("%s: la base debía rechazarlo", name)
			}
		}

		if _, err := s.DB.ExecContext(ctx(t), insSetting, inst.ID.String(), "printer", "termica-80mm", now); err != nil {
			t.Fatalf("ajuste válido rechazado: %v", err)
		}
		if _, err := s.DB.ExecContext(ctx(t), insSetting, inst.ID.String(), "printer", "otra", now); err == nil {
			t.Errorf("la clave primaria compuesta debía rechazar el duplicado")
		}
	})
}

func TestEngineCheckConstraint(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		s, _ := migrated(t, cfg)
		p := s.Dialect.Placeholder
		_, err := s.DB.ExecContext(ctx(t), "INSERT INTO installation (id, engine, created_at_utc) VALUES ("+p(1)+", "+p(2)+", "+p(3)+")",
			id.MustNew().String(), "mysql", s.Dialect.Timestamp(time.Now()))
		if err == nil {
			t.Fatal("engine inválido debía rechazarse")
		}
	})
}

func TestDownThenUpRestoresSchema(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		s, r := migrated(t, cfg)
		down, err := r.Down(ctx(t), 1)
		if err != nil || len(down) != 1 {
			t.Fatalf("Down = %d, %v", len(down), err)
		}
		if _, err := s.DB.ExecContext(ctx(t), "SELECT 1 FROM installation"); err == nil {
			t.Fatal("la tabla debía eliminarse")
		}
		st, err := r.Status(ctx(t))
		if err != nil || len(st.Pending) != len(r.Migrations) {
			t.Fatalf("Status tras Down = %+v, %v", st, err)
		}
		if _, err := r.Up(ctx(t)); err != nil {
			t.Fatalf("Up tras Down: %v", err)
		}
		if _, _, err := s.EnsureInstallation(ctx(t)); err != nil {
			t.Fatalf("esquema no restaurado: %v", err)
		}
	})
}

func TestIntegrityProblemsBlockMigration(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		s, r := migrated(t, cfg)

		tampered := r
		tampered.Migrations = append([]migration.Migration(nil), r.Migrations...)
		tampered.Migrations[0].Checksum = "0000"
		if _, err := tampered.Up(ctx(t)); !errors.Is(err, migration.ErrChecksumMismatch) {
			t.Errorf("checksum modificado: se esperaba ErrChecksumMismatch, obtuvo %v", err)
		}

		p := s.Dialect.Placeholder
		_, err := s.DB.ExecContext(ctx(t), "INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES ("+p(1)+", "+p(2)+", "+p(3)+", "+p(4)+")",
			999, "del_futuro", "x", s.Dialect.Timestamp(time.Now()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Up(ctx(t)); !errors.Is(err, migration.ErrDatabaseNewer) {
			t.Errorf("versión desconocida: se esperaba ErrDatabaseNewer, obtuvo %v", err)
		}
		if _, err := r.Down(ctx(t), 1); !errors.Is(err, migration.ErrDatabaseNewer) {
			t.Errorf("Down también debe negarse: %v", err)
		}
	})
}

func TestSQLiteOpenExistingDoesNotCreateFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "bp.db")
	_, err := persistence.Open(ctx(t), config.Database{Driver: config.DriverSQLite, SQLite: config.SQLite{Path: path}}, false)
	if !errors.Is(err, persistence.ErrNotInitialized) {
		t.Fatalf("se esperaba ErrNotInitialized, obtuvo %v", err)
	}
	if _, statErr := os.Stat(filepath.Dir(path)); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("no debía crear el directorio: %v", statErr)
	}
}

func TestSQLiteBackupCanBeRestored(t *testing.T) {
	cfg := engines[0].setup(t)
	s, _ := migrated(t, cfg)
	inst, _, err := s.EnsureInstallation(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "respaldo.db")
	if err := s.BackupSQLite(ctx(t), dest); err != nil {
		t.Fatal(err)
	}
	if err := s.BackupSQLite(ctx(t), dest); err == nil {
		t.Fatal("no debe sobrescribir un respaldo existente")
	}

	restored := open(t, config.Database{Driver: config.DriverSQLite, SQLite: config.SQLite{Path: dest}})
	var check string
	if err := restored.DB.QueryRowContext(ctx(t), "PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity_check = %q, %v", check, err)
	}
	got, err := restored.Installation(ctx(t))
	if err != nil || got.ID != inst.ID {
		t.Fatalf("el respaldo no conserva la instalación: %+v, %v", got, err)
	}
	r, _ := restored.Runner()
	if st, err := r.Status(ctx(t)); err != nil || len(st.Pending) != 0 {
		t.Fatalf("el respaldo no conserva las migraciones: %+v, %v", st, err)
	}
}

func TestSQLitePragmasAreActive(t *testing.T) {
	s := open(t, engines[0].setup(t))
	info, err := s.Info(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"journal wal", "synchronous full"} {
		if !strings.Contains(info, want) {
			t.Errorf("Info = %q, falta %q", info, want)
		}
	}
	if sqlite.FormatTime(time.Date(2026, 10, 25, 12, 0, 0, 5000, time.UTC)) != "2026-10-25T12:00:00.000005Z" {
		t.Errorf("FormatTime no canónico")
	}
}

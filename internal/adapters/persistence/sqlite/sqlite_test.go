package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gamijoam/inventario-go/internal/adapters/persistence/sqlite"
)

func openTemp(t testing.TB) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "bp.db"), sqlite.OpenOrCreate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenAppliesPragmasOnEveryConnection(t *testing.T) {
	db := openTemp(t)
	db.SetMaxOpenConns(4)
	ctx := context.Background()
	// Varias conexiones simultáneas: cada una debe tener foreign_keys activo.
	conns := make([]*sql.Conn, 4)
	for i := range conns {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns[i] = c
	}
	for i, c := range conns {
		var fk, timeout int
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
			t.Errorf("conexión %d: foreign_keys=%d, %v", i, fk, err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
			t.Errorf("conexión %d: busy_timeout=%d, %v", i, timeout, err)
		}
		_ = c.Close()
	}
}

func TestOpenExistingRequiresFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nada.db")
	if _, err := sqlite.Open(context.Background(), path, sqlite.OpenExisting); !errors.Is(err, sqlite.ErrNotInitialized) {
		t.Fatalf("se esperaba ErrNotInitialized, obtuvo %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("no debía crear el archivo")
	}
	if _, err := sqlite.Open(context.Background(), "", sqlite.OpenOrCreate); err == nil {
		t.Fatal("ruta vacía debía fallar")
	}
}

func TestCreatedFilesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("los permisos POSIX no aplican en Windows")
	}
	dir := filepath.Join(t.TempDir(), "nuevo")
	path := filepath.Join(dir, "bp.db")
	db, err := sqlite.Open(context.Background(), path, sqlite.OpenOrCreate)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for p, want := range map[string]os.FileMode{dir: 0o700, path: 0o600} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: permisos %v, se esperaba %v (%v)", p, info.Mode().Perm(), want, err)
		}
	}
}

func TestPathsWithSpacesAndAccents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Mis Documentos", "Panadería Ñandú", "bp.db")
	db, err := sqlite.Open(context.Background(), path, sqlite.OpenOrCreate)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("el archivo debía crearse en la ruta exacta: %v", err)
	}
}

// Benchmarks de referencia para ADR-0006 (ver docs/decisions). Ejecutar:
//
//	go test -run '^$' -bench . ./internal/adapters/persistence/sqlite/
func BenchmarkInsertBatchOf1000InOneTransaction(b *testing.B) {
	db := openTemp(b)
	ctx := context.Background()
	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, sku TEXT NOT NULL, qty TEXT NOT NULL) STRICT"); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			b.Fatal(err)
		}
		for j := range 1000 {
			if _, err := tx.ExecContext(ctx, "INSERT INTO t (sku, qty) VALUES (?, ?)", fmt.Sprintf("SKU-%d-%d", i, j), "1.250"); err != nil {
				b.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

// Una transacción por operación con synchronous=FULL: aproxima el costo de
// confirmar una venta pequeña de forma durable.
func BenchmarkCommitPerOperation(b *testing.B) {
	db := openTemp(b)
	ctx := context.Background()
	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, sku TEXT NOT NULL, qty TEXT NOT NULL) STRICT"); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO t (sku, qty) VALUES (?, ?)", fmt.Sprintf("SKU-%d", i), "1.250"); err != nil {
			b.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPointLookup(b *testing.B) {
	db := openTemp(b)
	ctx := context.Background()
	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, sku TEXT NOT NULL UNIQUE, qty TEXT NOT NULL) STRICT"); err != nil {
		b.Fatal(err)
	}
	tx, _ := db.Begin()
	for j := range 50000 {
		if _, err := tx.Exec("INSERT INTO t (sku, qty) VALUES (?, ?)", fmt.Sprintf("SKU-%d", j), "1"); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		var qty string
		if err := db.QueryRowContext(ctx, "SELECT qty FROM t WHERE sku = ?", fmt.Sprintf("SKU-%d", i%50000)).Scan(&qty); err != nil {
			b.Fatal(err)
		}
	}
}

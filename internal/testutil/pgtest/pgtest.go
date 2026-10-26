// Package pgtest crea bases PostgreSQL desechables para pruebas.
//
// Usa BALANZAPRO_TEST_POSTGRES_URL (un usuario con permiso CREATEDB). Si no
// está definida, la prueba se omite, salvo que BALANZAPRO_REQUIRE_POSTGRES=1
// (CI de Linux), en cuyo caso falla: así una CI mal configurada no pasa en
// verde sin probar PostgreSQL.
package pgtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// NewDatabase crea una base vacía y devuelve su URL; se elimina al terminar.
func NewDatabase(t testing.TB) string {
	t.Helper()
	admin := os.Getenv("BALANZAPRO_TEST_POSTGRES_URL")
	if admin == "" {
		if os.Getenv("BALANZAPRO_REQUIRE_POSTGRES") == "1" {
			t.Fatal("BALANZAPRO_REQUIRE_POSTGRES=1 pero BALANZAPRO_TEST_POSTGRES_URL no está definida")
		}
		t.Skip("PostgreSQL no disponible: defina BALANZAPRO_TEST_POSTGRES_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := sql.Open("pgx", admin)
	if err != nil {
		t.Fatalf("pgtest: %v", err)
	}
	defer db.Close()

	var suffix [6]byte
	_, _ = rand.Read(suffix[:])
	name := "bp_test_" + hex.EncodeToString(suffix[:])
	if _, err := db.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("pgtest: crear base de prueba: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		db, err := sql.Open("pgx", admin)
		if err != nil {
			return
		}
		defer db.Close()
		_, _ = db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})

	u, err := url.Parse(admin)
	if err != nil {
		t.Fatalf("pgtest: URL inválida")
	}
	u.Path = "/" + name
	return u.String()
}

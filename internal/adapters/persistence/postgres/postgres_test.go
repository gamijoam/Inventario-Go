package postgres_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/adapters/persistence/postgres"
	"github.com/gamijoam/inventario-go/internal/platform/config"
	"github.com/gamijoam/inventario-go/internal/testutil/pgtest"
)

func TestInvalidURLDoesNotEchoInput(t *testing.T) {
	_, err := postgres.Open(context.Background(), config.NewSecret("postgres://u:s3cr3t@h:no-es-puerto/d"))
	if !errors.Is(err, postgres.ErrInvalidURL) || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("err = %v", err)
	}
}

func TestWrongPasswordErrorDoesNotRevealPassword(t *testing.T) {
	good := pgtest.NewDatabase(t)
	u, _ := url.Parse(good)
	const wrong = "Clave-Incorrecta-123"
	u.User = url.UserPassword(u.User.Username(), wrong)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := postgres.Open(ctx, config.NewSecret(u.String()))
	if err == nil {
		t.Fatal("se esperaba error de autenticación")
	}
	if strings.Contains(err.Error(), wrong) {
		t.Fatalf("el error revela la contraseña: %v", err)
	}
	if !strings.Contains(err.Error(), u.Hostname()) {
		t.Fatalf("el error debe indicar el host para soporte: %v", err)
	}
}

func TestSessionUsesUTC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, config.NewSecret(pgtest.NewDatabase(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	info, err := postgres.Info(ctx, db)
	if err != nil || !strings.Contains(info, "zona de sesión UTC") {
		t.Fatalf("Info = %q, %v", info, err)
	}
}

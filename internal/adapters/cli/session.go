package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gamijoam/inventario-go/internal/adapters/persistence"
	"github.com/gamijoam/inventario-go/internal/application/auth"
	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/application/purchasing"
	"github.com/gamijoam/inventario-go/internal/application/rates"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/exchange"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/pricing"
	"github.com/gamijoam/inventario-go/internal/domain/purchase"
	"github.com/gamijoam/inventario-go/internal/platform/security"
)

// appSession es lo que necesita un comando de negocio: la base abierta, los
// servicios y el usuario autenticado. El ámbito proviene siempre de la
// sesión, nunca de IDs recibidos por la CLI (sección 26.3).
type appSession struct {
	rt    runtime
	store *persistence.Store
	org   *apporg.Service
	auth  *auth.Service
	cat   *appcatalog.Service
	stock *stock.Service
	buy   *purchasing.Service
	fx    *rates.Service
	price *apppricing.Service
	cur   apporg.Context
	who   auth.Authenticated
	close func()
}

func (s appSession) principal() identity.Principal { return s.who.Principal }

func newServices(store *persistence.Store) (*apporg.Service, *auth.Service) {
	now := func() time.Time { return time.Now().UTC() }
	return apporg.NewService(store.OrgStore(), now),
		auth.NewService(store.AuthStore(), security.Hasher{Params: security.DefaultArgon2}, security.Tokens{}, now)
}

// openStore abre la base existente con el esquema al día y la instalación
// configurada.
func openStore(ctx context.Context, env Env, flags *globalFlags, errOut io.Writer) (appSession, error) {
	rt, err := loadRuntime(env, flags, errOut)
	if err != nil {
		return appSession{}, err
	}
	store, err := persistence.Open(ctx, rt.loaded.Config.Database, false)
	if errors.Is(err, persistence.ErrNotInitialized) {
		rt.close()
		return appSession{}, appError(apporg.ErrNotBootstrapped)
	}
	if err != nil {
		rt.close()
		return appSession{}, unavailableError(err)
	}
	s := appSession{rt: rt, store: store, close: func() { _ = store.Close(); _ = rt.close() }}
	if err := requireSchema(ctx, store); err != nil {
		s.close()
		return appSession{}, err
	}
	s.org, s.auth = newServices(store)
	s.cat = appcatalog.NewService(store.CatalogStore(), func() time.Time { return time.Now().UTC() })
	s.stock = stock.NewService(store.StockStore(), func() time.Time { return time.Now().UTC() })
	s.buy = purchasing.NewService(store.PurchaseStore(), s.stock, func() time.Time { return time.Now().UTC() })
	s.fx = rates.NewService(store.RateStore(), func() time.Time { return time.Now().UTC() })
	s.price = apppricing.NewService(store.PricingStore(), func() time.Time { return time.Now().UTC() })
	if s.cur, err = s.org.Current(ctx); err != nil {
		s.close()
		return appSession{}, appError(err)
	}
	return s, nil
}

// openSession es openStore más la sesión iniciada con 'auth login'.
func openSession(ctx context.Context, env Env, flags *globalFlags, errOut io.Writer) (appSession, error) {
	s, err := openStore(ctx, env, flags, errOut)
	if err != nil {
		return appSession{}, err
	}
	token, err := readSessionFile(s.rt.loaded.Config.Session.File)
	if err == nil {
		s.who, err = s.auth.Authenticate(ctx, token)
	}
	if err != nil {
		s.rt.logger.Info("sesión rechazada", "reason", err.Error())
		s.close()
		return appSession{}, appError(err)
	}
	return s, nil
}

func withSession(cmd *cobra.Command, env Env, flags *globalFlags, fn func(context.Context, appSession) error) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), dbTimeout)
	defer cancel()
	s, err := openSession(ctx, env, flags, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	defer s.close()
	return fn(ctx, s)
}

// errNoSession equivale a ErrSessionInvalid con un mensaje propio.
var errNoSession error = noSession{}

type noSession struct{}

func (noSession) Error() string { return "no hay sesión iniciada en este equipo" }
func (noSession) Unwrap() error { return identity.ErrSessionInvalid }

func readSessionFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", errNoSession
	}
	if err != nil {
		return "", fmt.Errorf("leer la sesión %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// writeSessionFile guarda el token solo para el usuario del sistema
// operativo (0600) y de forma atómica.
func writeSessionFile(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func removeSessionFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// secrets lee contraseñas: con --password-stdin, una por línea de stdin;
// si no, con eco desactivado en la terminal. Nunca como argumento.
type secrets struct {
	env       Env
	fromStdin bool
	lines     *bufio.Reader
}

func newSecrets(env Env, fromStdin bool) *secrets {
	return &secrets{env: env, fromStdin: fromStdin, lines: bufio.NewReader(env.In)}
}

func (s *secrets) read(prompt string, confirm bool) (string, error) {
	if s.fromStdin {
		line, err := s.lines.ReadString('\n')
		if err != nil && !(errors.Is(err, io.EOF) && line != "") {
			return "", usageErrorf("--password-stdin: falta una línea con la contraseña en la entrada estándar")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	if !s.env.Interactive || s.env.ReadPassword == nil {
		return "", usageErrorf("se necesita una terminal para pedir la contraseña; use --password-stdin")
	}
	pw, err := s.env.ReadPassword(prompt + ": ")
	if err != nil {
		return "", err
	}
	if confirm {
		again, err := s.env.ReadPassword("Repita la contraseña: ")
		if err != nil {
			return "", err
		}
		if again != pw {
			return "", usageErrorf("las contraseñas no coinciden")
		}
	}
	return pw, nil
}

// line lee un dato visible (p. ej. el usuario) de la terminal.
func (s *secrets) line(prompt string) (string, error) {
	if s.fromStdin || !s.env.Interactive {
		return "", usageErrorf("falta --user")
	}
	fmt.Fprint(s.env.Err, prompt+": ")
	line, err := s.lines.ReadString('\n')
	if err != nil && line == "" {
		return "", usageErrorf("falta --user")
	}
	return strings.TrimSpace(line), nil
}

// appError traduce errores de la capa de aplicación a códigos de salida.
func appError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, identity.ErrSessionInvalid):
		return &exitError{code: ExitUnauthenticated, kind: "unauthenticated", msg: err.Error() + "; ejecute 'balanzapro auth login'"}
	case errors.Is(err, auth.ErrInvalidCredentials):
		return &exitError{code: ExitUnauthenticated, kind: "unauthenticated", msg: err.Error()}
	case auth.IsDenied(err):
		return &exitError{code: ExitForbidden, kind: "forbidden", msg: err.Error()}
	case errors.Is(err, apporg.ErrConflict), errors.Is(err, apporg.ErrAlreadyBootstrapped):
		return &exitError{code: ExitConflict, kind: "conflict", msg: err.Error()}
	case errors.Is(err, apporg.ErrNotFound), errors.Is(err, apporg.ErrNotBootstrapped):
		return &exitError{code: ExitNotFound, kind: "not_found", msg: err.Error()}
	case errors.Is(err, inventory.ErrValuation), errors.Is(err, exchange.ErrNoRate), errors.Is(err, pricing.ErrStale):
		return &exitError{code: ExitConflict, kind: "conflict", msg: err.Error()}
	case errors.Is(err, inventory.ErrInsufficient):
		return &exitError{code: ExitInsufficientStock, kind: "insufficient_stock", msg: err.Error()}
	case errors.Is(err, org.ErrInvalid), errors.Is(err, identity.ErrInvalid), errors.Is(err, catalog.ErrInvalid), errors.Is(err, inventory.ErrInvalid),
		errors.Is(err, purchase.ErrInvalid), errors.Is(err, exchange.ErrInvalid),
		errors.Is(err, pricing.ErrInvalid):
		return usageErrorf("%s", err)
	default:
		return err
	}
}

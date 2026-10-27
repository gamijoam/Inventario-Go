// Package rates contiene los casos de uso del registro de tasas de cambio
// (BP-M3-002): registrar una tasa con su fuente, ver su historial y saber
// cuál rige en una fecha.
//
// Una tasa queda vigente al registrarla, desde su fecha y hasta que otra la
// reemplace (ratificado 2026-10-27). No hay descarga automática: quien la
// registra declara la fuente. Una corrección es una versión nueva; los
// documentos que ya aplicaron la anterior la conservan.
package rates

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/domain/exchange"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

// Repository es el puerto de persistencia de las tasas.
type Repository interface {
	CompanyByCode(ctx context.Context, orgID id.ID, code org.Code) (org.Company, error)
	// NextRateVersion es la versión siguiente del par en la empresa.
	NextRateVersion(ctx context.Context, orgID, companyID id.ID, pair string) (int, error)
	// InsertRate envuelve apporg.ErrConflict si la versión ya existe.
	InsertRate(ctx context.Context, r exchange.Rate) error
	Rate(ctx context.Context, orgID, rateID id.ID) (exchange.Rate, error)
	// Rates lista las tasas de la empresa por fecha y versión; pair vacío
	// las incluye todas.
	Rates(ctx context.Context, orgID, companyID id.ID, pair string) ([]exchange.Rate, error)
	// EffectiveRate es la tasa del par que rige el día on; envuelve
	// exchange.ErrNoRate si no hay ninguna.
	EffectiveRate(ctx context.Context, orgID, companyID id.ID, pair string, on time.Time) (exchange.Rate, error)
}

type Store interface {
	InTx(ctx context.Context, fn func(Repository) error) error
}

type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store, now func() time.Time) *Service {
	return &Service{store: store, now: now}
}

// SetInput registra una tasa: 1 Base = Rate Quote desde Date.
type SetInput struct {
	Company     string
	Base, Quote string
	Rate        string
	Source      string // official, manual o provider
	Label       string // p. ej. "BCV"
	Reference   string
	Date        string // AAAA-MM-DD; vacía es hoy en Caracas
	Note        string
}

// RateView es una tasa con su empresa.
type RateView struct {
	exchange.Rate
	Company org.Company
}

// maxAttempts acota los reintentos cuando dos registros del mismo par
// toman la misma versión a la vez.
const maxAttempts = 8

// Set registra una tasa. Exige gestionar tasas con ámbito de empresa: la
// tasa rige para todas sus sucursales.
func (s *Service) Set(ctx context.Context, p identity.Principal, in SetInput) (RateView, error) {
	base, err := ParseCurrency(in.Base)
	if err != nil {
		return RateView{}, err
	}
	quote, err := ParseCurrency(in.Quote)
	if err != nil {
		return RateView{}, err
	}
	typ, err := exchange.ParseSourceType(in.Source)
	if err != nil {
		return RateView{}, err
	}
	now := s.now()
	on := timezone.DateOf(now)
	if strings.TrimSpace(in.Date) != "" {
		if on, err = ParseDate(in.Date); err != nil {
			return RateView{}, err
		}
	}
	var out RateView
	for range maxAttempts {
		err = s.store.InTx(ctx, func(r Repository) error {
			c, err := company(ctx, r, p, in.Company, identity.PermRateManage)
			if err != nil {
				return err
			}
			if err := p.Authorize(identity.PermRateManage, c.Scope(), "la empresa "+c.Code.String()); err != nil {
				return err
			}
			rate, err := exchange.NewRate(p.OrganizationID, c.ID, exchange.RateInput{Base: base, Quote: quote, Value: in.Rate,
				Source: money.RateSource{Type: typ, Label: in.Label}, Reference: in.Reference, ValidOn: on, Note: in.Note}, p.UserID, now)
			if err != nil {
				return err
			}
			if rate.Version, err = r.NextRateVersion(ctx, p.OrganizationID, c.ID, rate.Pair()); err != nil {
				return err
			}
			if err := r.InsertRate(ctx, rate); err != nil {
				return err
			}
			out = RateView{Rate: rate, Company: c}
			return nil
		})
		if !errors.Is(err, apporg.ErrConflict) {
			return out, err
		}
	}
	return RateView{}, err
}

// List devuelve el historial de tasas de la empresa, del más antiguo al más
// reciente. pair ("USD/VES") vacío las incluye todas.
func (s *Service) List(ctx context.Context, p identity.Principal, companyCode, pair string) ([]RateView, error) {
	var out []RateView
	err := s.store.InTx(ctx, func(r Repository) error {
		c, err := company(ctx, r, p, companyCode, identity.PermRateRead)
		if err != nil {
			return err
		}
		key := ""
		if strings.TrimSpace(pair) != "" {
			if key, err = ParsePair(pair); err != nil {
				return err
			}
		}
		list, err := r.Rates(ctx, p.OrganizationID, c.ID, key)
		for _, x := range list {
			out = append(out, RateView{Rate: x, Company: c})
		}
		return err
	})
	return out, err
}

// Effective devuelve la tasa entre a y b que rige en date (vacía es hoy).
func (s *Service) Effective(ctx context.Context, p identity.Principal, companyCode, a, b, date string) (RateView, error) {
	ca, err := ParseCurrency(a)
	if err != nil {
		return RateView{}, err
	}
	cb, err := ParseCurrency(b)
	if err != nil {
		return RateView{}, err
	}
	if ca == cb {
		return RateView{}, fmt.Errorf("%w: el par necesita dos monedas distintas", exchange.ErrInvalid)
	}
	on := timezone.DateOf(s.now())
	if strings.TrimSpace(date) != "" {
		if on, err = ParseDate(date); err != nil {
			return RateView{}, err
		}
	}
	var out RateView
	err = s.store.InTx(ctx, func(r Repository) error {
		c, err := company(ctx, r, p, companyCode, identity.PermRateRead)
		if err != nil {
			return err
		}
		rate, err := r.EffectiveRate(ctx, p.OrganizationID, c.ID, exchange.Pair(ca, cb), on)
		out = RateView{Rate: rate, Company: c}
		return err
	})
	return out, err
}

// company resuelve una empresa visible; para leer exige ver tasas en ella
// (registrar lo autoriza quien llama, con ámbito de empresa).
func company(ctx context.Context, r Repository, p identity.Principal, code string, perm identity.Permission) (org.Company, error) {
	cc, err := org.ParseCode(code)
	if err != nil {
		return org.Company{}, err
	}
	c, err := r.CompanyByCode(ctx, p.OrganizationID, cc)
	if err != nil {
		return c, err
	}
	if !p.Sees(identity.PermOrgRead, c.Scope()) && !p.Sees(perm, c.Scope()) {
		return c, fmt.Errorf("%w: empresa %s", apporg.ErrNotFound, cc)
	}
	if perm == identity.PermRateRead && !p.Sees(perm, c.Scope()) {
		return c, fmt.Errorf("%w: se requiere %s", identity.ErrDenied, perm)
	}
	return c, nil
}

// ParseCurrency acepta una moneda habilitada, sin distinguir mayúsculas.
func ParseCurrency(s string) (money.Currency, error) {
	c, err := money.Lookup(strings.ToUpper(strings.TrimSpace(s)))
	if err != nil {
		return c, fmt.Errorf("%w: %w", exchange.ErrInvalid, err)
	}
	return c, nil
}

// ParsePair lee "USD/VES" (en cualquier orden) y devuelve su clave.
func ParsePair(s string) (string, error) {
	a, b, ok := strings.Cut(s, "/")
	if !ok {
		return "", fmt.Errorf("%w: par %q (use USD/VES)", exchange.ErrInvalid, s)
	}
	ca, err := ParseCurrency(a)
	if err != nil {
		return "", err
	}
	cb, err := ParseCurrency(b)
	if err != nil {
		return "", err
	}
	if ca == cb {
		return "", fmt.Errorf("%w: el par necesita dos monedas distintas", exchange.ErrInvalid)
	}
	return exchange.Pair(ca, cb), nil
}

// ParseDate lee una fecha comercial AAAA-MM-DD.
func ParseDate(s string) (time.Time, error) {
	d, err := timezone.ParseDate(strings.TrimSpace(s))
	if err != nil {
		return d, fmt.Errorf("%w: fecha %q (use AAAA-MM-DD)", exchange.ErrInvalid, s)
	}
	return d, nil
}

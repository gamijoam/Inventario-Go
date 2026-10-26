package identity

import (
	"errors"
	"fmt"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/id"
)

// ErrSessionInvalid cubre toda sesión inexistente, vencida o revocada; el
// detalle solo va envuelto para el registro.
var ErrSessionInvalid = errors.New("sesión no válida o vencida")

// SessionPolicy fija la expiración por inactividad y la absoluta.
type SessionPolicy struct {
	Idle, Absolute time.Duration
	// ClockSkew es el retroceso de reloj tolerado antes de invalidar la
	// sesión (sección 27.1, punto 5: fallar en modo seguro).
	ClockSkew time.Duration
}

// DefaultSession es la política aprobada: 1 h de inactividad, 12 h máximo.
var DefaultSession = SessionPolicy{Idle: time.Hour, Absolute: 12 * time.Hour, ClockSkew: 5 * time.Minute}

type Session struct {
	ID             id.ID
	OrganizationID id.ID
	UserID         id.ID
	CreatedAt      time.Time
	LastSeenAt     time.Time
	ExpiresAt      time.Time
	RevokedAt      time.Time
}

func (p SessionPolicy) New(u User, now time.Time) (Session, error) {
	sid, err := id.New()
	if err != nil {
		return Session{}, err
	}
	now = now.UTC()
	return Session{ID: sid, OrganizationID: u.OrganizationID, UserID: u.ID, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(p.Absolute)}, nil
}

// Check valida la sesión en now.
func (p SessionPolicy) Check(s Session, now time.Time) error {
	switch {
	case !s.RevokedAt.IsZero():
		return fmt.Errorf("%w (revocada)", ErrSessionInvalid)
	case !now.Before(s.ExpiresAt):
		return fmt.Errorf("%w (duración máxima alcanzada)", ErrSessionInvalid)
	case now.Sub(s.LastSeenAt) >= p.Idle:
		return fmt.Errorf("%w (inactividad)", ErrSessionInvalid)
	case s.LastSeenAt.Sub(now) > p.ClockSkew:
		return fmt.Errorf("%w (el reloj del equipo retrocedió)", ErrSessionInvalid)
	}
	return nil
}

// Touch registra actividad.
func (s Session) Touch(now time.Time) Session {
	if now.After(s.LastSeenAt) {
		s.LastSeenAt = now.UTC()
	}
	return s
}

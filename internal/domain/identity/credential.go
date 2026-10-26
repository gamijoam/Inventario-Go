package identity

import (
	"bufio"
	_ "embed"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gamijoam/inventario-go/internal/domain/id"
)

// Longitudes de contraseña aprobadas (DEC-009.AUTH, ADR-0008). Sin reglas de
// composición, como recomienda NIST SP 800-63B rev. 4.
const (
	MinPasswordLength = 8
	MaxPasswordLength = 128
)

//go:embed common_passwords.txt
var commonPasswordsFile string

var commonPasswords = func() map[string]struct{} {
	m := map[string]struct{}{}
	sc := bufio.NewScanner(strings.NewReader(commonPasswordsFile))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "#") {
			m[strings.ToLower(line)] = struct{}{}
		}
	}
	return m
}()

// CheckPassword aplica la política de contraseñas. El mensaje de error
// nunca incluye la contraseña.
func CheckPassword(pw string, username Username) error {
	n := utf8.RuneCountInString(pw)
	if !utf8.ValidString(pw) {
		return fmt.Errorf("%w: la contraseña no es texto UTF-8 válido", ErrInvalid)
	}
	if n < MinPasswordLength || n > MaxPasswordLength {
		return fmt.Errorf("%w: la contraseña debe tener entre %d y %d caracteres", ErrInvalid, MinPasswordLength, MaxPasswordLength)
	}
	for _, r := range pw {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: la contraseña contiene caracteres de control", ErrInvalid)
		}
	}
	lower := strings.ToLower(pw)
	first, _ := utf8.DecodeRuneInString(lower)
	if strings.Count(lower, string(first)) == n {
		return fmt.Errorf("%w: la contraseña no puede repetir un solo carácter", ErrInvalid)
	}
	if _, ok := commonPasswords[lower]; ok {
		return fmt.Errorf("%w: la contraseña es demasiado común; elija otra", ErrInvalid)
	}
	if (!username.IsZero() && strings.Contains(lower, username.String())) || strings.Contains(lower, "balanzapro") {
		return fmt.Errorf("%w: la contraseña no puede contener el usuario ni el nombre del sistema", ErrInvalid)
	}
	return nil
}

// Lockout es el estado de intentos fallidos de una credencial.
type Lockout struct {
	FailedAttempts int
	// Streak cuenta los bloqueos consecutivos sin un acceso correcto.
	Streak      int
	LockedUntil time.Time
}

// LockoutPolicy: tras Threshold fallos seguidos la credencial se bloquea
// Base, y cada bloqueo siguiente sin acceso correcto dura el doble, hasta Max.
type LockoutPolicy struct {
	Threshold int
	Base, Max time.Duration
}

// DefaultLockout es la política aprobada: 5 fallos → 15 min, 30, 60… hasta 24 h.
var DefaultLockout = LockoutPolicy{Threshold: 5, Base: 15 * time.Minute, Max: 24 * time.Hour}

func (p LockoutPolicy) Locked(l Lockout, now time.Time) bool { return now.Before(l.LockedUntil) }

// Fail registra un intento fallido.
func (p LockoutPolicy) Fail(l Lockout, now time.Time) Lockout {
	l.FailedAttempts++
	if l.FailedAttempts < p.Threshold {
		return l
	}
	l.FailedAttempts = 0
	l.Streak++
	d := p.Base
	for i := 1; i < l.Streak && d < p.Max; i++ {
		d *= 2
	}
	l.LockedUntil = now.Add(min(d, p.Max)).UTC()
	return l
}

// Succeed reinicia el estado tras un acceso correcto o un desbloqueo.
func (p LockoutPolicy) Succeed() Lockout { return Lockout{} }

// Credential es el verificador de contraseña de un usuario: nunca la
// contraseña.
type Credential struct {
	UserID    id.ID
	Hash      string
	ChangedAt time.Time
	Lockout   Lockout
}

package identity_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/identity"
)

func TestPasswordPolicy(t *testing.T) {
	ana, _ := identity.ParseUsername("ana")
	ok := []string{"cafe con leche", "Ñandú#2026x", "s3gur4-y-l4rg4", strings.Repeat("ab", 64)}
	for _, pw := range ok {
		if err := identity.CheckPassword(pw, ana); err != nil {
			t.Errorf("%q rechazada: %v", pw, err)
		}
	}
	bad := map[string]string{
		"corta":                 "1234567",
		"larga":                 strings.Repeat("x1", 65),
		"común":                 "Password1",
		"común local":           "VENEZUELA1",
		"un solo carácter":      "zzzzzzzzzz",
		"contiene el usuario":   "soyana2026",
		"contiene el sistema":   "MiBalanzaPro!",
		"caracteres de control": "abc\x00defghi",
		"UTF-8 inválido":        "abcdefg\xff",
	}
	for name, pw := range bad {
		err := identity.CheckPassword(pw, ana)
		if !errors.Is(err, identity.ErrInvalid) {
			t.Errorf("%s: %q aceptada", name, pw)
			continue
		}
		if strings.Contains(err.Error(), pw) {
			t.Errorf("%s: el error revela la contraseña: %v", name, err)
		}
	}
	// 8 caracteres exactos y multibyte cuentan como caracteres, no bytes.
	if err := identity.CheckPassword("ñññññññá", ana); err != nil {
		t.Errorf("8 runas: %v", err)
	}
}

func TestLockoutGrowsAndResets(t *testing.T) {
	p := identity.DefaultLockout
	t0 := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	var l identity.Lockout
	for i := 0; i < 4; i++ {
		l = p.Fail(l, t0)
	}
	if p.Locked(l, t0) {
		t.Fatal("4 fallos no bloquean")
	}
	l = p.Fail(l, t0)
	if !p.Locked(l, t0) || !l.LockedUntil.Equal(t0.Add(15*time.Minute)) {
		t.Fatalf("5.º fallo: %+v", l)
	}
	if p.Locked(l, t0.Add(15*time.Minute)) {
		t.Fatal("el bloqueo vence a los 15 min")
	}
	want := []time.Duration{30 * time.Minute, time.Hour, 2 * time.Hour}
	for _, d := range want {
		t1 := l.LockedUntil
		for i := 0; i < 5; i++ {
			l = p.Fail(l, t1)
		}
		if got := l.LockedUntil.Sub(t1); got != d {
			t.Fatalf("racha %d: bloqueo de %v, se esperaba %v", l.Streak, got, d)
		}
	}
	for i := 0; i < 50; i++ {
		l = p.Fail(l, t0)
	}
	if got := l.LockedUntil.Sub(t0); got != 24*time.Hour {
		t.Fatalf("el máximo es 24 h, fue %v", got)
	}
	if l = p.Succeed(); l != (identity.Lockout{}) {
		t.Fatalf("Succeed = %+v", l)
	}
}

func TestSessionExpiry(t *testing.T) {
	tr := newTree(t)
	u, _ := identity.NewUser(tr.o, "ana", "Ana", now)
	p := identity.DefaultSession
	s, err := p.New(u, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Check(s, now.Add(59*time.Minute)); err != nil {
		t.Fatalf("59 min: %v", err)
	}
	if err := p.Check(s, now.Add(time.Hour)); !errors.Is(err, identity.ErrSessionInvalid) {
		t.Fatalf("1 h inactiva: %v", err)
	}
	// Actividad cada 50 min mantiene la sesión hasta el máximo de 12 h.
	at := now
	for at.Before(now.Add(11 * time.Hour)) {
		at = at.Add(50 * time.Minute)
		if err := p.Check(s, at); err != nil {
			t.Fatalf("%v: %v", at.Sub(now), err)
		}
		s = s.Touch(at)
	}
	if err := p.Check(s, now.Add(12*time.Hour)); !errors.Is(err, identity.ErrSessionInvalid) {
		t.Fatalf("12 h: %v", err)
	}
	s2, _ := p.New(u, now)
	if err := p.Check(s2, now.Add(-10*time.Minute)); !errors.Is(err, identity.ErrSessionInvalid) {
		t.Fatalf("reloj atrasado 10 min: %v", err)
	}
	if err := p.Check(s2, now.Add(-time.Minute)); err != nil {
		t.Fatalf("desvío tolerable: %v", err)
	}
	s2.RevokedAt = now
	if err := p.Check(s2, now); !errors.Is(err, identity.ErrSessionInvalid) {
		t.Fatalf("revocada: %v", err)
	}
}

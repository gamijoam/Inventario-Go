package config

import "log/slog"

const redacted = "[REDACTED]"

// Secret guarda un valor sensible (contraseña, URL con credenciales).
// Ninguna forma de impresión, JSON ni log revela el valor; solo Reveal.
type Secret struct {
	value string
}

func NewSecret(v string) Secret { return Secret{value: v} }

// Reveal devuelve el valor real. Úsese solo para entregarlo a quien lo
// necesita (p. ej. el driver de base de datos), nunca para mostrarlo.
func (s Secret) Reveal() string { return s.value }

func (s Secret) IsZero() bool { return s.value == "" }

func (s Secret) String() string {
	if s.value == "" {
		return ""
	}
	return redacted
}

func (s Secret) GoString() string { return "config.Secret{" + s.String() + "}" }

func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText permite decodificar el secreto desde TOML.
func (s *Secret) UnmarshalText(b []byte) error {
	s.value = string(b)
	return nil
}

// LogValue hace que slog registre el secreto redactado.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// Package logging crea loggers estructurados (log/slog) que redactan
// secretos antes de escribir.
//
// Se redacta: cualquier atributo cuya clave sugiera un secreto (password,
// pin, token, secret, dsn, authorization…), cualquier config.Secret sea cual
// sea su clave, y la contraseña de URLs con credenciales dentro de mensajes
// y valores de texto. Es una red de seguridad: el código no debe registrar
// secretos a propósito.
package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/gamijoam/inventario-go/internal/platform/config"
)

const redacted = "[REDACTED]"

// Palabras que marcan una clave como sensible en cualquier posición.
var sensitiveSubstrings = []string{
	"password", "passwd", "secret", "token", "authorization", "cookie",
	"credential", "apikey", "privatekey",
}

// Palabras cortas que solo cuentan como token completo, para no redactar
// claves como "shipping" o "mapping".
var sensitiveTokens = []string{"pin", "pass", "pwd", "dsn", "otp"}

// URLs con usuario:contraseña@ dentro de texto libre.
var credentialURL = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s/@:]*:[^\s/@]*@[^\s]*`)

// New crea un logger que escribe en w.
func New(w io.Writer, opts config.Log) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(opts.Level)); err != nil {
		return nil, fmt.Errorf("nivel de log inválido %q", opts.Level)
	}
	ho := &slog.HandlerOptions{Level: level, ReplaceAttr: redact}
	switch opts.Format {
	case "text":
		return slog.New(&messageRedactor{slog.NewTextHandler(w, ho)}), nil
	case "json":
		return slog.New(&messageRedactor{slog.NewJSONHandler(w, ho)}), nil
	default:
		return nil, fmt.Errorf("formato de log inválido %q", opts.Format)
	}
}

// Open crea el logger según la configuración: en opts.File si está
// definido (permisos 0600, se agrega al final) o en fallback. El cierre
// devuelto libera el archivo.
func Open(opts config.Log, fallback io.Writer) (*slog.Logger, func() error, error) {
	if opts.File == "" {
		l, err := New(fallback, opts)
		return l, func() error { return nil }, err
	}
	if err := os.MkdirAll(filepath.Dir(opts.File), 0o700); err != nil {
		return nil, nil, fmt.Errorf("no se puede crear el directorio de logs: %w", errors.Unwrap(err))
	}
	f, err := os.OpenFile(opts.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("no se puede abrir el archivo de log: %w", errors.Unwrap(err))
	}
	l, err := New(f, opts)
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return l, f.Close, nil
}

// Discard es un logger que no escribe nada (p. ej. durante la TUI).
func Discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func redact(groups []string, a slog.Attr) slog.Attr {
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, redacted)
	}
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, RedactText(a.Value.String()))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			return slog.String(a.Key, RedactText(err.Error()))
		}
		if s, ok := a.Value.Any().(fmt.Stringer); ok {
			return slog.String(a.Key, RedactText(s.String()))
		}
	}
	return a
}

func isSensitiveKey(key string) bool {
	tokens := keyTokens(key)
	joined := strings.Join(tokens, "")
	for _, part := range sensitiveSubstrings {
		if strings.Contains(joined, part) {
			return true
		}
	}
	for _, tok := range tokens {
		if slices.Contains(sensitiveTokens, tok) {
			return true
		}
	}
	return false
}

// keyTokens separa una clave en palabras en minúsculas por separadores y
// por cambios de minúscula a mayúscula: "userPIN_code" → user, pin, code.
func keyTokens(key string) []string {
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	prevLower := false
	for _, r := range key {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if unicode.IsUpper(r) && prevLower {
				flush()
			}
			cur.WriteRune(r)
			prevLower = unicode.IsLower(r) || unicode.IsDigit(r)
		default:
			flush()
			prevLower = false
		}
	}
	flush()
	return tokens
}

// RedactText oculta la contraseña de las URLs con credenciales en s.
func RedactText(s string) string {
	return credentialURL.ReplaceAllStringFunc(s, config.RedactURL)
}

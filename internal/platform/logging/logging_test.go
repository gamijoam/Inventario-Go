package logging_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/platform/config"
	"github.com/gamijoam/inventario-go/internal/platform/logging"
)

const pw = "s3cr3t-P4ss"

func newLogger(t *testing.T, format string) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	l, err := logging.New(&buf, config.Log{Level: "debug", Format: format})
	if err != nil {
		t.Fatal(err)
	}
	return l, &buf
}

func TestSensitiveKeysAreRedacted(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		l, buf := newLogger(t, format)
		l.Info("evento",
			"password", pw,
			"PIN", pw,
			"api_token", pw,
			"Authorization", "Bearer "+pw,
			"client_secret", pw,
			slog.Group("db", "dsn", pw, "pass", pw),
			"usuario", "ana",
		)
		out := buf.String()
		if strings.Contains(out, pw) {
			t.Fatalf("%s: el log revela el secreto:\n%s", format, out)
		}
		if !strings.Contains(out, "ana") || !strings.Contains(out, "[REDACTED]") {
			t.Fatalf("%s: se perdieron datos no sensibles o falta la marca:\n%s", format, out)
		}
	}
}

func TestSensitiveKeyDetection(t *testing.T) {
	sensitive := []string{"password", "DB_PASSWORD", "userPin", "user_pin", "PIN", "pass", "api_key",
		"apiKey", "accessToken", "Authorization", "session-cookie", "dsn", "privateKey", "otp"}
	harmless := []string{"shipping", "mapping", "spinner", "passenger", "compass", "keyboard", "idempotency_key", "usuario"}
	for _, k := range sensitive {
		l, buf := newLogger(t, "text")
		l.Info("x", k, pw)
		if strings.Contains(buf.String(), pw) {
			t.Errorf("clave %q debía redactarse", k)
		}
	}
	for _, k := range harmless {
		l, buf := newLogger(t, "text")
		l.Info("x", k, "visible")
		if !strings.Contains(buf.String(), "visible") {
			t.Errorf("clave %q no debía redactarse", k)
		}
	}
}

func TestSecretValuesAreRedactedWhateverTheKey(t *testing.T) {
	l, buf := newLogger(t, "json")
	l.Info("conexión", "destino", config.NewSecret("postgres://u:"+pw+"@h/d"))
	if strings.Contains(buf.String(), pw) || !strings.Contains(buf.String(), "[REDACTED]") {
		t.Fatalf("Secret no redactado:\n%s", buf)
	}
}

func TestURLCredentialsInsideStringsAreRedacted(t *testing.T) {
	l, buf := newLogger(t, "text")
	l.Warn("no se pudo conectar a postgres://bp:"+pw+"@db:5432/bp?sslmode=require",
		"detalle", "dial postgresql://bp:"+pw+"@db/bp: timeout",
		"err", errors.New("fallo en postgres://bp:"+pw+"@db/bp"))
	out := buf.String()
	if strings.Contains(out, pw) {
		t.Fatalf("URL con credenciales sin redactar:\n%s", out)
	}
	if !strings.Contains(out, "bp:xxxxx@db:5432") {
		t.Fatalf("la URL redactada debe conservar usuario y host:\n%s", out)
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l, err := logging.New(&buf, config.Log{Level: "warn", Format: "text"})
	if err != nil {
		t.Fatal(err)
	}
	l.Info("silenciado")
	l.Warn("visible")
	if strings.Contains(buf.String(), "silenciado") || !strings.Contains(buf.String(), "visible") {
		t.Fatalf("filtrado por nivel incorrecto:\n%s", buf.String())
	}
}

func TestJSONFormatProducesOneObjectPerLine(t *testing.T) {
	l, buf := newLogger(t, "json")
	l.Info("uno")
	l.Info("dos")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("se esperaban 2 líneas:\n%s", buf.String())
	}
	for _, line := range lines {
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("línea no es JSON: %q", line)
		}
	}
}

func TestInvalidOptionsFail(t *testing.T) {
	if _, err := logging.New(&bytes.Buffer{}, config.Log{Level: "verbose", Format: "text"}); err == nil {
		t.Errorf("nivel inválido debía fallar")
	}
	if _, err := logging.New(&bytes.Buffer{}, config.Log{Level: "info", Format: "xml"}); err == nil {
		t.Errorf("formato inválido debía fallar")
	}
}

func TestOpenWritesToFileWithPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "bp.log")
	l, closeFn, err := logging.Open(config.Log{Level: "info", Format: "text", File: path}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	l.Info("hola", "password", pw)
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hola") || strings.Contains(string(data), pw) {
		t.Fatalf("contenido del archivo:\n%s", data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("el archivo de log debe ser privado, permisos %#o", info.Mode().Perm())
	}
}

func TestOpenWithoutFileUsesFallback(t *testing.T) {
	var fallback bytes.Buffer
	l, closeFn, err := logging.Open(config.Log{Level: "info", Format: "text"}, &fallback)
	if err != nil {
		t.Fatal(err)
	}
	l.Info("a stderr")
	_ = closeFn()
	if !strings.Contains(fallback.String(), "a stderr") {
		t.Fatalf("no escribió en el destino por defecto")
	}
}

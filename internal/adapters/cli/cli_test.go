package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
	"github.com/gamijoam/inventario-go/internal/adapters/tui"
	"github.com/gamijoam/inventario-go/internal/platform/config"
)

type result struct {
	code        int
	stdout      string
	stderr      string
	tuiOpened   bool
	tuiNoColour bool
}

type runOpts struct {
	interactive bool
	env         map[string]string
	home        string
}

func run(t *testing.T, o runOpts, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	var r result
	if o.home == "" {
		o.home = t.TempDir()
	}
	r.code = cli.Execute(context.Background(), args, cli.Env{
		ConfigOptions: func(explicit string) config.LoadOptions {
			return config.LoadOptions{ExplicitPath: explicit, Getenv: func(k string) string { return o.env[k] }, GOOS: "linux", HomeDir: o.home}
		},
		In:          strings.NewReader(""),
		Out:         &out,
		Err:         &errOut,
		Getenv:      func(k string) string { return o.env[k] },
		Interactive: o.interactive,
		RunTUI: func(_ context.Context, _ io.Reader, _ io.Writer, opts tui.Options) error {
			r.tuiOpened, r.tuiNoColour = true, opts.NoColor
			if opts.Doctor == nil {
				t.Errorf("la TUI debe recibir el caso de uso de diagnóstico")
			}
			return nil
		},
	})
	r.stdout, r.stderr = out.String(), errOut.String()
	return r
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("salida no es un JSON válido: %v\n%s", err, s)
	}
	return v
}

func assertNoANSI(t *testing.T, s string) {
	t.Helper()
	if strings.Contains(s, "\x1b") {
		t.Fatalf("la salida contiene secuencias ANSI: %q", s)
	}
}

func TestVersionText(t *testing.T) {
	r := run(t, runOpts{}, "version")
	if r.code != cli.ExitOK || !strings.HasPrefix(r.stdout, "balanzapro ") || r.stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", r.code, r.stdout, r.stderr)
	}
}

func TestVersionJSONIsVersionedAndClean(t *testing.T) {
	r := run(t, runOpts{}, "version", "--json")
	if r.code != cli.ExitOK || r.stderr != "" {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	assertNoANSI(t, r.stdout)
	v := decode(t, r.stdout)
	if v["schema"] != "balanzapro.version/v1" {
		t.Fatalf("schema = %v", v["schema"])
	}
	for _, k := range []string{"version", "commit", "build_date", "go_version", "os", "arch", "modified"} {
		if _, ok := v[k]; !ok {
			t.Errorf("falta el campo %q", k)
		}
	}
}

func TestVersionFlag(t *testing.T) {
	r := run(t, runOpts{}, "--version")
	if r.code != cli.ExitOK || strings.TrimSpace(r.stdout) == "" {
		t.Fatalf("code=%d stdout=%q", r.code, r.stdout)
	}
}

func TestDoctorText(t *testing.T) {
	r := run(t, runOpts{}, "doctor")
	if r.code != cli.ExitOK {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	for _, want := range []string{"[ OK ] timezone", "America/Caracas", "decimal", "terminal", "0 fallos"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("falta %q en:\n%s", want, r.stdout)
		}
	}
	assertNoANSI(t, r.stdout)
}

func TestDoctorJSON(t *testing.T) {
	r := run(t, runOpts{}, "doctor", "--json")
	if r.code != cli.ExitOK || r.stderr != "" {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	assertNoANSI(t, r.stdout)
	v := decode(t, r.stdout)
	if v["schema"] != "balanzapro.doctor/v1" || v["healthy"] != true {
		t.Fatalf("payload = %v", v)
	}
	checks, ok := v["checks"].([]any)
	if !ok || len(checks) < 4 {
		t.Fatalf("checks = %v", v["checks"])
	}
	first := checks[0].(map[string]any)
	if first["name"] != "timezone" || first["status"] != "ok" {
		t.Fatalf("primera verificación = %v", first)
	}
}

func TestUsageErrorsExitWithCode2(t *testing.T) {
	cases := [][]string{
		{"nope"},
		{"version", "extra"},
		{"--flag-inexistente"},
		{"doctor", "--no-color=quizas"},
	}
	for _, args := range cases {
		r := run(t, runOpts{}, args...)
		if r.code != cli.ExitUsage {
			t.Errorf("%v: code=%d, se esperaba %d (stderr=%q)", args, r.code, cli.ExitUsage, r.stderr)
		}
		if r.stdout != "" {
			t.Errorf("%v: stdout debe quedar vacío, es %q", args, r.stdout)
		}
		if !strings.HasPrefix(r.stderr, "Error: ") || !strings.Contains(r.stderr, "--help") {
			t.Errorf("%v: stderr = %q", args, r.stderr)
		}
	}
}

func TestUsageErrorAsJSONGoesToStderr(t *testing.T) {
	r := run(t, runOpts{}, "nope", "--json")
	if r.code != cli.ExitUsage || r.stdout != "" {
		t.Fatalf("code=%d stdout=%q", r.code, r.stdout)
	}
	v := decode(t, r.stderr)
	errBody, _ := v["error"].(map[string]any)
	if v["schema"] != "balanzapro.error/v1" || errBody["kind"] != "usage" || errBody["message"] == "" {
		t.Fatalf("error JSON = %v", v)
	}
}

func TestBareCommandOpensTUIOnlyInATerminal(t *testing.T) {
	r := run(t, runOpts{interactive: true})
	if r.code != cli.ExitOK || !r.tuiOpened || r.tuiNoColour {
		t.Fatalf("code=%d tui=%v noColor=%v stderr=%q", r.code, r.tuiOpened, r.tuiNoColour, r.stderr)
	}

	r = run(t, runOpts{interactive: false})
	if r.code != cli.ExitUsage || r.tuiOpened || !strings.Contains(r.stderr, "terminal interactiva") {
		t.Fatalf("sin terminal: code=%d tui=%v stderr=%q", r.code, r.tuiOpened, r.stderr)
	}

	r = run(t, runOpts{interactive: true}, "--json")
	if r.code != cli.ExitUsage || r.tuiOpened {
		t.Fatalf("--json sin subcomando: code=%d tui=%v", r.code, r.tuiOpened)
	}
}

func TestColorCanBeDisabled(t *testing.T) {
	if r := run(t, runOpts{interactive: true}, "--no-color"); !r.tuiNoColour {
		t.Errorf("--no-color no desactivó el color")
	}
	if r := run(t, runOpts{interactive: true, env: map[string]string{"NO_COLOR": "1"}}); !r.tuiNoColour {
		t.Errorf("NO_COLOR no desactivó el color")
	}
}

func TestHelpListsCommands(t *testing.T) {
	r := run(t, runOpts{}, "--help")
	if r.code != cli.ExitOK {
		t.Fatalf("code=%d", r.code)
	}
	for _, want := range []string{"doctor", "version", "completion", "--json", "--no-color"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("la ayuda no menciona %q", want)
		}
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range []string{"bash", "powershell"} {
		r := run(t, runOpts{}, "completion", shell)
		if r.code != cli.ExitOK || !strings.Contains(r.stdout, "balanzapro") {
			t.Errorf("completion %s: code=%d", shell, r.code)
		}
	}
}

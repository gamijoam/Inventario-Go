package id_test

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/id"
)

func TestNewIsVersion7AndRFC9562Variant(t *testing.T) {
	v := id.MustNew()
	b := v.Bytes()
	if b[6]>>4 != 7 {
		t.Errorf("versión = %d, se esperaba 7", b[6]>>4)
	}
	if b[8]>>6 != 0b10 {
		t.Errorf("variante = %02b, se esperaba 10", b[8]>>6)
	}
	if v.IsZero() {
		t.Errorf("un ID nuevo no puede ser cero")
	}
}

func TestNewEmbedsCreationTime(t *testing.T) {
	before := time.Now().Truncate(time.Millisecond)
	v := id.MustNew()
	after := time.Now()
	if ts := v.Time(); ts.Before(before) || ts.After(after) {
		t.Fatalf("Time() = %v fuera de [%v, %v]", ts, before, after)
	}
}

func TestIDsAreStrictlyIncreasing(t *testing.T) {
	prev := id.MustNew()
	for i := 0; i < 10000; i++ {
		next := id.MustNew()
		p, n := prev.Bytes(), next.Bytes()
		if bytes.Compare(p[:], n[:]) >= 0 || next.Compare(prev) != 1 {
			t.Fatalf("orden roto en la iteración %d: %s >= %s", i, prev, next)
		}
		if strings.Compare(prev.String(), next.String()) >= 0 {
			t.Fatalf("el texto no conserva el orden: %s >= %s", prev, next)
		}
		prev = next
	}
}

func TestGeneratorIsMonotonicWithinTheSameMillisecondAndClockRegression(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	g := id.NewGenerator(func() time.Time { return now })

	prev := g.MustNew()
	for i := 0; i < 5000; i++ { // supera el contador de 12 bits
		if i == 2500 {
			now = now.Add(-time.Hour) // el reloj retrocede
		}
		next := g.MustNew()
		if next.Compare(prev) != 1 {
			t.Fatalf("iteración %d: %s no es mayor que %s", i, next, prev)
		}
		prev = next
	}
}

func TestConcurrentGenerationHasNoCollisions(t *testing.T) {
	const workers, perWorker = 8, 2000
	results := make(chan id.ID, workers*perWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				results <- id.MustNew()
			}
		}()
	}
	wg.Wait()
	close(results)
	seen := make(map[id.ID]struct{}, workers*perWorker)
	for v := range results {
		if _, dup := seen[v]; dup {
			t.Fatalf("ID duplicado: %s", v)
		}
		seen[v] = struct{}{}
	}
}

func TestParseRoundTrip(t *testing.T) {
	v := id.MustNew()
	parsed, err := id.Parse(v.String())
	if err != nil {
		t.Fatal(err)
	}
	if parsed != v {
		t.Fatalf("Parse(String()) = %s, se esperaba %s", parsed, v)
	}
	upper, err := id.Parse(strings.ToUpper(v.String()))
	if err != nil || upper != v {
		t.Fatalf("Parse en mayúsculas = %s, %v", upper, err)
	}
	if got := v.String(); len(got) != 36 || got != strings.ToLower(got) {
		t.Fatalf("String() no es canónico: %q", got)
	}
}

func TestParseRejectsInvalidInput(t *testing.T) {
	invalid := []string{
		"",
		"00000000-0000-0000-0000-000000000000", // cero = sin asignar
		"0190f3a2-7b1c-7d4e-8f00-12345678901",  // corto
		"0190f3a27b1c7d4e8f00123456789012",     // sin guiones
		"{0190f3a2-7b1c-7d4e-8f00-123456789012}",
		"0190f3a2-7b1c-7d4e-8f00-12345678901g",
		"0190f3a2_7b1c-7d4e-8f00-123456789012",
		"0190f3a2-7b1c-7d4e-cf00-123456789012", // variante no RFC 9562
	}
	for _, s := range invalid {
		if _, err := id.Parse(s); !errors.Is(err, id.ErrInvalidID) {
			t.Errorf("Parse(%q) debía fallar, obtuvo %v", s, err)
		}
	}
}

func TestParseAcceptsOtherVersions(t *testing.T) {
	// UUIDv4 generados por otros sistemas son válidos como referencia.
	if _, err := id.Parse("3f2b1c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"); err != nil {
		t.Fatalf("UUIDv4 válido rechazado: %v", err)
	}
}

func TestZeroValue(t *testing.T) {
	var z id.ID
	if !z.IsZero() || z.String() != "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("valor cero inconsistente: %s", z)
	}
}

func FuzzParse(f *testing.F) {
	f.Add("0190f3a2-7b1c-7d4e-8f00-123456789012")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		v, err := id.Parse(s)
		if err != nil {
			return
		}
		if v.String() != strings.ToLower(s) {
			t.Fatalf("Parse(%q).String() = %q", s, v)
		}
	})
}

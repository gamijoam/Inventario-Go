package admin_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/application/admin"
)

func TestDomainChecksPassOnAHealthyPlatform(t *testing.T) {
	report := admin.RunDoctor(context.Background(), admin.DomainChecks())
	if !report.Healthy() {
		t.Fatalf("diagnóstico con fallos: %+v", report)
	}
	names := map[string]bool{}
	for _, r := range report.Checks {
		names[r.Name] = true
		if r.Status != admin.StatusOK {
			t.Errorf("%s: estado %s (%s)", r.Name, r.Status, r.Detail)
		}
	}
	for _, want := range []string{"timezone", "decimal", "id"} {
		if !names[want] {
			t.Errorf("falta la verificación %q", want)
		}
	}
}

func TestRunDoctorKeepsOrderAndAggregatesStatus(t *testing.T) {
	checks := []admin.Check{
		{Name: "a", Run: func(context.Context) (admin.Status, string) { return admin.StatusOK, "bien" }},
		{Name: "b", Run: func(context.Context) (admin.Status, string) { return admin.StatusWarn, "ojo" }},
		{Name: "c", Run: func(context.Context) (admin.Status, string) { return admin.StatusFail, "mal" }},
	}
	report := admin.RunDoctor(context.Background(), checks)
	if report.Healthy() {
		t.Fatalf("un fallo debe hacer el diagnóstico no saludable")
	}
	got := []string{}
	for _, r := range report.Checks {
		got = append(got, r.Name+"="+string(r.Status))
	}
	if strings.Join(got, ",") != "a=ok,b=warn,c=fail" {
		t.Fatalf("resultados = %v", got)
	}
	ok, warn, fail := report.Counts()
	if ok != 1 || warn != 1 || fail != 1 {
		t.Fatalf("Counts = %d, %d, %d", ok, warn, fail)
	}
}

func TestWarningsDoNotMakeTheReportUnhealthy(t *testing.T) {
	report := admin.RunDoctor(context.Background(), []admin.Check{
		{Name: "w", Run: func(context.Context) (admin.Status, string) { return admin.StatusWarn, "" }},
	})
	if !report.Healthy() {
		t.Fatalf("una advertencia no es un fallo")
	}
}

func TestPanickingCheckBecomesFailure(t *testing.T) {
	report := admin.RunDoctor(context.Background(), []admin.Check{
		{Name: "p", Run: func(context.Context) (admin.Status, string) { panic("boom") }},
	})
	if report.Healthy() || report.Checks[0].Status != admin.StatusFail {
		t.Fatalf("un pánico debe registrarse como fallo: %+v", report)
	}
}

func TestCancelledContextSkipsRemainingChecks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := admin.RunDoctor(ctx, []admin.Check{
		{Name: "x", Run: func(context.Context) (admin.Status, string) { return admin.StatusOK, "" }},
	})
	if report.Healthy() || report.Checks[0].Status != admin.StatusFail {
		t.Fatalf("contexto cancelado debe fallar: %+v", report)
	}
}

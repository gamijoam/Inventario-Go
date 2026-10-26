package timezone_test

import (
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

func TestCaracasLocationLoads(t *testing.T) {
	if _, err := time.LoadLocation(timezone.CaracasName); err != nil {
		t.Fatalf("time.LoadLocation(%q): %v", timezone.CaracasName, err)
	}
	if got := timezone.Caracas().String(); got != "America/Caracas" {
		t.Fatalf("Caracas() = %q", got)
	}
}

func TestInstantIsShownInCaracasAndStoredInUTC(t *testing.T) {
	instant := time.Date(2026, 10, 25, 16, 0, 0, 0, time.UTC)

	local := timezone.InCaracas(instant)
	if local.Hour() != 12 {
		t.Fatalf("hora local = %d, se esperaba 12 (UTC−04:00)", local.Hour())
	}
	if _, offset := local.Zone(); offset != -4*3600 {
		t.Fatalf("desfase = %d s, se esperaba −14400", offset)
	}
	if !local.Equal(instant) {
		t.Fatalf("cambiar la zona no debe cambiar el instante")
	}

	stored := timezone.ToUTC(local)
	if stored.Location() != time.UTC || !stored.Equal(instant) {
		t.Fatalf("ToUTC = %v", stored)
	}
}

func TestBusinessDayBoundaryFollowsCaracas(t *testing.T) {
	// 02:30 UTC del 26/10 sigue siendo 25/10 en Caracas.
	instant := time.Date(2026, 10, 26, 2, 30, 0, 0, time.UTC)
	if got := timezone.InCaracas(instant).Format(time.DateOnly); got != "2026-10-25" {
		t.Fatalf("día comercial = %s, se esperaba 2026-10-25", got)
	}
}

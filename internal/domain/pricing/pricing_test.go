package pricing_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/pricing"
)

var now = time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)

func d(s string) decimal.Decimal { return decimal.MustParse(s) }

func list(t *testing.T) pricing.List {
	t.Helper()
	l, err := pricing.NewList(id.MustNew(), id.MustNew(), "general", "General", money.USD, id.MustNew(), now)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestSuggestUsesMarginOnSale(t *testing.T) {
	for _, c := range []struct{ cost, margin, want string }{
		{"100", "20", "125"},          // sección 19.3
		{"80", "20", "100"},           // sección 16.6: margen 20 % ⇒ recargo 25 %
		{"10", "0", "10"},             // sin margen, el costo
		{"1.13", "30", "1.61"},        // 1.6142… redondeado
		{"11.45", "33.3333", "17.17"}, // 17.17498…
		{"2.186987", "25", "2.92"},    // costo convertido de 6 decimales
		{"0", "50", "0"},
		{"1.125", "0", "1.13"}, // la mitad se aleja de cero
	} {
		got, err := pricing.Suggest(d(c.cost), d(c.margin), money.USD)
		if err != nil || got.String() != c.want {
			t.Errorf("Suggest(%s, %s) = %s %v, se esperaba %s", c.cost, c.margin, got, err, c.want)
		}
	}
	if m, ok := pricing.MarginOf(d("100"), d("80")); !ok || m.String() != "20" {
		t.Errorf("MarginOf = %s %v", m, ok)
	}
	if _, ok := pricing.MarginOf(d("0"), d("80")); ok {
		t.Error("margen con precio cero")
	}
	for _, bad := range []string{"100", "-1", "12.34567", "x"} {
		if _, err := pricing.ParseMargin(bad); !errors.Is(err, pricing.ErrInvalid) {
			t.Errorf("margen %q aceptado", bad)
		}
	}
	if _, err := pricing.ParsePrice("1.234", money.USD); !errors.Is(err, pricing.ErrInvalid) {
		t.Error("precio con 3 decimales en USD")
	}
}

func TestTargetMarginPrefersTheProduct(t *testing.T) {
	l, p := d("30"), d("15")
	if got := pricing.TargetMargin(&l, &p); got.String() != "15" {
		t.Errorf("excepción: %s", got)
	}
	if got := pricing.TargetMargin(&l, nil); got.String() != "30" {
		t.Errorf("lista: %s", got)
	}
	if pricing.TargetMargin(nil, nil) != nil {
		t.Error("sin margen")
	}
}

func TestCurrentPicksEffectiveThenVersion(t *testing.T) {
	at := func(h int) time.Time { return now.Add(time.Duration(h) * time.Hour) }
	revs := []pricing.Revision{
		{Version: 1, Price: d("10"), EffectiveAt: at(-48), Status: pricing.RevisionPublished},
		{Version: 2, Price: d("11"), EffectiveAt: at(-1), Status: pricing.RevisionPublished},
		{Version: 3, Price: d("12"), EffectiveAt: at(-1), Status: pricing.RevisionPublished}, // misma vigencia, versión mayor
		{Version: 4, Price: d("13"), EffectiveAt: at(5), Status: pricing.RevisionPublished},  // programada
		{Version: 5, Price: d("99"), EffectiveAt: at(-30), Status: pricing.RevisionCancelled},
	}
	for h, want := range map[int]string{-24: "10", 0: "12", 5: "13", 10: "13"} {
		if got, ok := pricing.Current(revs, at(h)); !ok || got.Price.String() != want {
			t.Errorf("a las %d h: %s %v, se esperaba %s", h, got.Price, ok, want)
		}
	}
	if _, ok := pricing.Current(revs, at(-72)); ok {
		t.Error("antes de todo precio")
	}
	if !revs[3].Scheduled(now) || revs[2].Scheduled(now) {
		t.Error("Scheduled")
	}
	if _, err := revs[2].Cancel(id.MustNew(), now); !errors.Is(err, pricing.ErrInvalid) {
		t.Error("anular una revisión que ya rige")
	}
	if c, err := revs[3].Cancel(id.MustNew(), now); err != nil || c.Status != pricing.RevisionCancelled {
		t.Errorf("anular programada: %v", err)
	}
}

func TestProposalLifecycle(t *testing.T) {
	l := list(t)
	actor := id.MustNew()
	ref := &pricing.Reference{Basis: pricing.BasisReplacement, Cost: d("100"), Currency: money.USD, ListCost: d("100")}
	m := d("20")
	p, err := pricing.NewProposal(l, id.MustNew(), pricing.KindSuggested, d("125"), ref, &m, "", actor, now)
	if err != nil || p.Status != pricing.ProposalPending {
		t.Fatal(p, err)
	}
	p.BaseVersion = 2
	if _, _, err := p.Approve(d("125"), money.USD, 3, time.Time{}, actor, now); !errors.Is(err, pricing.ErrStale) {
		t.Errorf("precio cambiado desde la propuesta: %v", err)
	}
	if _, _, err := p.Approve(d("125"), money.USD, 2, now.Add(-time.Hour), actor, now); !errors.Is(err, pricing.ErrInvalid) {
		t.Errorf("vigencia pasada: %v", err)
	}
	if !p.BelowCost(d("99.99")) || p.BelowCost(d("100")) {
		t.Error("BelowCost")
	}
	later := now.Add(24 * time.Hour)
	a, rev, err := p.Approve(d("124.5"), money.USD, 2, later, actor, now)
	if err != nil || a.Status != pricing.ProposalApproved || rev.Version != 3 || rev.Price.String() != "124.5" || !rev.EffectiveAt.Equal(later) || a.RevisionID != rev.ID {
		t.Fatalf("aprobar: %+v %+v %v", a, rev, err)
	}
	if _, _, err := a.Approve(d("1"), money.USD, 3, time.Time{}, actor, now); !errors.Is(err, pricing.ErrInvalid) {
		t.Errorf("aprobar dos veces: %v", err)
	}
	if _, err := a.Reject("", actor, now); !errors.Is(err, pricing.ErrInvalid) {
		t.Error("rechazar una aprobada")
	}
	r, err := p.Reject("muy alto", actor, now)
	if err != nil || r.Status != pricing.ProposalRejected || r.DecisionNote != "muy alto" {
		t.Fatalf("rechazar: %+v %v", r, err)
	}
	if s, err := p.Supersede(actor, now); err != nil || s.Status != pricing.ProposalSuperseded {
		t.Fatalf("sustituir: %v", err)
	}
	inactive := l
	inactive.Status = "inactive"
	if _, err := pricing.NewProposal(inactive, id.MustNew(), pricing.KindManual, d("1"), nil, nil, "", actor, now); !errors.Is(err, pricing.ErrInvalid) {
		t.Error("lista inactiva")
	}
}

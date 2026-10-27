package persistence_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/application/auth"
	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/application/purchasing"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/pricing"
	"github.com/gamijoam/inventario-go/internal/platform/config"
)

// El reloj marca el 26/10/2026 a las 11:00 de Caracas; la empresa
// "principal" valora en USD.

type pricingEnv struct {
	*rateEnv
	price *apppricing.Service
}

func newPricingEnv(t *testing.T, cfg config.Database) *pricingEnv {
	t.Helper()
	e := newRateEnv(t, cfg)
	return &pricingEnv{rateEnv: e, price: apppricing.NewService(e.store.PricingStore(), e.clock.now)}
}

func (e *pricingEnv) list(code, currency, margin string) {
	e.t.Helper()
	if _, err := e.price.CreateList(ctx(e.t), e.admin, apppricing.ListInput{Company: "principal", Code: code, Name: code, Currency: currency, Margin: margin}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *pricingEnv) propose(p identity.Principal, list, product, price string) (apppricing.ProposalView, error) {
	return e.price.Propose(ctx(e.t), p, apppricing.ProposeInput{Company: "principal", List: list, Product: product, Price: price})
}

func (e *pricingEnv) approve(ref string, in apppricing.ApproveInput) apppricing.ProposalView {
	e.t.Helper()
	v, err := e.price.Approve(ctx(e.t), e.admin, ref, in)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

// current devuelve "precio/versión" vigente, "-" si no hay, y la propuesta
// pendiente.
func (e *pricingEnv) current(list, product string) (string, string) {
	e.t.Helper()
	views, err := e.price.Prices(ctx(e.t), e.admin, "principal", list, product)
	if err != nil || len(views) != 1 {
		e.t.Fatalf("Prices: %+v %v", views, err)
	}
	cur, pending := "-", "-"
	if v := views[0]; v.Current != nil {
		cur = v.Current.Price.String()
	}
	if v := views[0]; v.Pending != nil {
		pending = v.Pending.Price.String()
	}
	return cur, pending
}

func (e *pricingEnv) receive(product, qty, cost string) purchasing.ReceiptView {
	e.t.Helper()
	r := e.receipt("")
	e.setLine(r.Number, product, qty, cost)
	return e.confirm(r.Number)
}

// DEC-007.P2: la recepción solo deja propuestas; el precio cambia al
// aprobar, para todas las existencias y las ventas futuras de la lista.
func TestReceiptsSuggestPricesThatNeedApproval(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newPricingEnv(t, cfg)
		c := ctx(t)
		e.list("general", "", "20")
		e.list("mayor", "USD", "")
		e.list("bs", "VES", "20")
		if _, err := e.price.SetMargin(c, e.admin, apppricing.MarginInput{Company: "principal", List: "mayor", Product: "A", Margin: "10"}); err != nil {
			t.Fatal(err)
		}
		// Precio inicial a mano: 10.
		p0, err := e.propose(e.admin, "general", "A", "10")
		if err != nil || p0.Kind != pricing.KindManual || p0.Reference != nil {
			t.Fatalf("propuesta inicial: %+v %v", p0, err)
		}
		if a := e.approve(p0.Number, apppricing.ApproveInput{}); a.Revision == nil || a.Revision.Version != 1 {
			t.Fatalf("aprobar: %+v", a)
		}
		e.in(e.here, "A", "10", "8") // existencia anterior

		// Llegan 10 a 9.6: el CPP cambia, el precio no.
		v := e.receive("A", "10", "9.6")
		if len(v.Suggestions) != 2 || len(v.Skipped) != 1 || v.Skipped[0].List.Code.String() != "BS" {
			t.Fatalf("sugerencias %+v, omitidas %+v", v.Suggestions, v.Skipped)
		}
		if cur, pending := e.current("general", "A"); cur != "10" || pending != "12" { // 9.6 ÷ 0.8
			t.Fatalf("general: vigente %s, pendiente %s", cur, pending)
		}
		if _, pending := e.current("mayor", "A"); pending != "10.67" { // excepción del producto: 10 %
			t.Fatalf("mayor: pendiente %s", pending)
		}
		if got := e.valued(e.here, "A"); got != "20/176/8.8" {
			t.Fatalf("CPP: %s", got)
		}
		sug := v.Suggestions[0]
		if sug.List.Code.String() != "GENERAL" {
			sug = v.Suggestions[1]
		}
		if sug.Kind != pricing.KindSuggested || sug.Reference.Basis != pricing.BasisLastPurchase || sug.Reference.ListCost.String() != "9.6" ||
			sug.Margin.String() != "20" || sug.Current.String() != "10" || sug.BaseVersion != 1 || sug.ReceiptID != v.ID {
			t.Fatalf("sugerencia: %+v %+v", sug.Proposal, sug.Reference)
		}
		// Confirmar de nuevo no repite sugerencias.
		if again := e.confirm(v.Number); !again.Replayed || len(again.Suggestions) != 2 {
			t.Fatalf("repetición: %+v", again.Suggestions)
		}

		// Aprobar: rige ya para las 20 unidades.
		if a := e.approve(sug.Number, apppricing.ApproveInput{}); a.Status != pricing.ProposalApproved || a.Revision.Version != 2 {
			t.Fatalf("aprobar sugerencia: %+v", a)
		}
		if cur, pending := e.current("general", "A"); cur != "12" || pending != "-" {
			t.Fatalf("tras aprobar: %s %s", cur, pending)
		}
		if _, err := e.price.Approve(c, e.admin, sug.Number, apppricing.ApproveInput{}); !errors.Is(err, pricing.ErrInvalid) {
			t.Errorf("aprobar dos veces: %v", err)
		}

		// Reposición más cara y tasa: la próxima recepción sugiere en las
		// tres listas y la nueva propuesta sustituye a la pendiente.
		if _, err := e.buy.SetReplacementCost(c, e.admin, "principal", "A", "11", "", ""); err != nil {
			t.Fatal(err)
		}
		e.mustRate("40", "2026-10-26")
		v = e.receive("A", "1", "9.6")
		if len(v.Suggestions) != 3 || len(v.Skipped) != 0 {
			t.Fatalf("segunda recepción: %+v %+v", v.Suggestions, v.Skipped)
		}
		if _, pending := e.current("general", "A"); pending != "13.75" { // 11 ÷ 0.8
			t.Errorf("general con reposición: %s", pending)
		}
		if _, pending := e.current("bs", "A"); pending != "550" { // 11 × 40 ÷ 0.8
			t.Errorf("bs: %s", pending)
		}
		old, _ := e.price.Proposals(c, e.admin, "principal", "mayor", pricing.ProposalSuperseded)
		if len(old) != 1 {
			t.Errorf("la pendiente anterior de mayor debía quedar sustituida: %d", len(old))
		}

		// Bajo el costo de referencia exige el permiso extraordinario.
		low, err := e.propose(e.admin, "general", "A", "9")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.price.Approve(c, e.admin, low.Number, apppricing.ApproveInput{}); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("bajo costo sin permiso: %v", err)
		}
		if a := e.approve(low.Number, apppricing.ApproveInput{Price: "11.5"}); a.Price.String() != "11.5" {
			t.Errorf("aprobar ajustando: %+v", a.Proposal)
		}

		// Rechazar no cambia el precio.
		r, _ := e.propose(e.admin, "general", "A", "20")
		if x, err := e.price.Reject(c, e.admin, r.Number, "muy alto"); err != nil || x.Status != pricing.ProposalRejected {
			t.Fatalf("rechazar: %+v %v", x, err)
		}
		if cur, _ := e.current("general", "A"); cur != "11.5" {
			t.Errorf("tras rechazar: %s", cur)
		}
		e.verify()
	})
}

// Un precio programado rige desde su hora de Caracas; antes se puede anular.
func TestScheduledPrices(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newPricingEnv(t, cfg)
		c := ctx(t)
		e.list("general", "", "")
		p, _ := e.propose(e.admin, "general", "A", "10")
		e.approve(p.Number, apppricing.ApproveInput{})
		p, _ = e.propose(e.admin, "general", "A", "12")
		a := e.approve(p.Number, apppricing.ApproveInput{At: "2026-10-27 08:00"})
		if want := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC); !a.Revision.EffectiveAt.Equal(want) {
			t.Fatalf("vigencia: %v", a.Revision.EffectiveAt)
		}
		views, _ := e.price.Prices(c, e.admin, "principal", "general", "A")
		if views[0].Current.Price.String() != "10" || len(views[0].Scheduled) != 1 {
			t.Fatalf("antes de la hora: %+v", views[0])
		}
		if _, err := e.price.Approve(c, e.admin, "PP-000009", apppricing.ApproveInput{}); err == nil {
			t.Error("propuesta inexistente")
		}
		if _, err := e.price.Approve(c, e.admin, p.Number, apppricing.ApproveInput{At: "2026-10-25"}); err == nil {
			t.Error("vigencia pasada")
		}
		e.clock.add(22 * time.Hour) // 27/10 09:00 en Caracas
		if cur, _ := e.current("general", "A"); cur != "12" {
			t.Fatalf("después de la hora: %s", cur)
		}
		if _, err := e.price.CancelRevision(c, e.admin, a.Revision.ID.String()); !errors.Is(err, pricing.ErrInvalid) {
			t.Errorf("anular una revisión que ya rige: %v", err)
		}
		p, _ = e.propose(e.admin, "general", "A", "15")
		a = e.approve(p.Number, apppricing.ApproveInput{At: "2026-10-30"})
		// La base solo deja anular: no cambiar el precio al anular.
		pp := e.store.Dialect.Placeholder
		cancel := "UPDATE price_revisions SET status = 'cancelled', cancelled_by = created_by, cancelled_at_utc = created_at_utc%s WHERE id = " + pp(1)
		for name, tc := range map[string]struct {
			set    string
			wantOK bool
		}{"anular y cambiar precio": {", price = '1'", false}, "anular": {"", true}} {
			tx, err := e.store.DB.BeginTx(c, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.ExecContext(c, fmt.Sprintf(cancel, tc.set), a.Revision.ID.String())
			_ = tx.Rollback()
			if (err == nil) != tc.wantOK {
				t.Errorf("%s: %v", name, err)
			}
		}
		if x, err := e.price.CancelRevision(c, e.admin, a.Revision.ID.String()); err != nil || x.Status != pricing.RevisionCancelled {
			t.Fatalf("anular programada: %+v %v", x, err)
		}
		e.clock.add(5 * 24 * time.Hour)
		if cur, _ := e.current("general", "A"); cur != "12" {
			t.Errorf("la anulada no debía regir: %s", cur)
		}
	})
}

func TestPricingPermissionsAndConcurrency(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newPricingEnv(t, cfg)
		c := ctx(t)
		e.list("general", "", "20")
		e.createUser(e.admin, "alm", "ALMACENISTA", auth.ScopeRef{Company: "principal", Branch: "principal"})
		e.createUser(e.admin, "caja", "CAJERO", auth.ScopeRef{Company: "principal", Branch: "principal"})
		e.createUser(e.admin, "enc", "ENCARGADO", auth.ScopeRef{Company: "principal", Branch: "principal"})
		e.createUser(e.admin, "gerente", "ENCARGADO", auth.ScopeRef{Company: "principal"})
		alm, caja, enc := e.login("alm", userPassword).Principal, e.login("caja", userPassword).Principal, e.login("enc", userPassword).Principal
		gerente := e.login("gerente", userPassword).Principal

		p, err := e.propose(alm, "general", "A", "10")
		if err != nil {
			t.Fatalf("almacenista propone: %v", err)
		}
		if _, err := e.price.Approve(c, alm, p.Number, apppricing.ApproveInput{}); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("almacenista aprueba: %v", err)
		}
		if _, err := e.price.Approve(c, enc, p.Number, apppricing.ApproveInput{}); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("encargado de sucursal aprueba: %v", err)
		}
		if _, err := e.propose(caja, "general", "A", "1"); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("cajero propone: %v", err)
		}
		if _, err := e.price.Prices(c, caja, "principal", "", ""); err != nil {
			t.Errorf("cajero consulta: %v", err)
		}
		if _, err := e.price.CreateList(c, gerente, apppricing.ListInput{Company: "principal", Code: "x", Name: "X"}); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("encargado crea lista: %v", err)
		}

		// Aprobaciones simultáneas de la misma propuesta: una sola revisión.
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok := 0
		for range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := e.price.Approve(c, gerente, p.Number, apppricing.ApproveInput{}); err == nil {
					mu.Lock()
					ok++
					mu.Unlock()
				} else if !errors.Is(err, pricing.ErrInvalid) {
					t.Errorf("aprobación simultánea: %v", err)
				}
			}()
		}
		wg.Wait()
		views, _ := e.price.Prices(c, e.admin, "principal", "general", "A")
		if ok != 1 || views[0].Current == nil || views[0].Current.Version != 1 {
			t.Fatalf("aprobaciones: %d, %+v", ok, views[0].Current)
		}

		// La base protege el historial aunque Go se saltara.
		pp := e.store.Dialect.Placeholder
		for name, q := range map[string]string{
			"editar precio":         "UPDATE price_revisions SET price = '1' WHERE version = " + pp(1),
			"anular vigente":        "UPDATE price_revisions SET status = 'cancelled', cancelled_by = created_by, cancelled_at_utc = created_at_utc WHERE version = " + pp(1),
			"borrar revisión":       "DELETE FROM price_revisions WHERE version = " + pp(1),
			"reabrir propuesta":     "UPDATE price_proposals SET status = 'pending', decided_by = NULL, decided_at_utc = NULL, revision_id = NULL WHERE base_version = " + pp(1) + " - 1",
			"borrar propuesta":      "DELETE FROM price_proposals WHERE base_version = " + pp(1) + " - 1",
			"editar margen":         "UPDATE price_margins SET margin = '1' WHERE seq = " + pp(1),
			"cambiar moneda":        "UPDATE price_lists SET currency = 'VES' WHERE " + pp(1) + " = 1",
			"sugerencia sin compra": "UPDATE price_proposals SET kind = 'suggested' WHERE base_version = " + pp(1) + " - 1",
		} {
			if _, err := e.store.DB.ExecContext(c, q, 1); err == nil {
				t.Errorf("%s: la base debía rechazarlo", name)
			}
		}
	})
}

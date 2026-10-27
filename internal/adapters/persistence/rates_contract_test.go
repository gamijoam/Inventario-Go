package persistence_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/gamijoam/inventario-go/internal/application/auth"
	"github.com/gamijoam/inventario-go/internal/application/purchasing"
	"github.com/gamijoam/inventario-go/internal/application/rates"
	"github.com/gamijoam/inventario-go/internal/domain/exchange"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/purchase"
	"github.com/gamijoam/inventario-go/internal/platform/config"
)

// El reloj de las pruebas marca el 26/10/2026 en Caracas; la empresa
// "principal" valora en USD. Tasas ficticias.

type rateEnv struct {
	*purchaseEnv
	fx *rates.Service
}

func newRateEnv(t *testing.T, cfg config.Database) *rateEnv {
	t.Helper()
	e := newPurchaseEnv(t, cfg)
	return &rateEnv{purchaseEnv: e, fx: rates.NewService(e.store.RateStore(), e.clock.now)}
}

func (e *rateEnv) setRate(p identity.Principal, base, quote, value, date string) (rates.RateView, error) {
	return e.fx.Set(ctx(e.t), p, rates.SetInput{Company: "principal", Base: base, Quote: quote, Rate: value, Source: "official", Label: "BCV", Date: date})
}

func (e *rateEnv) mustRate(value, date string) rates.RateView {
	e.t.Helper()
	v, err := e.setRate(e.admin, "USD", "VES", value, date)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *rateEnv) effective(date string) (string, int, error) {
	v, err := e.fx.Effective(ctx(e.t), e.admin, "principal", "VES", "USD", date)
	return v.Value.String(), v.Version, err
}

// Una tasa rige desde su fecha hasta que otra la reemplace; en una misma
// fecha gana la última versión; una fecha futura queda programada.
func TestRatesAreVersionedAndEffectiveByDate(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newRateEnv(t, cfg)
		c := ctx(t)
		e.mustRate("40", "2026-10-20")
		e.mustRate("45.725", "2026-10-25")
		if v := e.mustRate("46.000000000001", "2026-10-25"); v.Version != 3 { // corrección
			t.Fatalf("versión: %d", v.Version)
		}
		e.mustRate("50", "2026-10-30")
		for date, want := range map[string]string{"2026-10-20": "40", "2026-10-24": "40", "2026-10-25": "46.000000000001", "": "46.000000000001", "2026-10-30": "50"} {
			if got, _, err := e.effective(date); err != nil || got != want {
				t.Errorf("vigente el %q: %s %v, se esperaba %s", date, got, err, want)
			}
		}
		if _, _, err := e.effective("2026-10-19"); !errors.Is(err, exchange.ErrNoRate) {
			t.Errorf("antes de toda tasa: %v", err)
		}
		list, err := e.fx.List(c, e.admin, "principal", "ves/usd")
		if err != nil || len(list) != 4 || list[0].Value.String() != "40" || list[3].Version != 4 {
			t.Fatalf("historial: %+v %v", list, err)
		}
		for name, in := range map[string][4]string{
			"cero":         {"USD", "VES", "0", ""},
			"misma moneda": {"USD", "USD", "1", ""},
			"moneda ajena": {"USD", "EUR", "1", ""},
			"fecha":        {"USD", "VES", "1", "26/10/2026"},
		} {
			if _, err := e.setRate(e.admin, in[0], in[1], in[2], in[3]); !errors.Is(err, exchange.ErrInvalid) {
				t.Errorf("%s: %v", name, err)
			}
		}

		// Permisos: el cajero consulta; el encargado de sucursal no fija una
		// tasa de toda la empresa.
		e.createUser(e.admin, "caja", "CAJERO", auth.ScopeRef{Company: "principal", Branch: "principal"})
		e.createUser(e.admin, "enc", "ENCARGADO", auth.ScopeRef{Company: "principal", Branch: "principal"})
		e.createUser(e.admin, "gerente", "ENCARGADO", auth.ScopeRef{Company: "principal"})
		caja, enc := e.login("caja", userPassword).Principal, e.login("enc", userPassword).Principal
		if _, err := e.fx.Effective(c, caja, "principal", "USD", "VES", ""); err != nil {
			t.Errorf("cajero consulta: %v", err)
		}
		if _, err := e.setRate(caja, "USD", "VES", "1", ""); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("cajero registra: %v", err)
		}
		if _, err := e.setRate(enc, "USD", "VES", "1", ""); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("encargado de sucursal registra: %v", err)
		}
		if _, err := e.setRate(e.login("gerente", userPassword).Principal, "USD", "VES", "47", "2026-10-26"); err != nil {
			t.Errorf("encargado de empresa registra: %v", err)
		}

		// Registros simultáneos del mismo par toman versiones distintas.
		var wg sync.WaitGroup
		for range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := e.setRate(e.admin, "VES", "USD", "0.02", "2026-10-26"); err != nil {
					t.Errorf("registro simultáneo: %v", err)
				}
			}()
		}
		wg.Wait()
		list, _ = e.fx.List(c, e.admin, "principal", "")
		seen := map[int]bool{}
		for _, x := range list {
			if seen[x.Version] {
				t.Errorf("versión repetida %d", x.Version)
			}
			seen[x.Version] = true
		}
		if len(list) != 11 || !seen[11] {
			t.Fatalf("versiones: %d", len(list))
		}
		if got, v, _ := e.effective(""); got != "0.02" || v != 11 {
			t.Errorf("la última versión del día gana: %s v%d", got, v)
		}

		pp := e.store.Dialect.Placeholder
		for name, q := range map[string]string{
			"editar tasa": "UPDATE exchange_rates SET rate = '1' WHERE version = " + pp(1),
			"borrar tasa": "DELETE FROM exchange_rates WHERE version = " + pp(1),
		} {
			if _, err := e.store.DB.ExecContext(c, q, 1); err == nil {
				t.Errorf("%s: la base debía rechazarlo", name)
			}
		}
	})
}

// Una compra en VES en una empresa que valora en USD se convierte al
// confirmar con la tasa vigente a la fecha de la factura, aunque haya una
// más reciente; cada gasto, con la de su fecha. Las tasas posteriores no
// tocan lo confirmado y sin tasa vigente no se confirma.
func TestPurchaseInAnotherCurrency(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newRateEnv(t, cfg)
		c := ctx(t)
		r40 := e.mustRate("40", "2026-10-20")
		e.mustRate("50", "2026-10-25")

		r, err := e.buy.CreateReceipt(c, e.admin, purchasing.ReceiptInput{Where: e.here, Supplier: "dist", Currency: "ves", Date: "2026-10-22", Document: "FAC-9"})
		if err != nil || r.Currency != money.VES || r.Valuation != money.USD {
			t.Fatalf("recepción en VES: %+v %v", r, err)
		}
		e.setLine(r.Number, "A", "10", "400")                                                                                       // 4000 VES = 100 USD
		if _, err := e.buy.AddExpense(c, e.admin, r.Number, purchasing.ExpenseInput{Concept: "Flete", Amount: "500"}); err != nil { // 12.5 USD
			t.Fatal(err)
		}
		if _, err := e.buy.AddExpense(c, e.admin, r.Number, purchasing.ExpenseInput{Concept: "Seguro", Amount: "2", Currency: "USD"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.buy.AddExpense(c, e.admin, r.Number, purchasing.ExpenseInput{Concept: "X", Amount: "1", Date: "2026-10-19"}); !errors.Is(err, exchange.ErrNoRate) {
			t.Errorf("gasto sin tasa a su fecha: %v", err)
		}
		v := e.confirm(r.Number)
		if v.RateID != r40.ID || v.Lines[0].Valued.String() != "100" || v.Lines[0].Expenses.String() != "14.5" || v.Rates[r40.ID].Value.String() != "40" {
			t.Fatalf("confirmada: rate %v, línea %+v", v.RateID, v.Lines[0])
		}
		if v.Expenses[0].Valued.String() != "12.5" || v.Expenses[0].RateID != r40.ID || !v.Expenses[1].RateID.IsZero() {
			t.Fatalf("gastos: %+v", v.Expenses)
		}
		if got := e.valued(e.here, "A"); got != "10/114.5/11.45" {
			t.Fatalf("A: %s", got)
		}

		// Una corrección posterior de la tasa del 22 no recalcula lo
		// confirmado.
		e.mustRate("41", "2026-10-22")
		if again, _ := e.buy.ShowReceipt(c, e.admin, r.Number); again.RateID != r40.ID || again.Lines[0].Valued.String() != "100" {
			t.Errorf("la recepción cambió con la tasa nueva: %+v", again.Lines[0])
		}

		// Gasto tardío en VES: hoy rige 50.
		late, err := e.buy.AddExpense(c, e.admin, r.Number, purchasing.ExpenseInput{Concept: "Aduana", Amount: "100"})
		if err != nil {
			t.Fatal(err)
		}
		if x := late.Expenses[2]; !x.Late || x.Valued.String() != "2" || x.Date.Format("2006-01-02") != "2026-10-26" {
			t.Fatalf("tardío: %+v", x)
		}
		if got := e.valued(e.here, "A"); got != "10/116.5/11.65" {
			t.Fatalf("A tras el tardío: %s", got)
		}

		// Sin tasa vigente a la fecha de la factura no se confirma ni se
		// mueve nada.
		old, err := e.buy.CreateReceipt(c, e.admin, purchasing.ReceiptInput{Where: e.here, Supplier: "dist", Currency: "VES", Date: "2026-10-19"})
		if err != nil {
			t.Fatal(err)
		}
		e.setLine(old.Number, "A", "1", "1")
		if _, err := e.buy.ConfirmReceipt(c, e.admin, old.Number); !errors.Is(err, exchange.ErrNoRate) {
			t.Errorf("sin tasa: %v", err)
		}
		if st, _ := e.buy.ShowReceipt(c, e.admin, old.Number); st.Status != purchase.ReceiptDraft || e.valued(e.here, "A") != "10/116.5/11.65" {
			t.Errorf("sin tasa se movió algo: %s %s", st.Status, e.valued(e.here, "A"))
		}
		if _, err := e.buy.CreateReceipt(c, e.admin, purchasing.ReceiptInput{Where: e.here, Supplier: "dist", Date: "2026-10-27"}); !errors.Is(err, purchase.ErrInvalid) {
			t.Errorf("factura futura: %v", err)
		}

		// La orden fija la moneda de sus recepciones.
		o, err := e.buy.CreateOrder(c, e.admin, purchasing.OrderInput{Company: "principal", Branch: "principal", Supplier: "dist", Currency: "VES",
			Lines: []purchasing.LineInput{{Product: "B", Quantity: "2", UnitCost: "100"}}})
		if err != nil || o.Currency != money.VES {
			t.Fatalf("orden en VES: %+v %v", o, err)
		}
		if _, err := e.buy.CreateReceipt(c, e.admin, purchasing.ReceiptInput{Where: e.here, Order: o.Number, Currency: "USD"}); !errors.Is(err, purchase.ErrInvalid) {
			t.Errorf("moneda distinta de la orden: %v", err)
		}
		fromOrder, err := e.buy.CreateReceipt(c, e.admin, purchasing.ReceiptInput{Where: e.here, Order: o.Number})
		if err != nil || fromOrder.Currency != money.VES {
			t.Fatalf("recepción de la orden: %+v %v", fromOrder, err)
		}
		e.confirm(fromOrder.Number) // hoy rige 50: 200 VES = 4 USD
		if got := e.valued(e.here, "B"); got != "2/4/2" {
			t.Errorf("B: %s", got)
		}

		// Costos: el último de compra en su moneda y convertido; la
		// reposición con la suya.
		cv, err := e.buy.SetReplacementCost(c, e.admin, "principal", "A", "450", "VES", "cotización")
		if err != nil || cv.Replacement[0].Currency != money.VES {
			t.Fatalf("reposición en VES: %+v %v", cv.Replacement, err)
		}
		if l := cv.Last; l == nil || l.Currency != money.VES || l.UnitCost.String() != "400" || l.ValuedUnit.String() != "10" || l.TotalUnit.String() != "11.45" {
			t.Fatalf("último costo: %+v", cv.Last)
		}
		e.verify()

		// Revertir la 0009 confundiría estos documentos con la moneda de
		// valoración: la reversión falla sin cambios.
		runner, err := e.store.Runner()
		if err != nil {
			t.Fatal(err)
		}
		// La 0010 (precios) se revierte primero; la 0009 se niega.
		if _, err := runner.Down(c, 2); err == nil {
			t.Fatal("la reversión con documentos en VES debía fallar")
		}
		if _, err := runner.Up(c); err != nil {
			t.Fatal(err)
		}
		if again, err := e.buy.ShowReceipt(c, e.admin, r.Number); err != nil || again.Currency != money.VES {
			t.Fatalf("tras la reversión fallida: %v %v", again.Currency, err)
		}
	})
}

// La base exige moneda, valoración y una tasa de la empresa vigente a la
// fecha del documento, aunque Go se saltara.
func TestCurrencyIsGuardedByTheDatabase(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newRateEnv(t, cfg)
		c := ctx(t)
		early := e.mustRate("40", "2026-10-20")
		later := e.mustRate("50", "2026-10-25")
		r, err := e.buy.CreateReceipt(c, e.admin, purchasing.ReceiptInput{Where: e.here, Supplier: "dist", Currency: "VES", Date: "2026-10-22"})
		if err != nil {
			t.Fatal(err)
		}
		e.setLine(r.Number, "A", "10", "400")
		pp := e.store.Dialect.Placeholder
		ts := e.store.Dialect.Timestamp(e.clock.now())
		admin := e.admin.UserID.String()
		confirm := "UPDATE purchase_receipts SET status = 'confirmed', closed_by = " + pp(1) + ", closed_at_utc = " + pp(2) + ", rate_id = " + pp(3) +
			" WHERE number = " + pp(4)
		valued := "UPDATE purchase_receipt_lines SET valued = '100' WHERE receipt_id = (SELECT id FROM purchase_receipts WHERE number = " + pp(1) + ")"

		// Cada intento corre en su propia transacción y se revierte.
		run := func(name string, wantOK bool, steps ...[2]any) {
			t.Helper()
			tx, err := e.store.DB.BeginTx(c, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			var last error
			for _, s := range steps {
				if _, last = tx.ExecContext(c, s[0].(string), s[1].([]any)...); last != nil {
					break
				}
			}
			if (last == nil) != wantOK {
				t.Errorf("%s: err = %v, se esperaba éxito %v", name, last, wantOK)
			}
		}
		step := func(q string, args ...any) [2]any { return [2]any{q, args} }
		run("confirmar sin valorar", false, step(confirm, admin, ts, early.ID.String(), r.Number))
		run("confirmar sin tasa", false, step(valued, r.Number), step(confirm, admin, ts, nil, r.Number))
		run("tasa posterior a la factura", false, step(valued, r.Number), step(confirm, admin, ts, later.ID.String(), r.Number))
		run("tasa vigente", true, step(valued, r.Number), step(confirm, admin, ts, early.ID.String(), r.Number))
		run("tasa en borrador", false, step("UPDATE purchase_receipts SET rate_id = "+pp(1)+" WHERE number = "+pp(2), early.ID.String(), r.Number))
		run("cambiar moneda", false, step("UPDATE purchase_receipts SET currency = 'USD' WHERE number = "+pp(1), r.Number))
		run("cambiar fecha", false, step("UPDATE purchase_receipts SET document_date = "+pp(1)+" WHERE number = "+pp(2), "2026-10-25", r.Number))

		expense := "INSERT INTO purchase_expenses (organization_id, id, receipt_id, concept, amount, criterion, late, created_by, created_at_utc, currency, expense_date, rate_id, valued) " +
			"SELECT organization_id, " + pp(1) + ", id, 'X', '40', 'value', " + pp(2) + ", created_by, created_at_utc, 'VES', " + pp(3) + ", " + pp(4) + ", " + pp(5) +
			" FROM purchase_receipts WHERE number = " + pp(6)
		const newID = "01890000-0000-7000-8000-000000000001" // cada intento se revierte
		run("gasto en VES sin tasa", false, step(expense, newID, false, "2026-10-22", nil, "1", r.Number))
		run("gasto sin valorar", false, step(expense, newID, false, "2026-10-22", early.ID.String(), nil, r.Number))
		run("gasto con tasa futura", false, step(expense, newID, false, "2026-10-22", later.ID.String(), "1", r.Number))
		run("gasto válido", true, step(expense, newID, false, "2026-10-22", early.ID.String(), "1", r.Number))
	})
}

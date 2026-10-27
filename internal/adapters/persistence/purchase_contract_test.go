package persistence_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/gamijoam/inventario-go/internal/application/auth"
	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/application/purchasing"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/purchase"
	"github.com/gamijoam/inventario-go/internal/platform/config"
)

type purchaseEnv struct {
	*stockEnv
	buy *purchasing.Service
}

func newPurchaseEnv(t *testing.T, cfg config.Database) *purchaseEnv {
	t.Helper()
	e := newStockEnv(t, cfg)
	p := &purchaseEnv{stockEnv: e, buy: purchasing.NewService(e.store.PurchaseStore(), e.stock, e.clock.now)}
	if _, err := p.buy.CreateSupplier(ctx(t), e.admin, "dist", "Distribuidora, C.A.", "J-12345678-9"); err != nil {
		t.Fatal(err)
	}
	e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
	e.create(appcatalog.NewProduct{SKU: "B", Name: "B"})
	return p
}

func (e *purchaseEnv) receipt(order string) purchasing.ReceiptView {
	e.t.Helper()
	v, err := e.buy.CreateReceipt(ctx(e.t), e.admin, purchasing.ReceiptInput{Where: e.here, Supplier: "dist", Order: order, Document: "FAC-1"})
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *purchaseEnv) setLine(ref, product, qty, cost string) purchasing.ReceiptView {
	e.t.Helper()
	v, err := e.buy.SetReceiptLine(ctx(e.t), e.admin, ref, purchasing.LineInput{Product: product, Quantity: qty, UnitCost: cost})
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *purchaseEnv) confirm(ref string) purchasing.ReceiptView {
	e.t.Helper()
	v, err := e.buy.ConfirmReceipt(ctx(e.t), e.admin, ref)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

// Sección 19.7 (1): 10 a 80 y 5 a 95 por compras → CPP 85; el costo de
// reposición queda aparte y no toca el CPP.
func TestReceiptsRecalculateTheAverageCost(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newPurchaseEnv(t, cfg)
		c := ctx(t)
		r1 := e.receipt("")
		e.setLine(r1.Number, "A", "10", "80")
		v := e.confirm(r1.Number)
		if v.Status != purchase.ReceiptConfirmed || len(v.Movements) != 1 || v.Movements[0].Kind != inventory.KindPurchaseIn || v.Lines[0].MovementID.IsZero() {
			t.Fatalf("confirmar: %+v", v)
		}
		if again := e.confirm(r1.Number); !again.Replayed || len(again.Movements) != 0 {
			t.Fatalf("confirmar dos veces: %+v", again)
		}
		r2 := e.receipt("")
		e.setLine(r2.ID.String(), "A", "5", "95")
		e.confirm(r2.ID.String())
		if got := e.valued(e.here, "A"); got != "15/1275/85" {
			t.Fatalf("CPP: %s", got)
		}
		cv, err := e.buy.SetReplacementCost(c, e.admin, "principal", "A", "100", "cotización del 27/10")
		if err != nil || len(cv.Replacement) != 1 || cv.Replacement[0].UnitCost.String() != "100" {
			t.Fatalf("reposición: %+v %v", cv, err)
		}
		if got := e.valued(e.here, "A"); got != "15/1275/85" {
			t.Fatalf("la reposición no toca el CPP: %s", got)
		}
		cv, _ = e.buy.SetReplacementCost(c, e.admin, "principal", "A", "102", "")
		if cv.Last == nil || cv.Last.UnitCost.String() != "95" || cv.Last.Receipt != r2.Number || len(cv.Replacement) != 2 || cv.Replacement[0].UnitCost.String() != "102" ||
			len(cv.Balances) != 1 || cv.Balances[0].UnitCost().Value.String() != "85" {
			t.Fatalf("costos: %+v", cv)
		}
		// Un borrador cancelado no mueve nada; uno confirmado no se cancela.
		r3 := e.receipt("")
		e.setLine(r3.Number, "B", "1", "1")
		if v, err := e.buy.CancelReceipt(c, e.admin, r3.Number); err != nil || v.Status != purchase.ReceiptCancelled {
			t.Fatal(v, err)
		}
		if _, err := e.buy.ConfirmReceipt(c, e.admin, r3.Number); !errors.Is(err, purchase.ErrInvalid) {
			t.Errorf("confirmar una cancelada: %v", err)
		}
		if _, err := e.buy.CancelReceipt(c, e.admin, r1.Number); !errors.Is(err, purchase.ErrInvalid) {
			t.Errorf("cancelar una confirmada: %v", err)
		}
		if _, err := e.buy.SetReceiptLine(c, e.admin, r1.Number, purchasing.LineInput{Product: "B", Quantity: "1", UnitCost: "1"}); !errors.Is(err, purchase.ErrInvalid) {
			t.Errorf("editar una confirmada: %v", err)
		}
		e.verify()
	})
}

// Gastos en borrador: se prorratean al confirmar y entran al costo.
func TestExpensesAreCapitalized(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newPurchaseEnv(t, cfg)
		c := ctx(t)
		r := e.receipt("")
		e.setLine(r.Number, "A", "10", "8") // 80
		e.setLine(r.Number, "B", "20", "1") // 20
		if _, err := e.buy.AddExpense(c, e.admin, r.Number, "Flete", "10", "value"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.buy.AddExpense(c, e.admin, r.Number, "Seguro", "3", "quantity"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.buy.AddExpense(c, e.admin, r.Number, "Raro", "1", "peso"); !errors.Is(err, purchase.ErrInvalid) {
			t.Errorf("criterio inválido: %v", err)
		}
		v := e.confirm(r.Number)
		// A: 80 + 8 (flete) + 1 (seguro) = 89; B: 20 + 2 + 2 = 24.
		if got := e.valued(e.here, "A"); got != "10/89/8.9" {
			t.Fatalf("A: %s", got)
		}
		if got := e.valued(e.here, "B"); got != "20/24/1.2" {
			t.Fatalf("B: %s", got)
		}
		if len(v.Expenses) != 2 || len(v.Expenses[0].Allocations) != 2 || v.Lines[0].Expenses.String() != "9" {
			t.Fatalf("prorrateo: %+v", v)
		}
		e.verify()
	})
}

// Gasto tardío: lo que sigue en el almacén se revaloriza; lo que salió
// queda como diferencia (sección 19.3).
func TestLateExpenseRevaluesOnlyWhatRemains(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newPurchaseEnv(t, cfg)
		c := ctx(t)
		r := e.receipt("")
		e.setLine(r.Number, "A", "10", "8")
		e.confirm(r.Number)
		e.mustAdjust("A", inventory.Out, "4", "internal_use") // salen 4 de 10
		// El almacenista prepara compras pero no toca la valoración.
		e.createUser(e.admin, "alm", "ALMACENISTA", auth.ScopeRef{Company: "principal", Branch: "principal"})
		if _, err := e.buy.AddExpense(c, e.login("alm", userPassword).Principal, r.Number, "Flete", "10", ""); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("gasto tardío sin confirmar compras: %v", err)
		}
		v, err := e.buy.AddExpense(c, e.admin, r.Number, "Flete", "10", "")
		if err != nil {
			t.Fatal(err)
		}
		a := v.Expenses[0].Allocations[0]
		if !v.Expenses[0].Late || a.Capitalized.String() != "6" || a.Variance.String() != "4" || len(v.Movements) != 1 || v.Movements[0].Kind != inventory.KindRevaluation {
			t.Fatalf("gasto tardío: %+v %+v", v.Expenses, v.Movements)
		}
		// 6 unidades a 8 = 48, más 6 capitalizado.
		if got := e.valued(e.here, "A"); got != "6/54/9" {
			t.Fatalf("revaloración: %s", got)
		}
		// Si ya no queda nada, todo es diferencia y no hay movimiento.
		e.mustAdjust("A", inventory.Out, "6", "internal_use")
		v, err = e.buy.AddExpense(c, e.admin, r.Number, "Aduana", "5", "")
		if err != nil || v.Expenses[1].Allocations[0].Variance.String() != "5" || len(v.Movements) != 0 {
			t.Fatalf("sin existencia: %+v %v", v, err)
		}
		e.verify()
	})
}

// Órdenes: recepción parcial, prellenado, sin recibir de más; la orden se
// completa o se cancela.
func TestReceiptsAgainstOrders(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newPurchaseEnv(t, cfg)
		c := ctx(t)
		o, err := e.buy.CreateOrder(c, e.admin, purchasing.OrderInput{Company: "principal", Branch: "principal", Supplier: "dist",
			Lines: []purchasing.LineInput{{Product: "A", Quantity: "10", UnitCost: "80"}, {Product: "B", Quantity: "5", UnitCost: "2"}}})
		if err != nil || o.Number != "OC-000001" || len(o.Lines) != 2 {
			t.Fatalf("orden: %+v %v", o, err)
		}
		r := e.receipt(o.Number)
		if len(r.Lines) != 2 || r.Lines[0].OrderLineNo != 1 || r.Order != o.Number {
			t.Fatalf("prellenado: %+v", r)
		}
		e.setLine(r.Number, "A", "6", "82") // llegan 6 a otro precio
		if _, err := e.buy.RemoveReceiptLine(c, e.admin, r.Number, "B"); err != nil {
			t.Fatal(err)
		}
		e.confirm(r.Number)
		ov, _ := e.buy.ShowOrder(c, e.admin, o.Number)
		if ov.Status != purchase.OrderOpen || ov.Lines[0].Received.String() != "6" || ov.Lines[1].Received.String() != "0" {
			t.Fatalf("parcial: %+v", ov.Lines)
		}
		if got := e.valued(e.here, "A"); got != "6/492/82" {
			t.Fatalf("costo real de la recepción, no el pactado: %s", got)
		}
		// Recibir de más que lo pendiente se rechaza al confirmar.
		r2 := e.receipt(o.Number)
		e.setLine(r2.Number, "A", "5", "80")
		if _, err := e.buy.ConfirmReceipt(c, e.admin, r2.Number); !errors.Is(err, purchase.ErrInvalid) {
			t.Fatalf("recibir de más: %v", err)
		}
		if got := e.valued(e.here, "A"); got != "6/492/82" {
			t.Fatalf("el rechazo no movió nada: %s", got)
		}
		e.setLine(r2.Number, "A", "4", "80")
		e.confirm(r2.Number)
		if ov, _ := e.buy.ShowOrder(c, e.admin, o.Number); ov.Status != purchase.OrderReceived {
			t.Fatalf("orden completa: %v", ov.Status)
		}
		if _, err := e.buy.CreateReceipt(c, e.admin, purchasing.ReceiptInput{Where: e.here, Order: o.Number}); !errors.Is(err, purchase.ErrInvalid) {
			t.Errorf("recibir una orden cerrada: %v", err)
		}
		o2, _ := e.buy.CreateOrder(c, e.admin, purchasing.OrderInput{Company: "principal", Branch: "principal", Supplier: "dist",
			Lines: []purchasing.LineInput{{Product: "A", Quantity: "1", UnitCost: "1"}}})
		if v, err := e.buy.CancelOrder(c, e.admin, o2.Number); err != nil || v.Status != purchase.OrderCancelled {
			t.Fatal(v, err)
		}
		if list, _ := e.buy.Orders(c, e.admin, purchase.OrderOpen); len(list) != 0 {
			t.Errorf("órdenes abiertas: %d", len(list))
		}
		e.verify()
	})
}

func TestPurchasePermissions(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newPurchaseEnv(t, cfg)
		c := ctx(t)
		e.createUser(e.admin, "alm", "ALMACENISTA", auth.ScopeRef{Company: "principal", Branch: "principal"})
		e.createUser(e.admin, "caja", "CAJERO", auth.ScopeRef{Company: "principal", Branch: "principal"})
		e.createUser(e.admin, "enc", "ENCARGADO", auth.ScopeRef{Company: "principal", Branch: "principal"})
		alm, caja, enc := e.login("alm", userPassword).Principal, e.login("caja", userPassword).Principal, e.login("enc", userPassword).Principal
		if _, err := e.buy.CreateSupplier(c, enc, "otro", "Otro", ""); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("proveedor sin ámbito de organización: %v", err)
		}
		r, err := e.buy.CreateReceipt(c, alm, purchasing.ReceiptInput{Where: e.here, Supplier: "dist"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.buy.SetReceiptLine(c, alm, r.Number, purchasing.LineInput{Product: "A", Quantity: "1", UnitCost: "1"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.buy.ConfirmReceipt(c, alm, r.Number); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("almacenista confirma: %v", err)
		}
		if _, err := e.buy.ConfirmReceipt(c, enc, r.Number); err != nil {
			t.Errorf("encargado confirma: %v", err)
		}
		if _, err := e.buy.CreateReceipt(c, caja, purchasing.ReceiptInput{Where: e.here, Supplier: "dist"}); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("cajero recibe: %v", err)
		}
		if _, err := e.buy.Receipts(c, caja, ""); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("cajero lista compras: %v", err)
		}
		if _, err := e.buy.SetReplacementCost(c, alm, "principal", "A", "1", ""); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("almacenista fija reposición: %v", err)
		}
		if _, err := e.buy.CreateReceipt(c, e.admin, purchasing.ReceiptInput{Where: e.here, Supplier: "nadie"}); !errors.Is(err, apporg.ErrNotFound) {
			t.Errorf("proveedor inexistente: %v", err)
		}
		e.verify()
	})
}

// Dos confirmaciones simultáneas de la misma recepción: una sola entrada.
func TestConcurrentConfirmationEntersOnce(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newPurchaseEnv(t, cfg)
		r := e.receipt("")
		e.setLine(r.Number, "A", "10", "3")
		var wg sync.WaitGroup
		for range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := e.buy.ConfirmReceipt(ctx(t), e.admin, r.Number); err != nil {
					t.Errorf("confirmar: %v", err)
				}
			}()
		}
		wg.Wait()
		if got := e.valued(e.here, "A"); got != "10/30/3" {
			t.Fatalf("entrada única: %s", got)
		}
		e.verify()
	})
}

// La base protege los documentos de compra aunque Go se saltara.
func TestPurchaseDocumentsAreGuarded(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newPurchaseEnv(t, cfg)
		c := ctx(t)
		e.in(e.here, "A", "4", "5")
		r := e.receipt("")
		e.setLine(r.Number, "A", "6", "10")
		if _, err := e.buy.AddExpense(c, e.admin, r.Number, "Flete", "1", ""); err != nil {
			t.Fatal(err)
		}
		e.confirm(r.Number)
		if got := e.valued(e.here, "A"); got != "10/81/8.1" {
			t.Fatalf("A: %s", got)
		}
		if _, err := e.buy.SetReplacementCost(c, e.admin, "principal", "A", "9", "nota"); err != nil {
			t.Fatal(err)
		}
		pp := e.store.Dialect.Placeholder
		byNumber := "(SELECT id FROM purchase_receipts WHERE number = " + pp(1) + ")"
		for name, q := range map[string]string{
			"reabrir recepción": "UPDATE purchase_receipts SET status = 'draft', closed_by = NULL, closed_at_utc = NULL WHERE number = " + pp(1),
			"línea confirmada":  "UPDATE purchase_receipt_lines SET quantity = '7' WHERE receipt_id = " + byNumber,
			"borrar línea":      "DELETE FROM purchase_receipt_lines WHERE receipt_id = " + byNumber,
			"borrar recepción":  "DELETE FROM purchase_receipts WHERE number = " + pp(1),
			"editar gasto":      "UPDATE purchase_expenses SET amount = '2' WHERE receipt_id = " + byNumber,
			"editar prorrateo":  "UPDATE purchase_expense_allocations SET amount = '2' WHERE expense_id = (SELECT id FROM purchase_expenses WHERE receipt_id = " + byNumber + ")",
		} {
			if _, err := e.store.DB.ExecContext(c, q, r.Number); err == nil {
				t.Errorf("%s: la base debía rechazarlo", name)
			}
		}
		if _, err := e.store.DB.ExecContext(c, "DELETE FROM replacement_costs WHERE note = "+pp(1), "nota"); err == nil {
			t.Error("borrar reposición: la base debía rechazarlo")
		}
		e.verify()
	})
}

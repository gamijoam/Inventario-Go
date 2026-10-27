package persistence_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gamijoam/inventario-go/internal/application/auth"
	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/platform/config"
)

type stockEnv struct {
	*catalogEnv
	stock *stock.Service
	here  stock.WarehouseRef
}

func newStockEnv(t *testing.T, cfg config.Database) *stockEnv {
	t.Helper()
	e := newCatalogEnv(t, cfg)
	return &stockEnv{catalogEnv: e, stock: stock.NewService(e.store.StockStore(), e.clock.now),
		here: stock.WarehouseRef{Company: "principal", Branch: "principal"}}
}

func (e *stockEnv) adjust(p identity.Principal, product string, d inventory.Direction, qty, reason string) (stock.MovementView, error) {
	return e.stock.Adjust(ctx(e.t), p, stock.AdjustInput{Where: e.here, Product: product, Direction: d, Quantity: qty, Reason: reason})
}

func (e *stockEnv) mustAdjust(product string, d inventory.Direction, qty, reason string) stock.MovementView {
	e.t.Helper()
	v, err := e.adjust(e.admin, product, d, qty, reason)
	if err != nil {
		e.t.Fatalf("ajuste %s %v %s: %v", product, d, qty, err)
	}
	return v
}

func (e *stockEnv) onHand(product string) string {
	e.t.Helper()
	list, err := e.stock.Stock(ctx(e.t), e.admin, stock.StockFilter{Where: &e.here, Product: product})
	if err != nil {
		e.t.Fatal(err)
	}
	if len(list) == 0 {
		return "sin saldo"
	}
	return stock.FormatQuantity(list[0].OnHand, list[0].Unit)
}

func (e *stockEnv) verify() stock.Report {
	e.t.Helper()
	rep, err := e.stock.Verify(ctx(e.t), e.admin)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(rep.Problems) != 0 {
		e.t.Fatalf("el kardex no coincide con los saldos: %+v", rep.Problems)
	}
	return rep
}

// BP-M2-001: los movimientos suman exactamente el saldo y el kardex se
// reproduce de forma determinista.
func TestKardexEqualsBalanceAndReplays(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		e.create(appcatalog.NewProduct{SKU: "QUESO", Name: "Queso", UnitCode: "KG"})
		e.create(appcatalog.NewProduct{SKU: "ARROZ", Name: "Arroz"})
		e.mustAdjust("QUESO", inventory.In, "12.5", "initial_load")
		e.mustAdjust("QUESO", inventory.Out, "0.125", "shrinkage")
		e.mustAdjust("QUESO", inventory.Out, "2.375", "internal_use")
		e.mustAdjust("ARROZ", inventory.In, "40", "initial_load")
		if got := e.onHand("QUESO"); got != "10.000" {
			t.Fatalf("queso = %s", got)
		}
		bv, kardex, err := e.stock.Kardex(ctx(t), e.admin, e.here, "QUESO", 100, 0)
		if err != nil || len(kardex) != 3 || bv.Version != 3 || kardex[2].OnHandAfter.String() != "10" || kardex[1].Quantity.String() != "-0.125" {
			t.Fatalf("kardex = %+v %+v %v", bv, kardex, err)
		}
		if rep := e.verify(); rep.Balances != 2 || rep.Movements != 4 {
			t.Fatalf("verify = %+v", rep)
		}
	})
}

func TestOutboundNeverGoesNegative(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		if _, err := e.adjust(e.admin, "A", inventory.Out, "1", "correction"); !errors.Is(err, inventory.ErrInsufficient) {
			t.Fatalf("salida sin saldo: %v", err)
		}
		e.mustAdjust("A", inventory.In, "3", "initial_load")
		if _, err := e.adjust(e.admin, "A", inventory.Out, "4", "correction"); !errors.Is(err, inventory.ErrInsufficient) {
			t.Fatalf("salida mayor que el saldo: %v", err)
		}
		if got := e.onHand("A"); got != "3" {
			t.Fatalf("el rechazo no debe cambiar nada: %s", got)
		}
		for name, in := range map[string]stock.AdjustInput{
			"decimales de más":     {Where: e.here, Product: "A", Direction: inventory.In, Quantity: "1.5", Reason: "correction"},
			"motivo de salida":     {Where: e.here, Product: "A", Direction: inventory.In, Quantity: "1", Reason: "shrinkage"},
			"other sin nota":       {Where: e.here, Product: "A", Direction: inventory.In, Quantity: "1", Reason: "other"},
			"producto inexistente": {Where: e.here, Product: "NADA", Direction: inventory.In, Quantity: "1", Reason: "correction"},
			"almacén inexistente":  {Where: stock.WarehouseRef{Company: "principal", Branch: "principal", Warehouse: "x"}, Product: "A", Direction: inventory.In, Quantity: "1", Reason: "correction"},
		} {
			if _, err := e.stock.Adjust(ctx(t), e.admin, in); err == nil {
				t.Errorf("%s: aceptado", name)
			}
		}
		e.verify()
	})
}

// Varias cajas sacan a la vez de un saldo de 10: exactamente 10 salidas de 1
// se confirman, ninguna unidad se vende dos veces y el kardex cuadra.
func TestConcurrentOutboundsNeverOversell(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		e.create(appcatalog.NewProduct{SKU: "ULTIMA", Name: "Última unidad"})
		e.mustAdjust("ULTIMA", inventory.In, "10", "initial_load")
		const workers = 16
		var ok, short, other int
		var mu sync.Mutex
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := e.adjust(e.admin, "ULTIMA", inventory.Out, "1", "correction")
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					ok++
				case errors.Is(err, inventory.ErrInsufficient):
					short++
				default:
					other++
					t.Errorf("error inesperado: %v", err)
				}
			}()
		}
		wg.Wait()
		if ok != 10 || short != workers-10 || other != 0 {
			t.Fatalf("confirmadas=%d sin stock=%d otros=%d", ok, short, other)
		}
		if got := e.onHand("ULTIMA"); got != "0" {
			t.Fatalf("saldo final = %s", got)
		}
		if rep := e.verify(); rep.Movements != 11 {
			t.Fatalf("verify = %+v", rep)
		}
	})
}

func TestIdempotencyKeyAppliesOnce(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		in := stock.AdjustInput{Where: e.here, Product: "A", Direction: inventory.In, Quantity: "5", Reason: "initial_load", IdempotencyKey: "carga-1"}
		first, err := e.stock.Adjust(ctx(t), e.admin, in)
		if err != nil || first.Replayed {
			t.Fatal(first, err)
		}
		// La respuesta se perdió y se reintenta, incluso en paralelo.
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				again, err := e.stock.Adjust(ctx(t), e.admin, in)
				if err != nil || !again.Replayed || again.ID != first.ID {
					t.Errorf("reintento: %+v %v", again, err)
				}
			}()
		}
		wg.Wait()
		if got := e.onHand("A"); got != "5" {
			t.Fatalf("el reintento no debe sumar: %s", got)
		}
		in.Quantity = "6"
		if _, err := e.stock.Adjust(ctx(t), e.admin, in); !errors.Is(err, stock.ErrKeyReused) {
			t.Fatalf("misma clave, otros datos: %v", err)
		}
		e.verify()
	})
}

func TestReversalCorrectsWithoutEditingTheKardex(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		c := ctx(t)
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		wrong := e.mustAdjust("A", inventory.In, "50", "initial_load") // eran 5
		rev, err := e.stock.Reverse(c, e.admin, wrong.ID.String(), "se cargaron 50 en vez de 5", "")
		if err != nil || rev.Kind != inventory.KindReversal || rev.Quantity.String() != "-50" || rev.ReversesID != wrong.ID {
			t.Fatalf("reverso: %+v %v", rev, err)
		}
		if _, err := e.stock.Reverse(c, e.admin, wrong.ID.String(), "otra vez", ""); !errors.Is(err, stock.ErrAlreadyReversed) {
			t.Errorf("doble reverso: %v", err)
		}
		if _, err := e.stock.Reverse(c, e.admin, rev.ID.String(), "", ""); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("revertir un reverso: %v", err)
		}
		e.mustAdjust("A", inventory.In, "5", "initial_load")
		out := e.mustAdjust("A", inventory.Out, "5", "shrinkage")
		e.mustAdjust("A", inventory.In, "1", "correction")
		// Revertir la salida devuelve las 5 unidades.
		if _, err := e.stock.Reverse(c, e.admin, out.ID.String(), "", ""); err != nil {
			t.Fatal(err)
		}
		_, kardex, _ := e.stock.Kardex(c, e.admin, e.here, "A", 100, 0)
		if len(kardex) != 6 || kardex[0].Quantity.String() != "50" || e.onHand("A") != "6" {
			t.Fatalf("el original sigue intacto en el kardex: %d movimientos, saldo=%s", len(kardex), e.onHand("A"))
		}
		// Revertir una entrada cuyas unidades ya salieron falla igual que una salida.
		e.mustAdjust("A", inventory.Out, "6", "correction")
		if _, err := e.stock.Reverse(c, e.admin, kardex[2].ID.String(), "", ""); !errors.Is(err, inventory.ErrInsufficient) {
			t.Errorf("revertir entrada sin saldo: %v", err)
		}
		e.verify()
	})
}

func TestStockPermissionsByWarehouse(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		c := ctx(t)
		if _, err := e.org.CreateWarehouse(c, e.admin, "principal", "principal", "danados", "Dañados"); err != nil {
			t.Fatal(err)
		}
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		principal := stock.WarehouseRef{Company: "principal", Branch: "principal", Warehouse: "principal"}
		danados := stock.WarehouseRef{Company: "principal", Branch: "principal", Warehouse: "danados"}
		for _, w := range []stock.WarehouseRef{principal, danados} {
			if _, err := e.stock.Adjust(c, e.admin, stock.AdjustInput{Where: w, Product: "A", Direction: inventory.In, Quantity: "3", Reason: "initial_load"}); err != nil {
				t.Fatal(err)
			}
		}
		// Sin --warehouse y con dos almacenes activos, hay que elegir.
		if _, err := e.adjust(e.admin, "A", inventory.In, "1", "correction"); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("almacén ambiguo: %v", err)
		}
		// Almacenista solo del almacén principal; cajero de la sucursal.
		if _, err := e.svc.CreateUser(c, e.admin, auth.NewUserInput{Username: "alm", DisplayName: "Alm", Password: userPassword}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Assign(c, e.admin, "alm", "ALMACENISTA", auth.ScopeRef{Company: "principal", Branch: "principal", Warehouse: "principal"}); err != nil {
			t.Fatal(err)
		}
		e.createUser(e.admin, "caja", "CAJERO", auth.ScopeRef{Company: "principal", Branch: "principal"})
		alm := e.login("alm", userPassword).Principal
		caja := e.login("caja", userPassword).Principal

		if _, err := e.stock.Adjust(c, alm, stock.AdjustInput{Where: principal, Product: "A", Direction: inventory.Out, Quantity: "1", Reason: "shrinkage"}); err != nil {
			t.Errorf("almacenista en su almacén: %v", err)
		}
		// Otro almacén de su sucursal: no lo ve, así que no existe para él.
		if _, err := e.stock.Adjust(c, alm, stock.AdjustInput{Where: danados, Product: "A", Direction: inventory.Out, Quantity: "1", Reason: "shrinkage"}); !errors.Is(err, apporg.ErrNotFound) {
			t.Errorf("almacenista en otro almacén de su sucursal: %v", err)
		}
		if _, err := e.stock.Adjust(c, caja, stock.AdjustInput{Where: principal, Product: "A", Direction: inventory.In, Quantity: "1", Reason: "correction"}); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("cajero ajusta: %v", err)
		}
		if list, err := e.stock.Stock(c, caja, stock.StockFilter{}); err != nil || len(list) != 2 {
			t.Errorf("el cajero ve las existencias de su sucursal: %v %v", list, err)
		}
		if list, _ := e.stock.Stock(c, alm, stock.StockFilter{}); len(list) != 1 || list[0].Location.Warehouse.Code.String() != "PRINCIPAL" {
			t.Errorf("el almacenista solo ve su almacén: %v", list)
		}
		if _, err := e.stock.Verify(c, caja); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("verificar exige ámbito de organización: %v", err)
		}
		// Otra organización: ni ve ni toca el almacén de esta.
		b, err := orgAndUnits(t, e.store, "beto")
		if err != nil {
			t.Fatal(err)
		}
		other := identity.Principal{UserID: e.admin.UserID, OrganizationID: b.ID,
			Grants: []identity.Grant{{Scope: b.Scope(), Permissions: identity.Templates()[0].Permissions}}}
		if _, err := e.stock.Adjust(c, other, stock.AdjustInput{Where: principal, Product: "A", Direction: inventory.In, Quantity: "1", Reason: "correction"}); !errors.Is(err, apporg.ErrNotFound) {
			t.Errorf("otra organización: %v", err)
		}
		e.verify()
	})
}

// La base protege el kardex aunque el código de Go se saltara: no se edita,
// no se borra y no acepta movimientos que no coincidan con el saldo.
func TestKardexIsImmutableInTheDatabase(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		c := ctx(t)
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		m := e.mustAdjust("A", inventory.In, "5", "initial_load")
		pp := e.store.Dialect.Placeholder
		for name, q := range map[string]string{
			"editar movimiento": "UPDATE inventory_movements SET quantity = '6' WHERE id = " + pp(1),
			"borrar movimiento": "DELETE FROM inventory_movements WHERE id = " + pp(1),
			"saltar versión":    "UPDATE inventory_balances SET version = version + 2, on_hand = '7' WHERE product_id = (SELECT product_id FROM inventory_movements WHERE id = " + pp(1) + ")",
			"borrar saldo":      "DELETE FROM inventory_balances WHERE product_id = (SELECT product_id FROM inventory_movements WHERE id = " + pp(1) + ")",
		} {
			if _, err := e.store.DB.ExecContext(c, q, m.ID.String()); err == nil {
				t.Errorf("%s: la base debía rechazarlo", name)
			}
		}
		// Movimiento insertado a mano que no corresponde al saldo.
		sc := m.Scope
		ins := fmt.Sprintf("INSERT INTO inventory_movements (organization_id, id, company_id, branch_id, warehouse_id, product_id, sequence, kind, reason, quantity, on_hand_after, note, actor_id, occurred_at_utc, request_hash) "+
			"VALUES (%s, %s, %s, %s, %s, %s, 2, 'adjustment_in', 'correction', '1', '6', '', %s, %s, '%s')",
			pp(1), pp(2), pp(3), pp(4), pp(5), pp(6), pp(7), pp(8), strings.Repeat("a", 64))
		_, err := e.store.DB.ExecContext(c, ins, sc.Organization().String(), m.ID.String()[:35]+"f", sc.Company().String(), sc.Branch().String(),
			sc.Warehouse().String(), m.ProductID.String(), m.ActorID.String(), e.store.Dialect.Timestamp(e.clock.now()))
		if err == nil || !strings.Contains(err.Error(), "no coincide con el saldo") {
			t.Errorf("movimiento sin saldo correspondiente: %v", err)
		}
		e.verify()
	})
}

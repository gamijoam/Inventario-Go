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
	"github.com/gamijoam/inventario-go/internal/platform/migration"
	"github.com/gamijoam/inventario-go/migrations"
)

func (e *stockEnv) in(where stock.WarehouseRef, product, qty, unitCost string) stock.MovementView {
	e.t.Helper()
	v, err := e.stock.Adjust(ctx(e.t), e.admin, stock.AdjustInput{Where: where, Product: product, Direction: inventory.In, Quantity: qty, UnitCost: unitCost, Reason: "correction"})
	if err != nil {
		e.t.Fatalf("entrada %s %s a %s: %v", product, qty, unitCost, err)
	}
	return v
}

// valued devuelve "existencia/valor/CPP" del producto en el almacén.
func (e *stockEnv) valued(where stock.WarehouseRef, product string) string {
	e.t.Helper()
	list, err := e.stock.Stock(ctx(e.t), e.admin, stock.StockFilter{Where: &where, Product: product})
	if err != nil {
		e.t.Fatal(err)
	}
	if len(list) != 1 {
		return "sin saldo"
	}
	b := list[0]
	return fmt.Sprintf("%s/%s/%s", b.OnHand, b.Value, b.UnitCost())
}

func (e *stockEnv) secondWarehouse() (stock.WarehouseRef, stock.WarehouseRef) {
	e.t.Helper()
	if _, err := e.org.CreateWarehouse(ctx(e.t), e.admin, "principal", "principal", "b", "Depósito B"); err != nil {
		e.t.Fatal(err)
	}
	return stock.WarehouseRef{Company: "principal", Branch: "principal", Warehouse: "principal"},
		stock.WarehouseRef{Company: "principal", Branch: "principal", Warehouse: "b"}
}

func (e *stockEnv) transfer(p identity.Principal, product, qty, key string) (stock.TransferView, error) {
	return e.stock.Transfer(ctx(e.t), p, stock.TransferInput{Company: "principal", Branch: "principal", From: "principal", To: "b",
		Product: product, Quantity: qty, IdempotencyKey: key})
}

// Sección 19: mismo resultado exacto en SQLite y PostgreSQL.
func TestMovingAverageCostOnBothEngines(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		e.create(appcatalog.NewProduct{SKU: "Q", Name: "Q", UnitCode: "KG"})
		e.in(e.here, "A", "10", "80")
		e.in(e.here, "A", "5", "95")
		if got := e.valued(e.here, "A"); got != "15/1275/85" {
			t.Fatalf("tras dos entradas: %s", got)
		}
		out := e.mustAdjust("A", inventory.Out, "3", "shrinkage")
		if out.Cost.Value.String() != "-255" || out.UnitCost.Value.String() != "85" {
			t.Fatalf("salida = %v %v", out.Cost, out.UnitCost)
		}
		if got := e.valued(e.here, "A"); got != "12/1020/85" {
			t.Fatalf("tras la salida: %s", got)
		}
		// Entrada sin costo: al CPP vigente.
		if m := e.in(e.here, "A", "1", ""); m.Cost.Value.String() != "85" {
			t.Fatalf("entrada al CPP: %v", m.Cost)
		}
		// Redondeo con 6 decimales y salida que agota.
		e.in(e.here, "Q", "3", "1")
		e.mustAdjust("Q", inventory.Out, "1", "shrinkage")
		e.in(e.here, "Q", "1", "0.5")
		if m := e.mustAdjust("Q", inventory.Out, "1", "shrinkage"); m.Cost.Value.String() != "-0.833333" {
			t.Fatalf("salida redondeada: %v", m.Cost)
		}
		if got := e.valued(e.here, "Q"); got != "2/1.666667/0.8333335" {
			t.Fatalf("valor tras redondeo: %s", got)
		}
		if m := e.mustAdjust("Q", inventory.Out, "2", "shrinkage"); m.Cost.Value.String() != "-1.666667" || !m.ValueAfter.Value.IsZero() {
			t.Fatalf("la última salida se lleva el resto: %+v", m.Movement)
		}
		// Sin existencia no hay CPP: la entrada exige costo.
		if _, err := e.adjust(e.admin, "Q", inventory.In, "1", "correction"); err != nil {
			t.Fatalf("con costo: %v", err)
		}
		if _, err := e.stock.Adjust(ctx(t), e.admin, stock.AdjustInput{Where: e.here, Product: "Q", Direction: inventory.Out, Quantity: "1", UnitCost: "3", Reason: "shrinkage"}); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("una salida no lleva costo: %v", err)
		}
		e.mustAdjust("Q", inventory.Out, "1", "shrinkage")
		if _, err := e.stock.Adjust(ctx(t), e.admin, stock.AdjustInput{Where: e.here, Product: "Q", Direction: inventory.In, Quantity: "1", Reason: "correction"}); !errors.Is(err, inventory.ErrCostRequired) {
			t.Errorf("entrada sin costo con existencia cero: %v", err)
		}
		if rep := e.verify(); rep.Balances != 2 || len(rep.Unvalued) != 0 || !rep.OK() {
			t.Fatalf("verify = %+v", rep)
		}
	})
}

func TestReversalRestoresTheOriginalValue(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		e.in(e.here, "A", "10", "10")
		wrong := e.in(e.here, "A", "10", "100") // costo mal digitado
		r, err := e.stock.Reverse(ctx(t), e.admin, wrong.ID.String(), "costo mal digitado", "")
		if err != nil || r.Cost.Value.String() != "-1000" {
			t.Fatalf("reverso = %+v %v", r.Movement, err)
		}
		if got := e.valued(e.here, "A"); got != "10/100/10" {
			t.Fatalf("el error no queda mezclado en el CPP: %s", got)
		}
		out := e.mustAdjust("A", inventory.Out, "4", "shrinkage")
		if r, err := e.stock.Reverse(ctx(t), e.admin, out.ID.String(), "", ""); err != nil || r.Cost.Value.String() != "40" {
			t.Fatalf("reverso de salida = %+v %v", r.Movement, err)
		}
		e.verify()
	})
}

// Sección 21.1: A 15×85, B 6×100; transferir 4 de A a B conserva 1.875.
func TestTransferMovesValueBetweenWarehouses(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		c := ctx(t)
		a, b := e.secondWarehouse()
		e.create(appcatalog.NewProduct{SKU: "P", Name: "P"})
		e.in(a, "P", "15", "85")
		e.in(b, "P", "6", "100")
		tv, err := e.transfer(e.admin, "P", "4", "tr-1")
		if err != nil {
			t.Fatal(err)
		}
		if tv.Value.String() != "340" || tv.Out.Cost.Value.String() != "-340" || tv.In.Cost.Value.String() != "340" ||
			tv.Out.TransferID != tv.ID || tv.In.TransferID != tv.ID || tv.Replayed {
			t.Fatalf("transferencia = %+v", tv)
		}
		if ga, gb := e.valued(a, "P"), e.valued(b, "P"); ga != "11/935/85" || gb != "10/940/94" {
			t.Fatalf("A=%s B=%s", ga, gb)
		}
		// Reintento con la misma clave: no se duplica.
		again, err := e.transfer(e.admin, "P", "4", "tr-1")
		if err != nil || !again.Replayed || again.ID != tv.ID || again.In.ID != tv.In.ID || e.valued(a, "P") != "11/935/85" {
			t.Fatalf("reintento = %+v %v", again, err)
		}
		if _, err := e.transfer(e.admin, "P", "5", "tr-1"); !errors.Is(err, stock.ErrKeyReused) {
			t.Errorf("misma clave, otra cantidad: %v", err)
		}
		// Sin existencia suficiente no se escribe nada.
		if _, err := e.transfer(e.admin, "P", "12", ""); !errors.Is(err, inventory.ErrInsufficient) {
			t.Errorf("transferir de más: %v", err)
		}
		// Agotar el origen se lleva todo su valor.
		if tv, err := e.transfer(e.admin, "P", "11", ""); err != nil || tv.Value.String() != "935" {
			t.Fatalf("agotar origen: %+v %v", tv.Transfer, err)
		}
		if ga, gb := e.valued(a, "P"), e.valued(b, "P"); ga != "0/0/sin valor" || gb != "21/1875/89.285714285714" {
			t.Fatalf("A=%s B=%s", ga, gb)
		}
		// Un tramo no se revierte solo; mismo almacén y otra sucursal no son transferencias internas.
		if _, err := e.stock.Reverse(c, e.admin, tv.In.ID.String(), "", ""); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("revertir un tramo: %v", err)
		}
		same := stock.TransferInput{Company: "principal", Branch: "principal", From: "b", To: "b", Product: "P", Quantity: "1"}
		if _, err := e.stock.Transfer(c, e.admin, same); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("mismo almacén: %v", err)
		}
		if _, err := e.org.CreateBranch(c, e.admin, "principal", "norte", "Norte"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.org.CreateWarehouse(c, e.admin, "principal", "norte", "n1", "N1"); err != nil {
			t.Fatal(err)
		}
		other := stock.TransferInput{Company: "principal", Branch: "principal", From: "b", To: "n1", Product: "P", Quantity: "1"}
		if _, err := e.stock.Transfer(c, e.admin, other); !errors.Is(err, apporg.ErrNotFound) {
			t.Errorf("almacén de otra sucursal: %v", err)
		}
		rep := e.verify()
		if rep.Transfers != 2 || rep.Movements != 6 || !rep.OK() {
			t.Fatalf("verify = %+v", rep)
		}
	})
}

func TestTransferNeedsPermissionOnBothWarehouses(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		c := ctx(t)
		a, _ := e.secondWarehouse()
		e.create(appcatalog.NewProduct{SKU: "P", Name: "P"})
		e.in(a, "P", "5", "2")
		if _, err := e.svc.CreateUser(c, e.admin, auth.NewUserInput{Username: "alm", DisplayName: "Alm", Password: userPassword}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Assign(c, e.admin, "alm", "ALMACENISTA", auth.ScopeRef{Company: "principal", Branch: "principal", Warehouse: "principal"}); err != nil {
			t.Fatal(err)
		}
		e.createUser(e.admin, "caja", "CAJERO", auth.ScopeRef{Company: "principal", Branch: "principal"})
		e.createUser(e.admin, "enc", "ENCARGADO", auth.ScopeRef{Company: "principal", Branch: "principal"})
		alm := e.login("alm", userPassword).Principal
		if _, err := e.transfer(alm, "P", "1", ""); !errors.Is(err, apporg.ErrNotFound) {
			t.Errorf("almacenista sin el almacén destino: %v", err)
		}
		if _, err := e.transfer(e.login("caja", userPassword).Principal, "P", "1", ""); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("cajero: %v", err)
		}
		if _, err := e.transfer(e.login("enc", userPassword).Principal, "P", "1", ""); err != nil {
			t.Errorf("encargado de la sucursal: %v", err)
		}
		e.verify()
	})
}

// Transferencias simultáneas en sentidos opuestos: sin bloqueo mutuo, sin
// perder unidades ni valor.
func TestConcurrentTransfersConserveValue(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		a, b := e.secondWarehouse()
		e.create(appcatalog.NewProduct{SKU: "P", Name: "P"})
		e.in(a, "P", "30", "3")
		e.in(b, "P", "30", "7")
		const workers = 8
		var wg sync.WaitGroup
		errs := make(chan error, workers)
		for i := range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				in := stock.TransferInput{Company: "principal", Branch: "principal", From: "principal", To: "b", Product: "P", Quantity: "2"}
				if i%2 == 1 {
					in.From, in.To = "b", "principal"
				}
				for range 3 {
					if _, err := e.stock.Transfer(ctx(t), e.admin, in); err != nil {
						errs <- err
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Errorf("transferencia concurrente: %v", err)
		}
		list, err := e.stock.Stock(ctx(t), e.admin, stock.StockFilter{Product: "P"})
		if err != nil || len(list) != 2 {
			t.Fatal(list, err)
		}
		if q, v := list[0].OnHand.Add(list[1].OnHand), list[0].Value.Value.Add(list[1].Value.Value); q.String() != "60" || v.String() != "300" {
			t.Fatalf("total %s unidades por %s", q, v)
		}
		if rep := e.verify(); rep.Transfers != workers*3 || !rep.OK() {
			t.Fatalf("verify = %+v", rep)
		}
	})
}

// Existencia cargada con BP-M2-001: tras migrar queda sin valorar hasta que
// una entrada con costo la valore.
func TestStockLoadedBeforeValuationMigratesUnvalued(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		c := ctx(t)
		a, _ := e.secondWarehouse()
		e.create(appcatalog.NewProduct{SKU: "VIEJO", Name: "Viejo"})
		p := e.create(appcatalog.NewProduct{SKU: "CERO", Name: "Cero"})
		migs, err := migration.Load(migrations.FS, cfg.Driver)
		if err != nil {
			t.Fatal(err)
		}
		runner, err := e.store.Runner()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runner.Down(c, len(migs)-5); err != nil {
			t.Fatalf("Down a la 0005: %v", err)
		}
		// Como lo dejaba BP-M2-001: 10 unidades de VIEJO; CERO entró y salió.
		w := e.setup.Warehouse
		viejo, err := e.cat.ShowProduct(c, e.admin, "VIEJO")
		if err != nil {
			t.Fatal(err)
		}
		pp, ts := e.store.Dialect.Placeholder, e.store.Dialect.Timestamp(e.clock.now())
		bal := fmt.Sprintf("INSERT INTO inventory_balances (organization_id, company_id, branch_id, warehouse_id, product_id, on_hand, reserved, blocked, version, updated_at_utc) "+
			"VALUES (%s, %s, %s, %s, %s, %s, '0', '0', %s, %s)", pp(1), pp(2), pp(3), pp(4), pp(5), pp(6), pp(7), pp(8))
		mov := fmt.Sprintf("INSERT INTO inventory_movements (organization_id, id, company_id, branch_id, warehouse_id, product_id, sequence, kind, reason, quantity, on_hand_after, note, actor_id, occurred_at_utc, request_hash) "+
			"VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, '', %s, %s, '%s')", pp(1), pp(2), pp(3), pp(4), pp(5), pp(6), pp(7), pp(8), pp(9), pp(10), pp(11), pp(12), pp(13), strings.Repeat("a", 64))
		upd := fmt.Sprintf("UPDATE inventory_balances SET on_hand = %s, version = %s WHERE product_id = %s", pp(1), pp(2), pp(3))
		ids := []any{w.OrganizationID.String(), w.CompanyID.String(), w.BranchID.String(), w.ID.String()}
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := e.store.DB.ExecContext(c, q, args...); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		movement := func(n int, product, kind, reason, qty, after string) {
			mid := fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
			exec(mov, ids[0], mid, ids[1], ids[2], ids[3], product, n%10, kind, reason, qty, after, e.admin.UserID.String(), ts)
		}
		exec(bal, append(ids, viejo.ID.String(), "10", 1, ts)...)
		movement(1, viejo.ID.String(), "adjustment_in", "initial_load", "10", "10")
		exec(bal, append(ids, p.ID.String(), "4", 1, ts)...)
		movement(11, p.ID.String(), "adjustment_in", "initial_load", "4", "4")
		exec(upd, "0", 2, p.ID.String())
		movement(12, p.ID.String(), "adjustment_out", "shrinkage", "-4", "0")
		if _, err := runner.Up(c); err != nil {
			t.Fatalf("Up: %v", err)
		}

		rep := e.verify()
		if rep.Balances != 2 || len(rep.Unvalued) != 1 || rep.Unvalued[0].Product.SKU.String() != "VIEJO" || !rep.OK() {
			t.Fatalf("verify tras migrar = %+v", rep)
		}
		if got := e.valued(a, "VIEJO"); got != "10/sin valor/sin valor" {
			t.Fatalf("saldo migrado: %s", got)
		}
		if got := e.valued(a, "CERO"); got != "0/sin valor/sin valor" {
			t.Fatalf("saldo migrado en cero: %s", got)
		}
		// No se transfiere ni entra sin costo mientras no esté valorada.
		if _, err := e.transfer(e.admin, "VIEJO", "1", ""); !errors.Is(err, inventory.ErrValuation) {
			t.Errorf("transferir sin valor: %v", err)
		}
		if _, err := e.stock.Adjust(c, e.admin, stock.AdjustInput{Where: a, Product: "VIEJO", Direction: inventory.In, Quantity: "1", Reason: "correction"}); !errors.Is(err, inventory.ErrCostRequired) {
			t.Errorf("entrada sin costo: %v", err)
		}
		// Las salidas siguen funcionando, sin costo conocido.
		if m, err := e.stock.Adjust(c, e.admin, stock.AdjustInput{Where: a, Product: "VIEJO", Direction: inventory.Out, Quantity: "2", Reason: "shrinkage"}); err != nil || m.Cost.Known {
			t.Fatalf("salida sin valor: %+v %v", m.Movement, err)
		}
		// La primera entrada con costo valora todo.
		m := e.in(a, "VIEJO", "2", "4")
		if m.Cost.Value.String() != "8" || m.ValueAdjustment.String() != "32" || e.valued(a, "VIEJO") != "10/40/4" {
			t.Fatalf("entrada que valora: %+v %s", m.Movement, e.valued(a, "VIEJO"))
		}
		e.in(a, "CERO", "1", "3")
		if rep := e.verify(); len(rep.Unvalued) != 0 || !rep.OK() {
			t.Fatalf("verify valorado = %+v", rep)
		}
		if _, err := e.transfer(e.admin, "VIEJO", "1", ""); err != nil {
			t.Fatalf("transferir ya valorado: %v", err)
		}
	})
}

// La base protege la valoración aunque el código de Go se saltara.
func TestValuationIsGuardedInTheDatabase(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		c := ctx(t)
		e.secondWarehouse()
		e.create(appcatalog.NewProduct{SKU: "P", Name: "P"})
		e.in(e.setupRef(), "P", "5", "2")
		tv, err := e.transfer(e.admin, "P", "5", "")
		if err != nil {
			t.Fatal(err)
		}
		pp := e.store.Dialect.Placeholder
		for name, q := range map[string]string{
			"cambiar moneda":       "UPDATE companies SET valuation_currency = 'VES' WHERE id = " + pp(1),
			"valor sin existencia": "UPDATE inventory_balances SET value = '1', version = version + 1 WHERE warehouse_id = " + pp(1),
			"valor negativo":       "UPDATE inventory_balances SET value = '-1', version = version + 1 WHERE warehouse_id = " + pp(1),
			"editar transferencia": "UPDATE inventory_transfers SET value = '1' WHERE id = " + pp(1),
			"borrar transferencia": "DELETE FROM inventory_transfers WHERE id = " + pp(1),
			"moneda inexistente":   "UPDATE companies SET valuation_currency = 'EUR' WHERE id = " + pp(1),
		} {
			arg := tv.ID.String()
			switch {
			case strings.Contains(q, "companies"):
				arg = e.setup.Company.ID.String()
			case strings.Contains(q, "inventory_balances"):
				arg = tv.From.Warehouse().String()
			}
			if _, err := e.store.DB.ExecContext(c, q, arg); err == nil {
				t.Errorf("%s: la base debía rechazarlo", name)
			}
		}
		// Saldo adelantado a mano y movimiento con la secuencia y existencia
		// correctas pero otro valor: solo el disparador puede rechazarlo.
		m := tv.In
		sc := m.Scope
		bump := fmt.Sprintf("UPDATE inventory_balances SET version = version + 1, on_hand = '5.5', value = '11' WHERE warehouse_id = %s AND product_id = %s", pp(1), pp(2))
		if _, err := e.store.DB.ExecContext(c, bump, sc.Warehouse().String(), m.ProductID.String()); err != nil {
			t.Fatal(err)
		}
		ins := fmt.Sprintf("INSERT INTO inventory_movements (organization_id, id, company_id, branch_id, warehouse_id, product_id, sequence, kind, reason, quantity, on_hand_after, note, actor_id, occurred_at_utc, request_hash, unit_cost, cost, value_after) "+
			"VALUES (%s, %s, %s, %s, %s, %s, %s, 'adjustment_in', 'correction', '0.5', '5.5', '', %s, %s, '%s', '1', '0.5', %s)",
			pp(1), pp(2), pp(3), pp(4), pp(5), pp(6), pp(7), pp(8), pp(9), strings.Repeat("a", 64), pp(10))
		args := func(mid, value string) []any {
			return []any{sc.Organization().String(), mid, sc.Company().String(), sc.Branch().String(), sc.Warehouse().String(), m.ProductID.String(),
				m.Sequence + 1, m.ActorID.String(), e.store.Dialect.Timestamp(e.clock.now()), value}
		}
		if _, err := e.store.DB.ExecContext(c, ins, args("00000000-0000-4000-8000-000000000098", "999")...); err == nil || !strings.Contains(err.Error(), "no coincide con el saldo") {
			t.Errorf("movimiento con otro valor: %v", err)
		}
		if _, err := e.store.DB.ExecContext(c, ins, args("00000000-0000-4000-8000-000000000099", "11")...); err != nil {
			t.Fatalf("control con el mismo valor: %v", err)
		}
		// Ese movimiento coincide con el saldo, pero no con el kardex: verify lo ve.
		if rep, err := e.stock.Verify(c, e.admin); err != nil || len(rep.Problems) != 1 {
			t.Fatalf("verify tras la manipulación = %+v %v", rep, err)
		}
	})
}

func (e *stockEnv) setupRef() stock.WarehouseRef {
	return stock.WarehouseRef{Company: "principal", Branch: "principal", Warehouse: "principal"}
}

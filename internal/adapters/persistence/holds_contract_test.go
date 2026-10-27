package persistence_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/application/auth"
	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/platform/config"
)

// overrider crea un usuario con ALMACENISTA más el permiso extraordinario
// de existencia negativa en la sucursal.
func (e *stockEnv) overrider(username string) identity.Principal {
	e.t.Helper()
	c := ctx(e.t)
	if _, err := e.svc.CreateRole(c, e.admin, "NEGATIVOS", "Excepción de negativos", []string{string(identity.PermNegativeOverride)}); err != nil && !errors.Is(err, apporg.ErrConflict) {
		e.t.Fatal(err)
	}
	e.createUser(e.admin, username, "ALMACENISTA", auth.ScopeRef{Company: "principal", Branch: "principal"})
	if _, err := e.svc.Assign(c, e.admin, username, "NEGATIVOS", auth.ScopeRef{Company: "principal", Branch: "principal"}); err != nil {
		e.t.Fatal(err)
	}
	return e.login(username, userPassword).Principal
}

func (e *stockEnv) out(p identity.Principal, product, qty string, negative bool) (stock.MovementView, error) {
	return e.stock.Adjust(ctx(e.t), p, stock.AdjustInput{Where: e.here, Product: product, Direction: inventory.Out, Quantity: qty, Reason: "correction", AllowNegative: negative})
}

func (e *stockEnv) balance(product string) stock.BalanceView {
	e.t.Helper()
	list, err := e.stock.Stock(ctx(e.t), e.admin, stock.StockFilter{Where: &e.here, Product: product})
	if err != nil || len(list) != 1 {
		e.t.Fatalf("saldo %s: %v %v", product, list, err)
	}
	return list[0]
}

// commits devuelve "física/reservada/bloqueada/disponible".
func (e *stockEnv) commits(product string) string {
	e.t.Helper()
	b := e.balance(product)
	return fmt.Sprintf("%s/%s/%s/%s", b.OnHand, b.Reserved, b.Blocked, b.Available())
}

// DEC-007.F2 (24.10, NG-01 a NG-04, NG-07 y NG-09).
func TestNegativeStockFollowsCompanyAndProductPolicy(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		c := ctx(t)
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		e.create(appcatalog.NewProduct{SKU: "B", Name: "B"})
		e.in(e.here, "A", "2", "8")
		e.in(e.here, "B", "2", "1")
		neg := e.overrider("neg")

		// NG-01: empresa sin política explícita.
		if _, err := e.out(neg, "A", "5", true); !errors.Is(err, inventory.ErrInsufficient) {
			t.Fatalf("NG-01: %v", err)
		}
		// El permiso extraordinario no viene en ninguna plantilla.
		if _, err := e.out(e.admin, "A", "5", true); !errors.Is(err, identity.ErrDenied) {
			t.Fatalf("administrador sin el permiso extraordinario: %v", err)
		}
		if _, err := e.stock.SetCompanyPolicy(c, neg, "principal", true, "5"); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("almacenista configura la política: %v", err)
		}
		if _, err := e.stock.SetCompanyPolicy(c, e.admin, "principal", true, ""); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("habilitar sin límite: %v", err)
		}
		if _, err := e.stock.SetCompanyPolicy(c, e.admin, "principal", true, "5"); err != nil {
			t.Fatal(err)
		}
		pv, err := e.stock.SetProductPolicy(c, e.admin, "principal", "B", "deny", "")
		if err != nil || len(pv.Products) != 1 || pv.Products[0].Allowed || !pv.Current.Company.NegativeEnabled {
			t.Fatalf("política de producto: %+v %v", pv, err)
		}
		// NG-02: producto bloqueado.
		if _, err := e.out(neg, "B", "3", true); !errors.Is(err, inventory.ErrInsufficient) {
			t.Fatalf("NG-02: %v", err)
		}
		// Sin --allow-negative, ni siquiera quien puede.
		if _, err := e.out(neg, "A", "3", false); !errors.Is(err, inventory.ErrInsufficient) {
			t.Fatalf("salida sin pedir la excepción: %v", err)
		}
		// NG-03: dentro del límite, valorada al último costo, con la versión de política.
		key := stock.AdjustInput{Where: e.here, Product: "A", Direction: inventory.Out, Quantity: "5", Reason: "correction", AllowNegative: true, IdempotencyKey: "neg-1"}
		m, err := e.stock.Adjust(c, neg, key)
		if err != nil || m.OnHandAfter.String() != "-3" || m.Cost.Value.String() != "-40" || m.PolicyChangeID.IsZero() {
			t.Fatalf("NG-03: %+v %v", m.Movement, err)
		}
		// NG-07: el reintento no duplica.
		if again, err := e.stock.Adjust(c, neg, key); err != nil || !again.Replayed || again.ID != m.ID {
			t.Fatalf("NG-07: %+v %v", again, err)
		}
		// NG-04: superar el límite se rechaza íntegro.
		if _, err := e.out(neg, "A", "3", true); !errors.Is(err, inventory.ErrInsufficient) {
			t.Fatalf("NG-04: %v", err)
		}
		if got := e.valued(e.here, "A"); got != "-3/-24/sin valor" {
			t.Fatalf("saldo en déficit: %s", got)
		}
		// Nada se reserva ni se bloquea en déficit.
		if _, err := e.stock.Reserve(c, e.admin, stock.ReserveInput{Where: e.here, Product: "A", Quantity: "1", Reference: "PED-1"}); !errors.Is(err, inventory.ErrInsufficient) {
			t.Errorf("reservar en déficit: %v", err)
		}
		// NG-09: la compra siguiente liquida el déficit con su costo real.
		in := e.in(e.here, "A", "5", "10")
		if in.ValueAdjustment.String() != "-6" || e.valued(e.here, "A") != "2/20/10" {
			t.Fatalf("NG-09: %+v %s", in.Movement, e.valued(e.here, "A"))
		}
		// Producto con límite propio dentro de una empresa habilitada.
		if _, err := e.stock.SetProductPolicy(c, e.admin, "principal", "B", "allow", "1"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.out(neg, "B", "4", true); !errors.Is(err, inventory.ErrInsufficient) {
			t.Errorf("límite propio del producto: %v", err)
		}
		if _, err := e.out(neg, "B", "3", true); err != nil {
			t.Errorf("dentro del límite propio: %v", err)
		}
		// Deshabilitar la empresa corta la excepción para todos, sin tocar lo ya registrado.
		if _, err := e.stock.SetCompanyPolicy(c, e.admin, "principal", false, ""); err != nil {
			t.Fatal(err)
		}
		e.in(e.here, "A", "1", "10")
		if _, err := e.out(neg, "A", "4", true); !errors.Is(err, inventory.ErrInsufficient) {
			t.Errorf("empresa deshabilitada: %v", err)
		}
		pv, err = e.stock.Policy(c, e.admin, "principal", "")
		if err != nil || pv.Current.Company.NegativeEnabled || len(pv.Products) != 1 || pv.Products[0].PolicyChange.Product.Mode != inventory.NegativeAllow || pv.Products[0].Allowed {
			t.Fatalf("política vigente: %+v %v", pv, err)
		}
		rep := e.verify()
		if len(rep.Deficits) != 1 || rep.Deficits[0].Product.SKU.String() != "B" {
			t.Fatalf("déficits = %+v", rep.Deficits)
		}
	})
}

// NG-06: salidas en déficit simultáneas nunca superan el límite.
func TestConcurrentDeficitNeverExceedsTheLimit(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		e.in(e.here, "A", "2", "1")
		neg := e.overrider("neg")
		if _, err := e.stock.SetCompanyPolicy(ctx(t), e.admin, "principal", true, "3"); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok := 0
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := e.out(neg, "A", "1", true); err == nil {
					mu.Lock()
					ok++
					mu.Unlock()
				} else if !errors.Is(err, inventory.ErrInsufficient) {
					t.Errorf("salida concurrente: %v", err)
				}
			}()
		}
		wg.Wait()
		if ok != 5 || e.balance("A").OnHand.String() != "-3" {
			t.Fatalf("confirmadas %d, saldo %s", ok, e.balance("A").OnHand)
		}
		e.verify()
	})
}

// 23.8 (ST-01 a ST-04, ST-09 y ST-10) y 24.10 (RS-01 a RS-03).
func TestReservations(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		c := ctx(t)
		a, _ := e.secondWarehouse()
		e.here = a
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		e.in(a, "A", "100", "2")
		res := func(qty, ref string, expires time.Time) (stock.ReservationView, error) {
			return e.stock.Reserve(c, e.admin, stock.ReserveInput{Where: a, Product: "A", Quantity: qty, Reference: ref, ExpiresAt: expires})
		}
		r1, err := res("20", "PED-1", time.Time{})
		if err != nil || e.commits("A") != "100/20/0/80" {
			t.Fatalf("reservar: %v %s", err, e.commits("A"))
		}
		if _, err := e.stock.Block(c, e.admin, stock.BlockInput{Where: a, Product: "A", Quantity: "5", Reason: "quarantine"}); err != nil {
			t.Fatal(err)
		}
		// ST-01 y ST-02.
		if e.commits("A") != "100/20/5/75" {
			t.Fatalf("ST-01: %s", e.commits("A"))
		}
		if _, err := res("76", "PED-2", time.Time{}); !errors.Is(err, inventory.ErrInsufficient) || e.commits("A") != "100/20/5/75" {
			t.Fatalf("ST-02: %v %s", err, e.commits("A"))
		}
		// Una salida o transferencia no toca lo comprometido (ST-10).
		if _, err := e.out(e.admin, "A", "76", false); !errors.Is(err, inventory.ErrInsufficient) {
			t.Errorf("salida sobre lo comprometido: %v", err)
		}
		if _, err := e.stock.Transfer(c, e.admin, stock.TransferInput{Company: "principal", Branch: "principal", From: "principal", To: "b", Product: "A", Quantity: "76"}); !errors.Is(err, inventory.ErrInsufficient) {
			t.Errorf("ST-10 transferir lo reservado: %v", err)
		}
		// ST-04: despachar parte y luego el resto.
		d1, err := e.stock.ConsumeReservation(c, e.admin, r1.ID.String(), "8", "", "desp-1")
		if err != nil || d1.Movement.Cost.Value.String() != "-16" || d1.Status != inventory.HoldActive || e.commits("A") != "92/12/5/75" {
			t.Fatalf("ST-04 parcial: %+v %v %s", d1.Movement, err, e.commits("A"))
		}
		if again, err := e.stock.ConsumeReservation(c, e.admin, r1.ID.String(), "8", "", "desp-1"); err != nil || !again.Replayed || again.Movement.ID != d1.Movement.ID {
			t.Fatalf("despacho repetido con la misma clave: %+v %v", again, err)
		}
		if _, err := e.stock.ConsumeReservation(c, e.admin, r1.ID.String(), "13", "", ""); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("despachar más de lo pendiente: %v", err)
		}
		d2, err := e.stock.ConsumeReservation(c, e.admin, r1.ID.String(), "", "", "")
		if err != nil || d2.Status != inventory.HoldConsumed || e.commits("A") != "80/0/5/75" {
			t.Fatalf("ST-04 total: %v %s", err, e.commits("A"))
		}
		if _, err := e.stock.ReleaseReservation(c, e.admin, r1.ID.String(), ""); !errors.Is(err, apporg.ErrConflict) {
			t.Errorf("liberar una consumida: %v", err)
		}
		// ST-03: crear y cancelar no cambia existencia ni CPP.
		r2, _ := res("30", "PED-3", time.Time{})
		if rel, err := e.stock.ReleaseReservation(c, e.admin, r2.ID.String(), "cliente desistió"); err != nil || rel.Status != inventory.HoldReleased {
			t.Fatal(rel, err)
		}
		if e.commits("A") != "80/0/5/75" || e.valued(a, "A") != "80/160/2" {
			t.Fatalf("ST-03: %s %s", e.commits("A"), e.valued(a, "A"))
		}
		// ST-09: vence una sola vez, aunque se procese varias veces.
		r3, err := res("10", "PED-4", e.clock.now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := res("1", "PED-5", e.clock.now().Add(-time.Minute)); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("vencimiento pasado: %v", err)
		}
		e.clock.add(2 * time.Hour)
		if n, err := e.stock.ExpireReservations(c, e.admin); err != nil || n != 1 {
			t.Fatalf("expirar: %d %v", n, err)
		}
		if n, _ := e.stock.ExpireReservations(c, e.admin); n != 0 {
			t.Errorf("expirar otra vez: %d", n)
		}
		list, err := e.stock.Reservations(c, e.admin, stock.HoldQuery{Status: inventory.HoldExpired})
		if err != nil || len(list) != 1 || list[0].ID != r3.ID || !list[0].ClosedBy.IsZero() {
			t.Fatalf("vencidas: %+v %v", list, err)
		}
		// Una reserva vencida sin procesar se libera antes de leer el saldo.
		res("10", "PED-6", e.clock.now().Add(time.Minute))
		e.clock.add(time.Hour)
		if e.commits("A") != "80/0/5/75" {
			t.Fatalf("vencida al leer: %s", e.commits("A"))
		}
		// Nadie reserva en un almacén que no ve; el cajero sí reserva en su sucursal.
		e.createUser(e.admin, "caja", "CAJERO", auth.ScopeRef{Company: "principal", Branch: "principal"})
		caja := e.login("caja", userPassword).Principal
		if _, err := e.stock.Reserve(c, caja, stock.ReserveInput{Where: a, Product: "A", Quantity: "1", Reference: "PED-7"}); err != nil {
			t.Errorf("cajero reserva: %v", err)
		}
		if _, err := e.stock.Block(c, caja, stock.BlockInput{Where: a, Product: "A", Quantity: "1", Reason: "damage"}); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("cajero bloquea: %v", err)
		}
		e.verify()
	})
}

// RS-01: dos pedidos piden la última unidad a la vez; RS-03: vencer y
// cancelar a la vez liberan una sola vez.
func TestConcurrentReservations(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		c := ctx(t)
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		e.in(e.here, "A", "3", "1")
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok := 0
		for i := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := e.stock.Reserve(c, e.admin, stock.ReserveInput{Where: e.here, Product: "A", Quantity: "1", Reference: fmt.Sprintf("PED-%d", i),
					ExpiresAt: e.clock.now().Add(time.Minute)})
				if err == nil {
					mu.Lock()
					ok++
					mu.Unlock()
				} else if !errors.Is(err, inventory.ErrInsufficient) {
					t.Errorf("reserva concurrente: %v", err)
				}
			}()
		}
		wg.Wait()
		if ok != 3 || e.commits("A") != "3/3/0/0" {
			t.Fatalf("reservas confirmadas %d, saldo %s", ok, e.commits("A"))
		}
		list, _ := e.stock.Reservations(c, e.admin, stock.HoldQuery{Status: inventory.HoldActive})
		e.clock.add(time.Hour)
		for _, v := range list {
			wg.Add(2)
			go func() { defer wg.Done(); e.stock.ExpireReservations(c, e.admin) }()
			go func() { defer wg.Done(); e.stock.ReleaseReservation(c, e.admin, v.ID.String(), "") }()
		}
		wg.Wait()
		if e.commits("A") != "3/0/0/3" {
			t.Fatalf("RS-03: %s", e.commits("A"))
		}
		e.verify()
	})
}

// 24.5 (BL-01, BL-02) y ST-07/ST-08.
func TestBlocks(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		c := ctx(t)
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		e.in(e.here, "A", "10", "3")
		bl, err := e.stock.Block(c, e.admin, stock.BlockInput{Where: e.here, Product: "A", Quantity: "4", Reason: "damage", Note: "golpe", IdempotencyKey: "bl-1"})
		if err != nil || e.commits("A") != "10/0/4/6" || e.valued(e.here, "A") != "10/30/3" {
			t.Fatalf("ST-07: %v %s %s", err, e.commits("A"), e.valued(e.here, "A"))
		}
		if again, err := e.stock.Block(c, e.admin, stock.BlockInput{Where: e.here, Product: "A", Quantity: "4", Reason: "damage", Note: "golpe", IdempotencyKey: "bl-1"}); err != nil || !again.Replayed {
			t.Fatalf("bloqueo repetido: %v", err)
		}
		if _, err := e.stock.Block(c, e.admin, stock.BlockInput{Where: e.here, Product: "A", Quantity: "1", Reason: "robo"}); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("motivo inválido: %v", err)
		}
		// BL-02: la excepción de negativos no alcanza a lo bloqueado.
		neg := e.overrider("neg")
		e.stock.SetCompanyPolicy(c, e.admin, "principal", true, "100")
		if _, err := e.out(neg, "A", "7", true); !errors.Is(err, inventory.ErrInsufficient) {
			t.Errorf("BL-02: %v", err)
		}
		// BL-01 / ST-08: liberar devuelve el disponible sin compra artificial.
		if _, err := e.stock.CloseBlock(c, e.admin, bl.ID.String(), false, "revisado"); err != nil || e.commits("A") != "10/0/0/10" || e.valued(e.here, "A") != "10/30/3" {
			t.Fatalf("BL-01: %v %s", err, e.commits("A"))
		}
		if _, err := e.stock.CloseBlock(c, e.admin, bl.ID.String(), false, ""); !errors.Is(err, apporg.ErrConflict) {
			t.Errorf("liberar dos veces: %v", err)
		}
		// Merma de lo bloqueado: salida valorada aparte, con su permiso.
		b2, _ := e.stock.Block(c, e.admin, stock.BlockInput{Where: e.here, Product: "A", Quantity: "2", Reason: "damage"})
		e.createUser(e.admin, "enc", "ENCARGADO", auth.ScopeRef{Company: "principal", Branch: "principal"})
		w, err := e.stock.CloseBlock(c, e.login("enc", userPassword).Principal, b2.ID.String(), true, "sin reparación")
		if err != nil || w.Status != inventory.HoldWrittenOff || w.Movement.Cost.Value.String() != "-6" || w.Movement.BlockID != b2.ID || e.commits("A") != "8/0/0/8" {
			t.Fatalf("merma de bloqueo: %+v %v %s", w, err, e.commits("A"))
		}
		list, err := e.stock.Blocks(c, e.admin, stock.HoldQuery{})
		if err != nil || len(list) != 2 {
			t.Fatalf("bloqueos: %v %v", list, err)
		}
		e.verify()
	})
}

// 24.6 (CT-01 a CT-03).
func TestPhysicalCounts(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		c := ctx(t)
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		e.create(appcatalog.NewProduct{SKU: "B", Name: "B"})
		e.create(appcatalog.NewProduct{SKU: "N", Name: "Nuevo"})
		e.in(e.here, "A", "100", "2")
		e.in(e.here, "B", "10", "5")
		e.createUser(e.admin, "alm", "ALMACENISTA", auth.ScopeRef{Company: "principal", Branch: "principal"})
		e.createUser(e.admin, "enc", "ENCARGADO", auth.ScopeRef{Company: "principal", Branch: "principal"})
		alm, enc := e.login("alm", userPassword).Principal, e.login("enc", userPassword).Principal

		ct, err := e.stock.CreateCount(c, alm, e.here, "cierre de mes")
		if err != nil {
			t.Fatal(err)
		}
		id := ct.ID.String()
		for _, l := range [][2]string{{"A", "96"}, {"B", "12"}, {"N", "3"}} {
			if _, err := e.stock.RecordCount(c, alm, id, l[0], l[1], ""); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.stock.RecordCount(c, alm, id, "A", "-1", ""); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("conteo negativo: %v", err)
		}
		// Un producto sin existencia ni costo necesita costo al capturar.
		if _, err := e.stock.RecordCount(c, alm, id, "N", "3", "4"); err != nil {
			t.Fatal(err)
		}
		// CT-02: una venta posterior a la captura se respeta.
		e.mustAdjust("A", inventory.Out, "10", "internal_use")
		if _, err := e.stock.SubmitCount(c, alm, id); err != nil {
			t.Fatal(err)
		}
		if _, err := e.stock.RecordCount(c, alm, id, "A", "1", ""); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("capturar en revisión: %v", err)
		}
		// Quien contó no aprueba; el almacenista no tiene el permiso.
		if _, err := e.stock.DecideCount(c, alm, id, true, ""); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("almacenista aprueba: %v", err)
		}
		v, err := e.stock.DecideCount(c, enc, id, true, "conforme")
		if err != nil || v.Status != inventory.CountApplied || len(v.Movements) != 3 {
			t.Fatalf("aprobar: %+v %v", v, err)
		}
		// CT-01: −4 aparte, sobre el saldo actual (100 − 10 − 4 = 86).
		if got := e.valued(e.here, "A"); got != "86/172/2" {
			t.Fatalf("CT-01/CT-02: %s", got)
		}
		if got := e.valued(e.here, "B"); got != "12/60/5" {
			t.Fatalf("sobrante al CPP: %s", got)
		}
		if got := e.valued(e.here, "N"); got != "3/12/4" {
			t.Fatalf("producto nuevo al costo capturado: %s", got)
		}
		_, kardex, _ := e.stock.Kardex(c, e.admin, e.here, "A", 10, 0)
		if len(kardex) != 3 || kardex[0].Quantity.String() != "100" || kardex[2].Kind != inventory.KindCountOut || kardex[2].CountID != ct.ID {
			t.Fatalf("kardex intacto + ajuste: %+v", kardex)
		}
		if _, err := e.stock.DecideCount(c, enc, id, false, ""); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("decidir dos veces: %v", err)
		}

		// Mismo usuario cuenta y aprueba: no.
		own, _ := e.stock.CreateCount(c, enc, e.here, "")
		e.stock.RecordCount(c, enc, own.ID.String(), "A", "80", "")
		e.stock.SubmitCount(c, enc, own.ID.String())
		if _, err := e.stock.DecideCount(c, enc, own.ID.String(), true, ""); !errors.Is(err, inventory.ErrSameCounter) {
			t.Errorf("aprobar lo propio: %v", err)
		}
		if r, err := e.stock.DecideCount(c, e.admin, own.ID.String(), false, "recontar"); err != nil || r.Status != inventory.CountRejected || len(r.Movements) != 0 {
			t.Fatalf("rechazar: %+v %v", r, err)
		}
		if got := e.valued(e.here, "A"); got != "86/172/2" {
			t.Fatalf("un rechazo no ajusta: %s", got)
		}

		// CT-03: contar 0 sobre un déficit de −3 regulariza sin compra ficticia.
		neg := e.overrider("neg")
		e.stock.SetCompanyPolicy(c, e.admin, "principal", true, "5")
		if _, err := e.out(neg, "B", "15", true); err != nil {
			t.Fatal(err)
		}
		zero, _ := e.stock.CreateCount(c, alm, e.here, "")
		e.stock.RecordCount(c, alm, zero.ID.String(), "B", "0", "")
		e.stock.SubmitCount(c, alm, zero.ID.String())
		z, err := e.stock.DecideCount(c, enc, zero.ID.String(), true, "regulariza déficit")
		if err != nil || len(z.Movements) != 1 || z.Movements[0].Kind != inventory.KindCountIn || z.Movements[0].Reason != inventory.ReasonCount {
			t.Fatalf("CT-03: %+v %v", z, err)
		}
		if got := e.valued(e.here, "B"); got != "0/0/sin valor" {
			t.Fatalf("CT-03 saldo: %s", got)
		}
		if list, err := e.stock.Counts(c, e.admin, nil, ""); err != nil || len(list) != 3 {
			t.Fatalf("conteos: %d %v", len(list), err)
		}
		e.verify()
	})
}

// La base protege reservas, bloqueos, conteos y políticas aunque Go se
// saltara.
func TestHoldsAreGuardedInTheDatabase(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newStockEnv(t, cfg)
		c := ctx(t)
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		e.in(e.here, "A", "5", "1")
		r, _ := e.stock.Reserve(c, e.admin, stock.ReserveInput{Where: e.here, Product: "A", Quantity: "2", Reference: "P"})
		e.stock.ReleaseReservation(c, e.admin, r.ID.String(), "")
		ct, _ := e.stock.CreateCount(c, e.admin, e.here, "")
		e.stock.RecordCount(c, e.admin, ct.ID.String(), "A", "5", "")
		e.stock.SubmitCount(c, e.admin, ct.ID.String())
		pv, _ := e.stock.SetCompanyPolicy(c, e.admin, "principal", true, "1")
		pp := e.store.Dialect.Placeholder
		for name, q := range map[string][2]string{
			"reabrir reserva":         {"UPDATE inventory_reservations SET status = 'active', closed_by = NULL, closed_at_utc = NULL WHERE id = " + pp(1), r.ID.String()},
			"borrar reserva":          {"DELETE FROM inventory_reservations WHERE id = " + pp(1), r.ID.String()},
			"conteo hacia atrás":      {"UPDATE inventory_counts SET status = 'draft', submitted_by = NULL, submitted_at_utc = NULL WHERE id = " + pp(1), ct.ID.String()},
			"línea en revisión":       {"UPDATE inventory_count_lines SET counted = '1' WHERE count_id = " + pp(1), ct.ID.String()},
			"editar política":         {"UPDATE inventory_policy_changes SET negative_limit = '100' WHERE id = " + pp(1), pv.Current.ID.String()},
			"reservado negativo":      {"UPDATE inventory_balances SET reserved = '-1' WHERE product_id = " + pp(1), r.ProductID.String()},
			"compromiso cambia valor": {"UPDATE inventory_balances SET reserved = '1', on_hand = '6' WHERE product_id = " + pp(1), r.ProductID.String()},
		} {
			if _, err := e.store.DB.ExecContext(c, q[0], q[1]); err == nil {
				t.Errorf("%s: la base debía rechazarlo", name)
			}
		}
		e.verify()
		// Lo reservado alterado sin reserva que lo respalde: verify lo ve.
		if _, err := e.store.DB.ExecContext(c, "UPDATE inventory_balances SET reserved = '3' WHERE product_id = "+pp(1), r.ProductID.String()); err != nil {
			t.Fatal(err)
		}
		if rep, err := e.stock.Verify(c, e.admin); err != nil || len(rep.Problems) != 1 {
			t.Fatalf("reservado sin respaldo: %+v %v", rep, err)
		}
	})
}

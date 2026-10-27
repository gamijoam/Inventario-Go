package persistence_test

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/adapters/persistence"
	"github.com/gamijoam/inventario-go/internal/application/auth"
	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/platform/config"
	"github.com/gamijoam/inventario-go/internal/platform/migration"
	"github.com/gamijoam/inventario-go/migrations"
)

type catalogEnv struct {
	*authEnv
	cat   *appcatalog.Service
	admin identity.Principal
}

func newCatalogEnv(t *testing.T, cfg config.Database) *catalogEnv {
	t.Helper()
	e := newAuthEnv(t, cfg)
	return &catalogEnv{authEnv: e, cat: appcatalog.NewService(e.store.CatalogStore(), e.clock.now),
		admin: e.login("admin", adminPassword).Principal}
}

func (e *catalogEnv) create(in appcatalog.NewProduct) appcatalog.ProductView {
	e.t.Helper()
	v, err := e.cat.CreateProduct(ctx(e.t), e.admin, e.setup.Organization, in)
	if err != nil {
		e.t.Fatalf("CreateProduct %+v: %v", in, err)
	}
	return v
}

func TestProductReferenceIsSeparateFromID(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newCatalogEnv(t, cfg)
		c := ctx(t)
		harina := e.create(appcatalog.NewProduct{Name: "Harina de maíz", Barcodes: []string{"7591234567894"}})
		queso := e.create(appcatalog.NewProduct{Name: "Queso blanco", UnitCode: "kg", SKU: "que-01"})
		if harina.SKU.String() != "P-000001" || harina.Unit.Code != "UND" || queso.SKU.String() != "QUE-01" || queso.Unit.Scale != 3 {
			t.Fatalf("harina=%+v queso=%+v", harina, queso)
		}
		// El SKU cambia; el ID y los códigos siguen identificando el producto.
		newSKU := "HAR-001"
		v, err := e.cat.UpdateProduct(c, e.admin, e.setup.Organization, "P-000001", appcatalog.Changes{SKU: &newSKU})
		if err != nil || v.ID != harina.ID || v.Revision != 2 {
			t.Fatalf("update = %+v, %v", v, err)
		}
		for _, ref := range []string{"har-001", "7591234567894", "07591234567894", harina.ID.String()} {
			if got, err := e.cat.ShowProduct(c, e.admin, ref); err != nil || got.ID != harina.ID {
				t.Errorf("ref %s: %+v %v", ref, got, err)
			}
		}
		if _, err := e.cat.ShowProduct(c, e.admin, "P-000001"); !errors.Is(err, apporg.ErrNotFound) {
			t.Errorf("el SKU anterior ya no existe: %v", err)
		}
		// El siguiente automático no reutiliza P-000001.
		if p := e.create(appcatalog.NewProduct{Name: "Arroz"}); p.SKU.String() != "P-000002" {
			t.Errorf("SKU automático = %s", p.SKU)
		}
	})
}

func TestCatalogUniquenessAndValidation(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newCatalogEnv(t, cfg)
		c, o := ctx(t), e.setup.Organization
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A", Barcodes: []string{"012345678905"}})
		cases := map[string]struct {
			in   appcatalog.NewProduct
			want error
		}{
			"SKU repetido":              {appcatalog.NewProduct{SKU: "a", Name: "Otro"}, apporg.ErrConflict},
			"mismo GTIN como EAN-13":    {appcatalog.NewProduct{SKU: "B", Name: "B", Barcodes: []string{"0012345678905"}}, apporg.ErrConflict},
			"dígito verificador errado": {appcatalog.NewProduct{SKU: "C", Name: "C", Barcodes: []string{"0012345678900"}}, catalog.ErrInvalid},
			"unidad inexistente":        {appcatalog.NewProduct{SKU: "D", Name: "D", UnitCode: "LB"}, apporg.ErrNotFound},
			"nombre vacío":              {appcatalog.NewProduct{SKU: "E", Name: "  "}, nil},
		}
		for name, tc := range cases {
			_, err := e.cat.CreateProduct(c, e.admin, o, tc.in)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Errorf("%s: %v", name, err)
			}
		}
		// Ningún intento fallido dejó filas a medias.
		if n := count(t, e.store, "products"); n != 1 {
			t.Errorf("products = %d", n)
		}
		if _, err := e.cat.CreateUnit(c, e.admin, o, "kg", "Otro kilo", 2); !errors.Is(err, apporg.ErrConflict) {
			t.Errorf("unidad repetida: %v", err)
		}
		lb, err := e.cat.CreateUnit(c, e.admin, o, "lb", "Libra", 3)
		if err != nil || lb.Code != "LB" {
			t.Fatalf("CreateUnit: %+v %v", lb, err)
		}
	})
}

func TestProductUpdatesDetectConcurrentEditsAndNeverDelete(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newCatalogEnv(t, cfg)
		c, o := ctx(t), e.setup.Organization
		p := e.create(appcatalog.NewProduct{SKU: "A", Name: "Aceite", Barcodes: []string{"INT-1"}})
		name := "Aceite de girasol"
		if _, err := e.cat.UpdateProduct(c, e.admin, o, "A", appcatalog.Changes{Name: &name, ExpectedRevision: 1}); err != nil {
			t.Fatal(err)
		}
		other := "Aceite de oliva"
		if _, err := e.cat.UpdateProduct(c, e.admin, o, "A", appcatalog.Changes{Name: &other, ExpectedRevision: 1}); !errors.Is(err, appcatalog.ErrStale) {
			t.Fatalf("edición sobre revisión vieja: %v", err)
		}
		off := false
		v, err := e.cat.UpdateProduct(c, e.admin, o, "A", appcatalog.Changes{Active: &off, AddBarcodes: []string{"INT-2"}, RemoveBarcodes: []string{"INT-1"}})
		if err != nil || v.Status != catalog.StatusInactive || len(v.Barcodes) != 1 || v.Barcodes[0].String() != "INT-2" || v.Revision != 3 {
			t.Fatalf("inactivar: %+v %v", v, err)
		}
		if _, err := e.cat.UpdateProduct(c, e.admin, o, "A", appcatalog.Changes{}); !errors.Is(err, catalog.ErrInvalid) {
			t.Errorf("sin cambios: %v", err)
		}
		// Inactivo: fuera del listado por defecto, pero su código sigue reservado.
		if list, _ := e.cat.ListProducts(c, e.admin, appcatalog.Filter{Limit: 50}); len(list) != 0 {
			t.Errorf("listado activo = %v", list)
		}
		if list, _ := e.cat.ListProducts(c, e.admin, appcatalog.Filter{Limit: 50, IncludeInactive: true}); len(list) != 1 || list[0].ID != p.ID {
			t.Errorf("con inactivos = %v", list)
		}
		if _, err := e.cat.CreateProduct(c, e.admin, o, appcatalog.NewProduct{SKU: "B", Name: "B", Barcodes: []string{"INT-2"}}); !errors.Is(err, apporg.ErrConflict) {
			t.Errorf("código de un inactivo: %v", err)
		}
		// La base no permite borrar productos con códigos ni cambiar unidad o escala.
		pp := e.store.Dialect.Placeholder
		for name, q := range map[string]string{
			"borrar producto":   "DELETE FROM products WHERE id = " + pp(1),
			"cambiar la unidad": "UPDATE products SET unit_id = (SELECT id FROM units WHERE code = 'KG') WHERE id = " + pp(1),
		} {
			if _, err := e.store.DB.ExecContext(c, q, p.ID.String()); err == nil {
				t.Errorf("%s: la base debía rechazarlo", name)
			}
		}
		if _, err := e.store.DB.ExecContext(c, "UPDATE units SET scale = 2 WHERE code = 'UND'"); err == nil || !strings.Contains(err.Error(), "escala") {
			t.Errorf("cambiar escala: %v", err)
		}
		// Control positivo: el nombre de una unidad sí se puede cambiar.
		if _, err := e.store.DB.ExecContext(c, "UPDATE units SET name = 'Unidades' WHERE code = 'UND'"); err != nil {
			t.Errorf("control: %v", err)
		}
	})
}

func TestProductSearch(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newCatalogEnv(t, cfg)
		c := ctx(t)
		e.create(appcatalog.NewProduct{SKU: "CAF-01", Name: "Café Ñandú molido", Barcodes: []string{"7591234567894"}})
		e.create(appcatalog.NewProduct{SKU: "AZU-01", Name: "Azúcar refinada"})
		e.create(appcatalog.NewProduct{SKU: "X_1", Name: "Producto 100% real"})
		search := func(s string) []string {
			list, err := e.cat.ListProducts(c, e.admin, appcatalog.Filter{Search: s, Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, p := range list {
				out = append(out, p.SKU.String())
			}
			return out
		}
		for q, want := range map[string][]string{
			"nandu":          {"CAF-01"},
			"CAFÉ":           {"CAF-01"},
			"azu":            {"AZU-01"}, // prefijo de SKU y de nombre
			"07591234567894": {"CAF-01"}, // código de barras normalizado
			"100%":           {"X_1"},    // el % es literal
			"x_":             {"X_1"},    // el _ es literal
			"inexistente":    nil,
		} {
			if got := search(q); !reflect.DeepEqual(got, want) {
				t.Errorf("buscar %q = %v, se esperaba %v", q, got, want)
			}
		}
		if all := search(""); len(all) != 3 || all[0] != "AZU-01" {
			t.Errorf("orden por nombre = %v", all)
		}
		if page, _ := e.cat.ListProducts(c, e.admin, appcatalog.Filter{Limit: 1, Offset: 1}); len(page) != 1 || page[0].SKU.String() != "CAF-01" {
			t.Errorf("paginación = %v", page)
		}
		if _, err := e.cat.ListProducts(c, e.admin, appcatalog.Filter{Limit: 0}); !errors.Is(err, catalog.ErrInvalid) {
			t.Errorf("límite 0: %v", err)
		}
	})
}

func TestCatalogPermissions(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newCatalogEnv(t, cfg)
		c, o := ctx(t), e.setup.Organization
		e.create(appcatalog.NewProduct{SKU: "A", Name: "A"})
		e.createUser(e.admin, "cajero", "CAJERO", auth.ScopeRef{Company: "principal"})
		e.createUser(e.admin, "jefe", "ADMIN_EMPRESA", auth.ScopeRef{Company: "principal"})
		cajero := e.login("cajero", userPassword).Principal
		jefe := e.login("jefe", userPassword).Principal
		if _, err := e.cat.ShowProduct(c, cajero, "A"); err != nil {
			t.Errorf("el cajero consulta el catálogo: %v", err)
		}
		for name, p := range map[string]identity.Principal{"cajero": cajero, "admin de empresa": jefe} {
			if _, err := e.cat.CreateProduct(c, p, o, appcatalog.NewProduct{Name: "X"}); !errors.Is(err, identity.ErrDenied) {
				t.Errorf("%s crea productos: %v", name, err)
			}
			if _, err := e.cat.CreateUnit(c, p, o, "LB", "Libra", 3); !errors.Is(err, identity.ErrDenied) {
				t.Errorf("%s crea unidades: %v", name, err)
			}
		}
		nobody := identity.Principal{UserID: id.MustNew(), OrganizationID: o.ID}
		if _, err := e.cat.ListProducts(c, nobody, appcatalog.Filter{Limit: 10}); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("sin permisos: %v", err)
		}
	})
}

// Otra organización con los mismos códigos no ve ni choca con este catálogo.
func TestCatalogIsIsolatedByOrganization(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newCatalogEnv(t, cfg)
		c := ctx(t)
		mine := e.create(appcatalog.NewProduct{SKU: "A", Name: "Mío", Barcodes: []string{"INT-1"}})
		// Segunda organización en la misma base, con su administrador.
		b, err := orgAndUnits(t, e.store, "beto")
		if err != nil {
			t.Fatal(err)
		}
		bAdmin := identity.Principal{UserID: id.MustNew(), OrganizationID: b.ID,
			Grants: []identity.Grant{{Scope: b.Scope(), Permissions: identity.Templates()[0].Permissions}}}
		theirs, err := e.cat.CreateProduct(c, bAdmin, b, appcatalog.NewProduct{SKU: "A", Name: "Suyo", Barcodes: []string{"INT-1"}})
		if err != nil {
			t.Fatalf("mismos SKU y código en otra organización: %v", err)
		}
		if _, err := e.cat.ShowProduct(c, bAdmin, mine.ID.String()); !errors.Is(err, apporg.ErrNotFound) {
			t.Errorf("B ve el producto de A por ID: %v", err)
		}
		if got, _ := e.cat.ShowProduct(c, e.admin, "INT-1"); got.ID != mine.ID {
			t.Errorf("A resuelve INT-1 a %v", got.ID)
		}
		if _, err := e.cat.CreateProduct(c, e.admin, b, appcatalog.NewProduct{Name: "Intruso"}); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("A crea en el catálogo de B: %v", err)
		}
		// La base rechaza un producto de A con la unidad de B.
		var unitB string
		q := "SELECT CAST(id AS TEXT) FROM units WHERE code = 'UND' AND organization_id = " + e.store.Dialect.Placeholder(1)
		if err := e.store.DB.QueryRowContext(c, q, b.ID.String()).Scan(&unitB); err != nil {
			t.Fatal(err)
		}
		pp := e.store.Dialect.Placeholder
		ts := e.store.Dialect.Timestamp(e.clock.now())
		ins := "INSERT INTO products (organization_id, id, sku, name, name_key, unit_id, status, revision, created_at_utc, updated_at_utc) VALUES (" +
			pp(1) + ", " + pp(2) + ", 'Z', 'Z', 'z', " + pp(3) + ", 'active', 1, " + pp(4) + ", " + pp(5) + ")"
		if _, err := e.store.DB.ExecContext(c, ins, e.setup.Organization.ID.String(), id.MustNew().String(), unitB, ts, ts); err == nil || !isForeignKeyError(err) {
			t.Errorf("unidad de otra organización: %v", err)
		}
		if theirs.ID == mine.ID {
			t.Error("IDs repetidos")
		}
	})
}

// orgAndUnits inserta otra organización con sus unidades base.
func orgAndUnits(t *testing.T, s *persistence.Store, code string) (org.Organization, error) {
	t.Helper()
	o, err := org.NewOrganization(code, code, time.Now().UTC())
	if err != nil {
		return o, err
	}
	if err := s.OrgStore().InTx(ctx(t), func(r apporg.Repository) error { return r.InsertOrganization(ctx(t), o) }); err != nil {
		return o, err
	}
	return o, s.CatalogStore().InTx(ctx(t), func(r appcatalog.Repository) error {
		return appcatalog.SeedUnits(ctx(t), r, o, time.Now().UTC())
	})
}

// La migración 0004 siembra en organizaciones existentes lo mismo que
// 'setup' en una nueva: unidades base y permisos de catálogo en plantillas.
func TestMigrationSeedsTheSameAsSetup(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newAuthEnv(t, cfg)
		before := catalogSnapshot(t, e.store)

		migs, err := migration.Load(migrations.FS, cfg.Driver)
		if err != nil {
			t.Fatal(err)
		}
		runner, err := e.store.Runner()
		if err != nil {
			t.Fatal(err)
		}
		// Revertir solo la 0004 y volver a aplicarla sobre la organización ya creada.
		if len(migs) < 4 {
			t.Fatal("se esperaba la migración 0004")
		}
		if _, err := runner.Down(ctx(t), len(migs)-3); err != nil {
			t.Fatalf("Down: %v", err)
		}
		if n := countPerms(t, e.store, "catalog.%"); n != 0 {
			t.Fatalf("la bajada debe quitar los permisos de catálogo, quedan %d", n)
		}
		if _, err := runner.Up(ctx(t)); err != nil {
			t.Fatalf("Up: %v", err)
		}
		after := catalogSnapshot(t, e.store)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("setup y migración difieren:\nsetup:     %v\nmigración: %v", before, after)
		}
	})
}

func catalogSnapshot(t *testing.T, s *persistence.Store) []string {
	t.Helper()
	var out []string
	rows, err := s.DB.QueryContext(ctx(t), "SELECT code || ':' || name || ':' || CAST(scale AS TEXT) FROM units")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var v string
		_ = rows.Scan(&v)
		out = append(out, "unit "+v)
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx(t), "SELECT r.code || ':' || p.permission FROM role_permissions p JOIN roles r ON r.id = p.role_id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var v string
		_ = rows.Scan(&v)
		out = append(out, "perm "+v)
	}
	rows.Close()
	sort.Strings(out)
	return out
}

func countPerms(t *testing.T, s *persistence.Store, like string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRowContext(ctx(t), "SELECT COUNT(*) FROM role_permissions WHERE permission LIKE "+s.Dialect.Placeholder(1), like).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

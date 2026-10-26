package persistence_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/adapters/persistence"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/platform/config"
)

// isForeignKeyError reconoce la violación de FK de cada motor.
func isForeignKeyError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "foreign key") || strings.Contains(msg, "23503")
}

func orgInput(code string) apporg.BootstrapInput {
	return apporg.BootstrapInput{
		OrganizationCode: code, OrganizationName: "Bodega " + code,
		CompanyCode: "principal", CompanyLegalName: "Bodega " + code + ", C.A.", RIF: "J-12345678-9",
		BranchCode: "principal", BranchName: "Principal",
		WarehouseCode: "principal", WarehouseName: "Principal",
	}
}

// installed devuelve un Store migrado con la instalación registrada.
func installed(t *testing.T, cfg config.Database) (*persistence.Store, *apporg.Service) {
	t.Helper()
	s, _ := migrated(t, cfg)
	if _, _, err := s.EnsureInstallation(ctx(t)); err != nil {
		t.Fatal(err)
	}
	return s, apporg.NewService(s.OrgStore(), func() time.Time { return time.Now().UTC() })
}

func count(t *testing.T, s *persistence.Store, table string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRowContext(ctx(t), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBootstrapPersistsHierarchyAndBinding(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		s, svc := installed(t, cfg)
		res, err := svc.Bootstrap(ctx(t), orgInput("ana"))
		if err != nil {
			t.Fatal(err)
		}
		cur, err := svc.Current(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		if cur.Organization.ID != res.Organization.ID || cur.Company.RIF.String() != "J-12345678-9" ||
			cur.Branch.ID != res.Branch.ID || cur.Organization.Name.String() != "Bodega ana" {
			t.Fatalf("Current = %+v", cur)
		}
		if !cur.Company.CreatedAt.Equal(res.Company.CreatedAt.Truncate(time.Microsecond)) {
			t.Errorf("CreatedAt = %v, se esperaba %v", cur.Company.CreatedAt, res.Company.CreatedAt)
		}
		if _, err := svc.Bootstrap(ctx(t), orgInput("otra")); !errors.Is(err, apporg.ErrAlreadyBootstrapped) {
			t.Fatalf("segundo setup: %v", err)
		}
		if count(t, s, "organizations") != 1 || count(t, s, "warehouses") != 1 {
			t.Fatalf("el segundo setup no debe dejar datos")
		}
	})
}

func TestBootstrapRollsBackEverythingOnFailure(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		s, _ := migrated(t, cfg)
		// Sin instalación registrada, Bind falla después de insertar las
		// cuatro entidades: la transacción debe deshacerlas todas.
		svc := apporg.NewService(s.OrgStore(), func() time.Time { return time.Now().UTC() })
		if _, err := svc.Bootstrap(ctx(t), orgInput("ana")); err == nil {
			t.Fatal("se esperaba error")
		}
		for _, table := range []string{"organizations", "companies", "branches", "warehouses", "installation_binding"} {
			if n := count(t, s, table); n != 0 {
				t.Errorf("%s tiene %d filas tras el fallo", table, n)
			}
		}
	})
}

func TestCodesAndRIFAreUniqueWithinScope(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		_, svc := installed(t, cfg)
		res, err := svc.Bootstrap(ctx(t), orgInput("ana"))
		if err != nil {
			t.Fatal(err)
		}
		actor := res.Organization.Scope()

		if _, err := svc.CreateCompany(ctx(t), actor, "PRINCIPAL", "Duplicada", ""); !errors.Is(err, apporg.ErrConflict) {
			t.Errorf("código repetido: %v", err)
		}
		if _, err := svc.CreateCompany(ctx(t), actor, "dos", "Dos", "J-12345678-9"); !errors.Is(err, apporg.ErrConflict) {
			t.Errorf("RIF repetido: %v", err)
		}
		// Varias empresas sin RIF: el índice único es parcial.
		for _, code := range []string{"sin-rif-1", "sin-rif-2"} {
			if _, err := svc.CreateCompany(ctx(t), actor, code, "Sin RIF", ""); err != nil {
				t.Errorf("%s: %v", code, err)
			}
		}
		// El mismo código de sucursal y almacén en otra empresa/sucursal es válido.
		if _, err := svc.CreateBranch(ctx(t), actor, "sin-rif-1", "principal", "Principal"); err != nil {
			t.Errorf("sucursal PRINCIPAL en otra empresa: %v", err)
		}
		if _, err := svc.CreateWarehouse(ctx(t), actor, "sin-rif-1", "principal", "principal", "Principal"); err != nil {
			t.Errorf("almacén PRINCIPAL en otra sucursal: %v", err)
		}
		if _, err := svc.CreateWarehouse(ctx(t), actor, "principal", "principal", "principal", "Otro"); !errors.Is(err, apporg.ErrConflict) {
			t.Errorf("almacén repetido en la misma sucursal: %v", err)
		}

		companies, _ := svc.ListCompanies(ctx(t), actor)
		branches, _ := svc.ListBranches(ctx(t), actor, "")
		whs, _ := svc.ListWarehouses(ctx(t), actor, "", "")
		if len(companies) != 3 || len(branches) != 2 || len(whs) != 2 {
			t.Fatalf("listados: %d, %d, %d", len(companies), len(branches), len(whs))
		}
		if companies[0].Code.String() != "PRINCIPAL" || companies[1].Code.String() != "SIN-RIF-1" {
			t.Errorf("orden por código: %v, %v", companies[0].Code, companies[1].Code)
		}
	})
}

// Dos organizaciones en la misma base (escenario de nube compartida).
func TestOrganizationsAreIsolated(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		s, svc := installed(t, cfg)
		a, err := svc.Bootstrap(ctx(t), orgInput("ana"))
		if err != nil {
			t.Fatal(err)
		}
		// Organización B con los mismos códigos y RIF, insertada sin binding.
		now := time.Now().UTC()
		b, _ := org.NewOrganization("beto", "Beto", now)
		rif, _ := org.ParseRIF("J-12345678-9")
		bc, _ := org.NewCompany(b, "principal", "Beto, C.A.", rif, now)
		bb, _ := org.NewBranch(bc, "principal", "Principal", now)
		err = s.OrgStore().InTx(ctx(t), func(r apporg.Repository) error {
			if err := r.InsertOrganization(ctx(t), b); err != nil {
				return err
			}
			if err := r.InsertCompany(ctx(t), bc); err != nil {
				return err
			}
			return r.InsertBranch(ctx(t), bb)
		})
		if err != nil {
			t.Fatalf("los mismos códigos y RIF en otra organización deben aceptarse: %v", err)
		}

		actorA, actorB := a.Organization.Scope(), b.Scope()
		listA, _ := svc.ListCompanies(ctx(t), actorA)
		listB, _ := svc.ListCompanies(ctx(t), actorB)
		if len(listA) != 1 || listA[0].ID != a.Company.ID || len(listB) != 1 || listB[0].ID != bc.ID {
			t.Fatalf("listas cruzadas: A=%v B=%v", listA, listB)
		}
		// Crear en "PRINCIPAL" desde A afecta a la empresa de A, nunca a la de B.
		w, err := svc.CreateWarehouse(ctx(t), actorA, "principal", "principal", "alm-2", "Almacén 2")
		if err != nil || w.CompanyID != a.Company.ID {
			t.Fatalf("CreateWarehouse = %+v, %v", w, err)
		}
		if whsB, _ := svc.ListWarehouses(ctx(t), actorB, "", ""); len(whsB) != 0 {
			t.Fatalf("B ve almacenes de A: %v", whsB)
		}
	})
}

// Inserciones SQL directas que intentan mezclar organizaciones: las claves
// foráneas compuestas deben rechazarlas aunque los IDs existan.
func TestCompositeForeignKeysRejectCrossScopeReferences(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		s, svc := installed(t, cfg)
		a, err := svc.Bootstrap(ctx(t), orgInput("ana"))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		b, _ := org.NewOrganization("beto", "Beto", now)
		bc, _ := org.NewCompany(b, "principal", "Beto, C.A.", org.RIF{}, now)
		if err := s.OrgStore().InTx(ctx(t), func(r apporg.Repository) error {
			if err := r.InsertOrganization(ctx(t), b); err != nil {
				return err
			}
			return r.InsertCompany(ctx(t), bc)
		}); err != nil {
			t.Fatal(err)
		}

		p := s.Dialect.Placeholder
		ts := s.Dialect.Timestamp(now)
		// Marcadores distintos para cada valor: en SQLite "?" no reutiliza
		// posiciones como "$4" en PostgreSQL.
		insBranch := "INSERT INTO branches (organization_id, company_id, id, code, name, status, created_at_utc, updated_at_utc) VALUES (" +
			p(1) + ", " + p(2) + ", " + p(3) + ", 'X', 'X', 'active', " + p(4) + ", " + p(5) + ")"
		insWarehouse := "INSERT INTO warehouses (organization_id, company_id, branch_id, id, code, name, status, created_at_utc, updated_at_utc) VALUES (" +
			p(1) + ", " + p(2) + ", " + p(3) + ", " + p(4) + ", 'X', 'X', 'active', " + p(5) + ", " + p(6) + ")"

		hostile := map[string][]any{
			// Sucursal que dice ser de A pero apunta a la empresa de B.
			"sucursal A → empresa B": {insBranch, a.Organization.ID.String(), bc.ID.String(), id.MustNew().String(), ts, ts},
			// Almacén de B colgado de la sucursal de A.
			"almacén B → sucursal A": {insWarehouse, b.ID.String(), bc.ID.String(), a.Branch.ID.String(), id.MustNew().String(), ts, ts},
			// Almacén con empresa correcta pero sucursal de otra empresa.
			"almacén con sucursal ajena": {insWarehouse, a.Organization.ID.String(), a.Company.ID.String(), id.MustNew().String(), id.MustNew().String(), ts, ts},
		}
		for name, q := range hostile {
			_, err := s.DB.ExecContext(ctx(t), q[0].(string), q[1:]...)
			if err == nil {
				t.Errorf("%s: la base debía rechazarlo", name)
			} else if !isForeignKeyError(err) {
				t.Errorf("%s: se esperaba violación de FK, obtuvo %v", name, err)
			}
		}
		// Control positivo: las mismas sentencias con referencias coherentes
		// funcionan, así que los rechazos de arriba se deben a las FKs.
		branchID := id.MustNew().String()
		if _, err := s.DB.ExecContext(ctx(t), insBranch, a.Organization.ID.String(), a.Company.ID.String(), branchID, ts, ts); err != nil {
			t.Fatalf("control de sucursal: %v", err)
		}
		if _, err := s.DB.ExecContext(ctx(t), insWarehouse, a.Organization.ID.String(), a.Company.ID.String(), branchID, id.MustNew().String(), ts, ts); err != nil {
			t.Fatalf("control de almacén: %v", err)
		}
		if _, err := s.DB.ExecContext(ctx(t), "DELETE FROM companies WHERE id = "+p(1), a.Company.ID.String()); err == nil {
			t.Errorf("borrar una empresa con sucursales debía rechazarse (ON DELETE RESTRICT)")
		}
		if _, err := s.DB.ExecContext(ctx(t), "UPDATE installation_binding SET company_id = "+p(1), bc.ID.String()); err == nil {
			t.Errorf("reasociar la instalación a la empresa de B con la sucursal de A debía rechazarse")
		}
	})
}

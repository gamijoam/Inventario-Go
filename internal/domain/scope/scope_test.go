package scope_test

import (
	"errors"
	"testing"

	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

var (
	org       = id.MustNew()
	otherOrg  = id.MustNew()
	company   = id.MustNew()
	company2  = id.MustNew()
	branch    = id.MustNew()
	branch2   = id.MustNew()
	warehouse = id.MustNew()
)

func mustScope(t *testing.T, o, c, b, w id.ID) scope.BusinessScope {
	t.Helper()
	s, err := scope.New(o, c, b, w)
	if err != nil {
		t.Fatalf("scope.New: %v", err)
	}
	return s
}

func TestNewBuildsEachLevel(t *testing.T) {
	cases := []struct {
		c, b, w id.ID
		want    scope.Level
	}{
		{id.ID{}, id.ID{}, id.ID{}, scope.LevelOrganization},
		{company, id.ID{}, id.ID{}, scope.LevelCompany},
		{company, branch, id.ID{}, scope.LevelBranch},
		{company, branch, warehouse, scope.LevelWarehouse},
	}
	for _, c := range cases {
		s := mustScope(t, org, c.c, c.b, c.w)
		if s.Level() != c.want {
			t.Errorf("Level() = %v, se esperaba %v", s.Level(), c.want)
		}
		if s.Organization() != org || s.Company() != c.c || s.Branch() != c.b || s.Warehouse() != c.w {
			t.Errorf("componentes alterados: %v", s)
		}
	}
}

func TestNewRejectsGapsInHierarchy(t *testing.T) {
	cases := []struct {
		name       string
		o, c, b, w id.ID
	}{
		{"sin organización", id.ID{}, company, branch, warehouse},
		{"vacío", id.ID{}, id.ID{}, id.ID{}, id.ID{}},
		{"sucursal sin empresa", org, id.ID{}, branch, id.ID{}},
		{"almacén sin sucursal", org, company, id.ID{}, warehouse},
		{"almacén sin empresa", org, id.ID{}, id.ID{}, warehouse},
	}
	for _, c := range cases {
		if _, err := scope.New(c.o, c.c, c.b, c.w); !errors.Is(err, scope.ErrInvalidScope) {
			t.Errorf("%s: se esperaba ErrInvalidScope, obtuvo %v", c.name, err)
		}
	}
}

func TestNewRejectsRepeatedIDsAcrossLevels(t *testing.T) {
	if _, err := scope.New(org, org, id.ID{}, id.ID{}); !errors.Is(err, scope.ErrInvalidScope) {
		t.Fatalf("un mismo ID en dos niveles debía fallar, obtuvo %v", err)
	}
	if _, err := scope.New(org, company, branch, branch); !errors.Is(err, scope.ErrInvalidScope) {
		t.Fatalf("un mismo ID en dos niveles debía fallar, obtuvo %v", err)
	}
}

func TestContainsGrantsDescendantsOnly(t *testing.T) {
	orgScope := mustScope(t, org, id.ID{}, id.ID{}, id.ID{})
	companyScope := mustScope(t, org, company, id.ID{}, id.ID{})
	branchScope := mustScope(t, org, company, branch, id.ID{})
	warehouseScope := mustScope(t, org, company, branch, warehouse)

	allowed := []struct {
		name        string
		grant, used scope.BusinessScope
	}{
		{"organización contiene almacén", orgScope, warehouseScope},
		{"empresa contiene sucursal", companyScope, branchScope},
		{"sucursal contiene almacén", branchScope, warehouseScope},
		{"un ámbito se contiene a sí mismo", branchScope, branchScope},
	}
	for _, c := range allowed {
		if !c.grant.Contains(c.used) {
			t.Errorf("%s: Contains = false", c.name)
		}
	}

	denied := []struct {
		name        string
		grant, used scope.BusinessScope
	}{
		{"almacén no contiene su sucursal", warehouseScope, branchScope},
		{"empresa no contiene su organización", companyScope, orgScope},
		{"otra organización", mustScope(t, otherOrg, id.ID{}, id.ID{}, id.ID{}), warehouseScope},
		{"otra empresa de la misma organización", mustScope(t, org, company2, id.ID{}, id.ID{}), branchScope},
		{"otra sucursal de la misma empresa", mustScope(t, org, company, branch2, id.ID{}), warehouseScope},
		{"ámbito vacío no concede nada", scope.BusinessScope{}, warehouseScope},
		{"nada contiene un ámbito vacío", orgScope, scope.BusinessScope{}},
	}
	for _, c := range denied {
		if c.grant.Contains(c.used) {
			t.Errorf("%s: Contains = true", c.name)
		}
	}
}

func TestRequireLevel(t *testing.T) {
	branchScope := mustScope(t, org, company, branch, id.ID{})
	if err := branchScope.Require(scope.LevelBranch); err != nil {
		t.Errorf("sucursal cumple LevelBranch: %v", err)
	}
	if err := branchScope.Require(scope.LevelCompany); err != nil {
		t.Errorf("sucursal cumple LevelCompany: %v", err)
	}
	if err := branchScope.Require(scope.LevelWarehouse); !errors.Is(err, scope.ErrScopeTooBroad) {
		t.Errorf("un movimiento de stock exige almacén; se esperaba ErrScopeTooBroad, obtuvo %v", err)
	}
	if err := (scope.BusinessScope{}).Require(scope.LevelOrganization); !errors.Is(err, scope.ErrInvalidScope) {
		t.Errorf("ámbito vacío: se esperaba ErrInvalidScope, obtuvo %v", err)
	}
}

func TestStringIsReadable(t *testing.T) {
	s := mustScope(t, org, company, id.ID{}, id.ID{})
	want := "org=" + org.String() + " company=" + company.String()
	if s.String() != want {
		t.Fatalf("String() = %q, se esperaba %q", s.String(), want)
	}
	if got := scope.LevelWarehouse.String(); got != "warehouse" {
		t.Fatalf("LevelWarehouse.String() = %q", got)
	}
}

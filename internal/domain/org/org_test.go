package org_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

var now = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func TestParseCodeNormalizes(t *testing.T) {
	cases := map[string]string{"principal": "PRINCIPAL", " alm-01 ": "ALM-01", "Sede_2": "SEDE_2", "A": "A"}
	for in, want := range cases {
		c, err := org.ParseCode(in)
		if err != nil || c.String() != want {
			t.Errorf("ParseCode(%q) = %q, %v; se esperaba %q", in, c, err, want)
		}
	}
	for _, in := range []string{"", "  ", "-ALM", "ALM 01", "ÑANDÚ", strings.Repeat("A", 33), "a/b"} {
		if _, err := org.ParseCode(in); !errors.Is(err, org.ErrInvalid) {
			t.Errorf("ParseCode(%q) debía fallar, obtuvo %v", in, err)
		}
	}
}

func TestParseNameTrimsAndLimits(t *testing.T) {
	n, err := org.ParseName("  Panadería  Ñandú, C.A.  ")
	if err != nil || n.String() != "Panadería Ñandú, C.A." {
		t.Fatalf("ParseName = %q, %v", n, err)
	}
	for _, in := range []string{"", "   ", strings.Repeat("x", 121), "con\nsalto", "tab\tulado"} {
		if _, err := org.ParseName(in); !errors.Is(err, org.ErrInvalid) {
			t.Errorf("ParseName(%q) debía fallar, obtuvo %v", in, err)
		}
	}
	if _, err := org.ParseName(strings.Repeat("ñ", 120)); err != nil {
		t.Errorf("120 caracteres (no bytes) deben ser válidos: %v", err)
	}
}

func TestParseRIFNormalizesFormat(t *testing.T) {
	cases := map[string]string{
		"J-12345678-9":  "J-12345678-9",
		"j123456789":    "J-12345678-9",
		"J12345678-9":   "J-12345678-9",
		" v-01234567-0": "V-01234567-0",
		"G-20000001-5":  "G-20000001-5",
		"E-87654321-1":  "E-87654321-1",
		"P-11111111-1":  "P-11111111-1",
		"C-12345678-0":  "C-12345678-0",
	}
	for in, want := range cases {
		r, err := org.ParseRIF(in)
		if err != nil || r.String() != want {
			t.Errorf("ParseRIF(%q) = %q, %v; se esperaba %q", in, r, err, want)
		}
	}
	for _, in := range []string{"", "X-12345678-9", "J-1234567-9", "J-123456789-0", "J-12345678", "12345678-9", "J_12345678_9"} {
		if _, err := org.ParseRIF(in); !errors.Is(err, org.ErrInvalid) {
			t.Errorf("ParseRIF(%q) debía fallar, obtuvo %v", in, err)
		}
	}
	if !(org.RIF{}).IsZero() {
		t.Errorf("el RIF vacío representa 'sin RIF'")
	}
}

func TestHierarchyConstructorsInheritParentScope(t *testing.T) {
	o, err := org.NewOrganization("mi-negocio", "Mi Negocio", now)
	if err != nil {
		t.Fatal(err)
	}
	rif, _ := org.ParseRIF("J-12345678-9")
	c, err := org.NewCompany(o, "principal", "Mi Negocio, C.A.", rif, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := org.NewBranch(c, "centro", "Sucursal Centro", now)
	if err != nil {
		t.Fatal(err)
	}
	w, err := org.NewWarehouse(b, "alm-01", "Almacén principal", now)
	if err != nil {
		t.Fatal(err)
	}

	if c.OrganizationID != o.ID || b.OrganizationID != o.ID || b.CompanyID != c.ID ||
		w.OrganizationID != o.ID || w.CompanyID != c.ID || w.BranchID != b.ID {
		t.Fatalf("la jerarquía no se heredó: %+v %+v %+v", c, b, w)
	}
	ids := map[string]bool{o.ID.String(): true, c.ID.String(): true, b.ID.String(): true, w.ID.String(): true}
	if len(ids) != 4 {
		t.Fatalf("los IDs deben ser distintos")
	}
	for _, e := range []struct {
		name  string
		level scope.Level
		got   scope.BusinessScope
	}{
		{"organización", scope.LevelOrganization, o.Scope()},
		{"empresa", scope.LevelCompany, c.Scope()},
		{"sucursal", scope.LevelBranch, b.Scope()},
		{"almacén", scope.LevelWarehouse, w.Scope()},
	} {
		if e.got.Level() != e.level {
			t.Errorf("%s: Level = %v", e.name, e.got.Level())
		}
	}
	if !o.Scope().Contains(w.Scope()) || !c.Scope().Contains(w.Scope()) || !b.Scope().Contains(w.Scope()) {
		t.Fatalf("los ámbitos superiores deben contener al almacén")
	}
	if c.Status != org.StatusActive || !w.CreatedAt.Equal(now) || w.CreatedAt.Location() != time.UTC {
		t.Fatalf("estado o fecha incorrectos: %+v", w)
	}
}

func TestConstructorsValidateInput(t *testing.T) {
	o, _ := org.NewOrganization("org", "Org", now)
	c, _ := org.NewCompany(o, "c", "C", org.RIF{}, now)
	b, _ := org.NewBranch(c, "b", "B", now)

	checks := map[string]error{
		"org sin código":       second(org.NewOrganization("", "Org", now)),
		"org sin nombre":       second(org.NewOrganization("org", "", now)),
		"org sin fecha":        second(org.NewOrganization("org", "Org", time.Time{})),
		"empresa sin padre":    second(org.NewCompany(org.Organization{}, "c", "C", org.RIF{}, now)),
		"empresa sin nombre":   second(org.NewCompany(o, "c", " ", org.RIF{}, now)),
		"sucursal sin padre":   second(org.NewBranch(org.Company{}, "b", "B", now)),
		"sucursal código malo": second(org.NewBranch(c, "b b", "B", now)),
		"almacén sin padre":    second(org.NewWarehouse(org.Branch{}, "w", "W", now)),
		"almacén sin nombre":   second(org.NewWarehouse(b, "w", "", now)),
	}
	for name, err := range checks {
		if !errors.Is(err, org.ErrInvalid) {
			t.Errorf("%s: se esperaba ErrInvalid, obtuvo %v", name, err)
		}
	}
}

func second[T any](_ T, err error) error { return err }

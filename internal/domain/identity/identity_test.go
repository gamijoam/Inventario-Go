package identity_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

var now = time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)

type tree struct {
	o           org.Organization
	c1, c2      org.Company
	b1, b2      org.Branch // b1 en c1, b2 en c2
	w1          org.Warehouse
	otherOrgCo1 org.Company
}

func newTree(t *testing.T) tree {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var tr tree
	var err error
	tr.o, err = org.NewOrganization("ORG", "Org", now)
	must(err)
	tr.c1, err = org.NewCompany(tr.o, "C1", "C1", org.RIF{}, now)
	must(err)
	tr.c2, err = org.NewCompany(tr.o, "C2", "C2", org.RIF{}, now)
	must(err)
	tr.b1, err = org.NewBranch(tr.c1, "B1", "B1", now)
	must(err)
	tr.b2, err = org.NewBranch(tr.c2, "B2", "B2", now)
	must(err)
	tr.w1, err = org.NewWarehouse(tr.b1, "W1", "W1", now)
	must(err)
	other, err := org.NewOrganization("OTRA", "Otra", now)
	must(err)
	tr.otherOrgCo1, err = org.NewCompany(other, "C1", "C1", org.RIF{}, now)
	must(err)
	return tr
}

func principal(tr tree, grants ...identity.Grant) identity.Principal {
	return identity.Principal{UserID: id.MustNew(), OrganizationID: tr.o.ID, Grants: grants}
}

func grant(s scope.BusinessScope, perms ...identity.Permission) identity.Grant {
	return identity.Grant{Scope: s, Permissions: perms}
}

func TestPermissionDeniedByScopeAndOperation(t *testing.T) {
	tr := newTree(t)
	// Encargado de la sucursal B1: puede crear almacenes allí, nada más.
	p := principal(tr, grant(tr.b1.Scope(), identity.PermOrgRead, identity.PermWarehouseCreate))

	cases := []struct {
		name   string
		perm   identity.Permission
		target scope.BusinessScope
		want   bool
	}{
		{"operación permitida en su sucursal", identity.PermWarehouseCreate, tr.w1.Scope(), true},
		{"misma operación en otra sucursal", identity.PermWarehouseCreate, tr.b2.Scope(), false},
		{"misma operación sobre la empresa (ámbito más amplio)", identity.PermWarehouseCreate, tr.c1.Scope(), false},
		{"operación no concedida en su sucursal", identity.PermBranchCreate, tr.b1.Scope(), false},
		{"otra organización con los mismos códigos", identity.PermOrgRead, tr.otherOrgCo1.Scope(), false},
		{"ámbito vacío", identity.PermOrgRead, scope.BusinessScope{}, false},
	}
	for _, c := range cases {
		if got := p.Can(c.perm, c.target); got != c.want {
			t.Errorf("%s: Can = %v, se esperaba %v", c.name, got, c.want)
		}
	}
	err := p.Authorize(identity.PermBranchCreate, tr.c1.Scope(), "la empresa C1")
	if !errors.Is(err, identity.ErrDenied) || err.Error() != "permiso denegado: se requiere org.branch.create en la empresa C1" {
		t.Errorf("Authorize = %v", err)
	}
}

func TestGrantsCombineOnlyExplicitPermissions(t *testing.T) {
	tr := newTree(t)
	p := principal(tr,
		grant(tr.o.Scope(), identity.PermOrgRead),
		grant(tr.c2.Scope(), identity.PermBranchCreate),
	)
	if !p.Can(identity.PermBranchCreate, tr.b2.Scope()) || p.Can(identity.PermBranchCreate, tr.b1.Scope()) {
		t.Error("crear sucursal solo debe valer en C2")
	}
	if !p.Can(identity.PermOrgRead, tr.w1.Scope()) {
		t.Error("lectura de organización cubre todo lo que contiene")
	}
	if (identity.Principal{OrganizationID: tr.o.ID, Grants: p.Grants}).Can(identity.PermOrgRead, tr.o.Scope()) {
		t.Error("un principal sin usuario no puede nada")
	}
}

func TestSeesAncestorsAndDescendants(t *testing.T) {
	tr := newTree(t)
	p := principal(tr, grant(tr.b1.Scope(), identity.PermOrgRead))
	for _, s := range []scope.BusinessScope{tr.o.Scope(), tr.c1.Scope(), tr.b1.Scope(), tr.w1.Scope()} {
		if !p.Sees(identity.PermOrgRead, s) {
			t.Errorf("debe ver %v", s)
		}
	}
	for _, s := range []scope.BusinessScope{tr.c2.Scope(), tr.b2.Scope(), tr.otherOrgCo1.Scope()} {
		if p.Sees(identity.PermOrgRead, s) {
			t.Errorf("no debe ver %v", s)
		}
	}
	if p.Sees(identity.PermUserRead, tr.b1.Scope()) {
		t.Error("ver exige el permiso concreto")
	}
}

func TestTemplatesNeverIncludeExtraordinaryPermissions(t *testing.T) {
	codes := map[string]bool{}
	for _, tpl := range identity.Templates() {
		codes[tpl.Code] = true
		for _, p := range tpl.Permissions {
			if p.Extraordinary() {
				t.Errorf("plantilla %s incluye %s", tpl.Code, p)
			}
		}
	}
	for _, want := range []string{"ADMIN_ORG", "ADMIN_EMPRESA", "ENCARGADO", "CAJERO", "ALMACENISTA", "OPERADOR_TECNICO"} {
		if !codes[want] {
			t.Errorf("falta la plantilla %s", want)
		}
	}
	extra := 0
	for _, info := range identity.Catalog() {
		if info.Extraordinary {
			extra++
		}
	}
	if extra == 0 {
		t.Fatal("el catálogo debe tener permisos extraordinarios para que la prueba tenga sentido")
	}
}

func TestRoleNormalizesAndEditsPermissions(t *testing.T) {
	tr := newTree(t)
	r, err := identity.NewRole(tr.o, "caja", "Caja", []identity.Permission{identity.PermUserRead, identity.PermOrgRead, identity.PermOrgRead}, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Code.String() != "CAJA" || len(r.Permissions) != 2 || r.Permissions[0] != identity.PermUserRead {
		t.Fatalf("rol = %+v", r)
	}
	r2, err := r.Edit([]identity.Permission{identity.PermApproveBelowCost}, []identity.Permission{identity.PermUserRead})
	if err != nil || !r2.Has(identity.PermApproveBelowCost) || r2.Has(identity.PermUserRead) || !r2.Has(identity.PermOrgRead) {
		t.Fatalf("edit = %+v, %v", r2, err)
	}
	if !r.Has(identity.PermUserRead) {
		t.Error("Edit no debe modificar el rol original")
	}
	if _, err := r.Edit([]identity.Permission{"inventado.x"}, nil); !errors.Is(err, identity.ErrInvalid) {
		t.Errorf("permiso desconocido: %v", err)
	}
	if _, err := identity.ParsePermission("org.todo"); !errors.Is(err, identity.ErrInvalid) {
		t.Errorf("ParsePermission: %v", err)
	}
}

func TestUsername(t *testing.T) {
	u, err := identity.ParseUsername("  Ana.Perez ")
	if err != nil || u.String() != "ana.perez" {
		t.Fatalf("%v %v", u, err)
	}
	for _, bad := range []string{"", "ab", "ana perez", "-ana", "ñandu", "a234567890123456789012345678901234"} {
		if _, err := identity.ParseUsername(bad); !errors.Is(err, identity.ErrInvalid) {
			t.Errorf("%q aceptado", bad)
		}
	}
}

func TestAssignmentMustStayInTheUsersOrganization(t *testing.T) {
	tr := newTree(t)
	u, err := identity.NewUser(tr.o, "ana", "Ana", now)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := identity.NewRole(tr.o, "CAJERO", "Cajero", []identity.Permission{identity.PermOrgRead}, now)
	if _, err := identity.NewAssignment(u, r, tr.b1.Scope(), u.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.NewAssignment(u, r, tr.otherOrgCo1.Scope(), u.ID, now); !errors.Is(err, identity.ErrInvalid) {
		t.Errorf("ámbito de otra organización: %v", err)
	}
	if _, err := identity.NewAssignment(u, r, scope.BusinessScope{}, u.ID, now); !errors.Is(err, identity.ErrInvalid) {
		t.Errorf("ámbito vacío: %v", err)
	}
}

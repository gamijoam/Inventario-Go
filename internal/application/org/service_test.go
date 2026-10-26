package org_test

import (
	"context"
	"errors"
	"testing"
	"time"

	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

// memStore es una implementación en memoria de los puertos, con
// transacciones (se confirma solo si fn no falla) y unicidad por código.
type memStore struct {
	state memState
	fail  error // si no es nil, InsertWarehouse falla (para probar rollback)
}

type memState struct {
	orgs       []org.Organization
	companies  []org.Company
	branches   []org.Branch
	warehouses []org.Warehouse
	binding    *apporg.Binding
}

func (s memState) clone() memState {
	c := memState{
		orgs:       append([]org.Organization(nil), s.orgs...),
		companies:  append([]org.Company(nil), s.companies...),
		branches:   append([]org.Branch(nil), s.branches...),
		warehouses: append([]org.Warehouse(nil), s.warehouses...),
	}
	if s.binding != nil {
		b := *s.binding
		c.binding = &b
	}
	return c
}

func (m *memStore) InTx(ctx context.Context, fn func(apporg.Repository) error) error {
	tx := &memTx{st: m.state.clone(), fail: m.fail}
	if err := fn(tx); err != nil {
		return err
	}
	m.state = tx.st
	return nil
}

type memTx struct {
	st   memState
	fail error
}

func (t *memTx) InsertOrganization(_ context.Context, o org.Organization) error {
	for _, x := range t.st.orgs {
		if x.Code == o.Code {
			return apporg.ErrConflict
		}
	}
	t.st.orgs = append(t.st.orgs, o)
	return nil
}

func (t *memTx) InsertCompany(_ context.Context, c org.Company) error {
	for _, x := range t.st.companies {
		if x.OrganizationID == c.OrganizationID && (x.Code == c.Code || (!c.RIF.IsZero() && x.RIF == c.RIF)) {
			return apporg.ErrConflict
		}
	}
	t.st.companies = append(t.st.companies, c)
	return nil
}

func (t *memTx) InsertBranch(_ context.Context, b org.Branch) error {
	for _, x := range t.st.branches {
		if x.CompanyID == b.CompanyID && x.Code == b.Code {
			return apporg.ErrConflict
		}
	}
	t.st.branches = append(t.st.branches, b)
	return nil
}

func (t *memTx) InsertWarehouse(_ context.Context, w org.Warehouse) error {
	if t.fail != nil {
		return t.fail
	}
	for _, x := range t.st.warehouses {
		if x.BranchID == w.BranchID && x.Code == w.Code {
			return apporg.ErrConflict
		}
	}
	t.st.warehouses = append(t.st.warehouses, w)
	return nil
}

func (t *memTx) Organization(_ context.Context, orgID id.ID) (org.Organization, error) {
	for _, x := range t.st.orgs {
		if x.ID == orgID {
			return x, nil
		}
	}
	return org.Organization{}, apporg.ErrNotFound
}

func (t *memTx) Company(_ context.Context, orgID, companyID id.ID) (org.Company, error) {
	for _, x := range t.st.companies {
		if x.OrganizationID == orgID && x.ID == companyID {
			return x, nil
		}
	}
	return org.Company{}, apporg.ErrNotFound
}

func (t *memTx) Branch(_ context.Context, orgID, companyID, branchID id.ID) (org.Branch, error) {
	for _, x := range t.st.branches {
		if x.OrganizationID == orgID && x.CompanyID == companyID && x.ID == branchID {
			return x, nil
		}
	}
	return org.Branch{}, apporg.ErrNotFound
}

func (t *memTx) CompanyByCode(_ context.Context, orgID id.ID, code org.Code) (org.Company, error) {
	for _, x := range t.st.companies {
		if x.OrganizationID == orgID && x.Code == code {
			return x, nil
		}
	}
	return org.Company{}, apporg.ErrNotFound
}

func (t *memTx) BranchByCode(_ context.Context, orgID, companyID id.ID, code org.Code) (org.Branch, error) {
	for _, x := range t.st.branches {
		if x.OrganizationID == orgID && x.CompanyID == companyID && x.Code == code {
			return x, nil
		}
	}
	return org.Branch{}, apporg.ErrNotFound
}

func (t *memTx) Companies(_ context.Context, orgID id.ID) ([]org.Company, error) {
	var out []org.Company
	for _, x := range t.st.companies {
		if x.OrganizationID == orgID {
			out = append(out, x)
		}
	}
	return out, nil
}

func (t *memTx) Branches(_ context.Context, orgID, companyID id.ID) ([]org.Branch, error) {
	var out []org.Branch
	for _, x := range t.st.branches {
		if x.OrganizationID == orgID && (companyID.IsZero() || x.CompanyID == companyID) {
			out = append(out, x)
		}
	}
	return out, nil
}

func (t *memTx) Warehouses(_ context.Context, orgID, companyID, branchID id.ID) ([]org.Warehouse, error) {
	var out []org.Warehouse
	for _, x := range t.st.warehouses {
		if x.OrganizationID == orgID && (companyID.IsZero() || x.CompanyID == companyID) && (branchID.IsZero() || x.BranchID == branchID) {
			out = append(out, x)
		}
	}
	return out, nil
}

func (t *memTx) Binding(context.Context) (apporg.Binding, error) {
	if t.st.binding == nil {
		return apporg.Binding{}, apporg.ErrNotBootstrapped
	}
	return *t.st.binding, nil
}

func (t *memTx) Bind(_ context.Context, b apporg.Binding) error {
	t.st.binding = &b
	return nil
}

var fixedNow = time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)

func defaultInput() apporg.BootstrapInput {
	return apporg.BootstrapInput{
		OrganizationCode: "bodega-ana", OrganizationName: "Bodega Ana",
		CompanyCode: "principal", CompanyLegalName: "Bodega Ana, C.A.", RIF: "J-12345678-9",
		BranchCode: "principal", BranchName: "Principal",
		WarehouseCode: "principal", WarehouseName: "Principal",
	}
}

func bootstrapped(t *testing.T) (*apporg.Service, *memStore, apporg.Bootstrapped) {
	t.Helper()
	st := &memStore{}
	svc := apporg.NewService(st, func() time.Time { return fixedNow })
	res, err := svc.Bootstrap(context.Background(), defaultInput())
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	return svc, st, res
}

func TestBootstrapCreatesWholeHierarchyAndBinding(t *testing.T) {
	svc, st, res := bootstrapped(t)
	if len(st.state.orgs) != 1 || len(st.state.companies) != 1 || len(st.state.branches) != 1 || len(st.state.warehouses) != 1 {
		t.Fatalf("estado = %+v", st.state)
	}
	if res.Company.RIF.String() != "J-12345678-9" || res.Warehouse.BranchID != res.Branch.ID {
		t.Fatalf("resultado = %+v", res)
	}
	cur, err := svc.Current(context.Background())
	if err != nil || cur.Branch.ID != res.Branch.ID || cur.Organization.ID != res.Organization.ID {
		t.Fatalf("Current = %+v, %v", cur, err)
	}
	if cur.Scope().Level() != scope.LevelOrganization {
		t.Fatalf("el ámbito local de administración es la organización")
	}
}

func TestBootstrapTwiceFails(t *testing.T) {
	svc, st, _ := bootstrapped(t)
	_, err := svc.Bootstrap(context.Background(), defaultInput())
	if !errors.Is(err, apporg.ErrAlreadyBootstrapped) || len(st.state.orgs) != 1 {
		t.Fatalf("err=%v orgs=%d", err, len(st.state.orgs))
	}
}

func TestBootstrapIsAtomic(t *testing.T) {
	st := &memStore{fail: errors.New("disco lleno")}
	svc := apporg.NewService(st, func() time.Time { return fixedNow })
	if _, err := svc.Bootstrap(context.Background(), defaultInput()); err == nil {
		t.Fatal("se esperaba error")
	}
	if len(st.state.orgs)+len(st.state.companies)+len(st.state.branches) != 0 || st.state.binding != nil {
		t.Fatalf("no debe quedar estructura a medias: %+v", st.state)
	}
}

func TestBootstrapValidatesBeforeTouchingStorage(t *testing.T) {
	st := &memStore{}
	svc := apporg.NewService(st, func() time.Time { return fixedNow })
	in := defaultInput()
	in.RIF = "X-1"
	if _, err := svc.Bootstrap(context.Background(), in); !errors.Is(err, org.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	in = defaultInput()
	in.WarehouseName = ""
	if _, err := svc.Bootstrap(context.Background(), in); !errors.Is(err, org.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestCurrentBeforeBootstrap(t *testing.T) {
	svc := apporg.NewService(&memStore{}, func() time.Time { return fixedNow })
	if _, err := svc.Current(context.Background()); !errors.Is(err, apporg.ErrNotBootstrapped) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateAndListWithinOrganization(t *testing.T) {
	svc, _, res := bootstrapped(t)
	ctx := context.Background()
	actor := res.Organization.Scope()

	c, err := svc.CreateCompany(ctx, actor, "norte", "Bodega Ana Norte, C.A.", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.CreateBranch(ctx, actor, "norte", "maracay", "Maracay")
	if err != nil {
		t.Fatal(err)
	}
	w, err := svc.CreateWarehouse(ctx, actor, "norte", "maracay", "danados", "Mercancía dañada")
	if err != nil {
		t.Fatal(err)
	}
	if b.CompanyID != c.ID || w.BranchID != b.ID || w.CompanyID != c.ID {
		t.Fatalf("jerarquía incorrecta")
	}

	companies, _ := svc.ListCompanies(ctx, actor)
	branches, _ := svc.ListBranches(ctx, actor, "")
	norteBranches, _ := svc.ListBranches(ctx, actor, "NORTE")
	whs, _ := svc.ListWarehouses(ctx, actor, "norte", "maracay")
	all, _ := svc.ListWarehouses(ctx, actor, "", "")
	if len(companies) != 2 || len(branches) != 2 || len(norteBranches) != 1 || len(whs) != 1 || len(all) != 2 {
		t.Fatalf("listados: %d empresas, %d sucursales, %d de norte, %d almacenes maracay, %d total",
			len(companies), len(branches), len(norteBranches), len(whs), len(all))
	}
}

func TestDuplicateCodesConflictWithinParentOnly(t *testing.T) {
	svc, _, res := bootstrapped(t)
	ctx := context.Background()
	actor := res.Organization.Scope()

	if _, err := svc.CreateCompany(ctx, actor, "PRINCIPAL", "Otra", ""); !errors.Is(err, apporg.ErrConflict) {
		t.Errorf("código de empresa repetido: %v", err)
	}
	if _, err := svc.CreateCompany(ctx, actor, "otra", "Otra", "J-12345678-9"); !errors.Is(err, apporg.ErrConflict) {
		t.Errorf("RIF repetido en la organización: %v", err)
	}
	if _, err := svc.CreateBranch(ctx, actor, "principal", "principal", "Duplicada"); !errors.Is(err, apporg.ErrConflict) {
		t.Errorf("código de sucursal repetido: %v", err)
	}
	// El mismo código en otra empresa es válido.
	if _, err := svc.CreateCompany(ctx, actor, "segunda", "Segunda", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateBranch(ctx, actor, "segunda", "principal", "Principal"); err != nil {
		t.Errorf("el código de sucursal es único por empresa, no global: %v", err)
	}
}

func TestOtherOrganizationsAreInvisible(t *testing.T) {
	svc, st, res := bootstrapped(t)
	ctx := context.Background()

	// Una segunda organización en la misma base (p. ej. nube compartida).
	other, _ := org.NewOrganization("otra", "Otra", fixedNow)
	otherCo, _ := org.NewCompany(other, "secreta", "Secreta, C.A.", org.RIF{}, fixedNow)
	st.state.orgs = append(st.state.orgs, other)
	st.state.companies = append(st.state.companies, otherCo)

	actor := res.Organization.Scope()
	if _, err := svc.CreateBranch(ctx, actor, "secreta", "x", "X"); !errors.Is(err, apporg.ErrNotFound) {
		t.Errorf("empresa de otra organización: se esperaba ErrNotFound, obtuvo %v", err)
	}
	if list, _ := svc.ListCompanies(ctx, actor); len(list) != 1 {
		t.Errorf("solo debe ver su empresa, ve %d", len(list))
	}
	foreign := other.Scope()
	if list, _ := svc.ListCompanies(ctx, foreign); len(list) != 1 || list[0].ID != otherCo.ID {
		t.Errorf("la otra organización tampoco ve la primera: %+v", list)
	}
}

func TestNarrowActorsCannotWidenTheirScope(t *testing.T) {
	svc, _, res := bootstrapped(t)
	ctx := context.Background()
	orgActor := res.Organization.Scope()
	if _, err := svc.CreateCompany(ctx, orgActor, "b", "Empresa B", ""); err != nil {
		t.Fatal(err)
	}

	companyActor := res.Company.Scope()
	if _, err := svc.CreateCompany(ctx, companyActor, "c", "Empresa C", ""); !errors.Is(err, apporg.ErrForbidden) {
		t.Errorf("actor de empresa creando empresa: %v", err)
	}
	if _, err := svc.CreateBranch(ctx, companyActor, "b", "x", "X"); !errors.Is(err, apporg.ErrNotFound) {
		t.Errorf("actor de empresa A creando en empresa B: %v", err)
	}
	if list, _ := svc.ListCompanies(ctx, companyActor); len(list) != 1 || list[0].ID != res.Company.ID {
		t.Errorf("actor de empresa ve %d empresas", len(list))
	}

	branchActor := res.Branch.Scope()
	if _, err := svc.CreateBranch(ctx, branchActor, "principal", "nueva", "Nueva"); !errors.Is(err, apporg.ErrForbidden) {
		t.Errorf("actor de sucursal creando sucursal: %v", err)
	}
	if _, err := svc.CreateWarehouse(ctx, branchActor, "principal", "principal", "alm-2", "Almacén 2"); err != nil {
		t.Errorf("actor de sucursal puede crear almacenes en su sucursal: %v", err)
	}
	if list, _ := svc.ListBranches(ctx, branchActor, ""); len(list) != 1 {
		t.Errorf("actor de sucursal ve %d sucursales", len(list))
	}

	if _, err := svc.ListCompanies(ctx, scope.BusinessScope{}); !errors.Is(err, apporg.ErrForbidden) {
		t.Errorf("ámbito vacío: %v", err)
	}
}

func TestErrorsWrapSentinels(t *testing.T) {
	svc, _, res := bootstrapped(t)
	_, err := svc.CreateBranch(context.Background(), res.Organization.Scope(), "no-existe", "x", "X")
	if !errors.Is(err, apporg.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := svc.CreateBranch(context.Background(), res.Organization.Scope(), "mal código", "x", "X"); !errors.Is(err, org.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

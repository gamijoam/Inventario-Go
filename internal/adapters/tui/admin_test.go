package tui_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/gamijoam/inventario-go/internal/adapters/tui"
	"github.com/gamijoam/inventario-go/internal/application/auth"
	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/pricing"
)

// ---------- Fake: precios y administración ----------

type adminLog struct {
	lists      []apppricing.ListView
	margins    []apppricing.MarginInput
	proposed   []apppricing.ProposeInput
	cancelled  []string
	users      []auth.UserView
	created    []auth.NewUserInput
	assigned   []string
	unassigned []string
	active     []string
	unlocked   []string
	resets     []string
	changed    []string
	roles      []identity.Role
	roleEdits  []string
	companies  []org.Company
	branches   map[string][]org.Branch
	warehouses map[string][]org.Warehouse
	createdOrg []string
}

func mustUser(s string) identity.Username {
	u, err := identity.ParseUsername(s)
	if err != nil {
		panic(err)
	}
	return u
}

func (f *fake) adm() *adminLog {
	if f.alog == nil {
		pc := mustCode("PRINCIPAL")
		f.alog = &adminLog{
			lists: []apppricing.ListView{{List: list}},
			users: []auth.UserView{{User: identity.User{Username: mustUser("admin"), DisplayName: mustName("Administrador"), Status: identity.StatusActive},
				Assignments: []auth.AssignmentView{{RoleCode: mustCode("ADMIN_ORG")}}},
				{User: identity.User{Username: mustUser("maria"), DisplayName: mustName("María"), Status: identity.StatusActive}, Locked: true}},
			roles: []identity.Role{{Code: mustCode("ADMIN_ORG"), Name: mustName("Administrador"), Permissions: []identity.Permission{identity.PermOrgRead}},
				{Code: mustCode("CAJERO"), Name: mustName("Cajero"), Permissions: []identity.Permission{identity.PermOrgRead, identity.PermProductRead}},
				{Code: mustCode("AUDITOR"), Name: mustName("Auditor"), Permissions: []identity.Permission{identity.PermOrgRead}}},
			companies:  []org.Company{{Code: pc, LegalName: mustName("Bodega Ana, C.A."), Currency: money.USD, Status: org.StatusActive}},
			branches:   map[string][]org.Branch{"PRINCIPAL": {{Code: pc, Name: mustName("Principal")}, {Code: mustCode("ESTE"), Name: mustName("Este")}}},
			warehouses: map[string][]org.Warehouse{"PRINCIPAL/PRINCIPAL": {{Code: pc, Name: mustName("Principal")}}, "PRINCIPAL/ESTE": {{Code: mustCode("E1"), Name: mustName("Este 1")}}},
		}
	}
	return f.alog
}

func (f *fake) PriceLists(context.Context) ([]apppricing.ListView, error) { return f.adm().lists, nil }

func (f *fake) CreatePriceList(_ context.Context, in apppricing.ListInput) (apppricing.ListView, error) {
	v := apppricing.ListView{List: pricing.List{Code: mustCode(in.Code), Name: in.Name, Currency: money.USD, Status: org.StatusActive}}
	if in.Margin != "" {
		m := decimal.MustParse(in.Margin)
		v.Margin = &m
	}
	f.adm().lists = append(f.adm().lists, v)
	return v, nil
}

func (f *fake) SetMargin(_ context.Context, in apppricing.MarginInput) (apppricing.ListView, error) {
	f.adm().margins = append(f.adm().margins, in)
	for i, l := range f.adm().lists {
		if l.Code.String() == in.List && in.Product == "" {
			if in.Margin == "" {
				l.Margin = nil
			} else {
				m := decimal.MustParse(in.Margin)
				l.Margin = &m
			}
			f.adm().lists[i] = l
		}
	}
	return f.adm().lists[0], nil
}

func (f *fake) Propose(_ context.Context, in apppricing.ProposeInput) (apppricing.ProposalView, error) {
	if in.List == "" {
		return apppricing.ProposalView{}, errors.New("indique la lista")
	}
	f.adm().proposed = append(f.adm().proposed, in)
	return apppricing.ProposalView{Proposal: pricing.Proposal{Number: "PP-000003", Price: decimal.MustParse(in.Price)}, List: list, Product: harina}, nil
}

func (f *fake) CancelRevision(_ context.Context, rid string) (pricing.Revision, error) {
	f.adm().cancelled = append(f.adm().cancelled, rid)
	return pricing.Revision{}, nil
}

func (f *fake) Users(context.Context) ([]auth.UserView, error) { return f.adm().users, nil }

func (f *fake) CreateUser(_ context.Context, in auth.NewUserInput) (identity.User, error) {
	f.adm().created = append(f.adm().created, in)
	u := identity.User{Username: mustUser(in.Username), DisplayName: mustName(in.DisplayName), Status: identity.StatusActive}
	f.adm().users = append(f.adm().users, auth.UserView{User: u})
	return u, nil
}

func (f *fake) userIndex(name string) int {
	return slices.IndexFunc(f.adm().users, func(u auth.UserView) bool { return u.Username.String() == name })
}

func (f *fake) SetUserActive(_ context.Context, name string, active bool) (identity.User, error) {
	i := f.userIndex(name)
	f.adm().active = append(f.adm().active, name)
	f.adm().users[i].Status = identity.StatusDisabled
	if active {
		f.adm().users[i].Status = identity.StatusActive
	}
	return f.adm().users[i].User, nil
}

func (f *fake) Unlock(_ context.Context, name string) error {
	f.adm().unlocked = append(f.adm().unlocked, name)
	f.adm().users[f.userIndex(name)].Locked = false
	return nil
}

func (f *fake) ResetPassword(_ context.Context, name, pw string) error {
	f.adm().resets = append(f.adm().resets, name+"|"+pw)
	return nil
}

func (f *fake) ChangePassword(_ context.Context, current, next string) error {
	if current != "clave segura" {
		return errors.New("usuario o contraseña incorrectos")
	}
	f.adm().changed = append(f.adm().changed, next)
	return nil
}

func (f *fake) Assign(_ context.Context, name, role string, where auth.ScopeRef) (auth.AssignmentView, error) {
	f.adm().assigned = append(f.adm().assigned, name+"|"+role+"|"+where.Company+"/"+where.Branch+"/"+where.Warehouse)
	x := auth.AssignmentView{Assignment: identity.Assignment{CreatedAt: time.Now()}, RoleCode: mustCode(role), Where: where}
	i := f.userIndex(name)
	f.adm().users[i].Assignments = append(f.adm().users[i].Assignments, x)
	return x, nil
}

func (f *fake) Unassign(_ context.Context, name, role string, where auth.ScopeRef) error {
	f.adm().unassigned = append(f.adm().unassigned, name+"|"+role)
	i := f.userIndex(name)
	f.adm().users[i].Assignments = nil
	return nil
}

func (f *fake) Roles(context.Context) ([]identity.Role, error) { return f.adm().roles, nil }

func (f *fake) CreateRole(_ context.Context, code, name string, perms []string) (identity.Role, error) {
	f.adm().roleEdits = append(f.adm().roleEdits, "create|"+code+"|"+name+"|"+strings.Join(perms, ","))
	r := identity.Role{Code: mustCode(code), Name: mustName(name)}
	for _, p := range perms {
		r.Permissions = append(r.Permissions, identity.Permission(p))
	}
	f.adm().roles = append(f.adm().roles, r)
	return r, nil
}

func (f *fake) EditRole(_ context.Context, code string, add, remove []string) (identity.Role, error) {
	f.adm().roleEdits = append(f.adm().roleEdits, "edit|"+code+"|+"+strings.Join(add, ",")+"|-"+strings.Join(remove, ","))
	return identity.Role{Code: mustCode(code), Permissions: make([]identity.Permission, 2)}, nil
}

func (f *fake) Companies(context.Context) ([]org.Company, error) { return f.adm().companies, nil }

func (f *fake) Branches(_ context.Context, company string) ([]org.Branch, error) {
	return f.adm().branches[company], nil
}

func (f *fake) WarehousesOf(_ context.Context, company, branch string) ([]org.Warehouse, error) {
	return f.adm().warehouses[company+"/"+branch], nil
}

func (f *fake) CreateCompany(_ context.Context, code, legal, rif, currency string) (org.Company, error) {
	f.adm().createdOrg = append(f.adm().createdOrg, "company|"+code+"|"+currency)
	c := org.Company{Code: mustCode(code), LegalName: mustName(legal), Currency: money.VES}
	f.adm().companies = append(f.adm().companies, c)
	return c, nil
}

func (f *fake) CreateBranch(_ context.Context, company, code, name string) (org.Branch, error) {
	f.adm().createdOrg = append(f.adm().createdOrg, "branch|"+company+"|"+code)
	b := org.Branch{Code: mustCode(code), Name: mustName(name)}
	f.adm().branches[company] = append(f.adm().branches[company], b)
	return b, nil
}

func (f *fake) CreateWarehouse(_ context.Context, company, branch, code, name string) (org.Warehouse, error) {
	f.adm().createdOrg = append(f.adm().createdOrg, "warehouse|"+company+"/"+branch+"|"+code)
	w := org.Warehouse{Code: mustCode(code), Name: mustName(name)}
	k := company + "/" + branch
	f.adm().warehouses[k] = append(f.adm().warehouses[k], w)
	return w, nil
}

// ---------- Pruebas ----------

var adminPerms = append([]identity.Permission{identity.PermUserRead, identity.PermUserManage, identity.PermRoleManage, identity.PermOrgRead,
	identity.PermCompanyCreate, identity.PermBranchCreate, identity.PermWarehouseCreate, identity.PermPriceListManage, identity.PermPricePropose}, all...)

// open entra con sesión y abre el menú name.
func open(t *testing.T, f *fake, name string) tea.Model {
	t.Helper()
	f.saved = true
	m := start(t, tui.Options{Backend: f})
	for range 8 {
		if strings.Contains(content(m), " "+name+"\n") || strings.Contains(strings.SplitN(content(m), "\n", 4)[2], name) {
			return m
		}
		m, _ = send(t, m, keyRight)
	}
	t.Fatalf("no se llegó al menú %s:\n%s", name, content(m))
	return m
}

func TestPriceListsMarginsAndProposal(t *testing.T) {
	f := newFake(adminPerms...)
	m := open(t, f, "Precios")
	mustContain(t, m, "Proponer precio", "Listas de precios")
	m = keys(t, m, keyDown, keyDown, keyDown, keyEnter)
	mustContain(t, m, "Listas de precios", "GENERAL", "sin margen", "F2 Nueva", "F3 Margen", "F4 Margen de producto")
	m, _ = send(t, m, keyF2)
	m = typeText(t, m, "MAYOR")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "Al mayor")
	m = keys(t, m, keyEnter, keyEnter)
	m = typeText(t, m, "15")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "lista creada: MAYOR", "15 %")
	m, _ = send(t, m, keyF3) // margen de GENERAL
	m = typeText(t, m, "25")
	m, _ = send(t, m, keyEnter)
	if got := f.adm().margins; len(got) != 1 || got[0] != (apppricing.MarginInput{List: "GENERAL", Margin: "25"}) {
		t.Fatalf("márgenes: %+v", got)
	}
	mustContain(t, m, "margen actualizado en GENERAL", "25 %")
	m, _ = send(t, m, keyF4)
	m = typeText(t, m, "HARINA")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "30")
	m, _ = send(t, m, keyEnter)
	if got := f.adm().margins; len(got) != 2 || got[1].Product != "HARINA" || got[1].Margin != "30" {
		t.Fatalf("margen de producto: %+v", got)
	}

	m = keys(t, m, keyEsc, keyUp, keyEnter) // Proponer precio
	mustContain(t, m, "Proponer precio", "‹ GENERAL · General (USD) ›")
	m, _ = send(t, m, keyDown)
	m = typeText(t, m, "HARINA")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "12.5")
	m, _ = send(t, m, keyF10)
	if got := f.adm().proposed; len(got) != 1 || got[0].List != "GENERAL" || got[0].Product != "HARINA" || got[0].Price != "12.5" {
		t.Fatalf("propuestas: %+v", got)
	}
	mustContain(t, m, "propuesta PP-000003: HARINA 12.5 en GENERAL, pendiente de aprobación")
}

func TestPricesProposeAndCancelScheduled(t *testing.T) {
	f := newFake(adminPerms...)
	m := open(t, f, "Precios")
	m, _ = send(t, m, keyEnter) // Precios vigentes
	mustContain(t, m, "F2 Proponer")
	if strings.Contains(content(m), "F4 Anular programado") {
		t.Fatal("sin precio programado no hay nada que anular")
	}
	m, _ = send(t, m, keyF2)
	mustContain(t, m, "Proponer precio", "‹ GENERAL", "HARINA")
	m, _ = send(t, m, keyEsc)

	f.scheduled = true
	m, _ = send(t, m, key('r'))
	mustContain(t, m, "11 desde 2026-11-01 08:00", "F4 Anular programado")
	m, _ = send(t, m, keyF4)
	mustContain(t, m, "Anular el precio 11 USD de HARINA en la lista GENERAL", "2026-11-01", "08:00")
	m, _ = send(t, m, keyEnter)
	if got := f.adm().cancelled; len(got) != 1 || got[0] != kardexMove.String() {
		t.Fatalf("anuladas: %v", got)
	}
	mustContain(t, m, "precio programado anulado")
}

func TestUsersAssignAndPasswords(t *testing.T) {
	f := newFake(adminPerms...)
	m := open(t, f, "Administración")
	mustContain(t, m, "► Usuarios", "Roles", "Estructura")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "admin", "ADMIN_ORG", "maria", "bloqueado", "sin roles")
	m, _ = send(t, m, keyF2)
	m = typeText(t, m, "pedro")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "Pedro")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "clave larga 1")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "clave larga 2")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "las contraseñas no coinciden")
	m, _ = send(t, m, keyBack)
	m = typeText(t, m, "1")
	m, _ = send(t, m, keyEnter)
	if got := f.adm().created; len(got) != 1 || got[0].Username != "pedro" || got[0].Password != "clave larga 1" {
		t.Fatalf("usuarios: %+v", got)
	}
	mustContain(t, m, "Usuario pedro", "usuario pedro creado; asígnele un rol con F2", "Sin roles")

	m, _ = send(t, m, keyF2)
	mustContain(t, m, "Asignar rol a pedro", "‹ ADMIN_ORG · Administrador ›", "‹ Una sucursal ›", "‹ PRINCIPAL · Bodega Ana, C.A. ›",
		"‹ PRINCIPAL · Principal ›", "‹ — ›")
	m, _ = send(t, m, keyRight)       // CAJERO
	m = keys(t, m, keyDown, keyRight) // un almacén
	mustContain(t, m, "‹ Un almacén ›", "‹ PRINCIPAL · Principal ›")
	m = keys(t, m, keyDown, keyDown, keyRight) // sucursal ESTE: el almacén se recalcula
	mustContain(t, m, "‹ ESTE · Este ›", "‹ E1 · Este 1 ›")
	m, _ = send(t, m, keyF10)
	if got := f.adm().assigned; len(got) != 1 || got[0] != "pedro|CAJERO|PRINCIPAL/ESTE/E1" {
		t.Fatalf("asignaciones: %v", got)
	}
	mustContain(t, m, "rol CAJERO asignado", "CAJERO", "F8 Quitar")

	m, _ = send(t, m, keyF2)
	m = keys(t, m, keyDown, keyLeft, keyLeft) // toda la organización
	mustContain(t, m, "‹ Toda la organización ›")
	if strings.Count(content(m), "‹ — ›") != 3 {
		t.Fatalf("con toda la organización no se elige empresa, sucursal ni almacén:\n%s", content(m))
	}
	m, _ = send(t, m, keyF10)
	if got := f.adm().assigned; got[1] != "pedro|ADMIN_ORG|//" {
		t.Fatalf("asignación de organización: %v", got)
	}

	m, _ = send(t, m, tea.KeyPressMsg{Code: tea.KeyF8})
	m, _ = send(t, m, keyEnter)
	if len(f.adm().unassigned) != 1 {
		t.Fatal("no quitó el rol")
	}
	m, _ = send(t, m, tea.KeyPressMsg{Code: tea.KeyF5})
	m = typeText(t, m, "nueva clave 99")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "nueva clave 99")
	m, _ = send(t, m, keyEnter)
	if got := f.adm().resets; len(got) != 1 || got[0] != "pedro|nueva clave 99" {
		t.Fatalf("restablecer: %v", got)
	}
	mustContain(t, m, "contraseña de pedro restablecida")
	m, _ = send(t, m, keyF3)
	mustContain(t, m, "Inactivar a pedro")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "usuario pedro inactivado", "inactivo")

	m = keys(t, m, keyEsc, keyDown, keyEnter) // maria, bloqueada
	mustContain(t, m, "Usuario maria", "F6 Desbloquear")
	m = keys(t, m, tea.KeyPressMsg{Code: tea.KeyF6}, keyEnter)
	if got := f.adm().unlocked; len(got) != 1 || got[0] != "maria" {
		t.Fatalf("desbloquear: %v", got)
	}
	mustContain(t, m, "usuario maria desbloqueado")
}

func TestReadOnlyAdmin(t *testing.T) {
	f := newFake(identity.PermUserRead, identity.PermOrgRead)
	m := open(t, f, "Administración")
	m, _ = send(t, m, keyEnter)
	if strings.Contains(content(m), "F2 Nuevo") {
		t.Fatal("solo lectura no crea usuarios")
	}
	m = keys(t, m, keyEnter, keyF2, tea.KeyPressMsg{Code: tea.KeyF5})
	if v := content(m); strings.Contains(v, "Asignar rol") || strings.Contains(v, "Restablecer") {
		t.Fatalf("solo lectura no administra:\n%s", v)
	}
}

func TestRolesWithCheckboxes(t *testing.T) {
	f := newFake(adminPerms...)
	m := open(t, f, "Administración")
	m = keys(t, m, keyDown, keyEnter)
	mustContain(t, m, "ADMIN_ORG", "de fábrica", "AUDITOR", "propio", "F7 Copiar como nuevo")

	m, _ = send(t, m, keyEnter) // ADMIN_ORG: solo lectura
	mustContain(t, m, "Rol ADMIN_ORG (solo lectura)", "[x] Ver organización")
	m = keys(t, m, key(' '), keyF10)
	if len(f.adm().roleEdits) != 0 {
		t.Fatal("un rol de fábrica no se edita")
	}
	m, _ = send(t, m, keyEsc)

	m = keys(t, m, keyDown, keyDown, keyEnter) // AUDITOR: propio
	mustContain(t, m, "Rol AUDITOR", "Espacio Marcar", "1 de")
	m = keys(t, m, key(' '), keyDown, key(' ')) // quita org.read, agrega company.create
	m, _ = send(t, m, keyF10)
	if got := f.adm().roleEdits; len(got) != 1 || got[0] != "edit|AUDITOR|+org.company.create|-org.structure.read" {
		t.Fatalf("edición: %v", got)
	}
	mustContain(t, m, "rol AUDITOR actualizado")

	m = keys(t, m, keyUp, tea.KeyPressMsg{Code: tea.KeyF7}) // copiar CAJERO
	mustContain(t, m, "Nuevo rol", "Cajero (copia)", "2 de")
	m = typeText(t, m, "CAJERO2")
	m = keys(t, m, keyF10)
	if got := f.adm().roleEdits; len(got) != 2 || got[1] != "create|CAJERO2|Cajero (copia)|org.structure.read,catalog.product.read" {
		t.Fatalf("copia: %v", got)
	}
	mustContain(t, m, "rol CAJERO2 creado")
}

func TestStructureTree(t *testing.T) {
	f := newFake(adminPerms...)
	m := open(t, f, "Administración")
	m = keys(t, m, keyDown, keyDown, keyEnter)
	mustContain(t, m, "Estructura", "PRINCIPAL", "Bodega Ana, C.A.", "valoración USD", "  ESTE", "    E1", "F2 Empresa", "F3 Sucursal")
	if strings.Contains(content(m), "F4 Almacén") {
		t.Fatal("sobre una empresa no se crea un almacén")
	}
	m, _ = send(t, m, keyF3)
	m = typeText(t, m, "OESTE")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "Oeste")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "sucursal creada: PRINCIPAL/OESTE", "  OESTE")
	m = keys(t, m, keyDown, keyF4)
	m = typeText(t, m, "P2")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "Depósito")
	m, _ = send(t, m, keyEnter)
	if got := f.adm().createdOrg; len(got) != 2 || got[1] != "warehouse|PRINCIPAL/PRINCIPAL|P2" {
		t.Fatalf("estructura: %v", got)
	}
	m, _ = send(t, m, keyF2)
	m = typeText(t, m, "OTRA")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "Otra, C.A.")
	m = keys(t, m, keyEnter, keyEnter, keyRight, keyEnter)
	if got := f.adm().createdOrg; got[2] != "company|OTRA|VES" {
		t.Fatalf("empresa: %v", got)
	}
}

func TestChangeMyPassword(t *testing.T) {
	f := newFake(all...)
	m := open(t, f, "Sistema")
	m = keys(t, m, keyDown, keyDown, keyEnter)
	mustContain(t, m, "Cambiar mi contraseña", "Contraseña actual")
	m = typeText(t, m, "mala")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "otra clave 12")
	m, _ = send(t, m, keyEnter)
	m = typeText(t, m, "otra clave 12")
	m, _ = send(t, m, keyEnter)
	mustContain(t, m, "usuario o contraseña incorrectos")
	if strings.Contains(content(m), "otra clave") {
		t.Fatal("las contraseñas no se muestran")
	}
	m = keys(t, m, keyUp, keyUp)
	for range 4 {
		m, _ = send(t, m, keyBack)
	}
	m = typeText(t, m, "clave segura")
	m, _ = send(t, m, keyF10)
	if got := f.adm().changed; len(got) != 1 || got[0] != "otra clave 12" {
		t.Fatalf("cambios: %v", got)
	}
	mustContain(t, m, "contraseña cambiada")
}

var _ = id.MustNew

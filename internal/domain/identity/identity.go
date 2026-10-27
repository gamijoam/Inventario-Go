// Package identity modela usuarios, roles, permisos y el principal
// autenticado (sección 26.3).
//
// Un usuario pertenece a una organización y recibe asignaciones de rol, cada
// una limitada a un ámbito (organización, empresa, sucursal o almacén). Un
// permiso se concede solo si alguna asignación lo incluye explícitamente y su
// ámbito contiene al recurso: denegación por defecto. El nombre del rol no
// confiere nada por sí mismo.
package identity

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

var (
	ErrInvalid = errors.New("dato de identidad inválido")
	// ErrDenied indica que el principal no tiene el permiso en el ámbito.
	ErrDenied = errors.New("permiso denegado")
)

// Username es el nombre de inicio de sesión: único en la organización y en
// minúsculas.
type Username struct{ v string }

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

func ParseUsername(s string) (Username, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	if !usernamePattern.MatchString(v) {
		return Username{}, fmt.Errorf("%w: usuario %q (3 a 32 caracteres a-z, 0-9, punto, guion o guion bajo)", ErrInvalid, s)
	}
	return Username{v}, nil
}

func (u Username) String() string { return u.v }
func (u Username) IsZero() bool   { return u.v == "" }

// Status indica si el usuario puede iniciar sesión.
type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
)

type User struct {
	ID             id.ID
	OrganizationID id.ID
	Username       Username
	DisplayName    org.Name
	Status         Status
	CreatedAt      time.Time
}

func NewUser(o org.Organization, username, displayName string, now time.Time) (User, error) {
	u, err := ParseUsername(username)
	if err != nil {
		return User{}, err
	}
	n, err := org.ParseName(displayName)
	if err != nil {
		return User{}, err
	}
	uid, err := id.New()
	if err != nil {
		return User{}, err
	}
	return User{ID: uid, OrganizationID: o.ID, Username: u, DisplayName: n, Status: StatusActive, CreatedAt: now.UTC()}, nil
}

// Permission es una capacidad explícita, p. ej. "org.branch.create".
type Permission string

const (
	PermOrgRead         Permission = "org.structure.read"
	PermCompanyCreate   Permission = "org.company.create"
	PermBranchCreate    Permission = "org.branch.create"
	PermWarehouseCreate Permission = "org.warehouse.create"
	PermUserRead        Permission = "identity.user.read"
	PermUserManage      Permission = "identity.user.manage"
	PermRoleManage      Permission = "identity.role.manage"
	PermProductRead     Permission = "catalog.product.read"
	PermProductManage   Permission = "catalog.product.manage"
	PermUnitManage      Permission = "catalog.unit.manage"
	PermStockRead       Permission = "inventory.stock.read"
	PermStockAdjust     Permission = "inventory.stock.adjust"
	PermTransferCreate  Permission = "inventory.transfer.create"
	PermReservation     Permission = "inventory.reservation.manage"
	PermBlock           Permission = "inventory.block.manage"
	PermCountManage     Permission = "inventory.count.manage"
	PermCountApprove    Permission = "inventory.count.approve"
	PermPolicyManage    Permission = "inventory.policy.manage"
	PermPurchaseRead    Permission = "purchase.read"
	PermPurchaseManage  Permission = "purchase.manage"
	PermReceiptConfirm  Permission = "purchase.receipt.confirm"
	PermReplacementCost Permission = "purchase.replacement_cost.manage"
	PermRateRead        Permission = "currency.rate.read"
	PermRateManage      Permission = "currency.rate.manage"
	// Extraordinarios: nunca forman parte de una plantilla (sección 26.3).
	PermApproveBelowCost Permission = "pricing.override.approve_below_cost"
	PermBackupRestore    Permission = "system.backup.restore"
	PermNegativeOverride Permission = "inventory.negative_stock.override"
)

// PermissionInfo describe una entrada del catálogo.
type PermissionInfo struct {
	Permission    Permission
	Description   string
	Extraordinary bool
}

var catalog = []PermissionInfo{
	{PermOrgRead, "Ver organización, empresas, sucursales y almacenes", false},
	{PermCompanyCreate, "Crear empresas legales", false},
	{PermBranchCreate, "Crear sucursales", false},
	{PermWarehouseCreate, "Crear almacenes", false},
	{PermUserRead, "Ver usuarios y sus asignaciones", false},
	{PermUserManage, "Crear, desactivar y desbloquear usuarios; restablecer contraseñas; asignar roles", false},
	{PermRoleManage, "Crear y editar roles (solo con ámbito de organización)", false},
	{PermProductRead, "Ver el catálogo de productos y unidades", false},
	{PermProductManage, "Crear, editar e inactivar productos (solo con ámbito de organización)", false},
	{PermUnitManage, "Crear unidades de medida (solo con ámbito de organización)", false},
	{PermStockRead, "Ver existencias y kardex de los almacenes del ámbito", false},
	{PermStockAdjust, "Registrar entradas, salidas y reversos de ajuste en los almacenes del ámbito", false},
	{PermTransferCreate, "Transferir existencia entre almacenes de una sucursal (en ambos almacenes)", false},
	{PermReservation, "Crear, despachar y liberar reservas de existencia", false},
	{PermBlock, "Bloquear y liberar mercancía no vendible (daño, cuarentena…)", false},
	{PermCountManage, "Crear conteos físicos, capturar lo contado y enviarlos a revisión", false},
	{PermCountApprove, "Aprobar o rechazar conteos de otros usuarios; aplica el ajuste", false},
	{PermPolicyManage, "Configurar la política de existencia negativa de la empresa y sus productos", false},
	{PermPurchaseRead, "Ver proveedores, órdenes de compra y recepciones", false},
	{PermPurchaseManage, "Crear proveedores (solo con ámbito de organización), órdenes y recepciones en borrador, y gastos", false},
	{PermReceiptConfirm, "Confirmar recepciones: la mercancía entra al almacén y recalcula el costo promedio", false},
	{PermReplacementCost, "Registrar el costo de reposición estimado de los productos", false},
	{PermRateRead, "Ver las tasas de cambio de la empresa", false},
	{PermRateManage, "Registrar tasas de cambio con su fuente", false},
	{PermApproveBelowCost, "Aprobar ventas por debajo del costo (DEC-007.B2, nivel N2; uso en M4)", true},
	{PermBackupRestore, "Restaurar respaldos (uso en M5)", true},
	{PermNegativeOverride, "Registrar salidas que dejan existencia negativa, dentro de la política y su límite (DEC-007.F2)", true},
}

// Catalog devuelve todos los permisos conocidos, en orden estable.
func Catalog() []PermissionInfo { return slices.Clone(catalog) }

func ParsePermission(s string) (Permission, error) {
	p := Permission(strings.TrimSpace(s))
	if _, ok := lookup(p); !ok {
		return "", fmt.Errorf("%w: permiso desconocido %q (vea 'balanzapro role permissions')", ErrInvalid, s)
	}
	return p, nil
}

// Extraordinary indica un permiso de alto riesgo que debe concederse de
// forma expresa.
func (p Permission) Extraordinary() bool {
	info, _ := lookup(p)
	return info.Extraordinary
}

func lookup(p Permission) (PermissionInfo, bool) {
	for _, info := range catalog {
		if info.Permission == p {
			return info, true
		}
	}
	return PermissionInfo{}, false
}

// normalize valida, ordena y elimina duplicados.
func normalize(perms []Permission) ([]Permission, error) {
	out := make([]Permission, 0, len(perms))
	for _, p := range perms {
		if _, ok := lookup(p); !ok {
			return nil, fmt.Errorf("%w: permiso desconocido %q", ErrInvalid, p)
		}
		out = append(out, p)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// Role es un conjunto con nombre de permisos, propio de una organización.
type Role struct {
	ID             id.ID
	OrganizationID id.ID
	Code           org.Code
	Name           org.Name
	Permissions    []Permission
	CreatedAt      time.Time
}

func NewRole(o org.Organization, code, name string, perms []Permission, now time.Time) (Role, error) {
	c, err := org.ParseCode(code)
	if err != nil {
		return Role{}, err
	}
	n, err := org.ParseName(name)
	if err != nil {
		return Role{}, err
	}
	ps, err := normalize(perms)
	if err != nil {
		return Role{}, err
	}
	rid, err := id.New()
	if err != nil {
		return Role{}, err
	}
	return Role{ID: rid, OrganizationID: o.ID, Code: c, Name: n, Permissions: ps, CreatedAt: now.UTC()}, nil
}

func (r Role) Has(p Permission) bool { return slices.Contains(r.Permissions, p) }

// Edit devuelve el rol con permisos agregados y quitados.
func (r Role) Edit(add, remove []Permission) (Role, error) {
	ps, err := normalize(append(slices.Clone(r.Permissions), add...))
	if err != nil {
		return Role{}, err
	}
	if _, err := normalize(remove); err != nil {
		return Role{}, err
	}
	r.Permissions = slices.DeleteFunc(ps, func(p Permission) bool { return slices.Contains(remove, p) })
	return r, nil
}

// RoleTemplate es un rol sembrado al configurar la organización; luego se
// edita como cualquier otro.
type RoleTemplate struct {
	Code, Name  string
	Permissions []Permission
}

// AdminRoleCode es la plantilla que recibe el administrador inicial.
const AdminRoleCode = "ADMIN_ORG"

// Templates devuelve las plantillas de la sección 26.3. Ninguna incluye
// permisos extraordinarios.
func Templates() []RoleTemplate {
	var admin []Permission
	for _, info := range catalog {
		if !info.Extraordinary {
			admin = append(admin, info.Permission)
		}
	}
	return []RoleTemplate{
		{AdminRoleCode, "Administrador de organización", admin},
		{"ADMIN_EMPRESA", "Administrador de empresa", []Permission{PermOrgRead, PermBranchCreate, PermWarehouseCreate, PermUserRead, PermUserManage, PermProductRead, PermStockRead, PermStockAdjust, PermTransferCreate,
			PermReservation, PermBlock, PermCountManage, PermCountApprove, PermPolicyManage,
			PermPurchaseRead, PermPurchaseManage, PermReceiptConfirm, PermReplacementCost, PermRateRead, PermRateManage}},
		{"ENCARGADO", "Encargado", []Permission{PermOrgRead, PermWarehouseCreate, PermUserRead, PermProductRead, PermStockRead, PermStockAdjust, PermTransferCreate,
			PermReservation, PermBlock, PermCountManage, PermCountApprove,
			PermPurchaseRead, PermPurchaseManage, PermReceiptConfirm, PermReplacementCost, PermRateRead, PermRateManage}},
		{"CAJERO", "Cajero", []Permission{PermOrgRead, PermProductRead, PermStockRead, PermReservation, PermRateRead}},
		{"ALMACENISTA", "Almacenista", []Permission{PermOrgRead, PermProductRead, PermStockRead, PermStockAdjust, PermTransferCreate,
			PermReservation, PermBlock, PermCountManage, PermPurchaseRead, PermPurchaseManage, PermRateRead}},
		{"OPERADOR_TECNICO", "Operador técnico", []Permission{PermOrgRead, PermProductRead, PermStockRead}},
	}
}

// Assignment concede un rol a un usuario en un ámbito.
type Assignment struct {
	ID        id.ID
	UserID    id.ID
	RoleID    id.ID
	Scope     scope.BusinessScope
	CreatedAt time.Time
	CreatedBy id.ID
}

func NewAssignment(u User, r Role, s scope.BusinessScope, by id.ID, now time.Time) (Assignment, error) {
	if s.Level() == 0 || s.Organization() != u.OrganizationID || r.OrganizationID != u.OrganizationID {
		return Assignment{}, fmt.Errorf("%w: el usuario, el rol y el ámbito deben ser de la misma organización", ErrInvalid)
	}
	aid, err := id.New()
	if err != nil {
		return Assignment{}, err
	}
	return Assignment{ID: aid, UserID: u.ID, RoleID: r.ID, Scope: s, CreatedAt: now.UTC(), CreatedBy: by}, nil
}

// Grant es una asignación resuelta: los permisos del rol en su ámbito.
type Grant struct {
	RoleCode    org.Code
	Scope       scope.BusinessScope
	Permissions []Permission
}

// Principal es el usuario autenticado de una operación. Solo el servicio de
// autenticación lo construye a partir de una sesión válida; el ámbito nunca
// proviene de datos aportados por el cliente (sección 26.3).
type Principal struct {
	UserID         id.ID
	OrganizationID id.ID
	Username       Username
	SessionID      id.ID
	Grants         []Grant
}

// Can indica si alguna asignación concede perm en un ámbito que contiene a
// target.
func (p Principal) Can(perm Permission, target scope.BusinessScope) bool {
	if p.UserID.IsZero() || target.Organization() != p.OrganizationID {
		return false
	}
	for _, g := range p.Grants {
		if slices.Contains(g.Permissions, perm) && g.Scope.Contains(target) {
			return true
		}
	}
	return false
}

// Authorize es Can como error que nombra el permiso faltante.
func (p Principal) Authorize(perm Permission, target scope.BusinessScope, what string) error {
	if p.Can(perm, target) {
		return nil
	}
	return fmt.Errorf("%w: se requiere %s en %s", ErrDenied, perm, what)
}

// Sees indica si perm alcanza a target por arriba o por abajo: un usuario
// de sucursal ve su empresa y su organización, además de lo que contiene.
func (p Principal) Sees(perm Permission, target scope.BusinessScope) bool {
	if p.UserID.IsZero() || target.Organization() != p.OrganizationID {
		return false
	}
	for _, g := range p.Grants {
		if slices.Contains(g.Permissions, perm) && (g.Scope.Contains(target) || target.Contains(g.Scope)) {
			return true
		}
	}
	return false
}

// HasAnywhere indica si perm está concedido en algún ámbito.
func (p Principal) HasAnywhere(perm Permission) bool {
	if p.UserID.IsZero() {
		return false
	}
	for _, g := range p.Grants {
		if slices.Contains(g.Permissions, perm) {
			return true
		}
	}
	return false
}

// OrganizationScope es el ámbito de la organización del principal.
func (p Principal) OrganizationScope() scope.BusinessScope {
	s, _ := scope.New(p.OrganizationID, id.ID{}, id.ID{}, id.ID{})
	return s
}

// Package org modela la jerarquía de DEC-008.A: organización (tenant) →
// empresas legales → sucursales → almacenes.
//
// Los constructores reciben la entidad padre, no IDs sueltos, para que cada
// entidad herede una ruta coherente. Que el padre exista y pertenezca al
// mismo ámbito lo garantizan además la capa de aplicación y las claves
// foráneas compuestas de la base.
package org

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

var ErrInvalid = errors.New("dato organizacional inválido")

// Status indica si una entidad puede usarse en operaciones nuevas.
type Status string

const (
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
)

// Code es el identificador legible que escriben los usuarios: único dentro
// de su padre, distinto del ID técnico.
type Code struct{ v string }

var codePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,31}$`)

// ParseCode normaliza a mayúsculas sin espacios alrededor.
func ParseCode(s string) (Code, error) {
	v := strings.ToUpper(strings.TrimSpace(s))
	if !codePattern.MatchString(v) {
		return Code{}, fmt.Errorf("%w: código %q (1 a 32 caracteres A-Z, 0-9, guion o guion bajo)", ErrInvalid, s)
	}
	return Code{v}, nil
}

func (c Code) String() string { return c.v }
func (c Code) IsZero() bool   { return c.v == "" }

// MaxNameLength es la longitud máxima de un nombre, en caracteres.
const MaxNameLength = 120

// Name es un nombre visible: espacios normalizados, sin caracteres de control.
type Name struct{ v string }

func ParseName(s string) (Name, error) {
	for _, r := range s {
		if unicode.IsControl(r) {
			return Name{}, fmt.Errorf("%w: el nombre contiene caracteres de control", ErrInvalid)
		}
	}
	v := strings.Join(strings.Fields(s), " ")
	if v == "" || utf8.RuneCountInString(v) > MaxNameLength {
		return Name{}, fmt.Errorf("%w: el nombre debe tener entre 1 y %d caracteres", ErrInvalid, MaxNameLength)
	}
	return Name{v}, nil
}

func (n Name) String() string { return n.v }

// RIF es el Registro de Información Fiscal venezolano en forma canónica
// L-NNNNNNNN-D. Solo se valida el formato: el dígito verificador y el uso
// fiscal quedan pendientes de validación profesional (sección 22.3).
type RIF struct{ v string }

var rifPattern = regexp.MustCompile(`^([VEJPGC])-?(\d{8})-?(\d)$`)

func ParseRIF(s string) (RIF, error) {
	m := rifPattern.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
	if m == nil {
		return RIF{}, fmt.Errorf("%w: RIF %q (formato J-12345678-9; letras V, E, J, P, G o C)", ErrInvalid, s)
	}
	return RIF{m[1] + "-" + m[2] + "-" + m[3]}, nil
}

func (r RIF) String() string { return r.v }

// IsZero indica que la empresa no tiene RIF registrado.
func (r RIF) IsZero() bool { return r.v == "" }

// Organization es el tenant: frontera entre clientes de BalanzaPro.
type Organization struct {
	ID        id.ID
	Code      Code
	Name      Name
	Status    Status
	CreatedAt time.Time
}

// Company es una empresa legal: emite documentos y agrupa sucursales.
type Company struct {
	ID             id.ID
	OrganizationID id.ID
	Code           Code
	LegalName      Name
	RIF            RIF
	Status         Status
	CreatedAt      time.Time
}

// Branch es una sucursal: confirma ventas y movimientos físicos.
type Branch struct {
	ID             id.ID
	OrganizationID id.ID
	CompanyID      id.ID
	Code           Code
	Name           Name
	Status         Status
	CreatedAt      time.Time
}

// Warehouse es un almacén: ubicación de stock con CPP propio.
type Warehouse struct {
	ID             id.ID
	OrganizationID id.ID
	CompanyID      id.ID
	BranchID       id.ID
	Code           Code
	Name           Name
	Status         Status
	CreatedAt      time.Time
}

func NewOrganization(code, name string, now time.Time) (Organization, error) {
	c, n, err := codeAndName(code, name, now)
	if err != nil {
		return Organization{}, err
	}
	return Organization{ID: id.MustNew(), Code: c, Name: n, Status: StatusActive, CreatedAt: now.UTC()}, nil
}

func NewCompany(parent Organization, code, legalName string, rif RIF, now time.Time) (Company, error) {
	if parent.ID.IsZero() {
		return Company{}, fmt.Errorf("%w: la empresa necesita una organización", ErrInvalid)
	}
	c, n, err := codeAndName(code, legalName, now)
	if err != nil {
		return Company{}, err
	}
	return Company{ID: id.MustNew(), OrganizationID: parent.ID, Code: c, LegalName: n, RIF: rif, Status: StatusActive, CreatedAt: now.UTC()}, nil
}

func NewBranch(parent Company, code, name string, now time.Time) (Branch, error) {
	if parent.ID.IsZero() || parent.OrganizationID.IsZero() {
		return Branch{}, fmt.Errorf("%w: la sucursal necesita una empresa", ErrInvalid)
	}
	c, n, err := codeAndName(code, name, now)
	if err != nil {
		return Branch{}, err
	}
	return Branch{ID: id.MustNew(), OrganizationID: parent.OrganizationID, CompanyID: parent.ID, Code: c, Name: n, Status: StatusActive, CreatedAt: now.UTC()}, nil
}

func NewWarehouse(parent Branch, code, name string, now time.Time) (Warehouse, error) {
	if parent.ID.IsZero() || parent.CompanyID.IsZero() || parent.OrganizationID.IsZero() {
		return Warehouse{}, fmt.Errorf("%w: el almacén necesita una sucursal", ErrInvalid)
	}
	c, n, err := codeAndName(code, name, now)
	if err != nil {
		return Warehouse{}, err
	}
	return Warehouse{ID: id.MustNew(), OrganizationID: parent.OrganizationID, CompanyID: parent.CompanyID, BranchID: parent.ID,
		Code: c, Name: n, Status: StatusActive, CreatedAt: now.UTC()}, nil
}

func codeAndName(code, name string, now time.Time) (Code, Name, error) {
	if now.IsZero() {
		return Code{}, Name{}, fmt.Errorf("%w: falta la fecha de creación", ErrInvalid)
	}
	c, err := ParseCode(code)
	if err != nil {
		return Code{}, Name{}, err
	}
	n, err := ParseName(name)
	if err != nil {
		return Code{}, Name{}, err
	}
	return c, n, nil
}

// Scope devuelve el ámbito de cada entidad en la jerarquía.
func (o Organization) Scope() scope.BusinessScope { return mustScope(o.ID, id.ID{}, id.ID{}, id.ID{}) }
func (c Company) Scope() scope.BusinessScope {
	return mustScope(c.OrganizationID, c.ID, id.ID{}, id.ID{})
}
func (b Branch) Scope() scope.BusinessScope {
	return mustScope(b.OrganizationID, b.CompanyID, b.ID, id.ID{})
}
func (w Warehouse) Scope() scope.BusinessScope {
	return mustScope(w.OrganizationID, w.CompanyID, w.BranchID, w.ID)
}

// mustScope devuelve un ámbito vacío (que no concede nada) si la entidad no
// fue construida por los constructores de este paquete.
func mustScope(o, c, b, w id.ID) scope.BusinessScope {
	s, err := scope.New(o, c, b, w)
	if err != nil {
		return scope.BusinessScope{}
	}
	return s
}

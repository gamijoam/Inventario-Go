// Package scope define BusinessScope, el ámbito de negocio que acompaña a
// toda operación sensible: organización → empresa legal → sucursal →
// almacén (DEC-008.A).
//
// El ámbito solo describe una ruta coherente en la jerarquía. Que la
// sucursal pertenezca de verdad a la empresa lo garantizan la persistencia
// (claves compuestas, RLS) y los casos de uso, no este tipo.
package scope

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gamijoam/inventario-go/internal/domain/id"
)

var (
	ErrInvalidScope  = errors.New("ámbito de negocio inválido")
	ErrScopeTooBroad = errors.New("la operación requiere un ámbito más específico")
)

// Level es la profundidad de un ámbito en la jerarquía.
type Level int

const (
	LevelOrganization Level = iota + 1
	LevelCompany
	LevelBranch
	LevelWarehouse
)

func (l Level) String() string {
	switch l {
	case LevelOrganization:
		return "organization"
	case LevelCompany:
		return "company"
	case LevelBranch:
		return "branch"
	case LevelWarehouse:
		return "warehouse"
	default:
		return fmt.Sprintf("Level(%d)", int(l))
	}
}

// BusinessScope es inmutable. El valor cero es un ámbito vacío e inválido:
// no concede ni satisface nada.
type BusinessScope struct {
	organization, company, branch, warehouse id.ID
}

// New construye un ámbito. Los niveles más profundos pueden quedar en cero,
// pero no puede haber huecos: un almacén exige sucursal, empresa y
// organización.
func New(organization, company, branch, warehouse id.ID) (BusinessScope, error) {
	path := [...]id.ID{organization, company, branch, warehouse}
	if organization.IsZero() {
		return BusinessScope{}, fmt.Errorf("%w: falta la organización", ErrInvalidScope)
	}
	for i := 1; i < len(path); i++ {
		if !path[i].IsZero() && path[i-1].IsZero() {
			return BusinessScope{}, fmt.Errorf("%w: %v sin %v", ErrInvalidScope, Level(i+1), Level(i))
		}
		for j := 0; j < i; j++ {
			if !path[i].IsZero() && path[i] == path[j] {
				return BusinessScope{}, fmt.Errorf("%w: el ID %s se repite en %v y %v", ErrInvalidScope, path[i], Level(j+1), Level(i+1))
			}
		}
	}
	return BusinessScope{organization: organization, company: company, branch: branch, warehouse: warehouse}, nil
}

func (s BusinessScope) Organization() id.ID { return s.organization }
func (s BusinessScope) Company() id.ID      { return s.company }
func (s BusinessScope) Branch() id.ID       { return s.branch }
func (s BusinessScope) Warehouse() id.ID    { return s.warehouse }

// Level devuelve el nivel más profundo asignado, o 0 si el ámbito está vacío.
func (s BusinessScope) Level() Level {
	switch {
	case !s.warehouse.IsZero():
		return LevelWarehouse
	case !s.branch.IsZero():
		return LevelBranch
	case !s.company.IsZero():
		return LevelCompany
	case !s.organization.IsZero():
		return LevelOrganization
	default:
		return 0
	}
}

// Contains indica si s abarca a other: other es s o uno de sus
// descendientes. Sirve para comprobar que un permiso concedido en s cubre
// una operación en other.
func (s BusinessScope) Contains(other BusinessScope) bool {
	if s.Level() == 0 || other.Level() == 0 || other.Level() < s.Level() {
		return false
	}
	mine := [...]id.ID{s.organization, s.company, s.branch, s.warehouse}
	theirs := [...]id.ID{other.organization, other.company, other.branch, other.warehouse}
	for i := 0; i < int(s.Level()); i++ {
		if mine[i] != theirs[i] {
			return false
		}
	}
	return true
}

// Require falla si el ámbito no llega al nivel que la operación exige, por
// ejemplo LevelWarehouse para un movimiento de stock.
func (s BusinessScope) Require(level Level) error {
	if s.Level() == 0 {
		return ErrInvalidScope
	}
	if s.Level() < level {
		return fmt.Errorf("%w: se requiere %v, el ámbito es %v", ErrScopeTooBroad, level, s.Level())
	}
	return nil
}

func (s BusinessScope) String() string {
	var parts []string
	for _, p := range []struct {
		name string
		v    id.ID
	}{{"org", s.organization}, {"company", s.company}, {"branch", s.branch}, {"warehouse", s.warehouse}} {
		if !p.v.IsZero() {
			parts = append(parts, p.name+"="+p.v.String())
		}
	}
	return strings.Join(parts, " ")
}

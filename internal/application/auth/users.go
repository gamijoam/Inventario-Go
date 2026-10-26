package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"

	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

// AssignmentView es una asignación con códigos legibles.
type AssignmentView struct {
	identity.Assignment
	RoleCode org.Code
	Where    ScopeRef
}

// UserView es un usuario con sus asignaciones y estado de bloqueo.
type UserView struct {
	identity.User
	Assignments []AssignmentView
	Locked      bool
}

// Me describe al usuario de la sesión actual.
func (s *Service) Me(ctx context.Context, a Authenticated) (UserView, error) {
	var out UserView
	err := s.store.InTx(ctx, func(r Repository) error {
		var err error
		out, err = s.view(ctx, r, a.User)
		return err
	})
	return out, err
}

// ListUsers devuelve los usuarios visibles: todos con lectura en la
// organización; si no, los que tienen alguna asignación dentro del ámbito
// del lector.
func (s *Service) ListUsers(ctx context.Context, p identity.Principal) ([]UserView, error) {
	if !p.HasAnywhere(identity.PermUserRead) {
		return nil, fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermUserRead)
	}
	var out []UserView
	err := s.store.InTx(ctx, func(r Repository) error {
		users, err := r.Users(ctx, p.OrganizationID)
		if err != nil {
			return err
		}
		all := p.Can(identity.PermUserRead, p.OrganizationScope())
		for _, u := range users {
			v, err := s.view(ctx, r, u)
			if err != nil {
				return err
			}
			if all || slices.ContainsFunc(v.Assignments, func(a AssignmentView) bool { return p.Can(identity.PermUserRead, a.Scope) }) {
				out = append(out, v)
			}
		}
		return nil
	})
	return out, err
}

func (s *Service) view(ctx context.Context, r Repository, u identity.User) (UserView, error) {
	v := UserView{User: u}
	cred, err := r.Credential(ctx, u.OrganizationID, u.ID)
	if err != nil {
		return v, err
	}
	v.Locked = s.lockout.Locked(cred.Lockout, s.now())
	roles, err := rolesByID(ctx, r, u.OrganizationID)
	if err != nil {
		return v, err
	}
	all, err := r.Assignments(ctx, u.OrganizationID)
	if err != nil {
		return v, err
	}
	for _, a := range all {
		if a.UserID != u.ID {
			continue
		}
		where, err := describeScope(ctx, r, a.Scope)
		if err != nil {
			return v, err
		}
		v.Assignments = append(v.Assignments, AssignmentView{Assignment: a, RoleCode: roles[a.RoleID].Code, Where: where})
	}
	return v, nil
}

// NewUserInput describe un usuario nuevo; la contraseña inicial la fija
// quien lo crea.
type NewUserInput struct {
	Username, DisplayName, Password string
}

// CreateUser crea un usuario sin asignaciones: no puede hacer nada hasta que
// se le asigne un rol.
func (s *Service) CreateUser(ctx context.Context, p identity.Principal, in NewUserInput) (identity.User, error) {
	if !p.HasAnywhere(identity.PermUserManage) {
		return identity.User{}, fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermUserManage)
	}
	username, err := identity.ParseUsername(in.Username)
	if err != nil {
		return identity.User{}, err
	}
	if err := identity.CheckPassword(in.Password, username); err != nil {
		return identity.User{}, err
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return identity.User{}, err
	}
	var u identity.User
	err = s.store.InTx(ctx, func(r Repository) error {
		o, err := r.Organization(ctx, p.OrganizationID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if u, err = identity.NewUser(o, in.Username, in.DisplayName, now); err != nil {
			return err
		}
		if err := r.InsertUser(ctx, u); err != nil {
			return err
		}
		if err := r.InsertCredential(ctx, o.ID, identity.Credential{UserID: u.ID, Hash: hash, ChangedAt: now}); err != nil {
			return err
		}
		return s.event(ctx, r, o.ID, EventUserCreated, p.UserID, u.ID, u.Username.String())
	})
	return u, err
}

// target carga un usuario por nombre y comprueba que p puede gestionarlo:
// necesita identity.user.manage en el ámbito de cada asignación del usuario
// (o en la organización si no tiene ninguna). Así un administrador de
// empresa no puede desactivar a uno de organización.
func (s *Service) target(ctx context.Context, r Repository, p identity.Principal, username string) (UserView, error) {
	uname, err := identity.ParseUsername(username)
	if err != nil {
		return UserView{}, err
	}
	u, err := r.UserByUsername(ctx, p.OrganizationID, uname)
	if err != nil {
		return UserView{}, err
	}
	if u.ID == p.UserID {
		return UserView{}, ErrSelf
	}
	v, err := s.view(ctx, r, u)
	if err != nil {
		return UserView{}, err
	}
	if len(v.Assignments) == 0 {
		return v, p.Authorize(identity.PermUserManage, p.OrganizationScope(), "toda la organización (el usuario no tiene asignaciones)")
	}
	for _, a := range v.Assignments {
		if err := p.Authorize(identity.PermUserManage, a.Scope, a.Where.String()); err != nil {
			return UserView{}, err
		}
	}
	return v, nil
}

// SetUserActive activa o desactiva un usuario. Desactivar revoca sus
// sesiones.
func (s *Service) SetUserActive(ctx context.Context, p identity.Principal, username string, active bool) (identity.User, error) {
	var out identity.User
	err := s.store.InTx(ctx, func(r Repository) error {
		v, err := s.target(ctx, r, p, username)
		if err != nil {
			return err
		}
		u := v.User
		kind := EventUserEnabled
		u.Status = identity.StatusActive
		if !active {
			kind, u.Status = EventUserDisabled, identity.StatusDisabled
		}
		if err := r.UpdateUser(ctx, u); err != nil {
			return err
		}
		if !active {
			if err := r.RevokeSessions(ctx, u.OrganizationID, u.ID, s.now().UTC(), id.ID{}); err != nil {
				return err
			}
			if err := ensureAdmin(ctx, r, u.OrganizationID); err != nil {
				return err
			}
		}
		out = u
		return s.event(ctx, r, u.OrganizationID, kind, p.UserID, u.ID, "")
	})
	return out, err
}

// Unlock levanta el bloqueo por intentos fallidos.
func (s *Service) Unlock(ctx context.Context, p identity.Principal, username string) error {
	return s.store.InTx(ctx, func(r Repository) error {
		v, err := s.target(ctx, r, p, username)
		if err != nil {
			return err
		}
		cred, err := r.Credential(ctx, v.OrganizationID, v.ID)
		if err != nil {
			return err
		}
		cred.Lockout = s.lockout.Succeed()
		if err := r.UpdateCredential(ctx, v.OrganizationID, cred); err != nil {
			return err
		}
		return s.event(ctx, r, v.OrganizationID, EventUserUnlocked, p.UserID, v.ID, "")
	})
}

// ResetPassword fija una contraseña nueva a otro usuario, lo desbloquea y
// revoca sus sesiones.
func (s *Service) ResetPassword(ctx context.Context, p identity.Principal, username, password string) error {
	uname, err := identity.ParseUsername(username)
	if err != nil {
		return err
	}
	if err := identity.CheckPassword(password, uname); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return err
	}
	return s.store.InTx(ctx, func(r Repository) error {
		v, err := s.target(ctx, r, p, username)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if err := r.UpdateCredential(ctx, v.OrganizationID, identity.Credential{UserID: v.ID, Hash: hash, ChangedAt: now}); err != nil {
			return err
		}
		if err := r.RevokeSessions(ctx, v.OrganizationID, v.ID, now, id.ID{}); err != nil {
			return err
		}
		return s.event(ctx, r, v.OrganizationID, EventPasswordReset, p.UserID, v.ID, "")
	})
}

// ChangePassword cambia la contraseña propia. La actual se verifica con las
// mismas reglas de bloqueo que el inicio de sesión; se revocan las demás
// sesiones del usuario.
func (s *Service) ChangePassword(ctx context.Context, a Authenticated, current, next string) error {
	if err := identity.CheckPassword(next, a.User.Username); err != nil {
		return err
	}
	var cred identity.Credential
	err := s.store.InTx(ctx, func(r Repository) error {
		var err error
		cred, err = r.Credential(ctx, a.User.OrganizationID, a.User.ID)
		return err
	})
	if err != nil {
		return err
	}
	ok, _, err := s.hasher.Verify(current, cred.Hash)
	if err != nil {
		return err
	}
	hash, err := s.hasher.Hash(next)
	if err != nil {
		return err
	}
	changed := false
	err = s.store.InTx(ctx, func(r Repository) error {
		now := s.now().UTC()
		orgID, uid := a.User.OrganizationID, a.User.ID
		cred, err := r.Credential(ctx, orgID, uid)
		if err != nil {
			return err
		}
		if s.lockout.Locked(cred.Lockout, now) {
			return nil
		}
		if !ok {
			cred.Lockout = s.lockout.Fail(cred.Lockout, now)
			if err := r.UpdateCredential(ctx, orgID, cred); err != nil {
				return err
			}
			return s.event(ctx, r, orgID, EventLoginFailed, uid, uid, "contraseña actual incorrecta al cambiarla")
		}
		if err := r.UpdateCredential(ctx, orgID, identity.Credential{UserID: uid, Hash: hash, ChangedAt: now}); err != nil {
			return err
		}
		if err := r.RevokeSessions(ctx, orgID, uid, now, a.Session.ID); err != nil {
			return err
		}
		changed = true
		return s.event(ctx, r, orgID, EventPasswordChanged, uid, uid, "")
	})
	if err != nil {
		return err
	}
	if !changed {
		return ErrInvalidCredentials
	}
	return nil
}

// canDelegate: solo se concede lo que uno mismo tiene en ese ámbito. Los
// permisos extraordinarios no los tiene nadie por plantilla; los delega
// expresamente quien gestiona roles en toda la organización.
func canDelegate(p identity.Principal, perm identity.Permission, sc scope.BusinessScope) bool {
	return p.Can(perm, sc) || (perm.Extraordinary() && p.Can(identity.PermRoleManage, p.OrganizationScope()))
}

// Assign concede roleCode a username en el ámbito where.
func (s *Service) Assign(ctx context.Context, p identity.Principal, username, roleCode string, where ScopeRef) (AssignmentView, error) {
	var out AssignmentView
	err := s.store.InTx(ctx, func(r Repository) error {
		uname, err := identity.ParseUsername(username)
		if err != nil {
			return err
		}
		u, err := r.UserByUsername(ctx, p.OrganizationID, uname)
		if err != nil {
			return err
		}
		if u.ID == p.UserID {
			return ErrSelf
		}
		code, err := org.ParseCode(roleCode)
		if err != nil {
			return err
		}
		role, err := r.RoleByCode(ctx, p.OrganizationID, code)
		if err != nil {
			return err
		}
		sc, norm, err := resolveScope(ctx, r, p.OrganizationID, where)
		if err != nil {
			return err
		}
		if err := p.Authorize(identity.PermUserManage, sc, norm.String()); err != nil {
			return err
		}
		for _, perm := range role.Permissions {
			if !canDelegate(p, perm, sc) {
				return fmt.Errorf("%w: el rol %s incluye %s, que usted no tiene en %s", identity.ErrDenied, role.Code, perm, norm)
			}
		}
		a, err := identity.NewAssignment(u, role, sc, p.UserID, s.now())
		if err != nil {
			return err
		}
		if err := r.InsertAssignment(ctx, a); err != nil {
			return err
		}
		out = AssignmentView{Assignment: a, RoleCode: role.Code, Where: norm}
		return s.event(ctx, r, p.OrganizationID, EventRoleAssigned, p.UserID, u.ID, role.Code.String()+" en "+norm.String())
	})
	return out, err
}

// Unassign retira la asignación de roleCode a username en where.
func (s *Service) Unassign(ctx context.Context, p identity.Principal, username, roleCode string, where ScopeRef) error {
	return s.store.InTx(ctx, func(r Repository) error {
		uname, err := identity.ParseUsername(username)
		if err != nil {
			return err
		}
		u, err := r.UserByUsername(ctx, p.OrganizationID, uname)
		if err != nil {
			return err
		}
		if u.ID == p.UserID {
			return ErrSelf
		}
		code, err := org.ParseCode(roleCode)
		if err != nil {
			return err
		}
		sc, norm, err := resolveScope(ctx, r, p.OrganizationID, where)
		if err != nil {
			return err
		}
		if err := p.Authorize(identity.PermUserManage, sc, norm.String()); err != nil {
			return err
		}
		v, err := s.view(ctx, r, u)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(v.Assignments, func(a AssignmentView) bool { return a.RoleCode == code && a.Scope == sc })
		if i < 0 {
			return fmt.Errorf("%w: %s no tiene el rol %s en %s", apporg.ErrNotFound, u.Username, code, norm)
		}
		if err := r.DeleteAssignment(ctx, p.OrganizationID, v.Assignments[i].ID); err != nil {
			return err
		}
		if err := ensureAdmin(ctx, r, p.OrganizationID); err != nil {
			return err
		}
		return s.event(ctx, r, p.OrganizationID, EventRoleUnassigned, p.UserID, u.ID, code.String()+" en "+norm.String())
	})
}

// ListRoles devuelve los roles de la organización.
func (s *Service) ListRoles(ctx context.Context, p identity.Principal) ([]identity.Role, error) {
	if !p.HasAnywhere(identity.PermUserRead) && !p.HasAnywhere(identity.PermRoleManage) {
		return nil, fmt.Errorf("%w: se requiere %s", identity.ErrDenied, identity.PermUserRead)
	}
	var out []identity.Role
	err := s.store.InTx(ctx, func(r Repository) error {
		var err error
		out, err = r.Roles(ctx, p.OrganizationID)
		return err
	})
	return out, err
}

func parsePermissions(raw []string) ([]identity.Permission, error) {
	out := make([]identity.Permission, 0, len(raw))
	for _, s := range raw {
		p, err := identity.ParsePermission(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// CreateRole crea un rol de la organización.
func (s *Service) CreateRole(ctx context.Context, p identity.Principal, code, name string, perms []string) (identity.Role, error) {
	ps, err := parsePermissions(perms)
	if err != nil {
		return identity.Role{}, err
	}
	var role identity.Role
	err = s.store.InTx(ctx, func(r Repository) error {
		o, err := r.Organization(ctx, p.OrganizationID)
		if err != nil {
			return err
		}
		if err := p.Authorize(identity.PermRoleManage, o.Scope(), "toda la organización"); err != nil {
			return err
		}
		if role, err = identity.NewRole(o, code, name, ps, s.now()); err != nil {
			return err
		}
		if err := r.InsertRole(ctx, role); err != nil {
			return err
		}
		return s.event(ctx, r, o.ID, EventRoleCreated, p.UserID, id.ID{}, describeRole(role))
	})
	return role, err
}

// EditRole agrega y quita permisos de un rol; el cambio afecta a todos sus
// usuarios desde el siguiente comando.
func (s *Service) EditRole(ctx context.Context, p identity.Principal, code string, add, remove []string) (identity.Role, error) {
	addP, err := parsePermissions(add)
	if err != nil {
		return identity.Role{}, err
	}
	removeP, err := parsePermissions(remove)
	if err != nil {
		return identity.Role{}, err
	}
	var out identity.Role
	err = s.store.InTx(ctx, func(r Repository) error {
		if err := p.Authorize(identity.PermRoleManage, p.OrganizationScope(), "toda la organización"); err != nil {
			return err
		}
		c, err := org.ParseCode(code)
		if err != nil {
			return err
		}
		role, err := r.RoleByCode(ctx, p.OrganizationID, c)
		if err != nil {
			return err
		}
		if out, err = role.Edit(addP, removeP); err != nil {
			return err
		}
		if err := r.UpdateRole(ctx, out); err != nil {
			return err
		}
		if err := ensureAdmin(ctx, r, p.OrganizationID); err != nil {
			return err
		}
		return s.event(ctx, r, p.OrganizationID, EventRoleUpdated, p.UserID, id.ID{}, describeRole(out))
	})
	return out, err
}

func describeRole(r identity.Role) string {
	s := r.Code.String() + ":"
	for _, p := range r.Permissions {
		s += " " + string(p)
	}
	return s
}

// IsDenied agrupa los errores de autorización.
func IsDenied(err error) bool {
	return errors.Is(err, identity.ErrDenied) || errors.Is(err, apporg.ErrForbidden)
}

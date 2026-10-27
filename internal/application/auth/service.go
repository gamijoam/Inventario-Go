// Package auth contiene los casos de uso de identidad: alta del
// administrador inicial, inicio y cierre de sesión, autenticación de cada
// comando, gestión de usuarios, asignaciones de rol y roles (sección 26.3).
//
// Toda operación de gestión recibe el principal autenticado y comprueba el
// permiso en el ámbito afectado. Nadie puede delegar permisos que no tiene,
// modificar sus propias asignaciones ni dejar la organización sin
// administrador.
package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

var (
	// ErrInvalidCredentials es la única respuesta a un inicio de sesión
	// fallido: no revela si el usuario existe, está desactivado o bloqueado
	// (SEC-04).
	ErrInvalidCredentials = errors.New("usuario o contraseña incorrectos, o cuenta bloqueada temporalmente")
	ErrSelf               = fmt.Errorf("%w: no puede modificar su propio estado, contraseña ni asignaciones con este comando", identity.ErrDenied)
	ErrLastAdmin          = fmt.Errorf("%w: la organización debe conservar al menos un administrador activo (%s y %s en toda la organización)",
		apporg.ErrConflict, identity.PermRoleManage, identity.PermUserManage)
)

// Repository amplía el puerto de la organización con identidad: ambos
// comparten transacción.
type Repository interface {
	apporg.Repository
	appcatalog.UnitWriter

	InsertUser(ctx context.Context, u identity.User) error
	UpdateUser(ctx context.Context, u identity.User) error
	User(ctx context.Context, orgID, userID id.ID) (identity.User, error)
	UserByUsername(ctx context.Context, orgID id.ID, username identity.Username) (identity.User, error)
	Users(ctx context.Context, orgID id.ID) ([]identity.User, error)

	InsertCredential(ctx context.Context, orgID id.ID, c identity.Credential) error
	UpdateCredential(ctx context.Context, orgID id.ID, c identity.Credential) error
	Credential(ctx context.Context, orgID, userID id.ID) (identity.Credential, error)

	InsertRole(ctx context.Context, r identity.Role) error
	UpdateRole(ctx context.Context, r identity.Role) error
	RoleByCode(ctx context.Context, orgID id.ID, code org.Code) (identity.Role, error)
	Roles(ctx context.Context, orgID id.ID) ([]identity.Role, error)

	InsertAssignment(ctx context.Context, a identity.Assignment) error
	DeleteAssignment(ctx context.Context, orgID, assignmentID id.ID) error
	Assignments(ctx context.Context, orgID id.ID) ([]identity.Assignment, error)

	// InsertSession guarda la sesión con el resumen de su token.
	InsertSession(ctx context.Context, s identity.Session, digest string) error
	UpdateSession(ctx context.Context, s identity.Session) error
	SessionByDigest(ctx context.Context, digest string) (identity.Session, error)
	// RevokeSessions revoca las sesiones activas del usuario salvo except.
	RevokeSessions(ctx context.Context, orgID, userID id.ID, at time.Time, except id.ID) error

	InsertEvent(ctx context.Context, e Event) error
}

type Store interface {
	InTx(ctx context.Context, fn func(Repository) error) error
}

// PasswordHasher genera y comprueba verificadores de contraseña.
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encoded string) (ok, needsRehash bool, err error)
}

// TokenSource genera tokens de sesión y su resumen para guardar.
type TokenSource interface {
	New() (token, digest string, err error)
	Digest(token string) string
}

// Tipos de evento de seguridad (sección 26.9).
const (
	EventSetup           = "setup.completed"
	EventLoginSucceeded  = "login.succeeded"
	EventLoginFailed     = "login.failed"
	EventAccountLocked   = "login.locked"
	EventLogout          = "logout"
	EventUserCreated     = "user.created"
	EventUserDisabled    = "user.disabled"
	EventUserEnabled     = "user.enabled"
	EventUserUnlocked    = "user.unlocked"
	EventPasswordReset   = "user.password_reset"
	EventPasswordChanged = "user.password_changed"
	EventRoleAssigned    = "role.assigned"
	EventRoleUnassigned  = "role.unassigned"
	EventRoleCreated     = "role.created"
	EventRoleUpdated     = "role.updated"
)

// Event es un registro de seguridad append-only. Nunca contiene
// contraseñas, tokens ni verificadores.
type Event struct {
	ID             id.ID
	OrganizationID id.ID
	Kind           string
	ActorID        id.ID // cero si no hay usuario autenticado
	SubjectID      id.ID // usuario afectado, si aplica
	Detail         string
	At             time.Time
}

type Service struct {
	store   Store
	hasher  PasswordHasher
	tokens  TokenSource
	now     func() time.Time
	lockout identity.LockoutPolicy
	session identity.SessionPolicy
}

func NewService(store Store, hasher PasswordHasher, tokens TokenSource, now func() time.Time) *Service {
	return &Service{store: store, hasher: hasher, tokens: tokens, now: now,
		lockout: identity.DefaultLockout, session: identity.DefaultSession}
}

func (s *Service) event(ctx context.Context, r Repository, orgID id.ID, kind string, actor, subject id.ID, detail string) error {
	eid, err := id.New()
	if err != nil {
		return err
	}
	return r.InsertEvent(ctx, Event{ID: eid, OrganizationID: orgID, Kind: kind, ActorID: actor, SubjectID: subject, Detail: detail, At: s.now().UTC()})
}

// AdminInput es el primer usuario de la instalación.
type AdminInput struct {
	Username, DisplayName, Password string
}

// SetupResult es el resultado de Setup. Si la instalación ya tenía
// organización (creada antes de BP-M1-002) pero ningún usuario, Completed es
// true, Warehouse queda vacío y solo se agregan roles y administrador.
type SetupResult struct {
	apporg.Context
	Warehouse org.Warehouse
	Admin     identity.User
	Roles     []identity.Role
	Completed bool
}

// Setup crea la estructura organizacional, las plantillas de rol, las
// unidades de medida base y el administrador inicial con ADMIN_ORG en toda
// la organización, en una sola transacción.
func (s *Service) Setup(ctx context.Context, in apporg.BootstrapInput, admin AdminInput) (SetupResult, error) {
	username, err := identity.ParseUsername(admin.Username)
	if err != nil {
		return SetupResult{}, err
	}
	if _, err := org.ParseName(admin.DisplayName); err != nil {
		return SetupResult{}, err
	}
	if err := identity.CheckPassword(admin.Password, username); err != nil {
		return SetupResult{}, err
	}
	hash, err := s.hasher.Hash(admin.Password)
	if err != nil {
		return SetupResult{}, err
	}
	now := s.now().UTC()
	var out SetupResult
	err = s.store.InTx(ctx, func(r Repository) error {
		cur, err := apporg.CurrentTx(ctx, r)
		switch {
		case errors.Is(err, apporg.ErrNotBootstrapped):
			if strings.TrimSpace(in.OrganizationName) == "" {
				return fmt.Errorf("%w: falta el nombre de la organización", org.ErrInvalid)
			}
			b, err := apporg.BootstrapTx(ctx, r, in, now)
			if err != nil {
				return err
			}
			out.Context, out.Warehouse = b.Context, b.Warehouse
		case err != nil:
			return err
		default:
			users, err := r.Users(ctx, cur.Organization.ID)
			if err != nil {
				return err
			}
			if len(users) > 0 {
				return apporg.ErrAlreadyBootstrapped
			}
			out.Context, out.Completed = cur, true
		}
		o := out.Organization
		if out.Roles, err = s.seedRoles(ctx, r, o, now); err != nil {
			return err
		}
		if err := appcatalog.SeedUnits(ctx, r, o, now); err != nil {
			return err
		}
		u, err := identity.NewUser(o, username.String(), admin.DisplayName, now)
		if err != nil {
			return err
		}
		if err := r.InsertUser(ctx, u); err != nil {
			return err
		}
		if err := r.InsertCredential(ctx, o.ID, identity.Credential{UserID: u.ID, Hash: hash, ChangedAt: now}); err != nil {
			return err
		}
		adminRole, err := r.RoleByCode(ctx, o.ID, mustCode(identity.AdminRoleCode))
		if err != nil {
			return err
		}
		a, err := identity.NewAssignment(u, adminRole, o.Scope(), u.ID, now)
		if err != nil {
			return err
		}
		if err := r.InsertAssignment(ctx, a); err != nil {
			return err
		}
		out.Admin = u
		return s.event(ctx, r, o.ID, EventSetup, u.ID, u.ID, "administrador inicial "+u.Username.String())
	})
	if err != nil {
		return SetupResult{}, err
	}
	return out, nil
}

// seedRoles crea las plantillas que falten.
func (s *Service) seedRoles(ctx context.Context, r Repository, o org.Organization, now time.Time) ([]identity.Role, error) {
	for _, tpl := range identity.Templates() {
		if _, err := r.RoleByCode(ctx, o.ID, mustCode(tpl.Code)); err == nil {
			continue
		} else if !errors.Is(err, apporg.ErrNotFound) {
			return nil, err
		}
		role, err := identity.NewRole(o, tpl.Code, tpl.Name, tpl.Permissions, now)
		if err != nil {
			return nil, err
		}
		if err := r.InsertRole(ctx, role); err != nil {
			return nil, err
		}
	}
	return r.Roles(ctx, o.ID)
}

func mustCode(s string) org.Code {
	c, err := org.ParseCode(s)
	if err != nil {
		panic(err)
	}
	return c
}

// LoginResult contiene el token, que solo conoce quien inició sesión.
type LoginResult struct {
	Token   string
	Session identity.Session
	User    identity.User
}

// Login verifica la contraseña y abre una sesión. La verificación lenta
// ocurre fuera de la transacción para no retener la base.
func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	uname, uerr := identity.ParseUsername(username)
	var (
		orgID id.ID
		user  identity.User
		cred  identity.Credential
		found bool
	)
	err := s.store.InTx(ctx, func(r Repository) error {
		bind, err := r.Binding(ctx)
		if err != nil {
			return err
		}
		orgID = bind.OrganizationID
		if uerr != nil {
			return nil
		}
		user, err = r.UserByUsername(ctx, orgID, uname)
		if errors.Is(err, apporg.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if cred, err = r.Credential(ctx, orgID, user.ID); err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return LoginResult{}, err
	}

	// Con un usuario inexistente se calcula igual un verificador, que cuesta
	// lo mismo que comprobarlo: el tiempo de respuesta no revela si existe.
	var ok, rehash bool
	if found {
		if ok, rehash, err = s.hasher.Verify(password, cred.Hash); err != nil {
			return LoginResult{}, err
		}
	} else if _, err := s.hasher.Hash(password); err != nil {
		return LoginResult{}, err
	}
	var newHash string
	if found && ok && rehash {
		if newHash, err = s.hasher.Hash(password); err != nil {
			return LoginResult{}, err
		}
	}

	var out LoginResult
	granted := false
	err = s.store.InTx(ctx, func(r Repository) error {
		now := s.now().UTC()
		if !found {
			return s.event(ctx, r, orgID, EventLoginFailed, id.ID{}, id.ID{}, "usuario desconocido")
		}
		// Se relee el estado: otro intento pudo bloquear la cuenta mientras
		// se verificaba la contraseña.
		cred, err := r.Credential(ctx, orgID, user.ID)
		if err != nil {
			return err
		}
		switch {
		case s.lockout.Locked(cred.Lockout, now):
			return s.event(ctx, r, orgID, EventLoginFailed, id.ID{}, user.ID, "cuenta bloqueada")
		case !ok:
			cred.Lockout = s.lockout.Fail(cred.Lockout, now)
			if err := r.UpdateCredential(ctx, orgID, cred); err != nil {
				return err
			}
			if s.lockout.Locked(cred.Lockout, now) {
				return s.event(ctx, r, orgID, EventAccountLocked, id.ID{}, user.ID,
					fmt.Sprintf("bloqueada hasta %s", cred.Lockout.LockedUntil.Format(time.RFC3339)))
			}
			return s.event(ctx, r, orgID, EventLoginFailed, id.ID{}, user.ID, "contraseña incorrecta")
		case user.Status != identity.StatusActive:
			return s.event(ctx, r, orgID, EventLoginFailed, id.ID{}, user.ID, "usuario desactivado")
		}
		cred.Lockout = s.lockout.Succeed()
		if newHash != "" {
			cred.Hash = newHash
		}
		if err := r.UpdateCredential(ctx, orgID, cred); err != nil {
			return err
		}
		sess, err := s.session.New(user, now)
		if err != nil {
			return err
		}
		token, digest, err := s.tokens.New()
		if err != nil {
			return err
		}
		if err := r.InsertSession(ctx, sess, digest); err != nil {
			return err
		}
		out, granted = LoginResult{Token: token, Session: sess, User: user}, true
		return s.event(ctx, r, orgID, EventLoginSucceeded, user.ID, user.ID, "")
	})
	if err != nil {
		return LoginResult{}, err
	}
	if !granted {
		return LoginResult{}, ErrInvalidCredentials
	}
	return out, nil
}

// Authenticated es el resultado de validar un token.
type Authenticated struct {
	Principal identity.Principal
	User      identity.User
	Session   identity.Session
}

// Authenticate valida el token de sesión, registra actividad y resuelve los
// permisos vigentes del usuario: un cambio de asignaciones surte efecto en
// el siguiente comando.
func (s *Service) Authenticate(ctx context.Context, token string) (Authenticated, error) {
	if token == "" {
		return Authenticated{}, identity.ErrSessionInvalid
	}
	var out Authenticated
	err := s.store.InTx(ctx, func(r Repository) error {
		now := s.now().UTC()
		sess, err := r.SessionByDigest(ctx, s.tokens.Digest(token))
		if errors.Is(err, apporg.ErrNotFound) {
			return identity.ErrSessionInvalid
		}
		if err != nil {
			return err
		}
		if err := s.session.Check(sess, now); err != nil {
			return err
		}
		bind, err := r.Binding(ctx)
		if err != nil {
			return err
		}
		if bind.OrganizationID != sess.OrganizationID {
			return fmt.Errorf("%w (otra organización)", identity.ErrSessionInvalid)
		}
		user, err := r.User(ctx, sess.OrganizationID, sess.UserID)
		if err != nil {
			return err
		}
		if user.Status != identity.StatusActive {
			return fmt.Errorf("%w (usuario desactivado)", identity.ErrSessionInvalid)
		}
		grants, err := grantsOf(ctx, r, sess.OrganizationID, user.ID)
		if err != nil {
			return err
		}
		sess = sess.Touch(now)
		if err := r.UpdateSession(ctx, sess); err != nil {
			return err
		}
		out = Authenticated{
			Principal: identity.Principal{UserID: user.ID, OrganizationID: user.OrganizationID, Username: user.Username, SessionID: sess.ID, Grants: grants},
			User:      user,
			Session:   sess,
		}
		return nil
	})
	return out, err
}

// Logout revoca la sesión del token.
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.InTx(ctx, func(r Repository) error {
		sess, err := r.SessionByDigest(ctx, s.tokens.Digest(token))
		if errors.Is(err, apporg.ErrNotFound) {
			return identity.ErrSessionInvalid
		}
		if err != nil {
			return err
		}
		if !sess.RevokedAt.IsZero() {
			return nil
		}
		sess.RevokedAt = s.now().UTC()
		if err := r.UpdateSession(ctx, sess); err != nil {
			return err
		}
		return s.event(ctx, r, sess.OrganizationID, EventLogout, sess.UserID, sess.UserID, "")
	})
}

func grantsOf(ctx context.Context, r Repository, orgID, userID id.ID) ([]identity.Grant, error) {
	assignments, err := r.Assignments(ctx, orgID)
	if err != nil {
		return nil, err
	}
	roles, err := rolesByID(ctx, r, orgID)
	if err != nil {
		return nil, err
	}
	var out []identity.Grant
	for _, a := range assignments {
		if a.UserID != userID {
			continue
		}
		role := roles[a.RoleID]
		out = append(out, identity.Grant{RoleCode: role.Code, Scope: a.Scope, Permissions: slices.Clone(role.Permissions)})
	}
	return out, nil
}

func rolesByID(ctx context.Context, r Repository, orgID id.ID) (map[id.ID]identity.Role, error) {
	list, err := r.Roles(ctx, orgID)
	if err != nil {
		return nil, err
	}
	m := make(map[id.ID]identity.Role, len(list))
	for _, role := range list {
		m[role.ID] = role
	}
	return m, nil
}

// ensureAdmin verifica que quede al menos un usuario activo con gestión de
// roles y de usuarios en el ámbito de toda la organización.
func ensureAdmin(ctx context.Context, r Repository, orgID id.ID) error {
	users, err := r.Users(ctx, orgID)
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.Status != identity.StatusActive {
			continue
		}
		grants, err := grantsOf(ctx, r, orgID, u.ID)
		if err != nil {
			return err
		}
		p := identity.Principal{UserID: u.ID, OrganizationID: orgID, Grants: grants}
		orgScope := p.OrganizationScope()
		if p.Can(identity.PermRoleManage, orgScope) && p.Can(identity.PermUserManage, orgScope) {
			return nil
		}
	}
	return ErrLastAdmin
}

// scopeRef identifica un ámbito por códigos; vacío es la organización.
type ScopeRef struct {
	Company, Branch, Warehouse string
}

func (ref ScopeRef) String() string {
	switch {
	case ref.Warehouse != "":
		return "almacén " + ref.Company + "/" + ref.Branch + "/" + ref.Warehouse
	case ref.Branch != "":
		return "sucursal " + ref.Company + "/" + ref.Branch
	case ref.Company != "":
		return "empresa " + ref.Company
	default:
		return "toda la organización"
	}
}

var errScopeRef = fmt.Errorf("%w: indique --company, --company y --branch, o los tres con --warehouse", org.ErrInvalid)

// resolveScope convierte códigos en un ámbito de la organización.
func resolveScope(ctx context.Context, r Repository, orgID id.ID, ref ScopeRef) (scope.BusinessScope, ScopeRef, error) {
	if (ref.Branch != "" && ref.Company == "") || (ref.Warehouse != "" && ref.Branch == "") {
		return scope.BusinessScope{}, ref, errScopeRef
	}
	var companyID, branchID, warehouseID id.ID
	var norm ScopeRef
	if ref.Company != "" {
		code, err := org.ParseCode(ref.Company)
		if err != nil {
			return scope.BusinessScope{}, ref, err
		}
		c, err := r.CompanyByCode(ctx, orgID, code)
		if err != nil {
			return scope.BusinessScope{}, ref, err
		}
		companyID, norm.Company = c.ID, c.Code.String()
	}
	if ref.Branch != "" {
		code, err := org.ParseCode(ref.Branch)
		if err != nil {
			return scope.BusinessScope{}, ref, err
		}
		b, err := r.BranchByCode(ctx, orgID, companyID, code)
		if err != nil {
			return scope.BusinessScope{}, ref, err
		}
		branchID, norm.Branch = b.ID, b.Code.String()
	}
	if ref.Warehouse != "" {
		code, err := org.ParseCode(ref.Warehouse)
		if err != nil {
			return scope.BusinessScope{}, ref, err
		}
		list, err := r.Warehouses(ctx, orgID, companyID, branchID)
		if err != nil {
			return scope.BusinessScope{}, ref, err
		}
		i := slices.IndexFunc(list, func(w org.Warehouse) bool { return w.Code == code })
		if i < 0 {
			return scope.BusinessScope{}, ref, fmt.Errorf("%w: almacén %s", apporg.ErrNotFound, code)
		}
		warehouseID, norm.Warehouse = list[i].ID, code.String()
	}
	sc, err := scope.New(orgID, companyID, branchID, warehouseID)
	return sc, norm, err
}

// describeScope es la inversa de resolveScope, para mostrar asignaciones.
func describeScope(ctx context.Context, r Repository, sc scope.BusinessScope) (ScopeRef, error) {
	var ref ScopeRef
	orgID := sc.Organization()
	if !sc.Company().IsZero() {
		c, err := r.Company(ctx, orgID, sc.Company())
		if err != nil {
			return ref, err
		}
		ref.Company = c.Code.String()
	}
	if !sc.Branch().IsZero() {
		b, err := r.Branch(ctx, orgID, sc.Company(), sc.Branch())
		if err != nil {
			return ref, err
		}
		ref.Branch = b.Code.String()
	}
	if !sc.Warehouse().IsZero() {
		list, err := r.Warehouses(ctx, orgID, sc.Company(), sc.Branch())
		if err != nil {
			return ref, err
		}
		i := slices.IndexFunc(list, func(w org.Warehouse) bool { return w.ID == sc.Warehouse() })
		if i < 0 {
			return ref, fmt.Errorf("%w: almacén", apporg.ErrNotFound)
		}
		ref.Warehouse = list[i].Code.String()
	}
	return ref, nil
}

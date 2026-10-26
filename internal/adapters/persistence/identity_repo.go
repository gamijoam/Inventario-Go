package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gamijoam/inventario-go/internal/application/auth"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/scope"
)

// AuthStore implementa auth.Store: la misma transacción cubre organización
// e identidad.
func (s *Store) AuthStore() auth.Store { return authStore{s} }

type authStore struct{ s *Store }

func (a authStore) InTx(ctx context.Context, fn func(auth.Repository) error) error {
	return orgStore(a).InTx(ctx, func(r apporg.Repository) error { return fn(r.(*orgRepo)) })
}

// nullTime convierte el instante cero en NULL.
func (r *orgRepo) nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return r.ts(t)
}

func nullID(v id.ID) any {
	if v.IsZero() {
		return nil
	}
	return v.String()
}

func (r *orgRepo) InsertUser(ctx context.Context, u identity.User) error {
	return r.exec(ctx, "usuario "+u.Username.String(),
		"INSERT INTO users (organization_id, id, username, display_name, status, created_at_utc, updated_at_utc) VALUES (?, ?, ?, ?, ?, ?, ?)",
		u.OrganizationID.String(), u.ID.String(), u.Username.String(), u.DisplayName.String(), string(u.Status), r.ts(u.CreatedAt), r.ts(u.CreatedAt))
}

func (r *orgRepo) UpdateUser(ctx context.Context, u identity.User) error {
	return r.execOne(ctx, "usuario "+u.Username.String(),
		"UPDATE users SET display_name = ?, status = ?, updated_at_utc = ? WHERE organization_id = ? AND id = ?",
		u.DisplayName.String(), string(u.Status), r.ts(time.Now().UTC()), u.OrganizationID.String(), u.ID.String())
}

// execOne exige que la sentencia afecte exactamente una fila.
func (r *orgRepo) execOne(ctx context.Context, what, query string, args ...any) error {
	res, err := r.tx.ExecContext(ctx, r.q(query), args...)
	if err != nil {
		if r.s.isUniqueViolation(err) {
			return fmt.Errorf("%w: %s", apporg.ErrConflict, what)
		}
		return fmt.Errorf("guardar %s: %w", what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: %s", apporg.ErrNotFound, what)
	}
	return nil
}

const userCols = "CAST(organization_id AS TEXT), CAST(id AS TEXT), username, display_name, status, created_at_utc"

func scanUser(sc scanner) (identity.User, error) {
	var orgID, rawID, username, name, status string
	var created any
	if err := sc.Scan(&orgID, &rawID, &username, &name, &status, &created); err != nil {
		return identity.User{}, err
	}
	var u identity.User
	var err error
	if u.OrganizationID, err = id.Parse(orgID); err != nil {
		return u, err
	}
	if u.ID, err = id.Parse(rawID); err != nil {
		return u, err
	}
	if u.Username, err = identity.ParseUsername(username); err != nil {
		return u, err
	}
	if u.DisplayName, err = org.ParseName(name); err != nil {
		return u, err
	}
	u.Status = identity.Status(status)
	u.CreatedAt, err = parseTime(created)
	return u, err
}

func (r *orgRepo) User(ctx context.Context, orgID, userID id.ID) (identity.User, error) {
	row := r.tx.QueryRowContext(ctx, r.q("SELECT "+userCols+" FROM users WHERE organization_id = ? AND id = ?"), orgID.String(), userID.String())
	u, err := scanUser(row)
	return u, notFound(err, "usuario")
}

func (r *orgRepo) UserByUsername(ctx context.Context, orgID id.ID, username identity.Username) (identity.User, error) {
	row := r.tx.QueryRowContext(ctx, r.q("SELECT "+userCols+" FROM users WHERE organization_id = ? AND username = ?"), orgID.String(), username.String())
	u, err := scanUser(row)
	return u, notFound(err, "usuario "+username.String())
}

func (r *orgRepo) Users(ctx context.Context, orgID id.ID) ([]identity.User, error) {
	return queryAll(ctx, r, scanUser, "SELECT "+userCols+" FROM users WHERE organization_id = ? ORDER BY username", orgID.String())
}

func (r *orgRepo) InsertCredential(ctx context.Context, orgID id.ID, c identity.Credential) error {
	return r.exec(ctx, "credencial",
		"INSERT INTO user_credentials (organization_id, user_id, kind, verifier, changed_at_utc, failed_attempts, lock_streak, locked_until_utc) VALUES (?, ?, 'password', ?, ?, ?, ?, ?)",
		orgID.String(), c.UserID.String(), c.Hash, r.ts(c.ChangedAt), c.Lockout.FailedAttempts, c.Lockout.Streak, r.nullTime(c.Lockout.LockedUntil))
}

func (r *orgRepo) UpdateCredential(ctx context.Context, orgID id.ID, c identity.Credential) error {
	return r.execOne(ctx, "credencial",
		"UPDATE user_credentials SET verifier = ?, changed_at_utc = ?, failed_attempts = ?, lock_streak = ?, locked_until_utc = ? WHERE organization_id = ? AND user_id = ? AND kind = 'password'",
		c.Hash, r.ts(c.ChangedAt), c.Lockout.FailedAttempts, c.Lockout.Streak, r.nullTime(c.Lockout.LockedUntil), orgID.String(), c.UserID.String())
}

func (r *orgRepo) Credential(ctx context.Context, orgID, userID id.ID) (identity.Credential, error) {
	var c identity.Credential
	var changed, locked any
	err := r.tx.QueryRowContext(ctx, r.q("SELECT verifier, changed_at_utc, failed_attempts, lock_streak, locked_until_utc FROM user_credentials WHERE organization_id = ? AND user_id = ? AND kind = 'password'"),
		orgID.String(), userID.String()).Scan(&c.Hash, &changed, &c.Lockout.FailedAttempts, &c.Lockout.Streak, &locked)
	if err != nil {
		return c, notFound(err, "credencial")
	}
	c.UserID = userID
	if c.ChangedAt, err = parseTime(changed); err != nil {
		return c, err
	}
	if locked != nil {
		if c.Lockout.LockedUntil, err = parseTime(locked); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (r *orgRepo) InsertRole(ctx context.Context, role identity.Role) error {
	if err := r.exec(ctx, "rol "+role.Code.String(),
		"INSERT INTO roles (organization_id, id, code, name, created_at_utc, updated_at_utc) VALUES (?, ?, ?, ?, ?, ?)",
		role.OrganizationID.String(), role.ID.String(), role.Code.String(), role.Name.String(), r.ts(role.CreatedAt), r.ts(role.CreatedAt)); err != nil {
		return err
	}
	return r.insertPermissions(ctx, role)
}

func (r *orgRepo) insertPermissions(ctx context.Context, role identity.Role) error {
	for _, p := range role.Permissions {
		if err := r.exec(ctx, "permiso "+string(p),
			"INSERT INTO role_permissions (organization_id, role_id, permission) VALUES (?, ?, ?)",
			role.OrganizationID.String(), role.ID.String(), string(p)); err != nil {
			return err
		}
	}
	return nil
}

func (r *orgRepo) UpdateRole(ctx context.Context, role identity.Role) error {
	if err := r.execOne(ctx, "rol "+role.Code.String(),
		"UPDATE roles SET name = ?, updated_at_utc = ? WHERE organization_id = ? AND id = ?",
		role.Name.String(), r.ts(time.Now().UTC()), role.OrganizationID.String(), role.ID.String()); err != nil {
		return err
	}
	if err := r.exec(ctx, "permisos del rol "+role.Code.String(),
		"DELETE FROM role_permissions WHERE organization_id = ? AND role_id = ?", role.OrganizationID.String(), role.ID.String()); err != nil {
		return err
	}
	return r.insertPermissions(ctx, role)
}

const roleCols = "CAST(organization_id AS TEXT), CAST(id AS TEXT), code, name, created_at_utc"

func scanRole(sc scanner) (identity.Role, error) {
	var orgID, rawID, code, name string
	var created any
	if err := sc.Scan(&orgID, &rawID, &code, &name, &created); err != nil {
		return identity.Role{}, err
	}
	var role identity.Role
	var err error
	if role.OrganizationID, err = id.Parse(orgID); err != nil {
		return role, err
	}
	if role.ID, err = id.Parse(rawID); err != nil {
		return role, err
	}
	role.Code, role.Name, role.CreatedAt, err = decodeCommon(code, name, created)
	return role, err
}

func (r *orgRepo) RoleByCode(ctx context.Context, orgID id.ID, code org.Code) (identity.Role, error) {
	row := r.tx.QueryRowContext(ctx, r.q("SELECT "+roleCols+" FROM roles WHERE organization_id = ? AND code = ?"), orgID.String(), code.String())
	role, err := scanRole(row)
	if err != nil {
		return role, notFound(err, "rol "+code.String())
	}
	role.Permissions, err = r.permissions(ctx, orgID, role.ID)
	return role, err
}

func (r *orgRepo) Roles(ctx context.Context, orgID id.ID) ([]identity.Role, error) {
	list, err := queryAll(ctx, r, scanRole, "SELECT "+roleCols+" FROM roles WHERE organization_id = ? ORDER BY code", orgID.String())
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Permissions, err = r.permissions(ctx, orgID, list[i].ID); err != nil {
			return nil, err
		}
	}
	return list, nil
}

func (r *orgRepo) permissions(ctx context.Context, orgID, roleID id.ID) ([]identity.Permission, error) {
	scan := func(sc scanner) (identity.Permission, error) {
		var p string
		err := sc.Scan(&p)
		return identity.Permission(p), err
	}
	return queryAll(ctx, r, scan, "SELECT permission FROM role_permissions WHERE organization_id = ? AND role_id = ? ORDER BY permission", orgID.String(), roleID.String())
}

func (r *orgRepo) InsertAssignment(ctx context.Context, a identity.Assignment) error {
	return r.exec(ctx, "asignación (el usuario ya tiene ese rol en ese ámbito)",
		"INSERT INTO user_role_assignments (organization_id, id, user_id, role_id, company_id, branch_id, warehouse_id, created_by, created_at_utc) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		a.Scope.Organization().String(), a.ID.String(), a.UserID.String(), a.RoleID.String(),
		nullID(a.Scope.Company()), nullID(a.Scope.Branch()), nullID(a.Scope.Warehouse()), a.CreatedBy.String(), r.ts(a.CreatedAt))
}

func (r *orgRepo) DeleteAssignment(ctx context.Context, orgID, assignmentID id.ID) error {
	return r.execOne(ctx, "asignación", "DELETE FROM user_role_assignments WHERE organization_id = ? AND id = ?", orgID.String(), assignmentID.String())
}

func (r *orgRepo) Assignments(ctx context.Context, orgID id.ID) ([]identity.Assignment, error) {
	scan := func(sc scanner) (identity.Assignment, error) {
		var org, rawID, user, role, by string
		var company, branch, warehouse sql.NullString
		var created any
		if err := sc.Scan(&org, &rawID, &user, &role, &company, &branch, &warehouse, &by, &created); err != nil {
			return identity.Assignment{}, err
		}
		ids, err := parseIDs(org, rawID, user, role, by, company.String, branch.String, warehouse.String)
		if err != nil {
			return identity.Assignment{}, err
		}
		sc2, err := scope.New(ids[0], ids[5], ids[6], ids[7])
		if err != nil {
			return identity.Assignment{}, err
		}
		a := identity.Assignment{ID: ids[1], UserID: ids[2], RoleID: ids[3], CreatedBy: ids[4], Scope: sc2}
		a.CreatedAt, err = parseTime(created)
		return a, err
	}
	return queryAll(ctx, r, scan, "SELECT CAST(organization_id AS TEXT), CAST(id AS TEXT), CAST(user_id AS TEXT), CAST(role_id AS TEXT), "+
		"CAST(company_id AS TEXT), CAST(branch_id AS TEXT), CAST(warehouse_id AS TEXT), CAST(created_by AS TEXT), created_at_utc "+
		"FROM user_role_assignments WHERE organization_id = ? ORDER BY created_at_utc, id", orgID.String())
}

// parseIDs interpreta UUIDs; la cadena vacía es el ID cero.
func parseIDs(raw ...string) ([]id.ID, error) {
	out := make([]id.ID, len(raw))
	for i, s := range raw {
		if s == "" {
			continue
		}
		v, err := id.Parse(s)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (r *orgRepo) InsertSession(ctx context.Context, s identity.Session, digest string) error {
	return r.exec(ctx, "sesión",
		"INSERT INTO sessions (organization_id, id, user_id, token_digest, created_at_utc, last_seen_at_utc, expires_at_utc, revoked_at_utc) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		s.OrganizationID.String(), s.ID.String(), s.UserID.String(), digest, r.ts(s.CreatedAt), r.ts(s.LastSeenAt), r.ts(s.ExpiresAt), r.nullTime(s.RevokedAt))
}

func (r *orgRepo) UpdateSession(ctx context.Context, s identity.Session) error {
	return r.execOne(ctx, "sesión",
		"UPDATE sessions SET last_seen_at_utc = ?, revoked_at_utc = ? WHERE organization_id = ? AND id = ?",
		r.ts(s.LastSeenAt), r.nullTime(s.RevokedAt), s.OrganizationID.String(), s.ID.String())
}

func (r *orgRepo) SessionByDigest(ctx context.Context, digest string) (identity.Session, error) {
	var org, rawID, user string
	var created, seen, expires, revoked any
	err := r.tx.QueryRowContext(ctx, r.q("SELECT CAST(organization_id AS TEXT), CAST(id AS TEXT), CAST(user_id AS TEXT), created_at_utc, last_seen_at_utc, expires_at_utc, revoked_at_utc FROM sessions WHERE token_digest = ?"),
		digest).Scan(&org, &rawID, &user, &created, &seen, &expires, &revoked)
	if err != nil {
		return identity.Session{}, notFound(err, "sesión")
	}
	ids, err := parseIDs(org, rawID, user)
	if err != nil {
		return identity.Session{}, err
	}
	s := identity.Session{OrganizationID: ids[0], ID: ids[1], UserID: ids[2]}
	for _, f := range []struct {
		dst *time.Time
		v   any
	}{{&s.CreatedAt, created}, {&s.LastSeenAt, seen}, {&s.ExpiresAt, expires}, {&s.RevokedAt, revoked}} {
		if f.v == nil {
			continue
		}
		if *f.dst, err = parseTime(f.v); err != nil {
			return identity.Session{}, err
		}
	}
	return s, nil
}

func (r *orgRepo) RevokeSessions(ctx context.Context, orgID, userID id.ID, at time.Time, except id.ID) error {
	q, args := "UPDATE sessions SET revoked_at_utc = ? WHERE organization_id = ? AND user_id = ? AND revoked_at_utc IS NULL",
		[]any{r.ts(at), orgID.String(), userID.String()}
	if !except.IsZero() {
		q, args = q+" AND id <> ?", append(args, except.String())
	}
	return r.exec(ctx, "sesiones", q, args...)
}

// maxDetail es el límite de la columna security_events.detail.
const maxDetail = 500

func (r *orgRepo) InsertEvent(ctx context.Context, e auth.Event) error {
	detail := e.Detail
	if len([]rune(detail)) > maxDetail {
		detail = string([]rune(detail)[:maxDetail-1]) + "…"
	}
	return r.exec(ctx, "evento de seguridad",
		"INSERT INTO security_events (organization_id, id, kind, actor_id, subject_id, detail, occurred_at_utc) VALUES (?, ?, ?, ?, ?, ?, ?)",
		e.OrganizationID.String(), e.ID.String(), e.Kind, nullID(e.ActorID), nullID(e.SubjectID), strings.ToValidUTF8(detail, "?"), r.ts(e.At))
}

// SecurityEvents devuelve los eventos de la organización, del más reciente
// al más antiguo (para pruebas y para la futura consulta de auditoría).
func (s *Store) SecurityEvents(ctx context.Context, orgID id.ID) ([]auth.Event, error) {
	var out []auth.Event
	err := orgStore{s}.InTx(ctx, func(ar apporg.Repository) error {
		r := ar.(*orgRepo)
		scan := func(sc scanner) (auth.Event, error) {
			var org, rawID, kind, detail string
			var actor, subject sql.NullString
			var at any
			if err := sc.Scan(&org, &rawID, &kind, &actor, &subject, &detail, &at); err != nil {
				return auth.Event{}, err
			}
			ids, err := parseIDs(org, rawID, actor.String, subject.String)
			if err != nil {
				return auth.Event{}, err
			}
			e := auth.Event{OrganizationID: ids[0], ID: ids[1], ActorID: ids[2], SubjectID: ids[3], Kind: kind, Detail: detail}
			e.At, err = parseTime(at)
			return e, err
		}
		var err error
		out, err = queryAll(ctx, r, scan, "SELECT CAST(organization_id AS TEXT), CAST(id AS TEXT), kind, CAST(actor_id AS TEXT), CAST(subject_id AS TEXT), detail, occurred_at_utc "+
			"FROM security_events WHERE organization_id = ? ORDER BY occurred_at_utc DESC, id DESC", orgID.String())
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return out, err
}

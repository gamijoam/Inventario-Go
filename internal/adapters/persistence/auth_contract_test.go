package persistence_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/adapters/persistence"
	"github.com/gamijoam/inventario-go/internal/application/auth"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/platform/config"
	"github.com/gamijoam/inventario-go/internal/platform/security"
)

// fastHasher usa Argon2id con parámetros mínimos: el formato y la
// comparación son los reales, el costo no.
var fastHasher = security.Hasher{Params: security.Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type authEnv struct {
	t     *testing.T
	store *persistence.Store
	svc   *auth.Service
	org   *apporg.Service
	clock *clock
	setup auth.SetupResult
}

const (
	adminPassword = "cafe con leche"
	// userPassword es la contraseña inicial de los usuarios creados en las
	// pruebas; no contiene ningún nombre de usuario.
	userPassword = "sol y luna 2026"
)

func newAuthEnv(t *testing.T, cfg config.Database) *authEnv {
	t.Helper()
	s, _ := migrated(t, cfg)
	if _, _, err := s.EnsureInstallation(ctx(t)); err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)}
	e := &authEnv{t: t, store: s, clock: c,
		svc: auth.NewService(s.AuthStore(), fastHasher, security.Tokens{}, c.now),
		org: apporg.NewService(s.OrgStore(), c.now)}
	res, err := e.svc.Setup(ctx(t), orgInput("ana"), auth.AdminInput{Username: "admin", DisplayName: "Ana Admin", Password: adminPassword})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	e.setup = res
	return e
}

func (e *authEnv) login(user, pw string) auth.Authenticated {
	e.t.Helper()
	res, err := e.svc.Login(ctx(e.t), user, pw)
	if err != nil {
		e.t.Fatalf("Login %s: %v", user, err)
	}
	a, err := e.svc.Authenticate(ctx(e.t), res.Token)
	if err != nil {
		e.t.Fatalf("Authenticate %s: %v", user, err)
	}
	return a
}

func (e *authEnv) createUser(by identity.Principal, user, role string, where auth.ScopeRef) {
	e.t.Helper()
	if _, err := e.svc.CreateUser(ctx(e.t), by, auth.NewUserInput{Username: user, DisplayName: user, Password: userPassword}); err != nil {
		e.t.Fatalf("CreateUser %s: %v", user, err)
	}
	if role != "" {
		if _, err := e.svc.Assign(ctx(e.t), by, user, role, where); err != nil {
			e.t.Fatalf("Assign %s %s: %v", user, role, err)
		}
	}
}

func (e *authEnv) events(kind string) []auth.Event {
	e.t.Helper()
	all, err := e.store.SecurityEvents(ctx(e.t), e.setup.Organization.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []auth.Event
	for _, ev := range all {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func TestSetupCreatesTemplatesAndInitialAdmin(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newAuthEnv(t, cfg)
		if len(e.setup.Roles) != len(identity.Templates()) || e.setup.Completed {
			t.Fatalf("setup = %+v", e.setup)
		}
		a := e.login("ADMIN", adminPassword) // el usuario no distingue mayúsculas
		orgScope := e.setup.Organization.Scope()
		if len(a.Principal.Grants) != 1 || a.Principal.Grants[0].RoleCode.String() != identity.AdminRoleCode ||
			!a.Principal.Can(identity.PermRoleManage, orgScope) || a.Principal.Can(identity.PermApproveBelowCost, orgScope) {
			t.Fatalf("principal = %+v", a.Principal)
		}
		_, err := e.svc.Setup(ctx(t), orgInput("otra"), auth.AdminInput{Username: "otro", DisplayName: "Otro", Password: adminPassword})
		if !errors.Is(err, apporg.ErrAlreadyBootstrapped) {
			t.Fatalf("segundo setup: %v", err)
		}
		if n := count(t, e.store, "users"); n != 1 {
			t.Fatalf("users = %d", n)
		}
		var verifier string
		if err := e.store.DB.QueryRowContext(ctx(t), "SELECT verifier FROM user_credentials").Scan(&verifier); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(verifier, "$argon2id$") || strings.Contains(verifier, adminPassword) {
			t.Fatalf("verificador = %q", verifier)
		}
	})
}

func TestSetupCompletesAnOrganizationWithoutUsers(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		// Una instalación configurada antes de BP-M1-002: estructura sin usuarios.
		s, svc := installed(t, cfg)
		if _, err := svc.Bootstrap(ctx(t), orgInput("ana")); err != nil {
			t.Fatal(err)
		}
		a := auth.NewService(s.AuthStore(), fastHasher, security.Tokens{}, func() time.Time { return time.Now().UTC() })
		res, err := a.Setup(ctx(t), apporg.BootstrapInput{}, auth.AdminInput{Username: "admin", DisplayName: "Admin", Password: adminPassword})
		if err != nil || !res.Completed || res.Organization.Code.String() != "ANA" || res.Admin.Username.String() != "admin" {
			t.Fatalf("Setup = %+v, %v", res, err)
		}
		if count(t, s, "organizations") != 1 || count(t, s, "roles") != len(identity.Templates()) {
			t.Fatal("no debe duplicar estructura ni roles")
		}
	})
}

func TestLoginFailuresAreUniformAndLockTheAccount(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newAuthEnv(t, cfg)
		ctx := ctx(t)
		for _, c := range []struct{ user, pw string }{{"nadie", "lo que sea 123"}, {"admin", "incorrecta"}, {"no válido!", "x"}} {
			if _, err := e.svc.Login(ctx, c.user, c.pw); !errors.Is(err, auth.ErrInvalidCredentials) {
				t.Errorf("%s: %v", c.user, err)
			}
		}
		if ev := e.events(auth.EventLoginFailed); len(ev) != 3 || !ev[2].SubjectID.IsZero() || ev[1].SubjectID != e.setup.Admin.ID {
			t.Fatalf("eventos de fallo = %+v", ev)
		}
		for i := 0; i < 4; i++ { // 1 fallo previo + 4 = 5
			_, _ = e.svc.Login(ctx, "admin", "incorrecta")
		}
		if len(e.events(auth.EventAccountLocked)) != 1 {
			t.Fatal("el 5.º fallo debe bloquear y registrarse")
		}
		// Bloqueada: ni la contraseña correcta entra, con el mismo mensaje.
		if _, err := e.svc.Login(ctx, "admin", adminPassword); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("bloqueada: %v", err)
		}
		e.clock.add(15 * time.Minute)
		e.login("admin", adminPassword)
		// El acceso correcto reinicia la racha: 5 fallos vuelven a ser 15 min.
		for i := 0; i < 5; i++ {
			_, _ = e.svc.Login(ctx, "admin", "incorrecta")
		}
		e.clock.add(15 * time.Minute)
		e.login("admin", adminPassword)
	})
}

func TestSessionsAreHashedExpireAndRevoke(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newAuthEnv(t, cfg)
		ctx := ctx(t)
		res, err := e.svc.Login(ctx, "admin", adminPassword)
		if err != nil {
			t.Fatal(err)
		}
		var digest string
		if err := e.store.DB.QueryRowContext(ctx, "SELECT token_digest FROM sessions").Scan(&digest); err != nil {
			t.Fatal(err)
		}
		if digest == res.Token || strings.Contains(digest, strings.TrimPrefix(res.Token, "bps_")) {
			t.Fatal("la base no debe guardar el token")
		}
		if _, err := e.svc.Authenticate(ctx, res.Token+"x"); !errors.Is(err, identity.ErrSessionInvalid) {
			t.Fatalf("token alterado: %v", err)
		}
		e.clock.add(59 * time.Minute)
		if _, err := e.svc.Authenticate(ctx, res.Token); err != nil {
			t.Fatalf("activa a los 59 min: %v", err)
		}
		e.clock.add(59 * time.Minute) // la actividad anterior renovó la inactividad
		if _, err := e.svc.Authenticate(ctx, res.Token); err != nil {
			t.Fatalf("renovada: %v", err)
		}
		e.clock.add(time.Hour)
		if _, err := e.svc.Authenticate(ctx, res.Token); !errors.Is(err, identity.ErrSessionInvalid) {
			t.Fatalf("1 h inactiva: %v", err)
		}

		res2, _ := e.svc.Login(ctx, "admin", adminPassword)
		if err := e.svc.Logout(ctx, res2.Token); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Authenticate(ctx, res2.Token); !errors.Is(err, identity.ErrSessionInvalid) {
			t.Fatalf("tras logout: %v", err)
		}
	})
}

// BP-M1-002: "permiso denegado por ámbito y operación", de punta a punta
// sobre la base real.
func TestPermissionDeniedByScopeAndOperation(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newAuthEnv(t, cfg)
		ctx := ctx(t)
		root := e.login("admin", adminPassword).Principal
		if _, err := e.org.CreateCompany(ctx, root, "norte", "Norte, C.A.", "", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := e.org.CreateBranch(ctx, root, "norte", "maracay", "Maracay"); err != nil {
			t.Fatal(err)
		}
		principal := auth.ScopeRef{Company: "principal", Branch: "principal"}
		e.createUser(root, "cajero", "CAJERO", principal)
		e.createUser(root, "encargado", "ENCARGADO", principal)
		e.createUser(root, "jefe-norte", "ADMIN_EMPRESA", auth.ScopeRef{Company: "norte"})

		cajero := e.login("cajero", userPassword).Principal
		encargado := e.login("encargado", userPassword).Principal
		jefe := e.login("jefe-norte", userPassword).Principal

		// Operación no concedida, en su propio ámbito.
		if _, err := e.org.CreateWarehouse(ctx, cajero, "principal", "principal", "x", "X"); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("cajero crea almacén: %v", err)
		}
		// Operación concedida en su ámbito, y la misma fuera de él.
		if _, err := e.org.CreateWarehouse(ctx, encargado, "principal", "principal", "x", "X"); err != nil {
			t.Errorf("encargado en su sucursal: %v", err)
		}
		if _, err := e.org.CreateWarehouse(ctx, encargado, "norte", "maracay", "y", "Y"); !errors.Is(err, apporg.ErrNotFound) {
			t.Errorf("encargado en otra empresa: %v", err)
		}
		if _, err := e.org.CreateBranch(ctx, jefe, "norte", "valencia", "Valencia"); err != nil {
			t.Errorf("jefe de empresa crea sucursal en su empresa: %v", err)
		}
		if _, err := e.org.CreateBranch(ctx, jefe, "principal", "z", "Z"); !errors.Is(err, apporg.ErrNotFound) {
			t.Errorf("jefe de NORTE en PRINCIPAL: %v", err)
		}
		if _, err := e.org.CreateCompany(ctx, jefe, "sur", "Sur", "", ""); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("jefe de empresa crea empresa: %v", err)
		}
		if list, _ := e.org.ListCompanies(ctx, cajero); len(list) != 1 {
			t.Errorf("el cajero solo ve su empresa: %v", list)
		}

		// Gestión de usuarios: el jefe de NORTE no gestiona usuarios de PRINCIPAL.
		if err := e.svc.Unlock(ctx, jefe, "cajero"); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("jefe desbloquea cajero ajeno: %v", err)
		}
		if _, err := e.svc.Assign(ctx, jefe, "cajero", "CAJERO", auth.ScopeRef{Company: "norte", Branch: "maracay"}); err != nil {
			t.Errorf("jefe asigna en su empresa: %v", err)
		}
		if err := e.svc.Unlock(ctx, jefe, "cajero"); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("el cajero sigue teniendo una asignación fuera de NORTE: %v", err)
		}
		// No delega lo que no tiene, ni se modifica a sí mismo.
		if _, err := e.svc.Assign(ctx, jefe, "cajero", identity.AdminRoleCode, auth.ScopeRef{Company: "norte"}); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("jefe delega ADMIN_ORG: %v", err)
		}
		if _, err := e.svc.Assign(ctx, jefe, "jefe-norte", "ENCARGADO", auth.ScopeRef{Company: "norte"}); !errors.Is(err, auth.ErrSelf) {
			t.Errorf("autoasignación: %v", err)
		}
		if _, err := e.svc.ListUsers(ctx, cajero); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("cajero lista usuarios: %v", err)
		}
		if list, err := e.svc.ListUsers(ctx, jefe); err != nil || len(list) != 2 {
			t.Errorf("el jefe ve a quienes tienen asignaciones en NORTE (él y el cajero): %d %v", len(list), err)
		}
		if _, err := e.svc.CreateRole(ctx, jefe, "x", "X", nil); !errors.Is(err, identity.ErrDenied) {
			t.Errorf("jefe crea rol: %v", err)
		}
	})
}

func TestRevokedAssignmentsAndDisabledUsersTakeEffectImmediately(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newAuthEnv(t, cfg)
		ctx := ctx(t)
		root := e.login("admin", adminPassword).Principal
		branch := auth.ScopeRef{Company: "principal", Branch: "principal"}
		e.createUser(root, "encargado", "ENCARGADO", branch)
		res, err := e.svc.Login(ctx, "encargado", userPassword)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.svc.Unassign(ctx, root, "encargado", "ENCARGADO", branch); err != nil {
			t.Fatal(err)
		}
		a, err := e.svc.Authenticate(ctx, res.Token)
		if err != nil || len(a.Principal.Grants) != 0 {
			t.Fatalf("tras retirar el rol: %+v %v", a.Principal, err)
		}
		if _, err := e.svc.SetUserActive(ctx, root, "encargado", false); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Authenticate(ctx, res.Token); !errors.Is(err, identity.ErrSessionInvalid) {
			t.Fatalf("sesión de usuario desactivado: %v", err)
		}
		if _, err := e.svc.Login(ctx, "encargado", userPassword); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("login desactivado: %v", err)
		}
		if _, err := e.svc.SetUserActive(ctx, root, "encargado", true); err != nil {
			t.Fatal(err)
		}
		e.login("encargado", userPassword)
	})
}

func TestOrganizationKeepsAnAdministrator(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newAuthEnv(t, cfg)
		ctx := ctx(t)
		root := e.login("admin", adminPassword).Principal
		if _, err := e.svc.EditRole(ctx, root, identity.AdminRoleCode, nil, []string{string(identity.PermRoleManage)}); !errors.Is(err, auth.ErrLastAdmin) {
			t.Fatalf("quitar role.manage al único admin: %v", err)
		}
		if _, err := e.svc.SetUserActive(ctx, root, "admin", false); !errors.Is(err, auth.ErrSelf) {
			t.Fatalf("desactivarse: %v", err)
		}
		e.createUser(root, "admin2", identity.AdminRoleCode, auth.ScopeRef{})
		second := e.login("admin2", userPassword).Principal
		if _, err := e.svc.SetUserActive(ctx, second, "admin", false); err != nil {
			t.Fatalf("con dos admins se puede desactivar uno: %v", err)
		}
		if err := e.svc.Unassign(ctx, second, "admin2", identity.AdminRoleCode, auth.ScopeRef{}); !errors.Is(err, auth.ErrSelf) {
			t.Fatalf("retirarse el rol: %v", err)
		}
		r, err := e.svc.EditRole(ctx, second, "CAJERO", []string{string(identity.PermApproveBelowCost)}, nil)
		if err != nil || !r.Has(identity.PermApproveBelowCost) {
			t.Fatalf("el admin puede conceder expresamente un permiso extraordinario: %+v %v", r, err)
		}
	})
}

func TestPasswordResetAndChangeRevokeSessions(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newAuthEnv(t, cfg)
		ctx := ctx(t)
		rootAuth := e.login("admin", adminPassword)
		root := rootAuth.Principal
		e.createUser(root, "cajero", "CAJERO", auth.ScopeRef{Company: "principal"})
		old, _ := e.svc.Login(ctx, "cajero", userPassword)
		if err := e.svc.ResetPassword(ctx, root, "cajero", "nueva clave 2026"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Authenticate(ctx, old.Token); !errors.Is(err, identity.ErrSessionInvalid) {
			t.Fatalf("sesión previa al restablecimiento: %v", err)
		}
		if err := e.svc.ResetPassword(ctx, root, "cajero", "corta"); !errors.Is(err, identity.ErrInvalid) {
			t.Fatalf("política al restablecer: %v", err)
		}
		cajero := e.login("cajero", "nueva clave 2026")
		other, _ := e.svc.Login(ctx, "cajero", "nueva clave 2026")
		if err := e.svc.ChangePassword(ctx, cajero, "incorrecta", "otra clave 2026"); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("cambio con contraseña actual errada: %v", err)
		}
		if err := e.svc.ChangePassword(ctx, cajero, "nueva clave 2026", "otra clave 2026"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Authenticate(ctx, other.Token); !errors.Is(err, identity.ErrSessionInvalid) {
			t.Fatalf("otras sesiones deben revocarse: %v", err)
		}
		if _, err := e.svc.Authenticate(ctx, mustToken(t, e, "cajero", "otra clave 2026")); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.ResetPassword(ctx, root, "admin", "x"); !errors.Is(err, identity.ErrInvalid) && !errors.Is(err, auth.ErrSelf) {
			t.Fatalf("restablecer la propia: %v", err)
		}
		if len(e.events(auth.EventPasswordReset)) != 1 || len(e.events(auth.EventPasswordChanged)) != 1 {
			t.Fatal("faltan eventos de contraseña")
		}
	})
}

func mustToken(t *testing.T, e *authEnv, user, pw string) string {
	t.Helper()
	res, err := e.svc.Login(ctx(t), user, pw)
	if err != nil {
		t.Fatal(err)
	}
	return res.Token
}

// Inserciones directas que la base debe rechazar aunque el código de Go
// tuviera un error.
func TestIdentitySchemaRejectsHostileRows(t *testing.T) {
	forEachEngine(t, func(t *testing.T, cfg config.Database) {
		e := newAuthEnv(t, cfg)
		s := e.store
		p := s.Dialect.Placeholder
		ts := s.Dialect.Timestamp(time.Now().UTC())
		now := time.Now().UTC()

		// Otra organización con su propia empresa.
		b := org2(t, s, now)

		orgA := e.setup.Organization.ID.String()
		adminID := e.setup.Admin.ID.String()
		var roleID string
		if err := s.DB.QueryRowContext(ctx(t), "SELECT CAST(id AS TEXT) FROM roles WHERE code = 'CAJERO'").Scan(&roleID); err != nil {
			t.Fatal(err)
		}
		insAssign := "INSERT INTO user_role_assignments (organization_id, id, user_id, role_id, company_id, branch_id, warehouse_id, created_by, created_at_utc) VALUES (" +
			p(1) + ", " + p(2) + ", " + p(3) + ", " + p(4) + ", " + p(5) + ", " + p(6) + ", " + p(7) + ", " + p(8) + ", " + p(9) + ")"
		cases := map[string]struct {
			fk   bool
			sql  string
			args []any
		}{
			"asignación con empresa de otra organización": {true, insAssign,
				[]any{orgA, id.MustNew().String(), adminID, roleID, b.String(), nil, nil, adminID, ts}},
			"asignación con sucursal sin empresa": {false, insAssign,
				[]any{orgA, id.MustNew().String(), adminID, roleID, nil, e.setup.Branch.ID.String(), nil, adminID, ts}},
			"contraseña en claro": {false, "INSERT INTO user_credentials (organization_id, user_id, kind, verifier, changed_at_utc, failed_attempts, lock_streak) VALUES (" +
				p(1) + ", " + p(2) + ", 'password', 'cafe con leche', " + p(3) + ", 0, 0)", []any{orgA, adminID, ts}},
		}
		for name, c := range cases {
			_, err := s.DB.ExecContext(ctx(t), c.sql, c.args...)
			if err == nil {
				t.Errorf("%s: la base debía rechazarlo", name)
			} else if c.fk && !isForeignKeyError(err) {
				t.Errorf("%s: se esperaba violación de FK, obtuvo %v", name, err)
			}
		}
		// Control positivo: asignación coherente a la empresa propia.
		if _, err := s.DB.ExecContext(ctx(t), insAssign, orgA, id.MustNew().String(), adminID, roleID, e.setup.Company.ID.String(), nil, nil, adminID, ts); err != nil {
			t.Fatalf("control: %v", err)
		}
		// Duplicado exacto (mismo usuario, rol y ámbito, con NULLs): rechazado.
		if _, err := s.DB.ExecContext(ctx(t), insAssign, orgA, id.MustNew().String(), adminID, roleID, e.setup.Company.ID.String(), nil, nil, adminID, ts); err == nil {
			t.Error("asignación duplicada con niveles NULL debía rechazarse")
		}
		// Registro de seguridad de solo inserción.
		for _, q := range []string{"UPDATE security_events SET detail = 'x'", "DELETE FROM security_events"} {
			if _, err := s.DB.ExecContext(ctx(t), q); err == nil || !strings.Contains(err.Error(), "solo inserción") {
				t.Errorf("%s: %v", q, err)
			}
		}
	})
}

// org2 inserta otra organización con una empresa y devuelve el ID de esta.
func org2(t *testing.T, s *persistence.Store, now time.Time) id.ID {
	t.Helper()
	b, _ := org.NewOrganization("beto", "Beto", now)
	bc, _ := org.NewCompany(b, "principal", "Beto, C.A.", org.RIF{}, now)
	if err := s.OrgStore().InTx(ctx(t), func(r apporg.Repository) error {
		if err := r.InsertOrganization(ctx(t), b); err != nil {
			return err
		}
		return r.InsertCompany(ctx(t), bc)
	}); err != nil {
		t.Fatal(err)
	}
	return bc.ID
}

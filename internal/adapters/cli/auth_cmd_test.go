package cli_test

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
	"github.com/gamijoam/inventario-go/migrations"
)

const userPassword = "sol y luna 2026"

func sessionFile(o runOpts) string { return o.env["BALANZAPRO_SESSION_FILE"] }

// asUser devuelve opciones con un archivo de sesión propio, para que dos
// usuarios convivan en la misma base.
func asUser(t *testing.T, o runOpts, user, pw string) runOpts {
	t.Helper()
	u := runOpts{env: map[string]string{}}
	for k, v := range o.env {
		u.env[k] = v
	}
	u.env["BALANZAPRO_SESSION_FILE"] = filepath.Join(t.TempDir(), "session-"+user)
	if r := run(t, withStdin(u, pw), "auth", "login", "--user", user, "--password-stdin"); r.code != cli.ExitOK {
		t.Fatalf("login %s: code=%d stderr=%s", user, r.code, r.stderr)
	}
	return u
}

func TestSetupLeavesTheAdminLoggedIn(t *testing.T) {
	o := setupSQLite(t)
	data, err := os.ReadFile(sessionFile(o))
	if err != nil || !strings.HasPrefix(string(data), "bps_") {
		t.Fatalf("archivo de sesión: %q %v", data, err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(sessionFile(o)); fi.Mode().Perm() != 0o600 {
			t.Errorf("permisos de la sesión: %v", fi.Mode().Perm())
		}
	}
	v := decode(t, run(t, o, "auth", "whoami", "--json").stdout)
	user := v["user"].(map[string]any)
	if v["schema"] != "balanzapro.whoami/v1" || user["username"] != "admin" || v["organization_code"] != "PANADERIA-NANDU" ||
		user["assignments"].([]any)[0].(map[string]any)["role"] != "ADMIN_ORG" {
		t.Fatalf("whoami = %v", v)
	}
	text := run(t, o, "auth", "whoami").stdout
	if !strings.Contains(text, "ADMIN_ORG en toda la organización") {
		t.Errorf("whoami:\n%s", text)
	}
}

func TestBusinessCommandsRequireASession(t *testing.T) {
	o := setupSQLite(t)
	if r := run(t, o, "auth", "logout"); r.code != cli.ExitOK {
		t.Fatalf("logout: %d %s", r.code, r.stderr)
	}
	if _, err := os.Stat(sessionFile(o)); !os.IsNotExist(err) {
		t.Fatalf("logout debe borrar la sesión: %v", err)
	}
	for _, args := range [][]string{{"company", "list"}, {"org", "list"}, {"auth", "whoami"}, {"user", "list"}, {"role", "list"}} {
		r := run(t, o, args...)
		if r.code != cli.ExitUnauthenticated || !strings.Contains(r.stderr, "balanzapro auth login") {
			t.Errorf("%v: code=%d stderr=%s", args, r.code, r.stderr)
		}
	}
	r := run(t, o, "company", "list", "--json")
	if v := decode(t, r.stderr); v["error"].(map[string]any)["kind"] != "unauthenticated" {
		t.Errorf("error JSON = %v", v)
	}
	// Un token alterado tampoco sirve.
	if err := os.WriteFile(sessionFile(o), []byte("bps_falso\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := run(t, o, "company", "list"); r.code != cli.ExitUnauthenticated {
		t.Errorf("token falso: code=%d", r.code)
	}
	// Las herramientas técnicas no necesitan sesión.
	for _, args := range [][]string{{"version"}, {"doctor"}, {"db", "status"}, {"role", "permissions"}} {
		if r := run(t, o, args...); r.code != cli.ExitOK {
			t.Errorf("%v: code=%d stderr=%s", args, r.code, r.stderr)
		}
	}
}

func TestLoginReadsPasswordsSafely(t *testing.T) {
	o := setupSQLite(t)
	// Nunca como argumento.
	if r := run(t, o, "auth", "login", "--user", "admin", "--password", adminPassword); r.code != cli.ExitUsage {
		t.Errorf("--password: code=%d", r.code)
	}
	// Sin terminal ni --password-stdin, no hay forma de pedirla.
	if r := run(t, o, "auth", "login", "--user", "admin"); r.code != cli.ExitUsage || !strings.Contains(r.stderr, "--password-stdin") {
		t.Errorf("sin terminal: code=%d stderr=%s", r.code, r.stderr)
	}
	// En terminal, se pide oculta (aquí, el sustituto de ReadPassword).
	inter := o
	inter.interactive, inter.passwords, inter.stdin = true, []string{adminPassword}, "admin\n"
	if r := run(t, inter, "auth", "login"); r.code != cli.ExitOK || !strings.Contains(r.stdout, "sesión iniciada como admin") {
		t.Errorf("login interactivo: code=%d stdout=%s stderr=%s", r.code, r.stdout, r.stderr)
	}
	for _, c := range []struct{ user, pw string }{{"admin", "incorrecta"}, {"nadie", adminPassword}} {
		r := run(t, withStdin(o, c.pw), "auth", "login", "--user", c.user, "--password-stdin")
		if r.code != cli.ExitUnauthenticated || !strings.Contains(r.stderr, "usuario o contraseña incorrectos") ||
			strings.Contains(r.stderr+r.stdout, c.pw) {
			t.Errorf("%s: code=%d stderr=%s", c.user, r.code, r.stderr)
		}
	}
}

func TestPermissionDeniedByScopeAndOperationFromTheCLI(t *testing.T) {
	admin := setupSQLite(t)
	if r := run(t, admin, "company", "create", "--code", "norte", "--legal-name", "Norte, C.A."); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	if r := run(t, admin, "branch", "create", "--company", "norte", "--code", "maracay", "--name", "Maracay"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	r := run(t, withStdin(admin, userPassword), "user", "create", "--username", "Luis", "--name", "Luis Pérez", "--password-stdin", "--json")
	if v := decode(t, r.stdout); r.code != cli.ExitOK || v["schema"] != "balanzapro.user/v1" || v["username"] != "luis" || len(v["assignments"].([]any)) != 0 {
		t.Fatalf("user create: code=%d %v %s", r.code, r.stdout, r.stderr)
	}
	r = run(t, admin, "user", "assign", "--username", "luis", "--role", "encargado", "--company", "principal", "--branch", "principal", "--json")
	if v := decode(t, r.stdout); r.code != cli.ExitOK || v["role"] != "ENCARGADO" || v["scope"] != "branch" || v["branch_code"] != "PRINCIPAL" {
		t.Fatalf("assign: code=%d %s %s", r.code, r.stdout, r.stderr)
	}

	luis := asUser(t, admin, "luis", userPassword)
	// Concedido en su sucursal.
	if r := run(t, luis, "warehouse", "create", "--code", "danados", "--name", "Dañados"); r.code != cli.ExitOK {
		t.Errorf("encargado crea almacén en su sucursal: %d %s", r.code, r.stderr)
	}
	// La misma operación fuera de su ámbito: no ve la empresa.
	if r := run(t, luis, "warehouse", "create", "--company", "norte", "--branch", "maracay", "--code", "x", "--name", "X"); r.code != cli.ExitNotFound {
		t.Errorf("fuera de su ámbito: code=%d %s", r.code, r.stderr)
	}
	// Otra operación en su propio ámbito: denegada con el permiso faltante.
	r = run(t, luis, "branch", "create", "--code", "x", "--name", "X")
	if r.code != cli.ExitForbidden || !strings.Contains(r.stderr, "permiso denegado") || !strings.Contains(r.stderr, "org.branch.create") {
		t.Errorf("operación no concedida: code=%d stderr=%s", r.code, r.stderr)
	}
	r = run(t, luis, "branch", "create", "--code", "x", "--name", "X", "--json")
	if v := decode(t, r.stderr); v["error"].(map[string]any)["kind"] != "forbidden" {
		t.Errorf("error JSON = %v", v)
	}
	if r := run(t, luis, "user", "assign", "--username", "admin", "--role", "cajero", "--company", "principal"); r.code != cli.ExitForbidden {
		t.Errorf("encargado asigna roles: code=%d", r.code)
	}
	if items := decode(t, run(t, luis, "company", "list", "--json").stdout)["items"].([]any); len(items) != 1 {
		t.Errorf("el encargado solo ve su empresa: %v", items)
	}

	// Retirar el rol surte efecto en el siguiente comando, sin reiniciar sesión.
	if r := run(t, admin, "user", "unassign", "--username", "luis", "--role", "ENCARGADO", "--company", "principal", "--branch", "principal"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	if r := run(t, luis, "warehouse", "list"); r.code != cli.ExitForbidden {
		t.Errorf("sin roles: code=%d", r.code)
	}

	users := decode(t, run(t, admin, "user", "list", "--json").stdout)["items"].([]any)
	if len(users) != 2 {
		t.Fatalf("user list = %v", users)
	}
	if text := run(t, admin, "user", "list").stdout; !strings.Contains(text, "ADMIN_ORG en toda la organización") || !strings.Contains(text, "luis") {
		t.Errorf("user list:\n%s", text)
	}
}

func TestUserAdministration(t *testing.T) {
	admin := setupSQLite(t)
	if r := run(t, withStdin(admin, userPassword), "user", "create", "--username", "ana", "--name", "Ana", "--password-stdin"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	if r := run(t, admin, "user", "assign", "--username", "ana", "--role", "cajero", "--company", "principal"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	if r := run(t, withStdin(admin, "corta"), "user", "create", "--username", "beto", "--name", "Beto", "--password-stdin"); r.code != cli.ExitUsage ||
		!strings.Contains(r.stderr, "entre 8 y 128") {
		t.Errorf("contraseña corta: code=%d stderr=%s", r.code, r.stderr)
	}
	if r := run(t, withStdin(admin, userPassword), "user", "create", "--username", "ANA", "--name", "Otra", "--password-stdin"); r.code != cli.ExitConflict {
		t.Errorf("usuario repetido: code=%d", r.code)
	}

	// Bloqueo tras 5 fallos y desbloqueo por el administrador.
	for i := 0; i < 5; i++ {
		run(t, withStdin(admin, "incorrecta"), "auth", "login", "--user", "ana", "--password-stdin")
	}
	ana := admin
	ana.env = map[string]string{}
	for k, v := range admin.env {
		ana.env[k] = v
	}
	ana.env["BALANZAPRO_SESSION_FILE"] = filepath.Join(t.TempDir(), "ana")
	if r := run(t, withStdin(ana, userPassword), "auth", "login", "--user", "ana", "--password-stdin"); r.code != cli.ExitUnauthenticated {
		t.Fatalf("bloqueada: code=%d", r.code)
	}
	if text := run(t, admin, "user", "list").stdout; !strings.Contains(text, "bloqueado") {
		t.Errorf("user list debe mostrar el bloqueo:\n%s", text)
	}
	if r := run(t, admin, "user", "unlock", "--username", "ana"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	ana = asUser(t, admin, "ana", userPassword)

	// Cambio de contraseña propio (dos líneas: actual y nueva).
	if r := run(t, withStdin(ana, userPassword, "otra clave segura"), "auth", "passwd", "--password-stdin"); r.code != cli.ExitOK {
		t.Fatalf("passwd: %d %s", r.code, r.stderr)
	}
	if r := run(t, withStdin(ana, "otra clave segura"), "auth", "login", "--user", "ana", "--password-stdin"); r.code != cli.ExitOK {
		t.Fatalf("login con la nueva: %s", r.stderr)
	}

	// Desactivar cierra sus sesiones.
	if r := run(t, admin, "user", "disable", "--username", "ana", "--json"); r.code != cli.ExitOK || decode(t, r.stdout)["status"] != "disabled" {
		t.Fatalf("disable: %d %s", r.code, r.stderr)
	}
	if r := run(t, ana, "company", "list"); r.code != cli.ExitUnauthenticated {
		t.Errorf("sesión de usuario desactivado: code=%d", r.code)
	}
	if r := run(t, admin, "user", "enable", "--username", "ana"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	if r := run(t, withStdin(admin, "clave nueva de ella"), "user", "reset-password", "--username", "ana", "--password-stdin"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	asUser(t, admin, "ana", "clave nueva de ella")

	// No puede desactivarse a sí mismo ni dejar la organización sin administrador.
	if r := run(t, admin, "user", "disable", "--username", "admin"); r.code != cli.ExitForbidden {
		t.Errorf("desactivarse: code=%d %s", r.code, r.stderr)
	}
	if r := run(t, admin, "role", "edit", "--code", "admin_org", "--remove", "identity.user.manage"); r.code != cli.ExitConflict ||
		!strings.Contains(r.stderr, "al menos un administrador") {
		t.Errorf("último administrador: code=%d %s", r.code, r.stderr)
	}
}

func TestRoles(t *testing.T) {
	admin := setupSQLite(t)
	roles := decode(t, run(t, admin, "role", "list", "--json").stdout)["items"].([]any)
	if len(roles) != 6 {
		t.Fatalf("plantillas = %v", roles)
	}
	for _, r := range roles {
		for _, p := range r.(map[string]any)["permissions"].([]any) {
			if p == "pricing.override.approve_below_cost" || p == "system.backup.restore" {
				t.Errorf("la plantilla %v incluye %v", r.(map[string]any)["code"], p)
			}
		}
	}
	r := run(t, admin, "role", "create", "--code", "supervisor", "--name", "Supervisor", "--permission", "org.structure.read,identity.user.read", "--json")
	if v := decode(t, r.stdout); r.code != cli.ExitOK || v["code"] != "SUPERVISOR" || len(v["permissions"].([]any)) != 2 {
		t.Fatalf("role create: %d %s %s", r.code, r.stdout, r.stderr)
	}
	r = run(t, admin, "role", "edit", "--code", "supervisor", "--add", "org.warehouse.create", "--remove", "identity.user.read")
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "org.structure.read, org.warehouse.create") {
		t.Fatalf("role edit: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, admin, "role", "create", "--code", "x", "--name", "X", "--permission", "todo.poder"); r.code != cli.ExitUsage {
		t.Errorf("permiso desconocido: code=%d", r.code)
	}
	if r := run(t, admin, "role", "edit", "--code", "supervisor"); r.code != cli.ExitUsage {
		t.Errorf("edit sin cambios: code=%d", r.code)
	}
	text := run(t, admin, "role", "permissions").stdout
	if !strings.Contains(text, "system.backup.restore") || !strings.Contains(text, "[extraordinario]") {
		t.Errorf("catálogo:\n%s", text)
	}
}

func TestSetupCompletesAnInstallationFromM1001(t *testing.T) {
	o, path := sqliteEnv(t)
	if r := run(t, withStdin(o, adminPassword), "setup", "--name", "Ana", "--password-stdin"); r.code != cli.ExitOK {
		t.Fatal(r.stderr)
	}
	// Simula una base configurada con BP-M1-001: se revierte la migración de
	// identidad, y setup la vuelve a aplicar antes de completar.
	down, err := fs.ReadFile(migrations.FS, "sqlite/0003_identity.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{string(down), "DELETE FROM schema_migrations WHERE version = 3"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = db.Close()
	r := run(t, withStdin(o, adminPassword), "setup", "--admin-user", "dueno", "--password-stdin")
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "ya tenía organización") || !strings.Contains(r.stdout, "dueno") {
		t.Fatalf("completar: code=%d stdout=%s stderr=%s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, o, "company", "list"); r.code != cli.ExitOK {
		t.Fatalf("tras completar: %s", r.stderr)
	}
}

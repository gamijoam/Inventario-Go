# ADR-0008: Usuarios, contraseñas, sesiones y permisos por ámbito

- **Estado:** Aceptada (parámetros DEC-009.AUTH ratificados el 2026-10-26)
- **Fecha:** 2026-10-26
- **Decisiones relacionadas:** DEC-009.AUTH, DEC-007.B2, secciones 18.2–18.3, 26.2–26.4, 26.9, 27.1 y 28.2; BP-M1-002

## Contexto

Toda operación debe verificar **operación + sujeto + ámbito** en Go y
reforzarse en la base (sección 26.3). El nombre de un rol no confiere nada
por sí mismo y los permisos extraordinarios (bajo costo, restaurar) nunca se
conceden por defecto. La CLI no acepta contraseñas como argumento.

## Parámetros ratificados por el usuario

| Parámetro | Valor |
|---|---|
| Longitud de contraseña | mínimo 8, máximo 128 caracteres; sin reglas de composición; se rechazan contraseñas comunes, de un solo carácter repetido o que contengan el usuario o "balanzapro" |
| Sesión | vence tras **1 h sin uso** o **12 h** desde el inicio |
| Bloqueo | **5 fallos** seguidos → 15 min; cada bloqueo siguiente sin acceso correcto dura el doble (30, 60 min…), hasta 24 h; un administrador puede desbloquear |
| Roles | **plantillas editables** sembradas en cada organización |

## Decisión

1. **Dominio** (`internal/domain/identity`): `User`, `Role`, `Assignment`
   (rol + `BusinessScope`), `Permission` con catálogo cerrado, `Principal`
   y las políticas de contraseña, bloqueo y sesión como funciones puras.
   `Principal.Can(perm, ámbito)` solo es verdadero si alguna asignación
   incluye el permiso **y** su ámbito contiene al recurso. Para listar,
   `Sees` acepta también los ancestros (un cajero de sucursal ve su empresa).
2. **Catálogo inicial**: `org.structure.read`, `org.company.create`,
   `org.branch.create`, `org.warehouse.create`, `identity.user.read`,
   `identity.user.manage`, `identity.role.manage`, y los extraordinarios
   `pricing.override.approve_below_cost` (M4) y `system.backup.restore`
   (M5). Cada ticket agrega los permisos de sus casos de uso.
3. **Plantillas**: `ADMIN_ORG` (todo lo no extraordinario), `ADMIN_EMPRESA`,
   `ENCARGADO`, `CAJERO`, `ALMACENISTA`, `OPERADOR_TECNICO`. Se pueden editar
   y crear roles nuevos (`identity.role.manage` en la organización).
4. **Reglas contra la escalada de privilegios** (`internal/application/auth`):
   - Solo se delega lo que uno tiene en ese ámbito. Los extraordinarios los
     delega expresamente quien gestiona roles en toda la organización.
   - Nadie cambia su propio estado, contraseña (salvo `auth passwd`) ni
     asignaciones.
   - Gestionar a un usuario exige `identity.user.manage` en el ámbito de
     **todas** sus asignaciones: un administrador de empresa no toca a uno
     de organización.
   - Siempre queda al menos un usuario activo con `identity.role.manage` e
     `identity.user.manage` en toda la organización.
5. **Contraseñas**: Argon2id (`golang.org/x/crypto/argon2`) con los mínimos de
   OWASP: 19 MiB, t=2, p=1, sal de 16 bytes, clave de 32, en formato PHC. Se
   midió **30 ms** por verificación en el entorno de CI (4 núcleos);
   `Verify` informa si conviene regenerar el verificador con parámetros
   nuevos, y el inicio de sesión lo hace. La verificación lenta ocurre fuera
   de la transacción. Un usuario inexistente se verifica contra un
   verificador ficticio para tardar lo mismo.
6. **Inicio de sesión uniforme** (SEC-04): usuario inexistente, contraseña
   errada, cuenta bloqueada o desactivada responden lo mismo. Mientras dura
   el bloqueo no se cuentan intentos. El estado se relee en la segunda
   transacción, así dos intentos simultáneos no pierden un fallo.
7. **Sesiones**: token opaco de 256 bits (`bps_…`); la base guarda solo su
   SHA-256, y el archivo `session.file` (0600) solo el token. Cada comando
   resuelve de nuevo los permisos, así que retirar un rol o desactivar a un
   usuario surte efecto en el siguiente comando. Si el reloj retrocede más
   de 5 min respecto de la última actividad, la sesión se invalida (falla
   cerrada, sección 27.1). Cambiar o restablecer la contraseña y desactivar
   al usuario revocan sus sesiones.
8. **Base de datos** (migración 0003, ambos motores): `users`,
   `user_credentials` (un `CHECK` exige formato Argon2id: nunca texto
   plano), `roles`, `role_permissions`, `user_role_assignments` con FK
   compuestas por nivel de ámbito y un índice único por expresión que trata
   los niveles NULL como iguales, `sessions` y `security_events`
   (append-only por triggers; UPDATE, DELETE y TRUNCATE fallan).
9. **CLI**: `auth login|logout|whoami|passwd`, `user list|create|assign|
   unassign|disable|enable|unlock|reset-password`, `role list|permissions|
   create|edit`. Las contraseñas se piden sin eco o con `--password-stdin`.
   Nuevos códigos de salida: **8** permiso denegado, **9** sin sesión o
   credenciales incorrectas. `setup` crea además las plantillas y el
   administrador (`--admin-user`, por defecto `admin`) y deja su sesión
   iniciada; en una base configurada con BP-M1-001 sin usuarios, completa
   la instalación sin tocar la estructura.

## Alternativas consideradas

- **bcrypt**: más extendido, pero Argon2id es la recomendación actual de
  OWASP y el documento maestro lo propone; bcrypt además trunca a 72 bytes.
- **Contraseña en cada comando**: más segura en equipos compartidos, pero
  inviable para la operación diaria; la expiración de 1 h cubre el caso.
- **Roles fijos en código**: más simples, descartados por el usuario.
- **`NULLS NOT DISTINCT`** para la unicidad de asignaciones: solo existe en
  PostgreSQL ≥ 15; el índice por expresión funciona igual en ambos motores.

## Consecuencias

- Todos los comandos de negocio exigen sesión; `version`, `doctor`, `config`,
  `db` y `role permissions` no.
- La TUI todavía no pide inicio de sesión porque no muestra datos de
  negocio; deberá hacerlo cuando los muestre.
- Pendiente: bloqueo por terminal además de por cuenta (M5, multicaja),
  PIN de aprobación (BP-M4-003), consulta de `security_events` desde la CLI
  (`audit`), recuperación supervisada de la cuenta de administrador
  (sección 26.4) y *pepper* fuera de la base.
- Un PIN corto o una contraseña débil siguen expuestos a ataques fuera de
  línea si se roba la base; el cifrado de disco y de respaldos (DEC-009.BACKUP)
  es la defensa complementaria.

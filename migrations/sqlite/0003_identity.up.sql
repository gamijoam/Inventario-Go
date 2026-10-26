-- Identidad y RBAC contextual (BP-M1-002, sección 26.3): usuarios por
-- organización, verificadores de contraseña, roles con permisos explícitos,
-- asignaciones de rol limitadas a un ámbito, sesiones y eventos de seguridad.
-- Las claves foráneas compuestas impiden asignar un ámbito, rol o usuario de
-- otra organización.

CREATE TABLE users (
    organization_id TEXT NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    id              TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    username        TEXT NOT NULL CHECK (length(username) BETWEEN 3 AND 32 AND username = lower(username)),
    display_name    TEXT NOT NULL CHECK (length(display_name) BETWEEN 1 AND 120),
    status          TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
    created_at_utc  TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    updated_at_utc  TEXT NOT NULL CHECK (updated_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, username)
) STRICT;

-- Solo verificadores Argon2id: la base nunca guarda contraseñas en claro.
CREATE TABLE user_credentials (
    organization_id  TEXT    NOT NULL,
    user_id          TEXT    NOT NULL,
    kind             TEXT    NOT NULL CHECK (kind IN ('password')),
    verifier         TEXT    NOT NULL CHECK (verifier GLOB '$argon2id$v=*'),
    changed_at_utc   TEXT    NOT NULL CHECK (changed_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    failed_attempts  INTEGER NOT NULL CHECK (failed_attempts >= 0),
    lock_streak      INTEGER NOT NULL CHECK (lock_streak >= 0),
    locked_until_utc TEXT    CHECK (locked_until_utc IS NULL OR locked_until_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    PRIMARY KEY (user_id, kind),
    FOREIGN KEY (organization_id, user_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE TABLE roles (
    organization_id TEXT NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    id              TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    code            TEXT NOT NULL CHECK (length(code) BETWEEN 1 AND 32),
    name            TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    created_at_utc  TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    updated_at_utc  TEXT NOT NULL CHECK (updated_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, code)
) STRICT;

CREATE TABLE role_permissions (
    organization_id TEXT NOT NULL,
    role_id         TEXT NOT NULL,
    permission      TEXT NOT NULL CHECK (length(permission) BETWEEN 3 AND 64 AND permission GLOB '[a-z]*.[a-z]*'),
    PRIMARY KEY (role_id, permission),
    FOREIGN KEY (organization_id, role_id) REFERENCES roles (organization_id, id) ON DELETE CASCADE
) STRICT;

-- Ámbito: NULL en los niveles por debajo del asignado. Las FK compuestas no
-- se evalúan con columnas NULL (MATCH SIMPLE), por eso cada nivel tiene la
-- suya y los CHECK impiden huecos.
CREATE TABLE user_role_assignments (
    organization_id TEXT NOT NULL,
    id              TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    user_id         TEXT NOT NULL,
    role_id         TEXT NOT NULL,
    company_id      TEXT,
    branch_id       TEXT,
    warehouse_id    TEXT,
    created_by      TEXT NOT NULL,
    created_at_utc  TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    CHECK (branch_id IS NULL OR company_id IS NOT NULL),
    CHECK (warehouse_id IS NULL OR branch_id IS NOT NULL),
    FOREIGN KEY (organization_id, user_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, role_id) REFERENCES roles (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id) REFERENCES companies (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id, branch_id) REFERENCES branches (organization_id, company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT
) STRICT;

CREATE UNIQUE INDEX user_role_assignments_uq
    ON user_role_assignments (user_id, role_id, coalesce(company_id, ''), coalesce(branch_id, ''), coalesce(warehouse_id, ''));

-- Solo el SHA-256 del token: copiar la base no permite suplantar sesiones.
CREATE TABLE sessions (
    organization_id  TEXT NOT NULL,
    id               TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    user_id          TEXT NOT NULL,
    token_digest     TEXT NOT NULL UNIQUE CHECK (length(token_digest) = 64),
    created_at_utc   TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    last_seen_at_utc TEXT NOT NULL CHECK (last_seen_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    expires_at_utc   TEXT NOT NULL CHECK (expires_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    revoked_at_utc   TEXT CHECK (revoked_at_utc IS NULL OR revoked_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    FOREIGN KEY (organization_id, user_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX sessions_user_idx ON sessions (user_id);

-- Registro de seguridad de solo inserción (sección 26.9).
CREATE TABLE security_events (
    organization_id TEXT NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    id              TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    kind            TEXT NOT NULL CHECK (length(kind) BETWEEN 1 AND 64),
    actor_id        TEXT,
    subject_id      TEXT,
    detail          TEXT NOT NULL CHECK (length(detail) <= 500),
    occurred_at_utc TEXT NOT NULL CHECK (occurred_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    FOREIGN KEY (organization_id, actor_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, subject_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX security_events_time_idx ON security_events (organization_id, occurred_at_utc);

CREATE TRIGGER security_events_no_update BEFORE UPDATE ON security_events
BEGIN
    SELECT RAISE(ABORT, 'security_events es de solo inserción');
END;

CREATE TRIGGER security_events_no_delete BEFORE DELETE ON security_events
BEGIN
    SELECT RAISE(ABORT, 'security_events es de solo inserción');
END;

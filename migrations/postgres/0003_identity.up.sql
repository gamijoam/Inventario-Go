-- Identidad y RBAC contextual (BP-M1-002, sección 26.3): usuarios por
-- organización, verificadores de contraseña, roles con permisos explícitos,
-- asignaciones de rol limitadas a un ámbito, sesiones y eventos de seguridad.
-- Las claves foráneas compuestas impiden asignar un ámbito, rol o usuario de
-- otra organización.

CREATE TABLE users (
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    id              UUID        NOT NULL PRIMARY KEY,
    username        TEXT        NOT NULL CHECK (length(username) BETWEEN 3 AND 32 AND username = lower(username)),
    display_name    TEXT        NOT NULL CHECK (length(display_name) BETWEEN 1 AND 120),
    status          TEXT        NOT NULL CHECK (status IN ('active', 'disabled')),
    created_at_utc  TIMESTAMPTZ NOT NULL,
    updated_at_utc  TIMESTAMPTZ NOT NULL,
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, username)
);

-- Solo verificadores Argon2id: la base nunca guarda contraseñas en claro.
CREATE TABLE user_credentials (
    organization_id  UUID        NOT NULL,
    user_id          UUID        NOT NULL,
    kind             TEXT        NOT NULL CHECK (kind IN ('password')),
    verifier         TEXT        NOT NULL CHECK (verifier LIKE '$argon2id$v=%'),
    changed_at_utc   TIMESTAMPTZ NOT NULL,
    failed_attempts  INTEGER     NOT NULL CHECK (failed_attempts >= 0),
    lock_streak      INTEGER     NOT NULL CHECK (lock_streak >= 0),
    locked_until_utc TIMESTAMPTZ,
    PRIMARY KEY (user_id, kind),
    FOREIGN KEY (organization_id, user_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE TABLE roles (
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    id              UUID        NOT NULL PRIMARY KEY,
    code            TEXT        NOT NULL CHECK (length(code) BETWEEN 1 AND 32),
    name            TEXT        NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    created_at_utc  TIMESTAMPTZ NOT NULL,
    updated_at_utc  TIMESTAMPTZ NOT NULL,
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, code)
);

CREATE TABLE role_permissions (
    organization_id UUID NOT NULL,
    role_id         UUID NOT NULL,
    permission      TEXT NOT NULL CHECK (length(permission) BETWEEN 3 AND 64 AND permission ~ '^[a-z]+(\.[a-z_]+)+$'),
    PRIMARY KEY (role_id, permission),
    FOREIGN KEY (organization_id, role_id) REFERENCES roles (organization_id, id) ON DELETE CASCADE
);

-- Ámbito: NULL en los niveles por debajo del asignado. Las FK compuestas no
-- se evalúan con columnas NULL (MATCH SIMPLE), por eso cada nivel tiene la
-- suya y los CHECK impiden huecos.
CREATE TABLE user_role_assignments (
    organization_id UUID        NOT NULL,
    id              UUID        NOT NULL PRIMARY KEY,
    user_id         UUID        NOT NULL,
    role_id         UUID        NOT NULL,
    company_id      UUID,
    branch_id       UUID,
    warehouse_id    UUID,
    created_by      UUID        NOT NULL,
    created_at_utc  TIMESTAMPTZ NOT NULL,
    CHECK (branch_id IS NULL OR company_id IS NOT NULL),
    CHECK (warehouse_id IS NULL OR branch_id IS NOT NULL),
    FOREIGN KEY (organization_id, user_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, created_by) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, role_id) REFERENCES roles (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id) REFERENCES companies (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id, branch_id) REFERENCES branches (organization_id, company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, company_id, branch_id, warehouse_id) REFERENCES warehouses (organization_id, company_id, branch_id, id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX user_role_assignments_uq
    ON user_role_assignments (user_id, role_id,
        coalesce(company_id, '00000000-0000-0000-0000-000000000000'),
        coalesce(branch_id, '00000000-0000-0000-0000-000000000000'),
        coalesce(warehouse_id, '00000000-0000-0000-0000-000000000000'));

-- Solo el SHA-256 del token: copiar la base no permite suplantar sesiones.
CREATE TABLE sessions (
    organization_id  UUID        NOT NULL,
    id               UUID        NOT NULL PRIMARY KEY,
    user_id          UUID        NOT NULL,
    token_digest     TEXT        NOT NULL UNIQUE CHECK (length(token_digest) = 64),
    created_at_utc   TIMESTAMPTZ NOT NULL,
    last_seen_at_utc TIMESTAMPTZ NOT NULL,
    expires_at_utc   TIMESTAMPTZ NOT NULL,
    revoked_at_utc   TIMESTAMPTZ,
    FOREIGN KEY (organization_id, user_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX sessions_user_idx ON sessions (user_id);

-- Registro de seguridad de solo inserción (sección 26.9).
CREATE TABLE security_events (
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    id              UUID        NOT NULL PRIMARY KEY,
    kind            TEXT        NOT NULL CHECK (length(kind) BETWEEN 1 AND 64),
    actor_id        UUID,
    subject_id      UUID,
    detail          TEXT        NOT NULL CHECK (length(detail) <= 500),
    occurred_at_utc TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (organization_id, actor_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, subject_id) REFERENCES users (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX security_events_time_idx ON security_events (organization_id, occurred_at_utc);

CREATE FUNCTION security_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'security_events es de solo inserción';
END;
$$;

CREATE TRIGGER security_events_no_update BEFORE UPDATE OR DELETE ON security_events
    FOR EACH ROW EXECUTE FUNCTION security_events_append_only();
CREATE TRIGGER security_events_no_truncate BEFORE TRUNCATE ON security_events
    FOR EACH STATEMENT EXECUTE FUNCTION security_events_append_only();

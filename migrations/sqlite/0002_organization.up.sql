-- Jerarquía DEC-008.A: organización → empresas legales → sucursales → almacenes.
-- Las claves foráneas compuestas impiden referenciar entidades de otra
-- organización, empresa o sucursal aunque el ID exista (sección 22.5).

CREATE TABLE organizations (
    id             TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    code           TEXT NOT NULL UNIQUE CHECK (length(code) BETWEEN 1 AND 32),
    name           TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    status         TEXT NOT NULL CHECK (status IN ('active', 'inactive')),
    created_at_utc TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    updated_at_utc TEXT NOT NULL CHECK (updated_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z')
) STRICT;

CREATE TABLE companies (
    organization_id TEXT NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    id              TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    code            TEXT NOT NULL CHECK (length(code) BETWEEN 1 AND 32),
    legal_name      TEXT NOT NULL CHECK (length(legal_name) BETWEEN 1 AND 120),
    rif             TEXT CHECK (rif IS NULL OR rif GLOB '[VEJPGC]-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9]'),
    status          TEXT NOT NULL CHECK (status IN ('active', 'inactive')),
    created_at_utc  TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    updated_at_utc  TEXT NOT NULL CHECK (updated_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    UNIQUE (organization_id, id),
    UNIQUE (organization_id, code)
) STRICT;

CREATE UNIQUE INDEX companies_rif_uq ON companies (organization_id, rif) WHERE rif IS NOT NULL;

CREATE TABLE branches (
    organization_id TEXT NOT NULL,
    company_id      TEXT NOT NULL,
    id              TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    code            TEXT NOT NULL CHECK (length(code) BETWEEN 1 AND 32),
    name            TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    status          TEXT NOT NULL CHECK (status IN ('active', 'inactive')),
    created_at_utc  TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    updated_at_utc  TEXT NOT NULL CHECK (updated_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    UNIQUE (organization_id, company_id, id),
    UNIQUE (company_id, code),
    FOREIGN KEY (organization_id, company_id) REFERENCES companies (organization_id, id) ON DELETE RESTRICT
) STRICT;

CREATE TABLE warehouses (
    organization_id TEXT NOT NULL,
    company_id      TEXT NOT NULL,
    branch_id       TEXT NOT NULL,
    id              TEXT NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    code            TEXT NOT NULL CHECK (length(code) BETWEEN 1 AND 32),
    name            TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    status          TEXT NOT NULL CHECK (status IN ('active', 'inactive')),
    created_at_utc  TEXT NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    updated_at_utc  TEXT NOT NULL CHECK (updated_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    UNIQUE (organization_id, company_id, branch_id, id),
    UNIQUE (branch_id, code),
    FOREIGN KEY (organization_id, company_id, branch_id) REFERENCES branches (organization_id, company_id, id) ON DELETE RESTRICT
) STRICT;

-- Sucursal que custodia esta instalación (DEC-003: un nodo V1, una sucursal).
CREATE TABLE installation_binding (
    installation_id TEXT NOT NULL PRIMARY KEY REFERENCES installation (id) ON DELETE RESTRICT,
    organization_id TEXT NOT NULL,
    company_id      TEXT NOT NULL,
    branch_id       TEXT NOT NULL,
    bound_at_utc    TEXT NOT NULL CHECK (bound_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    FOREIGN KEY (organization_id, company_id, branch_id) REFERENCES branches (organization_id, company_id, id) ON DELETE RESTRICT
) STRICT;

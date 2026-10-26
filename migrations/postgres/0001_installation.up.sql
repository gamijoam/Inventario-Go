-- Identidad de esta instalación (nodo) y ajustes locales no secretos.
-- Los secretos nunca se guardan aquí (ADR-0005).

CREATE TABLE installation (
    id             UUID        NOT NULL PRIMARY KEY,
    -- Garantiza una sola fila por base de datos.
    singleton      BOOLEAN     NOT NULL DEFAULT TRUE UNIQUE CHECK (singleton),
    engine         TEXT        NOT NULL CHECK (engine IN ('sqlite', 'postgres')),
    created_at_utc TIMESTAMPTZ NOT NULL
);

CREATE TABLE installation_settings (
    installation_id UUID        NOT NULL REFERENCES installation (id) ON DELETE RESTRICT,
    key             TEXT        NOT NULL CHECK (length(key) BETWEEN 1 AND 128),
    value           TEXT        NOT NULL,
    updated_at_utc  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (installation_id, key)
);

CREATE INDEX installation_settings_updated_idx ON installation_settings (updated_at_utc);

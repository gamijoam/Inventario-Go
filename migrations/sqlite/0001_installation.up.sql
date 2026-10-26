-- Identidad de esta instalación (nodo) y ajustes locales no secretos.
-- Los secretos nunca se guardan aquí (ADR-0005).

CREATE TABLE installation (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36),
    -- Garantiza una sola fila por base de datos.
    singleton      INTEGER NOT NULL DEFAULT 1 UNIQUE CHECK (singleton = 1),
    engine         TEXT    NOT NULL CHECK (engine IN ('sqlite', 'postgres')),
    created_at_utc TEXT    NOT NULL CHECK (created_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z')
) STRICT;

CREATE TABLE installation_settings (
    installation_id TEXT NOT NULL REFERENCES installation (id) ON DELETE RESTRICT,
    key             TEXT NOT NULL CHECK (length(key) BETWEEN 1 AND 128),
    value           TEXT NOT NULL,
    updated_at_utc  TEXT NOT NULL CHECK (updated_at_utc GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*Z'),
    PRIMARY KEY (installation_id, key)
) STRICT;

CREATE INDEX installation_settings_updated_idx ON installation_settings (updated_at_utc);

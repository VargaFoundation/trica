-- +goose Up
-- UI themes (ADR 0054): the archives an administrator uploaded (or that a configured
-- URL served), and which one is in force. This is not inventory: nothing in it is a
-- claim about equipment, so the single-writer and nothing-is-deleted rules (ADR 0005)
-- do not apply; every change is audited by the API and the tables are in the backup
-- like the rest of the database (ADR 0021).

CREATE TABLE ui_themes (
    sha256      bytea PRIMARY KEY CHECK (octet_length(sha256) = 32),
    name        text NOT NULL,
    title       text NOT NULL,
    version     text NOT NULL,
    archive     bytea NOT NULL CHECK (octet_length(archive) <= 2097152),
    origin      text NOT NULL CHECK (origin IN ('upload', 'url')),
    uploaded_by text NOT NULL,
    uploaded_at timestamptz NOT NULL DEFAULT now()
);

-- At most one row: the administrator's selection. No row means the deployment
-- default (the configuration, or the built-in default).
CREATE TABLE ui_theme_active (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    sha256    bytea REFERENCES ui_themes (sha256),
    builtin   text,
    set_by    text NOT NULL,
    set_at    timestamptz NOT NULL,
    CHECK ((sha256 IS NULL) <> (builtin IS NULL))
);

-- +goose Down
DROP TABLE ui_theme_active;
DROP TABLE ui_themes;

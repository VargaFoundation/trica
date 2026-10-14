-- +goose Up
-- Core inventory model. See docs/adr/0002-storage-model.md.
--
-- Every value is a claim made by a source. The resolved value of an
-- attribute is the winning claim under the priority rules; it is historised
-- in attr_history with a validity range, so "what was true at T" is a range
-- containment query. Relations follow the same pattern. Nothing is ever
-- deleted: objects become absent or decommissioned, ranges get closed.

CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE changesets (
    id          bigserial PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT now(),
    source      text        NOT NULL,
    author      text        NOT NULL DEFAULT '',
    reason      text        NOT NULL DEFAULT '',
    kind        text        NOT NULL CHECK (kind IN ('collection', 'declaration', 'proposal', 'system', 'import')),
    stats       jsonb       NOT NULL DEFAULT '{}'
);
CREATE INDEX changesets_at ON changesets (at DESC);

CREATE TABLE objects (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    type        text        NOT NULL,
    parent_id   uuid        REFERENCES objects (id),
    display     text        NOT NULL DEFAULT '',
    status      text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'absent')),
    attrs       jsonb       NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    first_seen  timestamptz,
    last_seen   timestamptz,
    observed    boolean     NOT NULL DEFAULT false,
    search_text text        NOT NULL DEFAULT '',
    search_tsv  tsvector    GENERATED ALWAYS AS (to_tsvector('simple', search_text)) STORED
);
CREATE INDEX objects_type ON objects (type);
CREATE INDEX objects_parent ON objects (parent_id);
CREATE INDEX objects_search_trgm ON objects USING gin (search_text gin_trgm_ops);
CREATE INDEX objects_search_tsv ON objects USING gin (search_tsv);
CREATE INDEX objects_attrs ON objects USING gin (attrs jsonb_path_ops);
CREATE INDEX objects_last_seen ON objects (last_seen) WHERE observed AND status = 'active';

-- One row per (object, attribute, source): what each source currently says.
-- ident holds the normalised value when the attribute identifies the object.
CREATE TABLE claims (
    object_id    uuid        NOT NULL REFERENCES objects (id),
    key          text        NOT NULL,
    source       text        NOT NULL,
    value        jsonb       NOT NULL,
    ident        text,
    strong       boolean     NOT NULL DEFAULT false,
    confidence   text        NOT NULL CHECK (confidence IN ('observed', 'deduced', 'declared')),
    first_seen   timestamptz NOT NULL,
    last_seen    timestamptz NOT NULL,
    changeset_id bigint      NOT NULL REFERENCES changesets (id),
    PRIMARY KEY (object_id, key, source)
);
CREATE INDEX claims_ident ON claims (key, ident) WHERE ident IS NOT NULL;
CREATE INDEX claims_source ON claims (source);

-- Resolved attribute values over time. Keys starting with "_" are system
-- attributes: _status (active/absent) and _parent, so presence and moves are
-- historised like any other value.
CREATE TABLE attr_history (
    id           bigserial   PRIMARY KEY,
    object_id    uuid        NOT NULL REFERENCES objects (id),
    key          text        NOT NULL,
    value        jsonb       NOT NULL,
    source       text        NOT NULL,
    confidence   text        NOT NULL,
    author       text        NOT NULL DEFAULT '',
    valid        tstzrange   NOT NULL,
    changeset_id bigint      NOT NULL REFERENCES changesets (id),
    EXCLUDE USING gist (object_id WITH =, key WITH =, valid WITH &&)
);
CREATE INDEX attr_history_current ON attr_history (object_id, key) WHERE upper_inf(valid);
CREATE INDEX attr_history_changeset ON attr_history (changeset_id);
CREATE INDEX attr_history_value_trgm ON attr_history USING gin ((value #>> '{}') gin_trgm_ops);

CREATE TABLE relations (
    id           bigserial   PRIMARY KEY,
    type         text        NOT NULL,
    from_id      uuid        NOT NULL REFERENCES objects (id),
    to_id        uuid        NOT NULL REFERENCES objects (id),
    attrs        jsonb       NOT NULL DEFAULT '{}',
    source       text        NOT NULL,
    confidence   text        NOT NULL,
    valid        tstzrange   NOT NULL,
    changeset_id bigint      NOT NULL REFERENCES changesets (id),
    closed_by    bigint      REFERENCES changesets (id),
    EXCLUDE USING gist (type WITH =, from_id WITH =, to_id WITH =, valid WITH &&)
);
CREATE INDEX relations_from ON relations (from_id) WHERE upper_inf(valid);
CREATE INDEX relations_to ON relations (to_id) WHERE upper_inf(valid);
CREATE INDEX relations_valid ON relations USING gist (valid);

CREATE TABLE rel_claims (
    type         text        NOT NULL,
    from_id      uuid        NOT NULL REFERENCES objects (id),
    to_id        uuid        NOT NULL REFERENCES objects (id),
    source       text        NOT NULL,
    attrs        jsonb       NOT NULL DEFAULT '{}',
    confidence   text        NOT NULL,
    first_seen   timestamptz NOT NULL,
    last_seen    timestamptz NOT NULL,
    changeset_id bigint      NOT NULL REFERENCES changesets (id),
    PRIMARY KEY (type, from_id, to_id, source)
);
CREATE INDEX rel_claims_source ON rel_claims (source);

CREATE TABLE discrepancies (
    id           bigserial   PRIMARY KEY,
    kind         text        NOT NULL,
    fingerprint  text        NOT NULL,
    object_id    uuid        REFERENCES objects (id),
    related_id   uuid        REFERENCES objects (id),
    key          text        NOT NULL DEFAULT '',
    declared     jsonb,
    observed     jsonb,
    details      jsonb       NOT NULL DEFAULT '{}',
    status       text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'accepted')),
    opened_at    timestamptz NOT NULL DEFAULT now(),
    last_seen    timestamptz NOT NULL DEFAULT now(),
    closed_at    timestamptz,
    closed_by    text        NOT NULL DEFAULT '',
    note         text        NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX discrepancies_open ON discrepancies (fingerprint) WHERE status = 'open';
CREATE INDEX discrepancies_object ON discrepancies (object_id);

CREATE TABLE proposals (
    id           bigserial   PRIMARY KEY,
    created_at   timestamptz NOT NULL DEFAULT now(),
    source       text        NOT NULL,
    author       text        NOT NULL DEFAULT '',
    reason       text        NOT NULL DEFAULT '',
    batch        jsonb       NOT NULL,
    fingerprint  text        NOT NULL,
    status       text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'applied', 'rejected', 'superseded')),
    decided_at   timestamptz,
    decided_by   text        NOT NULL DEFAULT '',
    changeset_id bigint      REFERENCES changesets (id)
);
CREATE UNIQUE INDEX proposals_pending ON proposals (fingerprint) WHERE status = 'pending';

CREATE TABLE source_health (
    source       text        PRIMARY KEY,
    collector    text        NOT NULL DEFAULT '',
    target       text        NOT NULL DEFAULT '',
    version      text        NOT NULL DEFAULT '',
    ok           boolean     NOT NULL,
    last_attempt timestamptz NOT NULL,
    last_success timestamptz,
    last_error   text        NOT NULL DEFAULT '',
    duration_ms  bigint      NOT NULL DEFAULT 0,
    items        integer     NOT NULL DEFAULT 0,
    warnings     jsonb       NOT NULL DEFAULT '[]',
    failures     integer     NOT NULL DEFAULT 0
);

-- User-defined types, attributes and relations (schema overlay).
CREATE TABLE custom_schema (
    kind         text        NOT NULL CHECK (kind IN ('type', 'relation')),
    name         text        NOT NULL,
    definition   jsonb       NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    updated_by   text        NOT NULL DEFAULT '',
    PRIMARY KEY (kind, name)
);

-- Tamper-evident audit trail: each row hashes the previous row's hash.
CREATE TABLE audit_log (
    id           bigserial   PRIMARY KEY,
    at           timestamptz NOT NULL DEFAULT now(),
    actor        text        NOT NULL,
    action       text        NOT NULL,
    target       text        NOT NULL DEFAULT '',
    details      jsonb       NOT NULL DEFAULT '{}',
    prev_hash    bytea       NOT NULL,
    hash         bytea       NOT NULL
);

CREATE TABLE saved_queries (
    id           bigserial   PRIMARY KEY,
    name         text        NOT NULL UNIQUE,
    query        text        NOT NULL,
    description  text        NOT NULL DEFAULT '',
    owner        text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE api_tokens (
    id           bigserial   PRIMARY KEY,
    name         text        NOT NULL,
    hash         bytea       NOT NULL UNIQUE,
    scopes       text[]      NOT NULL,
    created_by   text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz,
    last_used    timestamptz,
    revoked_at   timestamptz
);

-- +goose Down
DROP TABLE api_tokens;
DROP TABLE saved_queries;
DROP TABLE audit_log;
DROP TABLE custom_schema;
DROP TABLE source_health;
DROP TABLE proposals;
DROP TABLE discrepancies;
DROP TABLE rel_claims;
DROP TABLE relations;
DROP TABLE attr_history;
DROP TABLE claims;
DROP TABLE objects;
DROP TABLE changesets;

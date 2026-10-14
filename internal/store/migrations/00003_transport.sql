-- +goose Up
-- Agent and collector transport (docs/adr/0008-transport.md): enrolment
-- tokens, issued client certificates and the revocation list. These tables
-- belong to internal/transport; they hold no inventory data.

-- One-time enrolment tokens. Only the sha256 of the token is stored: a read
-- of this table does not let anyone enrol. A token grants one source binding
-- (the prefix the enrolled client may report as) and works once.
CREATE TABLE enrol_tokens (
    token_hash  bytea       PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    binding     text        NOT NULL,
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz
);

-- Every client certificate the CA signed, so an operator can find the serial
-- to revoke from a binding.
CREATE TABLE issued_certs (
    serial      text        PRIMARY KEY,
    binding     text        NOT NULL,
    not_before  timestamptz NOT NULL,
    not_after   timestamptz NOT NULL,
    issued_at   timestamptz NOT NULL
);
CREATE INDEX issued_certs_binding ON issued_certs (binding);

-- Revoked certificate serials (lowercase hex), checked on every request.
CREATE TABLE revoked_certs (
    serial      text        PRIMARY KEY,
    reason      text        NOT NULL DEFAULT '',
    revoked_at  timestamptz NOT NULL
);

-- +goose Down
DROP TABLE revoked_certs;
DROP TABLE issued_certs;
DROP TABLE enrol_tokens;

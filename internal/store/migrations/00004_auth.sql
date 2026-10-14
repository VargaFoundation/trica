-- +goose Up
-- Authentication and audit. See docs/adr/0013-authentication-and-audit.md.
--
-- audit_log gains the outcome and the request id, and becomes append-only at the
-- database level: UPDATE, DELETE and TRUNCATE are refused by triggers. The hash chain
-- (internal/audit) detects tampering by a role that disables the triggers; the triggers
-- stop an application bug from doing it by accident.
--
-- api_tokens gains the owner's roles at creation time (a token never exceeds them) and the
-- optional restriction to object types and sites. An empty array means "no restriction".

ALTER TABLE audit_log
    ADD COLUMN outcome    text NOT NULL DEFAULT 'success' CHECK (outcome IN ('success', 'denied', 'failure')),
    ADD COLUMN request_id text NOT NULL DEFAULT '';

-- +goose StatementBegin
CREATE FUNCTION audit_log_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only (% refused)', TG_OP USING ERRCODE = 'insufficient_privilege';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_log_no_change BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_append_only();
CREATE TRIGGER audit_log_no_truncate BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION audit_log_append_only();

ALTER TABLE api_tokens
    ADD COLUMN owner_roles text[] NOT NULL DEFAULT '{}',
    ADD COLUMN types       text[] NOT NULL DEFAULT '{}',
    ADD COLUMN sites       text[] NOT NULL DEFAULT '{}',
    ADD CONSTRAINT api_tokens_scopes_known CHECK (scopes <@ ARRAY['read', 'write', 'admin']);

-- +goose Down
ALTER TABLE api_tokens
    DROP CONSTRAINT api_tokens_scopes_known,
    DROP COLUMN sites,
    DROP COLUMN types,
    DROP COLUMN owner_roles;

DROP TRIGGER audit_log_no_truncate ON audit_log;
DROP TRIGGER audit_log_no_change ON audit_log;
DROP FUNCTION audit_log_append_only();

ALTER TABLE audit_log
    DROP COLUMN request_id,
    DROP COLUMN outcome;

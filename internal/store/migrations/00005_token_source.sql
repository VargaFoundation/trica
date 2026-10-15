-- +goose Up
-- An API token may be bound to a source (#53, docs/adr/0028-token-bound-source.md): the batches
-- it submits are then attributed to that source, so source priorities (ADR 0004) rank them by
-- their real name. NULL keeps the derived name api:token-<id>. The format is that of a transport
-- binding (internal/pki): two segments at least, so a token cannot speak for a whole kind.

ALTER TABLE api_tokens
    ADD COLUMN source text
        CONSTRAINT api_tokens_source_format CHECK (source ~ '^[A-Za-z0-9._-]+(:[A-Za-z0-9._-]+)+$');

-- +goose Down
ALTER TABLE api_tokens DROP COLUMN source;

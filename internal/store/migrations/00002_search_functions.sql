-- +goose Up
-- Helpers for the structured search (ADR 0012). Both are immutable and never
-- raise, so the search can apply them to any attribute value.

-- trica_version_key turns "4.20.1F" into {4,20,1}: versions then compare
-- component by component, "4.9" < "4.20".
-- +goose StatementBegin
CREATE FUNCTION trica_version_key(v text) RETURNS numeric[]
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT coalesce(array_agg(m[1]::numeric ORDER BY n), '{}')
    FROM regexp_matches(v, '[0-9]+', 'g') WITH ORDINALITY AS t(m, n)
$$;
-- +goose StatementEnd

-- trica_inet casts to inet, or returns NULL for a value that is not an
-- address or a prefix.
-- +goose StatementBegin
CREATE FUNCTION trica_inet(v text) RETURNS inet
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
BEGIN
    RETURN v::inet;
EXCEPTION WHEN others THEN
    RETURN NULL;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION trica_inet(text);
DROP FUNCTION trica_version_key(text);

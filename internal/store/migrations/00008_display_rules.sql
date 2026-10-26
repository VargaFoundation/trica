-- +goose Up
-- The display rule each object type was last applied with (ADR 0065). The display
-- name of an object is stored (objects.display), so a change of rule (a new
-- display template) leaves the stored names of existing objects as they were
-- until the engine recomputes them: at start, it compares this table with the
-- schema's rules and recomputes the types that differ. The table only records
-- which rule the stored names follow; it is not inventory and holds no claim
-- about equipment.
CREATE TABLE display_rules (
    type       text PRIMARY KEY,
    rule       text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE display_rules;

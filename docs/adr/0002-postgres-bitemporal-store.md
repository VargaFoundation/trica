# ADR-0002: PostgreSQL only, with history and provenance in the schema

- **Status**: accepted (2026-10)
- **Concerns**: `internal/store`, `internal/reconcile`

## Context

The specification asks for the state at any past date, the diff between two dates, a timeline
per object, and for every value: its source, its first and last observation, its author and its
confidence. It also asks that nothing disappears without a trace.

## Decision

- `objects` holds the current, materialised state (status, attributes as `jsonb`, display name,
  search text) for fast reads.
- `claims` holds what each source currently says, per object, key and source, with its
  confidence and its first/last seen. Identity claims carry `ident` and `strong`.
- `attr_history` holds the resolved value of each attribute over time as a `tstzrange`, with a
  GiST exclusion constraint so that two values can never overlap. System keys (`_status`,
  `_parent`) are historised the same way, which makes moves and absences part of the timeline.
- `relations` and `rel_claims` do the same for relations.
- `changesets` group every write with its kind (collection, declaration, proposal, system,
  import), source and author.
- Migrations are `goose`, embedded, and always reversible; a test runs up, down, up.

## Consequences

- "Who had this IP on March 3rd" and "what changed between two dates" are SQL range queries.
- The schema is PostgreSQL-specific (`tstzrange`, `btree_gist`, `pg_trgm`). That is accepted.

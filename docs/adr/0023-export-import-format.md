# ADR-0023: Inventory export and import: a versioned document, import through the engine

- **Status**: accepted (2026-10)
- **Concerns**: `internal/inventoryio`, `internal/store` (read paths), `cmd/trica`

## Context

Milestone 2 asks for full JSON/YAML export and import. Two risks: an import that writes
tables directly would bypass identity, priority and history (ADR 0005); an export that dumps
everything would leak secrets and tie the format to table layout.

## Decision

- **Format**: one document, `format: trica-inventory`, `version: 1`, in JSON or YAML (same
  structure). Objects carry a file-local `ref` (the exporting inventory's id), `type`, `parent`
  (a ref), `identity` (the identifying attributes), `attributes`, optional `provenance` (source,
  confidence, since per attribute) and, with `--history`, `history`. Relations link refs.
  Decoding rejects unknown fields and any other `format` or `version`: a future version 2 is
  a new reader, never a silent reinterpretation.
- **Import never writes a table.** `inventoryio.ToBatch` turns the document into an `sdk.Batch`
  (source `import:<name>`, confidence `declared`, scope `partial` by default or `full` when
  asked) and `Import` submits it to the engine. Identity, priority, history and proposals
  policy apply as for any source. Consequences: a declared import never overrides an observed
  value; `--scope full` withdraws what an earlier import of the same name claimed and the new
  one omits; re-importing the same file changes nothing.
- **What import reads**: objects (identity, attributes, parent) and open relations. It ignores
  `provenance`, `status`, `history`, closed relations and ids: provenance would claim a source
  the file's author is not, and the past cannot be replayed through a batch (the engine writes
  history as it happens).
- **State at a date** (`--at`): objects, parents, values and relations valid at that instant,
  read from the history tables. Objects that did not exist yet are absent from the file.
- **Round trip contract**: export, import into an empty database, export again gives the same
  objects, identities, attributes, parent links and relations. Not preserved, by design: ids and
  refs, sources and confidences (everything becomes `import:<name>` / `declared`), timestamps,
  history, absent status.
- **Secrets are never exported**, in the state or in the history, and import refuses a file
  that carries one. `internal/schema` has no sensitive marker on main, so the guard is a name
  check (`password`, `secret`, `token`, `community`, `api_key`, `private_key`, `credential`,
  `psk`, ...) applied to every type and every relation attribute; it must also consult
  `schema.Sensitive` once that lands (PR #26). The export reports how many values it left out.
- Export reads through new `store` read paths (`ValuesAt`, `RelationsAt`); there is no new write path.

## Consequences

- A name-based guard can miss a secret named innocuously: until the schema marker is used,
  operators defining custom attributes must name them accordingly.
- Custom types stored in the database overlay are not loaded by the CLI yet (built-in registry
  only), so objects of a custom type fail the import with an unknown-type error.
- Importing a file exported from a live inventory turns observed facts into declarations; that
  is the intended use for migration and seeding, not for replication.

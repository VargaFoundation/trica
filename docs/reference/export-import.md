# Export and import

`trica export` writes the inventory as JSON or YAML; `trica import` submits such a file to the
reconciliation engine. Design: ADR 0023.

The database is given by `--database-url` or `TRICA_DATABASE_URL`.

## Export

```
trica export [-o inventory.yaml] [--format json|yaml] [--at 2026-03-01] [--history] [--no-provenance]
```

| Flag | Effect |
|---|---|
| `-o FILE` | Write to FILE (stdout by default). The format follows the extension (`.yaml`, `.yml`), else JSON. |
| `--format` | Force `json` or `yaml`. |
| `--at DATE` | State at that instant (RFC 3339 or `YYYY-MM-DD`, UTC): objects, values, parents and relations valid then. |
| `--history` | Add every past value (`history`, per object) and every closed relation (`until` set). With `--at`, only what started by that date. |
| `--no-provenance` | Leave out each attribute's source, confidence and start date. |

Sensitive attributes (names matching `password`, `secret`, `token`, `community`, `api_key`,
`private_key`, `credential`, `psk`) are never written, current or historical; the command says on
stderr how many it left out.

## Document

```yaml
format: trica-inventory
version: 1
exported_at: 2026-03-01T08:00:00Z
objects:
  - ref: 6f1c...            # unique in the file; the exporting inventory's id
    type: device
    parent: 0a9e...         # a ref, optional
    status: active          # informational
    identity: {serial: JPE111, name: sw-a1}
    attributes: {serial: JPE111, name: sw-a1, role: switch, mgmt_ip: 192.0.2.10}
    provenance:             # informational, ignored by import
      role: {source: "arista:sw-a1", confidence: observed, since: 2026-03-01T08:00:00Z}
relations:
  - {type: mounted_in, from: 6f1c..., to: 0a9e..., attributes: {position: 10, face: front}}
```

A hand-written file needs only `format`, `version`, objects (`ref`, `type`, `identity`,
`attributes`, `parent`) and relations. `identity` keys must be identifying attributes of the type
(see the schema) and are written as attributes too.

## Import

```
trica import FILE [--name NAME] [--scope partial|full] [--dry-run] [--author A] [--reason R]
```

- The batch source is `import:NAME` (NAME defaults to the file name), confidence `declared`.
  Declared values rank below observed ones: an import never overrides what a collector sees.
- `--scope partial` (default) adds and refreshes. `--scope full` also withdraws what an earlier
  import with the same NAME claimed and this file omits (history keeps it; nothing is deleted).
- `--dry-run` runs the engine in a rolled-back transaction and prints the counts.
- Read: objects and open relations. Ignored: `provenance`, `status`, `history`, closed relations
  (`until`), `exported_at`, `at`. Refused: unknown fields, another `format` or `version`, an
  attribute that looks like a secret, an unknown type or relation, a dangling ref.
- Re-importing the same file changes nothing.

## Round trip

Export, import into an empty database, export again gives the same objects, identities,
attributes, parent links and relations. Ids, refs, sources, confidences, timestamps, history and
absent status are not preserved (everything comes back as `import:NAME`, `declared`).

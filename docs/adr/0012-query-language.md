# ADR-0012: A small query language (TQL) compiled to parameterised SQL

- **Status**: accepted (2026-10)
- **Concerns**: `internal/tql`, `internal/search`, the search bar, the API, MCP `search` and
  `rank_filter`

## Context

The search bar, the API, saved queries and the MCP tools all need the same two things: free
text that forgives typos and any format of an address ("find everything about 192.0.2.13"),
and structured filters that can be saved ("Arista switches on old firmware", "SSDs worn over
70 % in rack 2"). Attributes are dynamic (custom types live in the database), so the filters
cannot be a fixed set of API parameters. Exposing SQL, or a general language such as CEL or
JMESPath over JSON, would hand callers the schema of the store or a full evaluation engine.

## Decision

- One grammar, `field op value` terms joined by AND, OR, NOT and parentheses, plus free text and
  two top-level directives, `sort:` and `limit:`. Reference: `docs/reference/tql.md`.
- `internal/tql` only parses. `internal/search` compiles the tree to SQL on the `objects`
  table, with every value and attribute name passed as a parameter: no query text is ever
  spliced into SQL.
- Fields are the object columns (`type`, `status`, `id`, `display`, times), a few relational
  fields (`parent`, `has`, `source`, `rel.<type>`), and otherwise attributes. Values compare as
  numbers, as versions (`4.9.1 < 4.20.7M`) or as text, by the stored JSON type.
- Absent objects are left out unless the query mentions `status`.
- Free text matches exactly first (substring on the search text, a MAC in any notation, an id);
  only when that finds nothing does a second pass tolerate typos, by trigram similarity on the
  search text and on the display name stripped of punctuation. A typo-tolerant first pass
  buried exact hits among their neighbours (`sw-a1` also found `sw-a2`).
- Searches run in a read-only transaction with a 5 s statement timeout.

## Consequences

- Saved queries are TQL text, validated on save; the same text works in the UI, the API and
  MCP.
- Performance is a property of the compiled SQL: the acceptance test loads 2,000 servers
  through the engine and fails if a representative query's median exceeds 200 ms. A relational
  predicate written as a correlated walk took 4.4 s there; it is compiled as a set built once.
- Search in the past ("which device had this address on 3 March") reads the history tables
  and is not part of this grammar yet.

## Addendum: a version policy's selector is validated when written (2026-10)

A selector is parsed as TQL, and text that is not a query does not fail to parse: it becomes
free text and matches nothing. The schema example (`device where vendor = 'Arista'`) was such a
text, so a policy written from it governed zero objects and a compliance report read "nothing is
behind". The engine now refuses, with `ErrInvalid`, a batch whose `version_policy.selector` does
not parse **or** is not made of field predicates only (free text, `sort:` and `limit:` are
refused). An empty selector stays allowed. The schema example is now `type:device vendor:arista`.

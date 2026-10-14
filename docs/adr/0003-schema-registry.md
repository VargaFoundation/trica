# ADR-0003: A schema registry, built-in types in YAML, custom types in the database

- **Status**: accepted (2026-10)
- **Concerns**: `internal/schema`, API, search, MCP, UI

## Context

The model covers about sixty types from the site to the contract, and users must be able to add
types and fields without a release. The API, the search, the MCP tools and the UI must all see
the same model.

## Decision

- Built-in types and relation types live in `internal/schema/builtin.yaml`, embedded.
- Custom types and fields live in the `custom_schema` table and are merged over the built-in
  registry; a custom overlay may add, never redefine a built-in attribute's kind.
- Each type declares its identity keys as strong (serial, MAC, UUID) or weak (name, IP), and
  whether weak keys are scoped by the parent.
- Each relation type declares its impact mode (ADR 0011).
- Everything else (OpenAPI, MCP tool descriptions, UI forms) is generated from the registry.

## Consequences

- A custom type is visible in the API, the search and MCP without code; a test guards it.

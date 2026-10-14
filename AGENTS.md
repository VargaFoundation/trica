# trica: instructions for agents

`CLAUDE.md` is a symbolic link to this file.

## Context
trica is an infrastructure inventory and visualisation platform (CMDB/DCIM): a historised graph
of everything from the site to the application, where every value carries its source, its
confidence and its history, fed by automatic discovery and queryable through an MCP server.
Varga Foundation, Apache-2.0. Plan and status: `docs/plan/`. Structural decisions: `docs/adr/`.
The collector contract is `pkg/sdk`: it is public, and a breaking change needs an ADR.

## Language
Everything in this repository is in **English**: code, comments, documentation, commit messages,
pull requests, issues.

## Commands (the ones CI runs, no others)
- What blocks a PR: `make ci` = `lint` + `vet` + `test-cov` (coverage across packages, minimum 80 %).
- Integration tests need PostgreSQL: `make db` starts one and prints the
  `TRICA_TEST_DATABASE_URL` to export. Without it they are skipped; with `TRICA_REQUIRE_DB=1`
  (set in CI) a skip is a failure.
- `make tools` installs the pinned golangci-lint.

## Architecture rules
- **Only `internal/reconcile` writes the inventory.** Collectors, the agent, declaration files,
  the UI, the API and the MCP tools all submit an `sdk.Batch`; the engine decides what to
  create, update, propose or flag (ADR 0005). No other package issues an INSERT or UPDATE on
  `objects`, `claims`, `attr_history`, `relations` or `rel_claims`.
- **Nothing is deleted.** An object nobody sees becomes `absent`; a value nobody claims is
  closed in the history. A DELETE on inventory tables is a bug.
- **Collectors are read-only.** A collector never writes to a device, whatever the protocol
  allows. A collector that needs a write is a design error.
- **Fakes are mandatory**: every integration has a `Fake*` serving recorded, anonymised
  responses (`test/fixtures/`), so that tests and the simulated lab never need real equipment.
- **No secret in code, fixtures or logs.** Device credentials are references (a Kubernetes
  Secret, a file); the API never returns them. Fixtures are anonymised before commit.
- Logs are JSON (`log/slog`), with the keys `source`, `collector`, `target`, `object_id` when
  they exist.

## Conventions
- Go (version in `go.mod`), `pgx/v5`, `goose` migrations that are always reversible, standard
  library `net/http`. Front end: React + TypeScript + Vite in `web/`.
- Conventional commits with a scope: `feat(reconcile): …`, `fix(collect/arista): …`. One story
  = one PR = one squash. Every commit is signed off (`git commit -s`, DCO).
- Tests are table-driven where it reads better; integration tests use `internal/testdb`, which
  gives each test its own database.

## Definition of done (blocking)
`make ci` green, docs updated, acceptance criteria checked in the PR, no `TODO` without a linked
issue, `docs/plan/STATUS.md` updated **saying what the PR proves and what it does not**.

**Nothing is declared done without a test that fails in its absence.** A feature whose guard can
be removed without turning the suite red is not delivered, it is hoped for.

## Out of scope
A problem found outside your story: do not fix it there. Open an issue labelled `finding` with
`origin: <story>`, the evidence and a proposal.

## Memory
Read `docs/plan/` and `docs/adr/` before deciding; add an ADR (`docs/adr/NNNN-slug.md`) for every
structural decision. When a page and the code disagree, **the code wins and the page is a bug**.

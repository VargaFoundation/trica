# ADR-0018: Web UI structure: embedded SPA, generated API types, own i18n, TanStack Query

- **Status**: accepted (2026-10)
- **Concerns**: `web/`, `internal/web`, `Dockerfile`, the `web` CI job, `GET /api/v1/me`

## Context

ADR 0001 chose React + TypeScript + Vite, built into the server binary. Doing it raises
structural questions that are expensive to change later: how the build output reaches the Go
binary without making Node a requirement of `make ci`, how the UI learns the API's shapes, which
libraries carry routing, data fetching and translation, how a browser authenticates against an
API that takes bearer credentials only, and what a strict Content-Security-Policy imposes.

## Decision

**Embedding.** Vite builds into `internal/web/dist` (not committed). `internal/web` has two
files selected by a build tag: by default it embeds `placeholder/index.html`, a committed page
that says the UI is not built in (`Built == false`); with `-tags webui` it embeds `dist/`.
`go build`, `go test` and `make ci` therefore need no Node; the image build and the `web` CI job
use the tag. `web.New(fs.FS)` returns an `http.Handler` that the server mounts at `/`: the SPA
fallback (`index.html`) for extensionless paths, 404 for `/api*` and for any missing file with
an extension (a stale hashed asset must fail loudly, not receive HTML), `immutable` caching for
`assets/` (Vite hashes their names), `no-cache` + ETag for everything else, and on every response
a strict CSP (`default-src 'none'`, scripts, styles, images, fonts and `connect-src` limited to
`'self'`, `frame-ancestors 'none'`, `form-action 'none'`) plus `nosniff`, `Referrer-Policy:
no-referrer`. The UI is written to need nothing more: no inline script or style (ESLint forbids
JSX `style` and `dangerouslySetInnerHTML`; a `webui` test checks the built `index.html`), no web
font, no CDN. The package is not wired into `cmd/trica` here; the server mounts it.

**API types from the OpenAPI document, checked three ways.** `web/openapi.json` is a committed
snapshot of `GET /api/v1/openapi.json`; `openapi-typescript` (pinned) generates
`src/api/schema.gen.ts` from it. A hand-written client stays thin (`fetch` wrapper, problem+json
errors, bearer header). Three tests keep it honest: a Go test fails when the snapshot is stale
against what the server generates (`TRICA_UPDATE_OPENAPI=1` rewrites it); a Vitest test fails when
the generated file is not what the tool makes of the snapshot; a contract test fails when the
client calls a path, method or query parameter the document does not declare. We did not
generate the fetch layer too: it would add a runtime dependency or a large generated file for
about fifteen calls. The document's `Object` `oneOf` maps each `type` value to its variant
(`discriminator.mapping`, #27), so the generated union narrows on `type`; the UI still types
objects with the base schema, because a custom type added at run time is not in a union
generated at build time.

**Own i18n, two languages.** `en` is the reference catalogue (`as const`, so a key is a type);
`fr` is `Record<MessageKey, string>`, so a missing translation does not compile. Plurals use
`Intl.PluralRules`, dates and numbers `Intl`. Schema labels come from the API (`label.{en,fr}`).
A library (i18next, FormatJS) would bring ICU message parsing and a dozen packages for a
catalogue of about two hundred short strings. A test checks key and placeholder parity. Revisit
when a third language or gendered plurals arrive.

**Data and state.** react-router 7 (data router not used: pages fetch through TanStack Query),
TanStack Query 5 for every read. Every page is an address (filters, pages and the open type are in
the query string); there is no other global state. Writes are two calls (`POST /proposals/{id}/approve`
and `/reject`), which create no inventory change themselves: the engine applies a proposal, the UI
only asks (CLAUDE.md: only `internal/reconcile` writes). After a decision the proposal, lists and
the affected object queries are invalidated; a 409 or 403 is shown, not hidden.

**Authentication.** The API takes bearer credentials only. The UI asks for one (API token or
OIDC access token), keeps it in memory and `sessionStorage` (cleared when the tab closes, never
`localStorage`), sends it as `Authorization: Bearer` with `credentials: "omit"`, and drops it on a
401. The browser OIDC flow (authorisation code with PKCE) is out of this slice.

**Who may do what: `GET /api/v1/me`.** The UI must not guess permissions from a role name. A new
endpoint returns the subject and the actions the caller holds globally. The UI shows approve and
reject only when `approve` is in the list and the proposal is pending. The server still checks
every call; a grant limited to a type or site can pass `/me` and be refused on one proposal, and
the refusal is displayed.

**Dependencies.** Versions are exact and every one was published at least two weeks before it
was added (`npm install --before=<date> --save-exact`, lockfile committed, CI uses `npm ci`).
npm rather than pnpm: it is what Node ships, and the lockfile is the reproducibility guarantee.
Majors released less than that margin ago were not taken (react-router 8, Vitest 5, TypeScript
7, Vite 8). `eslint-plugin-jsx-a11y` is not used: it does not support ESLint 10; accessibility is
checked by axe-core (WCAG 2.2 A/AA rules) on every page in the tests, plus a computation of the
contrast ratio of every colour pair of both themes. `npm audit --omit=dev --audit-level=high` runs
in CI; a development-only advisory (`source-map-js`, fixed in a release younger than the margin)
is accepted and listed in the PR.

## Consequences

- `make ci` is unchanged and needs no Node; `make web` is the UI gate. The image build runs the
  Node stage first, so the shipped binary embeds the real UI.
- Adding an endpoint means: Go handler, `make web-api-update`, then the client function; the
  three tests fail until all three agree.
- A page that needs an inline script, a remote font or an iframe needs an ADR amending the CSP.
- Out of this ADR: rack elevations, floor plans, L2 topology (Cytoscape + ELK), export, the
  browser OIDC flow, end-to-end tests in a real browser.

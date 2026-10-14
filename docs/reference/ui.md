# Web UI

A single-page application in `web/` (React 19, TypeScript strict, Vite, react-router 7, TanStack
Query 5), served by `internal/web`. Structural choices: ADR 0018. This page is what a user, a
developer and an operator need; when it and the code disagree, the code wins.

## What it shows

| Address | Screen |
|---|---|
| `/` | Search examples and the other sections |
| `/search?q=<query>` | Results grouped by type, largest group first, with the count per type. A group that is only partly shown links to `/search?q=<query>&type=<type>` (the whole type, paged, `offset=`) |
| `/objects/<id>` | The object: facts, **attributes with their provenance** (source, confidence, first and last seen; other sources' claims behind a disclosure; a value with no provenance says so), relations and dependencies seen from this object (names and inverse names from the schema), timeline of changes in words (older changes on demand) |
| `/discrepancies?status=&kind=&offset=` | Declared against observed; open by default |
| `/proposals?status=&offset=` | Proposals waiting for a decision (pending by default); `/proposals/<id>` shows the preview of what it would change |
| `/health` | Last run of each source, failing sources first |

Every address is a deep link: filters, the open type and the page are in the URL, and the
application answers any of them (the server returns the shell for client routes). The search field
reads its query from the address on `/search`.

The query is the language of `docs/reference/tql.md`: free text (exact first, typo-tolerant when
nothing matches) or `type:device vendor:arista os_version<4.20`.

## Keyboard and accessibility

- `/` or Ctrl+K (Cmd+K on macOS) focuses the search field from anywhere. `/` is a character while
  typing in a field; Ctrl/Cmd+K works there too.
- A skip link is the first tab stop. On every navigation focus moves to the content and the page
  title changes, so a screen-reader user knows where they landed.
- Status is text first (`observed`, `deduced`, `declared`, `failing`); colour only reinforces it.
- Tables keep their semantics (captions, `scope`); below 40 rem each row becomes a card with the
  column title before each value.
- Light, dark and system themes (the selector sets `data-theme` on `<html>`; "system" follows
  `prefers-color-scheme`). Reduced motion is respected.
- Language: English or French, chosen by the selector, remembered in `localStorage`, otherwise
  from the browser. Object and attribute labels come from the schema (`label.fr`).
- Checked in the tests: axe-core (WCAG 2.0, 2.1, 2.2 A and AA rules and best practices) on every
  page and on the interactive states, and the contrast ratio of every colour pair of both themes
  computed from the tokens in `src/styles/tokens.css`. **Not checked**: rendered contrast, real
  screen readers, phone layout in a real browser.

## Signing in and permissions

The API takes bearer credentials only. The UI asks for one (an API token `trica_<id>_<secret>` or
an OIDC access token), keeps it in memory and in `sessionStorage` (gone when the tab closes), and
sends it as `Authorization: Bearer`. A 401 drops it and returns to the form. Cookies are not
used (`credentials: "omit"`). The browser OIDC login flow is not part of this slice.

`GET /api/v1/me` returns the actions the caller holds (`read`, `propose`, `approve`, `admin`). The
UI shows **Approve** and **Reject** only to a caller who holds `approve`, only on pending
proposals, and asks for a confirmation. The decision is the API's: a 403 (a grant limited to a
type or site) or a 409 (someone decided first) is shown as the server wrote it. The UI never
writes the inventory: approving a proposal asks the engine to apply it.

## The API client

`web/openapi.json` is a snapshot of `GET /api/v1/openapi.json`; `src/api/schema.gen.ts` is
generated from it by `openapi-typescript`. After changing an endpoint:

```
make web-api-update   # re-exports the snapshot from the Go API, regenerates the types
```

Three tests fail when the Go API, the snapshot, the generated types and the client disagree
(`TestWebOpenAPISnapshotIsCurrent`, `src/api/schema.test.ts`). Test fixtures
(`src/test/fixtures.ts`) are anonymised (192.0.2.0/24, 198.51.100.0/24, `sw-a1`) and typed by the
generated schemas; they are written by hand, not recorded from a server.

## Build, test, run

| Command | What |
|---|---|
| `make web-install` | `npm ci` from the lockfile |
| `make web` | install, lint, typecheck, generated-types check, tests, build, Go tests on the build |
| `make web-lint`, `web-typecheck`, `web-test`, `web-build`, `web-audit` | One step each |
| `npm --prefix web run dev` | Vite dev server; `/api` is proxied to `TRICA_API` (default `http://127.0.0.1:8080`) |

Node 20.19 or newer (CI and the image use 22.23.3). `make web-build` writes `internal/web/dist`.

## Serving it (`internal/web`)

`web.New(fsys)` / `web.Default()` return an `http.Handler` for the server to mount at `/` (it is
not wired into a binary in this PR).

| Request | Answer |
|---|---|
| `/`, `/index.html`, any extensionless path (`/objects/<id>`) | `index.html`, `Cache-Control: no-cache`, ETag |
| `/assets/<name>-<hash>.<ext>` | The file, `Cache-Control: public, max-age=31536000, immutable` |
| Another existing file (`/favicon.svg`) | The file, `no-cache`, ETag |
| `/api`, `/api/...`, a missing file with an extension, a missing `/assets/...` | 404, never the shell (a stale hashed asset must fail, not load HTML as a script) |
| Methods other than GET and HEAD | 405 |

Every response carries `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src
'self'; img-src 'self'; font-src 'self'; connect-src 'self'; manifest-src 'self'; base-uri 'none';
form-action 'none'; frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, `X-Frame-Options:
DENY`, `Referrer-Policy: no-referrer`, a restrictive `Permissions-Policy` and same-origin
opener/resource policies. The application therefore uses no inline script or style, no web font
and no remote origin; the tests enforce it.

Two builds: without a tag the binary embeds `internal/web/placeholder/index.html` (a page that says
the UI is not built in; `web.Built` is false); `go build -tags webui` embeds the real build. The
Dockerfile builds the UI in a Node stage and compiles with the tag, so the image ships the UI on
distroless.

## Not in this version

Rack elevations, floor plan, L2 topology (Cytoscape + ELK), PNG/PDF export, the browser OIDC flow,
saved queries, history ("which device had this address on 3 March"), end-to-end tests in a real
browser (Playwright).

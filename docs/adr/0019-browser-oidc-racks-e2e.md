# ADR-0019: Browser sign-in (OIDC, public client with PKCE), rack and room views, browser tests

- **Status**: accepted (2026-10)
- **Concerns**: `web/`, `internal/web`, `cmd/trica-e2e-server`, the `e2e` CI job. Builds on ADR 0018;
  the API still verifies bearer tokens exactly as ADR 0013 says (that ADR is on the auth branch).

## Context

ADR 0018 left three things out: a way for a person to sign in with the company's identity
provider instead of pasting a token, the physical views (rack elevations, floor plan), and tests
in a real browser. Each has a structural choice that is expensive to reverse.

## Decision

**The browser is a public OIDC client: authorisation code with PKCE (S256), no client secret.**
A secret in a browser is not a secret. The page discovers the provider
(`<issuer>/.well-known/openid-configuration`), redirects with `state`, `nonce` and a `code_challenge`,
and on return exchanges the code at the token endpoint with the `code_verifier`. State, verifier and
nonce live in `sessionStorage` for ten minutes and are taken once: a replayed or forged callback
finds nothing. The page refuses a provider whose announced issuer differs from the configured
one, whose endpoints are not https, whose token endpoint is not on the issuer's origin, or that
does not list S256. The API is untouched: it receives `Authorization: Bearer <token>` and verifies
signature, issuer, audience and expiry itself (ADR 0013). The page never trusts or parses a token
for authorisation; it only compares the ID token's `nonce`.

**Which token is the bearer.** `access_token` by default; `id_token` if configured
(`oidc.bearer`). The server's verifier accepts either, but the audience must match the audiences
it is configured with: with Keycloak an access token's `aud` is usually `account` unless an
audience mapper adds the API's, so an operator may find the ID token (`aud` = the client id) the
simpler choice. It is a deployment decision, not a code one.

**Configuration reaches the page through `GET /ui-config.json`**, served by `internal/web`
outside `/api`, unauthenticated, `Cache-Control: no-store`. The alternative, injecting the values
into `index.html`, would make the page dynamic, defeat its ETag and need either an inline script
(forbidden by the CSP) or a data attribute to parse. The document holds the issuer, the client id,
the scopes and the bearer choice; nothing in it is secret. Without `oidc` the page offers the pasted
token only. `web.DefaultWithConfig(web.Config{OIDC: ...})` validates it at start-up (https issuer
without userinfo, query or fragment; a client id; `openid` always requested) and the server fails
to start on a bad one rather than serving a broken sign-in. Wiring it into `cmd/trica` belongs to
the story that mounts the UI.

**The CSP gains one origin.** `connect-src 'self'` becomes `connect-src 'self' <issuer origin>`
when SSO is configured, and nothing else changes: the page can reach its API and its provider,
and no other origin. The authorisation redirect is a navigation, which `default-src 'none'` does
not restrict.

**Redirect path: `/oidc/callback`** (redirect URI `https://<host>/oidc/callback`). It is a client
route; the server already returns the shell for it. Keycloak must have: a public client
(`Client authentication` off), standard flow on, PKCE method `S256`, that valid redirect URI, and
the UI's origin in `Web origins` (the page itself calls the discovery and token endpoints, so the
provider must answer CORS for it). Pasted-token sign-in stays as a fallback, for API tokens
(`trica_<id>_<secret>`) and for deployments with no provider.

**Racks and rooms use the schema as it is.** Position and face are attributes of `mounted_in`
(`position` in U, 1 at the bottom, the lowest U of the device; `face` front or rear); height and
depth come from the device's `device_type` (`u_height`, `full_depth`) through `instance_of`; the
rack has `u_height`, `floor_x`, `floor_y`, `facing`. No schema change was needed. A full-depth
device is drawn on its own face and as a dashed shadow on the other, and two devices overlap only
if they occupy the same face (or one is full depth). What the data does not say is shown, not
hidden: a missing height is drawn as 1 U, a missing face as front, a missing position lists the
device apart; an overlap or a device outside the rack gets an outline and a text note. Two
problems are filed, not fixed here: the contents of a rack need one request per device
(finding #42), and the floor tile is not defined (finding #43; the UI assumes 600 mm and says so on
the page). The views read only; the UI never writes the inventory.

**Browser tests: Playwright, Chromium only, against a Go test server.** `cmd/trica-e2e-server`
serves the built UI through the real `internal/web` handler (so the real CSP, headers, SPA fallback
and `/ui-config.json`), a read-only mock of `/api/v1` over an anonymised inventory, and a fake
OpenID provider that checks what a real one checks (PKCE verifier against challenge, redirect_uri,
no client_secret, single-use code, and the API refuses any token it did not issue). It listens on
self-signed TLS because the page accepts https endpoints only. A Go server rather than a Node mock
because the point is to test the handler's CSP against the real build, and because the Go toolchain
is already in the job. It lives under `cmd/` so it is neither covered nor shipped. Every page, in
both languages, is checked with `@axe-core/playwright` (WCAG 2.0 to 2.2, A and AA), which, unlike the
jsdom tests, computes colour contrast. One engine keeps the job short; the accessibility rules and
the layout we ship do not differ by engine enough to justify three. Retries are off: a flaky test is
a bug. The browser is cached on the lockfile hash.

## Consequences

- Keycloak needs a client registered as above; until then the SSO button reports the provider as
  unreachable and the token form works.
- The mock API is hand-written and follows `web/openapi.json` only by convention on the server
  side (the browser side is typed). It proves the UI against the documented shapes, not against a
  running trica.
- Fixing finding #42 replaces the request-per-device loading with one call; the layout code is
  unaffected.
- Out of this ADR: L2 topology, PNG/PDF export, wiring the UI into `cmd/trica`.

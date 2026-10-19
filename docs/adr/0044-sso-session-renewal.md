# ADR-0044: The browser renews its single sign-on token

- **Status**: accepted (2026-10)
- **Concerns**: `web/src/auth/renew.ts`, `web/src/api/token.ts`, `web/src/api/client.ts`. Amends
  ADR 0019 (browser sign-in).

## Context

ADR 0019 made the page a public OIDC client and kept only the token it was given. An access token
from Keycloak lives five minutes by default, so a reader signed in by SSO was sent back to the
sign-in form every five minutes: the first API call after the expiry answered 401 and the page
dropped the token. Lengthening the access token's life at the provider would also lengthen the
window in which a stolen token is good, for every client of the realm.

## Decision

**The page keeps the refresh token the provider gives at sign-in and spends it before each
expiry.** With it, `sessionStorage` (`trica.oidc.renewal`) holds the token endpoint discovery
validated (https, the issuer's origin), the client id and the bearer choice. A minute before the
`exp` of the current token (five seconds at least), the page posts `grant_type=refresh_token` with
the client id and no secret, `credentials: "omit"`, and replaces the bearer; a rotated refresh
token replaces the old one. A tab that comes back from the background with a stale token renews
at once.

**A 401 renews once and replays the call.** A timer in a sleeping tab may fire late; the call that
meets the expiry renews (concurrent callers share one request) and is replayed with the new token.
A second 401 drops the session as before.

**A refusal ends the session, a failure does not.** A 4xx from the provider (`invalid_grant`: the
session ended, was revoked or reached its maximum lifetime) drops token and renewal and shows the
sign-in form. A network error or a 5xx keeps both and the next call or timer tries again.

**The renewal lives and dies with the token.** Pasting an API token, signing out or a rejected
token forgets it. A pasted token has no `exp` the page reads and is never renewed.

The page reads `exp` to plan a renewal; it still decides nothing on a token's content. The API is
untouched and keeps verifying every bearer.

## Consequences

- A refresh token in `sessionStorage` is exposed to the same thing as the access token beside it:
  script running in the page, which the CSP (`script-src 'self'`) forbids. Its reach is bounded by
  the provider's session: SSO idle and maximum lifetimes decide how long a reader stays signed in
  (with Keycloak's defaults, 30 minutes idle and 10 hours at most). Lengthen those on the realm
  or the client, not the access token's life.
- `offline_access` is not requested: an offline token would outlive the provider's session.
- A provider that issues no refresh token keeps the ADR 0019 behaviour: one sign-in per access
  token.

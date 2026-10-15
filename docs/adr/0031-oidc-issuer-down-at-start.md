# ADR-0031: The server starts while its OIDC issuer is down

- **Status**: accepted (2026-10)
- **Concerns**: `internal/auth` (`NewLazyVerifier`, `Verifier.Discover`, `ErrIssuerUnreachable`),
  `internal/wiring`. Amends ADR 0025 (an unreachable issuer stopped the start) and ADR 0013.

## Context

ADR 0025 made an OIDC issuer that cannot be reached at start stop the server, so that a
deployment would never fall back to tokens only without anyone noticing. In practice this ties
the inventory to the identity provider: a Keycloak restart, a DNS hiccup or a network policy
applied in the wrong order keeps trica in a crash loop, and with it the collectors' ingestion
and the API tokens, which never needed the issuer (#57). Several replicas make it worse: they
all fail together.

## Decision

- Discovery is attempted at start, as before, with a 10 s budget.
- **An issuer that does not answer** (a network error or an error status:
  `auth.ErrIssuerUnreachable`) no longer stops the start. The server logs a warning naming the
  issuer, and starts.
- **A discovery document that contradicts the configuration** (another `issuer`, a `jwks_uri`
  that is not https) still stops the start: no retry fixes a wrong configuration.
- Until discovery succeeds, a JWT is answered **503**, not 401: the credential may be valid, and
  a client must not throw it away. API tokens are not affected. A bearer that is not a JWT is
  still a 401 and never causes a request to the issuer.
- Discovery is retried on use, at most once per `MinRefreshInterval` (30 s), like JWKS refreshes,
  so a stream of requests while the issuer is down is not a stream of requests to it.

## Consequences

- The fallback is not silent: a warning at start, and every JWT gets a 503 with the reason in the
  audit log, until the issuer answers. Readiness (`/readyz`) does not depend on the issuer.
- A deployment whose issuer URL is wrong but unreachable (a typo in the host name) starts and
  answers 503 to every JWT instead of failing at start. The warning is the signal.
- `auth.NewVerifier` keeps its behaviour (discovery or an error) for callers that want it.

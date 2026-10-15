# ADR-0036: Addresses are identified within a routing domain

- **Status**: accepted (2026-10)
- **Concerns**: `internal/schema` (`Type.IdentityScope`, `NormalizeScope`, `ScopedIdentity`,
  `ScopeBatch`, `ip_address` and `prefix` in `builtin.yaml`), `internal/reconcile`
  (`identities`, claim writing), `internal/agent` (`Config.Domain`), `cmd/trica-agent`
  (`--domain`), `internal/collect` (`Target.Domain`), `internal/collect/runner`,
  `deploy/kubernetes/trica-agent-daemonset.yaml`, `packaging/agent.env`. Closes issue #15;
  completes ADR 0006.

## Context

ADR 0006 identifies an `ip_address` by its address alone (strong) and a `prefix` by its CIDR.
Private ranges are reused: `10.0.0.1` behind one site's NAT and `10.0.0.1` in another site's
VRF are two hosts' addresses, and the engine merged them into one object, so that a `talks_to`
or `assigned_to` of one site landed on the other's. The address cannot be weakened (scoped by
a parent) instead: an address has no natural parent, the agent's remote addresses have none at
all, and a weak identity would also merge two sites' addresses that have no parent.

## Decision

- A type may name an **identity scope**, `identity_scope: <attribute>`: a string attribute that
  is not itself an identifier, on a type that has identifiers (`Validate` refuses anything
  else). `ip_address` and `prefix` are scoped by `domain`: the routing domain (VRF, site,
  network) the value is unique in.
- The scope is given **with the identity** (`Identify("domain", "site-a")`, or the `domain` key
  of a relation endpoint's identity). It is normalised (trimmed, lower case, 1 to 64 letters,
  digits, `.`, `_`, `:`, `-`; anything else is `ErrInvalid`) and every identifier of the object
  is stored and matched as `<domain>|<value>`. The domain is also stored as the `domain`
  attribute, so it is searchable and visible.
- **No domain is the global domain**, and its identifiers are stored as before (`10.0.0.1`,
  not `|10.0.0.1`): existing objects keep their identity, nothing is migrated.
- A domain given as a plain value (`Set("domain", …)`) is a schema problem (refused for API
  callers, a warning for collectors) and is not stored: the object would show a domain its
  identity is not in.
- Sources that know the domain of what they observe stamp it on all their scoped objects and
  endpoints at once (`Registry.ScopeBatch`), never overriding a domain the source gave itself:
  the agent with `--domain` (`TRICA_AGENT_DOMAIN`, an optional key of the DaemonSet's ConfigMap),
  a remote collector target with `domain:`. Both refuse an invalid domain at start.

## Consequences

- The same address in two domains is two objects, with the same display (the address): the
  `domain` attribute tells them apart in lists and search, not the label.
- Changing the domain of a source (from global to `site-a`, or between two domains) creates new
  objects; the old ones are no longer seen and become absent. Set it before the first run.
- Two sources that disagree on the domain of the same address (one says `site-a`, one gives
  none) produce two objects. Nothing reconciles them; the duplicate is visible.
- Only the built-in types are stamped by the agent and the runner: they do not read the
  server's custom schema, so a custom type given an `identity_scope` gets its domain only from
  the source itself.
- No collector reads VRFs from the equipment yet (Arista, MikroTik, pfSense): their domain is
  the target's, one per target. A device carrying several VRFs needs that work.
- A prefix's occupancy, described by the schema but not computed yet, will have to count only
  the addresses of its own domain.

# Security policy

## Reporting a vulnerability

Write to **security@varga.foundation**, or open a private security advisory on GitHub. Please do
not open a public issue: we would rather fix before we publish.

Include the version or commit, what you observed, how to reproduce it, and the impact you
estimate. We acknowledge within 3 working days and give a first assessment within 10.

## Supported versions

The project is at **0.x**: only the latest minor receives security fixes, and there is no
long-term support branch. Until a 1.0 exists, upgrading is the fix.

## What we consider a vulnerability

trica holds a map of an infrastructure and, by design, reaches its management plane. That makes
the following vulnerabilities:

- Any way to read a device credential back out of trica: API, MCP, logs, exports, backups.
- Any write to a device or a system from a collector. Collectors are read-only by contract.
- Any change to the inventory that bypasses the reconciliation engine, its provenance or the
  proposal queue (a write that leaves no author, no source, no history).
- Any way for an MCP client to change the inventory other than by creating a proposal.
- Any way for an agent or a collector to report under another source's identity.
- Any read beyond what a token's scope or a user's role allows.
- Any undetected alteration of the audit log.

## What we do not consider a vulnerability

- Data a collector reports wrongly because the device reports it wrongly: that is a
  discrepancy, and trica shows it as one.
- An MCP client following an injected instruction **without that granting it any power**: MCP
  writes are proposals, and a proposal waits for a human.

## What we do, and where to check it

Only claims enforced by code or CI are listed. A claim moves here with the PR that enforces it.

| Claim | Where it is enforced |
| :-- | :-- |
| Secrets never enter git: gitleaks is a gate on every pull request | `.github/workflows/ci.yml`, job `secrets` |
| Every commit certifies the DCO | `ci.yml`, job `dco` |
| The inventory is only written by the reconciliation engine, and every value keeps its source, confidence and history | `internal/reconcile`, `internal/store/migrations` |

## What we do **not** guarantee yet, stated rather than discovered

- **No release exists.** Image signing, SBOM and provenance are planned for the first tag;
  nothing is published yet.
- **There is no authentication yet**: no API, no MCP server, no agent transport is shipped.
  Do not expose a development build to a network.
- **The audit log is not tamper-evident yet.** The table carries a hash chain column; no code
  verifies it.

Where a page and the code disagree, the code wins, and the page is a bug.

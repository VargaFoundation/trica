# Roadmap: release 1

Release 1 is the specification's first lot: an inventory that discovers a small datacentre on
its own, reconciles what it sees with what people declare, and answers questions about it
through an API, a UI and MCP.

## Acceptance criteria of release 1

| # | Criterion | How it is measured |
|---|---|---|
| 1 | 95 % of equipment discovered without manual entry | count against a reference inventory of the target site |
| 2 | 90 % of cables deduced | count against the site's verified port map |
| 3 | A moved cable shows in under 15 minutes | move one cable, time it |
| 4 | 10 of 12 reference MCP questions answered in one call | `test/mcp/questions.yaml`, run against the target site |
| 5 | Installation in under 30 minutes | `deploy/compose` on a blank VM, timed |
| 6 | Search under 200 ms, impact under 2 s | `test/bench` on a synthetic 50-rack, 2 000-device dataset |

## Milestones

One story = one PR, squashed.

| # | Milestone | Content | Done when |
|---|---|---|---|
| 1 | Bootstrap | Governance files, ADRs, `make ci`, CI with PostgreSQL, gitleaks, DCO | CI green on `main` |
| 2 | Model and storage | Schema registry, historised store with provenance, state at a date, diff, timeline, custom types, JSON/YAML import and export, device type library | PostgreSQL integration tests; a custom type is visible in the API |
| 3 | Reconciliation, SDK, transport | Identity engine, source priorities, proposal queue, absence, discrepancies, `pkg/sdk`, mTLS transport with spool, collection health | Engine scenario tests: rename, readdress, NIC change, disappearance |
| 4 | Equipment collectors | Arista eAPI, MikroTik API, pfSense REST, Redfish (iLO 4/5, iDRAC), MAAS, Kubernetes; cabling deduction from LLDP and MAC tables | One `Fake*` per integration on anonymised recorded responses; a compatibility matrix per version |
| 5 | Linux agent | Packages, services, listeners, connections, LLDP, SMART/NVMe; static binary, packages, DaemonSet; remote update in waves with rollback | CI benchmark under 50 MB and 1 % of a core; rollback test |
| 6 | API, search, MCP | Dynamic REST/OpenAPI, query language, text and historical search, saved queries, MCP tools, OIDC, tokens, RBAC, chained audit | Benchmarks (criterion 6); MCP questions (criterion 4) |
| 7 | UI | Global search, object page with provenance and timeline, rack elevations, room plan, L2 topology, discrepancies, proposals, collection health | Playwright and axe (AA); a rack renders in under 1 s |
| 8 | Packaging and operations | Helm chart, compose, backup and restore, release pipeline (signed images, SBOM, chart OCI), getting-started, deployment, security and SDK docs | Nightly end-to-end on kind against a simulated lab |
| 9 | First deployment | A real site, criteria 1 to 5 measured there | Measured numbers recorded in `STATUS.md` |

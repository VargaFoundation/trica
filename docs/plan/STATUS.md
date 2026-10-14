# Status

What each delivered piece proves, and what it does not. Updated by every PR.

| # | Piece | Proves | Does not prove |
|---|---|---|---|
| 1 | `pkg/sdk` | A batch validates its refs, parents, cycles, endpoints and confidences | Nothing about transport: no collector sends batches over the network yet |
| 2 | `internal/schema` | The built-in registry parses and validates; overlays cannot change an attribute's kind; an attribute whose name says it holds a secret (password, token, community…) cannot be searchable or identifying, so it never reaches the search index | No custom type is reachable through an API yet (there is no API) |
| 3 | `internal/store` | Migrations go up, down and up again on PostgreSQL 17; history ranges cannot overlap | Read paths beyond what the engine tests exercise; no performance figure |
| 4 | `internal/reconcile` | 16 scenarios on a real PostgreSQL: idempotence, rename, readdress, NIC change, weak merge, serial conflict, absence and return, priority rules and discrepancies, full-scope withdrawal, failed runs, unresolved neighbours, proposals, preview | Throughput under load; concurrent submitters beyond the advisory lock |
| 5 | CI | `make ci` (lint, vet, tests with PostgreSQL, coverage >= 80 % across packages), gitleaks, DCO | Images, charts, web: none exist yet |
| 6 | `internal/graph` | Impact rules of ADR 0011 on a hand-built lab (containment, hosts, runs_on, depends_on, redundant power and replicas, degrade without propagation, reachability from anchors, absent objects); impact on 2 000 servers in 50 racks in about 11 ms on a developer machine, checked under 2 s by a test; neighbours and shortest path; the cache reloads once per burst and again after a commit landing mid-load | Nothing wires the cache to the engine's commits yet; impact is not reachable from an API or MCP yet; the synthetic site is generated, not a real topology |

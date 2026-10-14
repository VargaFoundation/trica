# trica

**An infrastructure inventory that knows where each fact comes from.**

trica discovers your datacentre (switches, routers, firewalls, servers through their BMCs,
bare-metal provisioning, Kubernetes, and the hosts themselves through a light agent), keeps
every observed and declared value with its source, its confidence and its history, and answers
questions about it: through a search bar, rack and topology views, a REST API, and an MCP
server for AI assistants.

> **Status: pre-release.** The data model, the historised store and the reconciliation engine
> exist and are tested; collectors, API, MCP and UI are being built. See
> [`docs/plan/STATUS.md`](docs/plan/STATUS.md) for what is proven and what is not.

## Why another CMDB

- **Provenance on every value.** "The OS version is 4.20.7M, read on the switch 3 minutes ago;
  the documentation says 4.18." Both are kept; the disagreement is a first-class discrepancy.
- **History by construction.** The state at any date, the diff between two dates, and "who had
  this IP on March 3rd" are queries, not archaeology.
- **Discovery first, declaration where nothing can be discovered.** Rack positions and power
  feeds are declared; everything a device can report is collected.
- **Nothing written behind your back.** Collectors are read-only. MCP and deduced cables can be
  routed to a proposal queue with a preview of their effect.

## Architecture in one paragraph

Collectors and agents send *batches of observations* (`pkg/sdk`). A single reconciliation
engine matches them to existing objects (strong identifiers first, then weak ones scoped by
parent), resolves each attribute by per-attribute source priorities, records history, opens
discrepancies, and marks unseen objects absent, never deleted. Everything lives in PostgreSQL.
Decisions are in [`docs/adr/`](docs/adr/).

## Developing

```bash
make db        # throwaway PostgreSQL in Docker; export the URL it prints
make ci        # what blocks a pull request
```

See [`docs/CONTRIBUTING.md`](docs/CONTRIBUTING.md).

## Licence

Apache-2.0. See [LICENSE](LICENSE), [NOTICE](NOTICE) and [docs/TRADEMARK.md](docs/TRADEMARK.md).
Security reports: [SECURITY.md](SECURITY.md).

# ADR-0001: Go, PostgreSQL and React, one server binary

- **Status**: accepted (2026-10)
- **Concerns**: every component

## Context

trica must install in under 30 minutes, run as a handful of pods on a small cluster, ship an
agent under 50 MB of memory and 1 % of a core, and talk to network equipment, BMCs and
Kubernetes. The ecosystem it collects from (client-go, RouterOS, Redfish clients) is mostly Go.

## Decision

- **Go** for the server, the remote collector, the MCP server, the CLI and the agent. The
  server, collector, MCP and CLI are subcommands of one `trica` binary; `trica-agent` is a
  separate static binary so that it stays small.
- **PostgreSQL** is the only stateful dependency (ADR 0002).
- **React + TypeScript + Vite** for the UI, built into the server binary with `embed`.
- Libraries: `pgx/v5`, `goose`, the standard `net/http` router, `modelcontextprotocol/go-sdk`,
  `coreos/go-oidc`, `client-go`, `go-routeros`. Redfish, Arista eAPI, pfSense and MAAS clients
  are written here: they are small, and the third-party ones drag large dependency trees.

## Consequences

- One image for the server side, one for the agent; one deployment for a small site.
- No graph database to operate: graph traversals are served from memory (ADR 0011).

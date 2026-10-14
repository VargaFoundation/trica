# ADR-0012: Linux agent inventory, read-only by construction, one full batch per run

- **Status**: accepted (2026-10)
- **Concerns**: `internal/agent`, `cmd/trica-agent`, `internal/schema/builtin.yaml`
- **Relates to**: ADR-0004 (claims), ADR-0006 (identity), ADR-0007 (absence), ADR-0008 (transport)

## Context

The agent runs as root on production hosts and describes them. It must be
provably harmless, small (under 50 MB, under 1 % of a core), and testable without
the machine it describes. The transport (ADR-0008) comes later; this decision
covers what the agent observes and how it maps to the schema.

## Decision

1. **Read-only by construction.** Files are read through an `FS` interface backed
   by `os.Root` (no write method, no symbolic-link escape). Commands go through one
   `Runner`; `ExecRunner` accepts an exact allow-list of command lines (`rpm -qa`,
   `systemctl list-units`, `lldpctl -f json`, `smartctl --json -a /dev/<name>`),
   resolves binaries in fixed directories, and passes an empty environment. Process
   names come from `/proc/<pid>/comm`; `cmdline` and `environ` are never opened.
2. **A root other than `/` is a tree to describe, not a host.** The agent then runs
   no command and asks no netlink, so tests and forensic runs never leak the
   machine they run on into the description of another.
3. **Addresses via netlink, from the standard library.** `syscall.NetlinkRIB`
   gives address, prefix, interface and flags; `fib_trie` has no interface. No
   dependency, `CGO_ENABLED=0`.
4. **A failed probe makes the run `Health.OK = false`.** The batch is full-scope,
   so a probe that broke would make the engine withdraw everything it would have
   claimed. The engine already treats a not-OK full batch as partial (nothing
   withdrawn); the agent uses that. A probe that could not look (tool absent, not
   root) is a warning only.
5. **Software is keyed per version** (`kind/name/version`, the schema's key): an
   upgrade is a new software object, a closed `runs` and an opened `runs`. The
   history of what a host ran is the `runs` relation.
6. **`machine_id` is a weak identity** on `device` and `vm` (a cloned VM image
   shares it); `serial` and `hw_uuid` / `uuid` stay the strong ones. `vm` gains
   `kernel` and `primary_ip`.
7. **Only stable observations.** Uptime, temperatures, counters, container and
   ephemeral interfaces, ephemeral sockets are not reported; `power_on_hours` is
   rounded down to 24 h. Everything reported changes only when the host does, so an
   idle host's second run changes nothing (tested on PostgreSQL).
8. **A VM reports no disks or CPU sockets**: the schema's `disk` and `cpu` types
   have no `vm` parent.

## Consequences

- The allow-list is the security boundary and is tested (a command off the list
  never starts); adding a probe that needs a command means extending it here.
- Hosts with only a `machine-id` and a hostname still get a batch; hosts with
  neither a serial nor UUID rely on weak identity and the engine's merge rules.
- Overlapping private address ranges across sites share one `ip_address` object
  per address; that is the schema's identity for addresses, not the agent's.

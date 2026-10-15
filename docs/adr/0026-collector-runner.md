# ADR-0026: Collectors run from one scheduler, in the server or as a remote collector

- **Status**: accepted (2026-10)
- **Concerns**: `internal/collect/runner` and `internal/collect/all` (new), `internal/wiring`,
  `internal/server` (configuration), `cmd/trica`, `charts/trica`. Builds on ADR 0005, 0008, 0014,
  0025.

## Context

Six collectors existed (Arista, Kubernetes, MAAS, MikroTik, pfSense, Redfish), each tested against
its fake, and none ran: nothing read a list of targets, built the collectors, called them on a
schedule or handed their batches to the engine. The transport client existed too, and nothing
used it.

## Decision

**One scheduler, `internal/collect/runner`, whatever the emitter.** It reads a targets file (the
format `collect.Target` already decodes), builds each collector with `collect.New`, and runs each
target every `Target.EffectiveInterval()` under `collect.Context`. The emitter is the only thing
that differs between modes: in the server it submits to the reconcile engine in-process, in a
remote collector it sends through `uplink.Client`. A collector therefore behaves the same in
both, and only `internal/reconcile` writes the inventory in both.

**A run never leaves the board silent.** Collectors already emit `collect.Failed` or
`collect.Succeeded` batches. The runner adds the cases where they cannot: an error returned
without a failed record, a panic (recovered, one target only), a collector that reports nothing,
a batch the emitter refused. Each ends in a health record and in the failure metric. A panic
that is not recovered would stop every target of the process, which is why it is contained per
run.

**A run that is still going is skipped, not queued.** Queuing would make a slow device accumulate
runs and hit it harder exactly when it is struggling. The skip is logged and counted
(`trica_collect_skipped_total`). **Jitter**: the first run of each target waits a random part of
its interval (10 % by default), and each following wait is drawn the same way, so a restart does
not fire every target at once and targets do not stay in lockstep.

**In the server, one replica collects.** The component is mounted only when
`collect.targets_file` is set. The server may have several replicas (ADR 0014) and each device
must be polled once per interval, so the component takes a PostgreSQL session advisory lock
(key 7426204, next to audit 7426202 and deduce 7426203) on a connection it keeps; the others
retry every 15 seconds. If the connection is lost the runner is cancelled, since the lock is
gone with the session, and the component competes again. A targets file that does not parse
stops the start: a deployment that discovers nothing in silence is worse than one that does not
start.

**Remote: one identity per target.** A client certificate is bound to a binding and the transport
accepts a batch whose source equals the binding or starts with `binding + ":"` (ADR 0008). A
collector's source is `<kind>:<name>` (the health board's key), so one certificate cannot carry
two targets of different names. The remote collector therefore enrols and keeps state per target
(`state_dir/<kind>_<name>/`). The alternative, changing the binding rule to allow a wildcard per
site, widens what a stolen certificate can write; it is not done here (see Consequences).

**Secrets stay references.** The targets file holds `file:` and `env:` references only; a plain
password is refused at load and never echoed. In Kubernetes, existing Secrets are mounted and
referenced by path. The chart's optional ClusterRole for an in-cluster Kubernetes target grants
`get`, `list` and `watch` on exactly what the collector reads, with no Secrets or ConfigMaps,
and is off by default; the service account token is mounted only when it is on.

## Consequences

- A remote site with many devices needs one enrolment token per target. Each is minted with
  `trica enrol create --binding <kind>:<name>`, and a per-site prefix
  binding would remove the multiplication at the price of a wider certificate. A later ADR.
- The targets file is read at start; changing it needs a restart.
- Collection pauses for up to the retry period when the collecting replica dies.
- An interval shorter than a run is not an error, it shows as skips; the operator lengthens it.

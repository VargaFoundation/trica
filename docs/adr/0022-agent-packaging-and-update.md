# ADR-0022: Agent packaging, unprivileged service, signed update in waves with rollback

- **Status**: accepted (2026-10)
- **Concerns**: `internal/agent/update`, `cmd/trica-agent`, `packaging/`, `deploy/kubernetes/`, `Makefile`, CI job `agent`
- **Relates to**: ADR-0015 (agent inventory), ADR-0008 (transport), release pipeline (PR #17), transport (PR #19)

## Context

The agent inventory (ADR-0015) delivered probes and mapping only. Running it on a
fleet needs three more things: a way to install it (packages, a service that does
not run as root, a Kubernetes example), a way to update it that cannot become a
remote code execution channel, and a measured footprint instead of a Go-heap
estimate. The transport does not exist on this branch, so the update has to be
specified and tested without it.

## Decision

**Update contract.** `internal/agent/update` defines `Source` (`Desired`, `Open`)
and ships `FakeSource`. It lives next to the agent, not in `pkg/sdk`: it is a
contract between the server and the agent binary, not between collectors and the
engine, and `pkg/sdk` is public and frozen by ADR. The real `Source` comes with
the transport (PR #19).

**Trust.** A release is `trica-agent_<v>_<os>_<arch>` plus `SHA256SUMS` plus
`SHA256SUMS.sig`. The agent holds a **pinned public key** and verifies the signature
over `SHA256SUMS` *before* downloading the artifact, then compares the artifact's
SHA-256 to the listed one. Accepted keys: ECDSA P-256 (the format of `cosign
generate-key-pair`, signature as `cosign sign-blob --key` writes it) and Ed25519.
Standard library only. sigstore-go was not used: keyless verification needs Fulcio
and Rekor trust roots, a TUF root and network access at update time, and pulls a
large dependency tree into a binary budgeted at 50 MB and run with capabilities.
A pinned key fails closed with nothing to fetch. Limits, accepted: no freshness
or expiry (mitigated by anti-downgrade below), and key rotation needs a package
update. Compatibility with a real cosign binary is **not verified**; the tests
sign the documented way.

**Never auto-install the unverifiable.** No key, no update. Bad signature, unlisted
or duplicate sums, wrong digest, oversize download (artifact 128 MiB, sums 1 MiB,
signature 8 KiB), non-`MAJOR.MINOR.PATCH` version: nothing is staged.

**Anti-downgrade.** Only strictly newer versions; a version that was rolled back is
recorded as bad and never offered again on that host; nothing is accepted while a
version is on probation.

**Waves.** `InWave(identity, version, percent)` = first 8 bytes of
`SHA-256(version NUL identity)` mod 10 000 below `percent x 100`. The version is in
the hash so canaries differ between releases; the function is monotonic in
`percent`. The server decides per agent; the agent checks again, so a server (or
whoever impersonates it) offering to everyone cannot push outside the wave.

**Where the new binary lives.** The service runs as an unprivileged user and cannot
write `/usr/bin`. Versions are staged under `/var/lib/trica-agent/versions/<v>/`
and the packaged binary is a launcher: `Boot()` reads `state.json`
(`current`, `previous`, `pending`, `bad`) and `exec`s the staged binary with
`TRICA_AGENT_BOOTED=1`. The state holds **versions, never paths**, so a tampered
file can only select what the updater staged. A switch is two atomic renames.
Rejected: swapping `/usr/bin` (needs root), a supervisor child process (doubles the
resident set), systemd `Type=notify` health (couples to systemd, absent in a
DaemonSet).

**Health and rollback.** The new version is healthy when it has emitted one batch
(`Confirm`). A watchdog stops it with status 1 after `--health-timeout`; the
service manager restarts it (`Restart=always`), and `Boot()` rolls back when the
deadline has passed, when more than 3 starts happened without confirmation, or when
the staged binary is missing. Tested with real binaries built in the test
(good, crashing, hanging) under a harness playing the service manager.

**Capabilities derived from the probes**: `CAP_DAC_READ_SEARCH` (DMI 0400 files,
opening `/dev/sdX`), `CAP_SYS_PTRACE` (`/proc/<pid>/fd` of other users),
`CAP_SYS_RAWIO` (smartctl SG_IO), as bounding and ambient set. Not granted:
`CAP_SYS_ADMIN` (NVMe SMART; NVMe wear is skipped), the lldpd group (a drop-in).
A test pins the exact set and requires each to be justified in the unit and in
`docs/reference/agent.md`.

**Packages.** nfpm (deb and rpm, amd64 and arm64) from `make package-agent`,
installed with `go install` at a pinned version, not in `go.mod`. Packages are
unsigned here: signing belongs to the release pipeline (PR #17). The post-install
script creates the user and reloads systemd but neither enables nor starts the
service; removal deletes nothing.

**Kubernetes.** An example DaemonSet, not a release artifact. `hostPID` and
`hostNetwork` are on because two probes cannot work without them (socket owners;
addresses and `/proc/net`); uid 0 because capabilities are effective only for
uid 0; all capabilities dropped but two; read-only root file system; three
read-only hostPath files; no `--state-dir` (update by rollout, 10 % at a time).

**Footprint.** A CI job runs the real static binary as root on the runner at the
30 s interval over a full cycle and fails above 50 MB peak RSS or 1 % of a core.

## Consequences

- An update can be refused for many reasons and each is a test; none can be
  skipped without turning the suite red.
- The agent does not yet check for updates in production: wiring `Updater.Run` and
  choosing the key's location (flag or file) wait for the transport.
- The DaemonSet inventories no RPM packages, services, SMART or LLDP (no tools in a
  static image); that is a known gap.
- A reduced runner measurement is not a fleet measurement; hosts with very many
  processes may cost more.

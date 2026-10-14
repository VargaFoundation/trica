# Contributing to trica

## In five minutes

```bash
git clone https://github.com/VargaFoundation/trica && cd trica
make tools     # the pinned golangci-lint
make db        # PostgreSQL for the integration tests; export the URL it prints
make ci        # what blocks a PR
```

Read [`AGENTS.md`](../AGENTS.md): it is the short list of rules, for humans and agents alike.

## Repository layout

| Path | What |
|---|---|
| `pkg/sdk` | The public collector contract: batches of observations |
| `internal/schema` | The type registry (`builtin.yaml`) |
| `internal/store` | PostgreSQL access and migrations |
| `internal/reconcile` | The only writer: identity, priorities, history, proposals, discrepancies, absence |
| `docs/adr` | Structural decisions |
| `docs/plan` | Roadmap and status |

## Writing a collector

A collector implements `sdk.Collector` and emits `sdk.Batch` values. It never writes to the
device. It ships with a `Fake*` that serves recorded responses from its package's `testdata/`
directory, anonymised before commit (serial numbers, MAC addresses, names, addresses, anything
secret). `testdata/README.md` says, per file, whether it was recorded or synthesised, from which
version, and what was anonymised. Addresses come from the documentation ranges (192.0.2.0/24,
198.51.100.0/24, 203.0.113.0/24, 2001:db8::/32) and MAC addresses are locally administered
(`02:…`).

## Signing off: the DCO

Every commit carries a `Signed-off-by` line matching its author:

```bash
git commit -s -m "fix(reconcile): …"
git config format.signOff true          # once, for this repository
```

It certifies the [Developer Certificate of Origin](../DCO) 1.1: you wrote the change, or you have
the right to submit it under Apache-2.0. **CI refuses a PR whose commits are unsigned.**

## Using the name

See [TRADEMARK.md](TRADEMARK.md).

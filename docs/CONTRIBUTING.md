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
device. It ships with a `Fake*` that serves recorded responses from `test/fixtures/`, anonymised
before commit (serial numbers, MAC addresses, names, addresses, anything secret).

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

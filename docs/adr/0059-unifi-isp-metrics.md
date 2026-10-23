# ADR-0059: UniFi ISP metrics are series on the active Internet access, the newest period of a bounded window per run

- **Status**: accepted (2026-10)
- **Concerns**: `internal/collect/unifi` (a fourth endpoint, `metrics.go`), `internal/schema/builtin.yaml`
  (`internet_access`), `web/openapi.json` and `web/src/api/schema.gen.ts` (regenerated),
  `docs/reference/collectors.md`, `docs/reference/ui.md`. Builds on ADR 0045 (measurements over time
  are read from the attribute history) and amends ADR 0057 items 2, 9 and 10 (notes in that ADR).
  `pkg/sdk`, the reconcile engine, the store and the migrations are untouched. Closes #162.

## Context

The Site Manager API also serves ISP metrics: for each site, periods of 5 minutes (or 1 hour) with
the average and maximum latency to the provider, packet loss, uptime, downtime and throughput of the
WAN that carried the traffic. They are what an operator checks first when "the Internet is slow",
and the object that holds them is the `internet_access` ADR 0057 already makes.

What was verified on the real API on 2026-10-23 (one account, one console, recordings in
`internal/collect/unifi/testdata/recorded-2026-10/`):

- `GET /v1/isp-metrics/{type}` with `type` `5m` or `1h` is a **stable** `/v1/` endpoint; the early
  access one, `/ea/isp-metrics`, which ADR 0057 left out, is not needed.
- It takes `beginTimestamp` and `endTimestamp`, or a `duration`. For `5m` the only duration it
  accepts is `24h` (any other answers 400 `parameter_invalid`, recorded), about 60 KB per site; a
  bounded `beginTimestamp`/`endTimestamp` of one hour returns the 13 periods in it, both ends
  inclusive.
- Metrics are **per site**, not per WAN: a period carries one `wan` object, the WAN in use. The
  site's WANs, their `portUp`, `failoverPriority` and the site's `wanLoadBalancingMode` come from
  `/v1/sites` (already read).
- Throughput (`download_kbps`, `upload_kbps`) is 0 in every recorded period, in 5m (08:00 to 09:00Z
  on a working morning with 48 clients online) and in 1h. What the field measures is unverified.
- Whether `metricTime` is the start or the end of a period, and whether the newest period can still
  be revised, is not known.

What the engine imposes (`internal/reconcile/engine.go`, `writeHistory`): a value is stored when the
engine applies it, **at the engine's clock**, never at a time the batch names. `Batch.ObservedAt`
only feeds `first_seen` and `last_seen`. A full batch withdraws, attribute by attribute, any claim it
does not repeat. A value equal to the open history row (same value, source and confidence) writes
nothing. ADR 0045 is built on this: a reading is a change of value, and one row lasts until the
value changes.

## Decision

1. **One bounded call per run:** `GET /v1/isp-metrics/5m?beginTimestamp=<now-30m>&endTimestamp=<now>`,
   timestamps RFC 3339 in UTC to the second. No `duration`, no `1h` call, no `hostIds` filter (it is
   unverified on this endpoint: entries are matched on the client by `hostId`), never `/ea/` and never
   the `POST .../query` form. About seven periods per site, roughly 1.6 KB per site. It goes through
   the same list reader as the other endpoints (`nextToken` followed, at most 50 pages, 32 MB cap). The
   call is not made when `/v1/sites` failed (no period could be matched to a WAN) or when no console is
   selected. Thirty minutes lets the API publish late without the series flapping.
2. **The newest period is the current value.** Each run takes the period with the latest `metricTime`
   in `[now-30m, now]` that has measurements, and sets it on the object. A period after the run's
   own clock is never used, even when the server returns it. No state is kept by the collector: the
   history is the state. Two overlapping runs that see the same newest period write nothing; a newer
   period with another value writes exactly one row and closes the previous one. Restarts and replicas
   need nothing.
3. **The series goes on the Internet access of the site's primary WAN.** For each `(hostId, siteId)`
   entry the `/v1/sites` entry with the same ids is read, and the primary WAN is the one with
   `portUp: true` and the lowest `failoverPriority` (1 beats 2; a missing priority sorts last; a tie
   goes by WAN name). Nothing is attributed, with a warning, when no WAN is up; when the
   `wanLoadBalancingMode` is not `FAILOVER_ONLY` and more than one WAN is up (the figures mix them);
   when the period's `ispName` differs from the active WAN's provider (the metrics and the
   statistics disagree); when the site is unknown to `/v1/sites`; or when the WAN is not an enabled
   port of the console. A WAN that stops being primary is not emitted, so the full batch withdraws its
   values: its curve gets a gap and the other WAN's curve starts. A console that is filtered out or
   blocked has no Internet access and is skipped silently; if two sites resolve to one access, the
   first wins.
4. **Four new attributes of `internet_access`, all `series: true` floats**, named like the gateway's
   so one query covers both: `latency_ms` (`avgLatency`), `latency_max_ms` (`maxLatency`), `loss_pct`
   (`packetLoss`, a percentage) and `uptime_pct` (`uptime`). A value that is null, not a number or
   a numeric string is left out silently; one that is negative, or above 100 for a percentage, is left
   out with a warning. `downtime`, `ispAsn` and `version` are not decoded.
5. **Throughput is not stored.** `download_kbps` and `upload_kbps` are 0 in every recording, the
   field's meaning is unverified and a curve of zeros would be wrong rather than empty; ADR 0041 and
   ADR 0045 also keep throughput out of trica (the metrics store keeps it). Follow-up issue #166 asks for
   it once a recording shows non-zero values.
6. **A failing metrics call never fails or degrades the run.** On 4xx, 5xx, 429, a malformed answer
   or a timeout the collector adds a warning `ISP metrics: <error>` (with the `Retry-After` when
   present); the scope stays what it would have been and `Health.OK` stays true. The series are not
   emitted, so a full batch withdraws them and the curve has a gap where there was no reading. It is
   not `partial`: a permanently failing endpoint (the key's role, an API change) would make every run
   partial for ever and switch off absence detection for every device. This is a third outcome class
   next to ADR 0057 item 9 (a failed call that leaves a partial run) and a failed run. When a connected
   console has no period in the window, a warning says so. *Amended (#175): the warning also covers a
   console with an Internet access that no entry of the answer is about (empty `data`, or entries of
   other hosts only); it is given once per console and only when the call was made and succeeded.*
7. **`options.isp_metrics`, default `true`**; `false` makes no such call.
8. **The read-only scan gets stricter, `/ea/` stays forbidden.** `TestOnlyGETIsBuilt` now also refuses
   any string that starts with `/v1/` and has something after it unless it is one of `/v1/hosts`,
   `/v1/sites`, `/v1/devices`, `/v1/isp-metrics/5m`. `/v1/isp-metrics/5m/query`, `/v1/isp-metrics/1h`
   and any other path are therefore a red test. This is the conscious change to the scan that #162 asked
   for.
9. **The fake serves what the API does**: `isp-metrics/5m` and `/1h` from the recordings, with the
   `beginTimestamp`/`endTimestamp` filter (both ends inclusive), the recorded 400 for a `5m` duration
   other than `24h`, a synthesised 400 for another type or an unparsable timestamp.
   `ReplayMetricsUntil` shifts the recorded periods to a test clock far from the recording.

## Rejected

- **Backdated points** (each 5-minute period stored at its `metricTime`, backfilling what happened
  between runs). It needs timestamped samples in `pkg/sdk` and rows written in the past, alongside
  rows already open, in the engine: a breaking structural change that contradicts ADR 0045's "no new
  storage". The curve is as fine as the run interval instead.
- **`duration=24h`**: about 60 KB per site every run to use the last few periods.
- **`/ea/isp-metrics` and `POST .../query`**: early access and a write verb; the scan forbids both.
- **A second source per run** (a separate "metrics" source with its own batch): the series would be
  withdrawn or kept on a different rhythm from the Internet access they describe.
- **A `partial` run on a metrics failure**: see item 6.

## Consequences

- A curve is as fine as the run interval (5 minutes by default) and lags by up to one period, more
  if the newest period is still being revised: a revised value just becomes one more row. Periods
  between two runs are not backfilled, and a run that fails leaves a gap.
- The Internet access page shows **Trends** only for the series it holds as a number now (ADR 0045).
  While metrics are stale (no period in the window, a failover to an unattributable state, a failing
  call) the series are withdrawn, so the card disappears although the history exists. This is existing
  behaviour; a finding (#167) was opened rather than changed here.
- The web needed no change: `GET /objects/{id}/series` and the Trends card are generic over the
  schema's `series` flag. The OpenAPI snapshot and the generated types are regenerated.
- Egress, key role and personal data are unchanged from ADR 0057; the metrics answers carry no
  personal data and the host id, as before, is only used to match entries.
- Unverified, listed in `testdata/README.md`: whether `metricTime` labels a period's start or end,
  whether the newest period can change, `packetLoss`'s unit (0 throughout the recording), paging and
  `hostIds` on this endpoint, the 400 body of `15m`, load-balanced and multi-site consoles.

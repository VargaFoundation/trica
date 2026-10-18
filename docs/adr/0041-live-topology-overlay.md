# ADR-0041: Live topology overlay, proxied by the server; cables inferred for management controllers

- **Status**: accepted (2026-10)
- **Concerns**: `internal/live`, `internal/api` (one endpoint), `internal/server` (the `live` block),
  `internal/deduce`. Amends ADR 0024 (the topology reads more than relations) and ADR 0010 (who a
  forwarding table may cable).

## Context

The topology page draws what the inventory knows: devices, interfaces, cables and their
confidence. Operators also want to see what the links are doing now, the way switch vendors'
controllers do: throughput on each link, links down, saturated aggregates. That data already exists
in most deployments, in a Prometheus-compatible store fed by exporters (switches, routers,
firewalls, node-exporter), under names trica does not control.

Three constraints shape the answer. The UI's content security policy allows `connect-src 'self'`
only, and should keep doing so. trica must be deployable by any organisation: no assumption about
a particular metrics stack, scrape layout or exporter version. And measurements are not inventory
facts: a 30-second rate has no provenance worth historising, and writing it would flood the
history tables (ADR 0002) with values nobody claims.

Separately, the cable map misses the ports of management controllers (iLO, iDRAC, any Redfish
BMC): they speak no LLDP, so the only evidence of where they are cabled is a switch forwarding
table, and ADR 0010 restricted that evidence to physical ports (`kind: physical`), which a BMC
port (`kind: bmc`) is not.

## Decision

**The server proxies, the browser never reaches the metrics store.** A new read endpoint,
`GET /api/v1/topology/live`, returns `{enabled, generated_at, stale, refresh_seconds, links,
sources}`, where `links` maps an interface id to `{rx_bps, tx_bps, oper, util}`. The server queries
the configured endpoint (`/api/v1/query`, which Prometheus, VictoriaMetrics, Thanos and Mimir all
serve) with its own credentials: basic authentication or a bearer token, given as `file:` or
`env:` references like collector credentials. The CSP is unchanged, the credentials never reach a
browser, and the store's address is never in an answer.

**A fixed allow-list of queries.** The server sends exactly two shapes,
`rate(<metric>{<matchers>}[<window>s])` and `<metric>{<matchers>}`. Metric and label names come
from the configuration and are validated as Prometheus names when it is loaded; matcher values are
quoted by the server; the window is a whole number of seconds. No client sends an expression, and
no configuration value can inject one. The endpoint can therefore be given a read-only account.

**Sources are presets, not assumptions.** A source has a kind: `arista`, `mikrotik` (mktxp),
`pfsense`, `node` (node-exporter), whose defaults are the exporters' own metric and label names, or
`generic`, which has none. Every name is overridable (`metrics`, `labels`, `matchers`,
`exclude_interfaces`, `unit`), so another exporter (SNMP, gNMI, a recording rule) is a few lines
of configuration, not code. The block is optional; without it the endpoint answers
`enabled: false` and the UI draws the inventory alone.

**The join is to identities trica already holds.** A series names its device by an address or a
name, matched against the device's `mgmt_ip`, `bmc_ip`, `primary_ip` and `name` (addresses with or
without a port, names case-insensitively and with or without their domain); a key two devices share
is dropped rather than guessed. The interface is matched by name in a canonical form (lower case,
EOS abbreviations expanded) among the interfaces of that device, modules included. Two series for
one interface keep the highest value, never the sum: a duplicate scrape must not double the
traffic. When two sources measure one interface, the first configured wins. An aggregate (LAG)
with no series of its own is the sum of its members, through the `member_of` relations between
interfaces that the collectors already write (`aggregated: true`, `members: n`), so the UI can
draw a port-channel even when only the physical ports are exported.

**Shared, cached, and honest about staleness.** One measurement set serves every caller for
`refresh` (30 s by default); a refresh happens on demand, at most once per period whatever its
outcome, so neither a busy UI nor a failing endpoint multiplies the queries. A failed query keeps
the source's last good answer for at most ten periods and marks the answer `stale`; past that the
source contributes nothing. The refresh is not cancelled by the caller that triggered it.

**Rights as for relations.** An interface is in the answer only when the caller may read it (type
and site); the per-source counts are given only to a caller who reads interfaces everywhere.

**Nothing is stored.** No measurement reaches the reconciliation engine; the overlay reads the
inventory (devices, interfaces, `member_of`) and never writes it. Its own health is published on
`/metrics`: queries by outcome, their duration, refreshes, matched and unmatched series per source.

**Forwarding tables may cable a management controller.** The deducer (ADR 0010) now takes as host
ports the physical ports and the ports of kind `bmc` the Redfish collector writes for each manager
NIC, with its MAC. On an edge port that LLDP has no opinion about:

- the only known address learned is one physical host port's: cable to it (as before);
- the only known address learned is one BMC port's: cable to it;
- the addresses learned are those of one physical host port and of BMC ports of the same device
  (a controller sharing a LAN-on-motherboard port): cable to the physical port;
- anything else (two devices, two host ports, a BMC learned on two edge ports) is left undecided,
  with its reason in the log, as before.

A port with any LLDP adjacency is no longer read as an edge port, even when its neighbours conflict:
LLDP's ambiguity is reported, not overridden by a weaker signal. Every deduced cable carries a
`reason` attribute, a sentence naming what it rests on (both LLDP views, one view, which learned
address), next to `evidence` and `level`, which are unchanged: a BMC cable is `mac_table` / `low`.

## Consequences

- The topology colours links by utilisation and state without a second origin in the CSP
  (docs/reference/ui.md, "Live overlay"): the page polls the endpoint every 30 s in a query of its
  own, applies the figures to the lines already drawn (no new layout), and keeps the confidence in
  the line style. Device icons are files of the application, because `img-src 'self'` refuses
  `data:` URLs and cytoscape draws a node's picture from a URL.
- The live view is only as good as the join: a device whose exporter labels carry neither an
  address nor a name trica knows is unmatched, and says so in `trica_live_series{state="unmatched"}`
  and in the answer's `sources`. Fixing it is a `labels` override or a relabelling on the metrics
  side, not code.
- There is no history of measurements in trica, by design: the metrics store keeps that, and a
  question about last week's traffic is asked there.
- Cost: one instant query per configured metric and source per period; the join reads the devices,
  modules and interfaces of the inventory at each refresh.
- MLAG: each switch's port-channel is its own interface with its own members; the two halves of an
  MLAG are grouped by the UI, by name across the peer pair, not by this endpoint.
- A host whose two data ports are bonded behind an aggregate is still not cabled from the
  forwarding table: the address is learned on the aggregate, and the member link is unknown
  (ADR 0010). LLDP on the host (lldpd, the agent) remains the way to cable those.

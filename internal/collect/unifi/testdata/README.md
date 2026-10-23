# Fixtures of the unifi collector

Two sets. Each file is the complete JSON body of one UniFi Site Manager API answer
(`https://api.ui.com`), served by `FakeSiteManager` at the path without `.json`;
`<name>.<token>.json` is the page asked with `nextToken=<token>`.

| Set | Recorded or synthesised | Content |
|---|---|---|
| `recorded-2026-10/` | **Recorded** on 2026-10-23 through the Site Manager API v1 (`GET /v1/hosts`, `/v1/sites`, `/v1/devices`, `/v1/isp-metrics/5m` and `/1h`, and the 401 and the 5m duration 400 answers), one key with `owner: false`, from one console, then anonymised. | A UniFi Dream Machine Pro Max (UniFi OS 5.1.x; hardware firmware 5.1.33; Network 10.6.106, Protect 7.3.70, Access 4.3.22) with two WAN ports (10G SFP+ plugged, 2.5GbE unplugged) and 10 adopted devices: 6 U7 Pro (8.7.11), 1 USW Pro Max 24 PoE (7.5.15), 1 UA Hub Door (4.13.9.0), 1 UA G2 Pro (1.14.17.0). One page each (no `nextToken`). `errors/401.json` is the real shape of a refused key; the fake serves it for a wrong key. `v1/isp-metrics/5m.json`: **recorded** 2026-10-23, the 13 periods of 08:00 to 09:00Z (one site, a working morning, 48 clients online), requested with `beginTimestamp`/`endTimestamp`. `v1/isp-metrics/1h.json`: **recorded** the same day, 00:00 to 08:00Z; served by the fake but never read by the collector. `errors/400-isp-metrics-duration.json`: **recorded** answer to `5m` with a `duration` other than `24h`. |
| `synthesised/` | **Synthesised** by hand from the recorded shapes and the public API documentation. Nothing in it was observed. | Three hosts over two pages (a UDM Pro console, a self-hosted Network Server, and a UCG Max console that is `disconnected`); two pages of devices; an offline AP; a switch with an update available whose version is a number; a Protect camera; a model no rule knows; a device that is not adopted; `null`, number and string values where strings are expected; a WAN with no public address (the unplugged WAN2) and one whose public address only the host entry gives. `v1/isp-metrics/5m.json` covers two sites around 2026-10-23T09:00Z: periods with `null`, a number as text and an empty `data`, one period after the clock of the test, and a disconnected console whose last period is hours old; `v1/sites.json` carries `failoverPriority` and `wanLoadBalancingMode`. |

The early-access ISP-metrics (`/ea/isp-metrics`) and SD-WAN answers were also seen and are **not**
committed: the collector does not read them.

## Anonymisation (recorded set)

Applied by hand, consistently across the three files (one real value always got the same fake value):

- Account data: the e-mail is `operator@example.test`, the name `Example Operator`, the permissions
  are reduced to one key (`network.management`: `["read"]`), user, role, hardware and anonymous ids
  are `00000000-0000-4000-8000-0000000000xx`. They are kept as **sentinels**: the collector never
  decodes them, and `privacy_test.go` proves that none of them reaches a batch.
- Location: coordinates and radius 0, text `Example City` (also a sentinel). The time zone is kept
  (`Europe/Paris`, country-level only) and is also a sentinel.
- Every MAC address, in each recorded format (12 hex digits in the device list, colon-separated in
  the hosts and sites), and the console's serial number (which equals its MAC on UniFi) are
  locally administered, `02:00:00:00:00:NN` or `0200000000NN`, mapped consistently. The IPv6
  link-local addresses were re-derived from the fake MACs.
- Addresses: the office's public address is `203.0.113.10`, the console's WAN side address
  `198.51.100.2`, its LAN address `192.0.2.1`, the devices' `192.0.2.11` to `192.0.2.19`.
- Host id: zero-filled, same shape, ending `1:1234567890`; site id `000000000000000000000001`; trace
  ids all zeros. The direct-connect domain is `0000000000000000.id.ui.direct`, the QR id `AAAAAA`;
  the CPU id and the internal bridge URIs are emptied; the JavaScript `prefetch` bundle lists are
  emptied; backup paths are `/data/backup/example`.
- ISP metrics: the ISP name is `Example ISP` and its AS number `64500` (also in `ispAsn`); the host
  and site ids are those of the rest of the recorded set. **Kept as recorded**: the periods' times,
  latency, loss and uptime values, `version` (the Network application's) and the zero throughput.
- Names: host name `office-a`, console name `Office A`, devices `ap-01` to `ap-06`, `sw-01`,
  `door-hub-01`, `reader-01`; notes are emptied. The ISP is `Example ISP`, AS64500 (a documentation
  AS number).
- **Kept as recorded**: product data (model, shortname, BOM, hardware revision, sysid, the UIDB
  image ids), versions, timestamps, counts, controller and application states.
- No API key, token or password appears. `anonymised_test.go` fails on a MAC that is not locally
  administered, an IPv4 address outside the documentation ranges, loopback and link-local, an IPv6
  address that is not link-local or documentation, an e-mail outside `example.test`, a non-zero
  coordinate, or a key named like a credential. It skips version strings, which look like addresses
  (`10.6.106.0`).
- The **synthesised** set uses the same ranges (`02:00:00:0N:...` MACs, documentation addresses).

## To verify against the real API

1. Pagination: the field `nextToken`, the query parameter `nextToken`, and that `pageSize` may be
   omitted. The recorded account has one console, so no second page was observed.
2. The body of the 403 and 429 answers (the 401 is recorded; the other two are synthesised).
3. `hostIds[]` filtering on `/v1/devices`.
4. Status values other than `online` (`offline`, `updating`, ...), and `firmwareStatus` and
   `updateAvailable` when an update exists.
5. Which of `reportedState.version` (5.1.132) and `hardware.firmwareVersion` (5.1.33, the same as
   the device list's `version`) the UniFi interface shows as "UniFi OS". The collector uses the
   device list's.
6. A Protect camera's `productLine` and `model` strings.
7. Whether a self-hosted Network Server reports the same fields as a console.
8. Whether `metricTime` is the start or the end of a period, and whether the newest period can still
   change.
9. The unit of `packetLoss` (0 throughout the recording; the collector treats it as a percentage and
   leaves a value above 100 out), and whether `download_kbps` and `upload_kbps` are ever not 0.
10. Paging, and `hostIds[]` filtering, on `/v1/isp-metrics/5m`; the 400 body of an unsupported type
    such as `15m` (the fake's is synthesised).
11. Load-balanced and multi-site consoles: which WAN an entry describes, and one entry per site.

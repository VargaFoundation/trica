# ADR-0060: UniFi ports, PoE and uplinks come from the console's Network Integration API, read-only, from the LAN

- **Status**: accepted (2026-10)
- **Concerns**: `internal/collect/unifi` (`options.api: network`, `network.go`, `map_network.go`,
  `fake_network.go`, the client), `internal/schema/builtin.yaml` (`uplinked_to`, four `interface`
  attributes), `web/openapi.json` and `web/src/api/schema.gen.ts` (regenerated),
  `docs/reference/collectors.md`, `charts/trica/README.md`. Amends ADR 0057 item 10 (note in that
  ADR). Builds on ADR 0005 (single writer), ADR 0010 (no guessing), ADR 0036 (identity scope).
  `pkg/sdk`, the reconcile engine, the store and the migrations are untouched. Closes #163.

## Context

The cloud API (ADR 0057) gives devices but no port, no PoE and no uplink. A console serves its own
Network Integration API, reached from the LAN with a key created on the console. Its specification
is the OpenAPI document of UniFi Network 10.6.106 (the version of the recorded console), embedded in
the reference pages of <https://developer.ui.com/network/v10.6.106/getadopteddevicedetails> and
<https://developer.ui.com/network/v10.6.106/connectorget> (there is no downloadable file; an
unofficial mirror is github.com/opastorello/unifi-api-docs). **No console was available: everything
below is built from that document, never run against a real console.**

What the specification gives, and what it does not:

- `GET /v1/info`, `/v1/sites`, `/v1/sites/{id}/devices`, `/v1/sites/{id}/devices/{id}`, lists paged
  by `offset` and `limit` (default 25, maximum 200) with `totalCount`.
- A device's detail has `interfaces.ports[]` (`idx`, `state`, `connector`, `speedMbps`,
  `maxSpeedMbps`, `poe{enabled, standard, state}`) and `uplink{deviceId}`.
- **No port says what it is connected to**: no port name, no MAC, no LLDP, no forwarding table, and
  the uplink names the parent *device*, not a port on either end. `internal/deduce` works port to
  port (`neighbor_of`, `learned_macs`), so no cable can be deduced from this API. Matching ports by
  speed or PoE would be a guess (six access points sit on six identical 802.3bt ports).
- The specification declares no security scheme: the `X-API-KEY` header is from the issue and
  community sources, unverified.

## Decision

1. **Same kind, `options.api: network`** (default `site-manager`) plus `options.sites` (by name or
   internal reference, case-insensitive; all unmatched is an error). Options of the other API are
   refused by `New` (`hosts`, `isp_metrics` with `network`; `sites` with `site-manager`). `address`
   is required, https, no path; the collector adds `/proxy/network/integration`. It runs in mode 2
   (`trica collector`) on the LAN, with the **same `domain`** as the cloud target so addresses merge.
   Redirects are not followed; the key is in one header.
2. **GET only, four endpoint shapes.** The source guard is an exact allow-list: any string literal
   starting with a slash and a letter, in a non-test, non-fake source, must be one of the allowed
   paths. `/clients`, `/actions`, `/pending-devices`, the legacy `/proxy/network/api` and the cloud
   connector all fail it. A server-supplied id goes into a path only when it is a UUID, escaped.
3. **Merge with the cloud view by `base_mac`.** The two sources never set one attribute to two
   values: this one sets `base_mac`, `name`, `vendor`, `part_number` (= `model`, assumed equal to the
   cloud's shortname), `role` when known, `os_version`, `health`, `mgmt_ip` except on a gateway (the
   cloud reports its WAN side), and the address as `ip_address`. It does not set `model`, the
   firmware record, `uptime_s`, `serial`, `managed_by`. Role order: `options.roles`, features
   (gateway, accessPoint, switching), the cloud's model rules, a key prefix `UA` that is not
   `UAP`/`UAL` (Access), else unset with a warning.
4. **One `interface` per port**, `Port <idx>`: `oper_status`, `speed_mbps` (when up),
   `max_speed_mbps`, `connector`, `poe_status` (`disabled`, `delivering`, `idle`, `limited`,
   `unknown`; absent without PoE) and `poe_standard`. Radios, LAGs and `poe.type` are left out.
5. **`uplinked_to`**, a new relation from a device to the device it reaches the network through,
   resolved across the selected sites; impact `to_from` (if the uplink fails, this device is cut
   off). An unknown id is a warning; a self-loop is ignored. **No `neighbor_of`, no cable** (see
   Context); a test runs `deduce` on the merged inventory and requires none.
6. **Clients are never requested**: even a list would move people's names, MACs and addresses
   through the collector.
7. **Partial runs.** `/v1/info` or `/v1/sites` failing fails the run. A site's list failing, or a
   device's detail failing, makes the batch partial (nothing withdrawn); the device is kept without
   ports or uplink. A 401 or 429 on a call stops the remaining calls. An id that is not a UUID is a
   warning and a partial batch, without a health error.
8. **The cloud connector variant is rejected**: it needs an owner or organisation key, forwards
   POST/PUT/DELETE/PATCH (a leaked key could write to the network), and cannot be tested. The guard
   keeps refusing `/connector/`; revisiting it needs an amendment.

## Consequences

- The console's two WAN ports appear twice: the cloud's `eth8`/`eth9` and the console's `Port 9`,
  `Port 10`; the API gives no port role to match them.
- `uplinked_to` is drawn on the topology page as a device-level line (ADR 0062); port-level adjacency needs
  either the unofficial legacy endpoint (not in the official documentation, needs a real console to
  record) or a later API version (follow-up issue).
- Evidence stays `recorded` (one value per module); the `Tested` entry says synthesised, never run.
- `testdata/README.md` lists 13 points to verify on a real console.

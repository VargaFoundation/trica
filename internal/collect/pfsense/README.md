# pfsense collector

Reads a pfSense firewall through the **pfSense REST API package v2**
(pfrest, <https://pfrest.org>). Read-only: the client has one method, `GET`,
authenticated by an API key in the `X-API-Key` header. Redirects are never
followed, so the key cannot leave the configured address.

> Status: fixtures are **synthesised from documentation**, not recorded.
> Field names are from memory of the pfrest v2 documentation and are
> unverified against a real firewall. See `testdata/README.md` for the list of
> things to check.

## Target

```yaml
- kind: pfsense
  name: fw-site-a                # label of the firewall in trica (source "pfsense:fw-site-a")
  address: fw-a1.example.test:443  # host[:port], https only; the REST API is served by the web GUI
  password: file:/run/secrets/pfsense-fw-site-a-key   # the API key; "username" is unused
  tls: {ca_file: /etc/trica/ca.pem}  # or insecure: true for a self-signed certificate
  timeout: 30s
  options:
    skip: []                     # firewall_rules, nat, vpn, routes
    base_path: /api/v2           # only behind a reverse proxy that rewrites it
```

`name` also namespaces the objects the schema cannot scope under the device
(gateways, tunnels, VLANs): they are named `<name>/<gateway>`,
`<name>/openvpn-server-<vpnid>`, `<name>/ipsec-<ikeid>`, VLAN key `<name>:<vid>`,
because two firewalls both have a `WAN_DHCP` gateway and a VLAN 20. Choose a
`name` once and keep it.

## What is collected

| pfSense data (endpoint) | trica |
| --- | --- |
| `system/hostname` | `device.name` (identity, weak) |
| `status/system` `serial` | `device.serial` (identity, strong); placeholders (`none`, ...) ignored |
| `status/system` `platform`, `cpu_count`, `uptime` | `device.model`, `device.cpu_count`, `device.uptime_s` |
| `system/version` | `device.os_version` (`2.8.1-p1`), `device.os_name` = pfSense; health `version` |
| target address, when it is an IP | `device.mgmt_ip` (identity, weak) |
| constant | `device.role` = firewall |
| `interfaces` + `status/interfaces` + `interface/vlans` | `interface` in the device, named by OS name (`igb0`, `igb1.20`): `kind` (physical, vlan, loopback, lag, tunnel, virtual), `description`, `mac`, `admin_status`, `oper_status`, `speed_mbps`, `duplex` (from `media`), `mtu`. A VLAN parent that is not assigned still gets an interface |
| `interface/vlans` | `vlan` (key `<name>:<tag>`, `vid`, `name`, `domain`) and `member_of` (tagged) from the VLAN interface |
| interface addresses (live from `status/interfaces`, configured as fallback; IPv4 and IPv6, link-local skipped) | `ip_address` + `prefix_length`, `assigned_to` the interface; `prefix` (the network, `assigned_to` the interface) |
| `routing/gateways` + `status/gateways` | `gateway` (`address`, `status` online/offline/degraded, `latency_ms`, `loss_pct`), `contains` from the device. Dynamic gateways (`WAN_DHCP`) exist only in the live state and are reported from it. The `defaultgw` one gets `routes_via` from the device |
| `routing/static_routes` (enabled, network is a CIDR, gateway known) | `prefix` `routes_via` the `gateway` |
| `firewall/rules` | `firewall_rule` in the device, `kind` filter, `action` (pass/block/reject), `interface` (ids, comma-joined), `protocol`, `source`, `destination`, `port` (destination port), `order`, `enabled`, `description`. Key `filter:<tracker>` |
| `firewall/nat/port_forwards` | `firewall_rule` `kind` nat_port_forward, `target` = `address:local_port`. Key `nat_port_forward:<associated_rule_id>` |
| `firewall/nat/outbound/mappings` | `firewall_rule` `kind` nat_outbound, `target`, `action` = `nonat` when excluded from NAT. Key `nat_outbound:t<created_time>` |
| `firewall/nat/one_to_one/mappings` | `firewall_rule` `kind` nat_one_to_one, `target` = external address |
| `vpn/openvpn/servers`, `vpn/openvpn/clients` + `status/openvpn/{servers,clients}` | `tunnel` `kind` openvpn, `peer` (clients), `networks`, `status` (up/down), `contains` from the device |
| `vpn/ipsec/phase1s` + `status/ipsec/sas` | `tunnel` `kind` ipsec, `peer` (not for mobile), `status` (up when an SA `con<ikeid>` is ESTABLISHED, else down) |

Schema additions: none. Everything fits the existing types.

Rule identity is the pfSense identifier of the rule, not its position: the
`tracker` of a filter rule; for NAT rules (which have none) the associated
rule id, else the creation time. A rule with no stable id is keyed by its
position (`filter:#3`) and the run carries a warning: reordering such rules
creates new objects. `order` is the position in the list the API returns.

Tunnel status is `unknown` (left unset) when the status endpoint could not be
read, `down` when it was read and does not list the tunnel or the tunnel is
disabled.

## Run outcomes

| Situation | Batch |
| --- | --- |
| everything read | scope `full`, health OK |
| key refused (401) or firewall unreachable on the first request | scope `partial`, no object, health not OK, one request only |
| every endpoint failed (not the REST API: HTML, redirect, all 404) | scope `partial`, no object, health not OK |
| a sub-request fails (403 missing privilege, 5xx, malformed, timeout) | what was read is kept, scope `partial`, health not OK with the error |
| an endpoint that feeds objects answers 404 (this REST API version lacks it) | scope `partial` (the objects cannot be claimed complete), health OK, warning |
| a status endpoint answers 404 | scope `full`, health OK, warning; the attributes it feeds stay unset |

A 404 is deliberately not an unhealthy run: nothing is broken, the endpoint
does not exist. The 404 on an object endpoint keeps the scope partial so that
nothing already known is withdrawn.

## Version compatibility

| pfSense | REST API package | Status |
| --- | --- | --- |
| CE 2.7.2 | v2.x | fixtures `2.7.2/` (synthesised) |
| CE 2.8.x (2.8.1) | v2.x | fixtures `2.8.1/` (synthesised) |
| Plus (24.x, 25.x), CE 2.7.0-2.7.1, 2.8.0 | v2.x | untested; expected to work, the collector reports the version it sees |
| any | REST API v1 (`/api/v1`) | not supported: different paths and envelope |

The REST API package version itself is not read (the collector reports the
pfSense version from `system/version`); the exact version present at the
target site is unknown.

## Read-only account on the firewall

Goal: an API key whose user can only GET the endpoints above.

**Confidence.** The GUI paths are those of pfSense CE 2.7/2.8 with REST API v2.
The **exact names of the REST privileges are not verified**: pfrest v2 adds
privileges per endpoint and per verb, and I could not confirm their labels
without a firewall. Select by meaning, then verify with the tests at the end:
they are the proof, not this text.

1. **System > REST API > Settings**: under *Authentication methods* enable
   **API key** (the default is local-database Basic authentication only). Enable
   **Read-only mode** if the package offers it (it refuses every non-GET
   request whatever the user's privileges: a second lock). Check the
   *allowed interfaces* setting: the interface the collector reaches the web
   GUI from must be allowed.
2. **System > User Manager > Groups > Add**: name `trica-readonly`, scope
   *Local*. Under *Assigned Privileges* add only the REST API privileges that
   read (GET) the endpoints listed in the table, nothing else. In the
   privilege list, filter on `api` / `REST`. Never add `page-all` ("WebCfg -
   All pages") nor any create/update/delete (POST, PATCH, PUT, DELETE)
   privilege.
3. **System > User Manager > Users > Add**: name `trica`, a long random
   password, not expired, *Group membership* `trica-readonly`. The password is
   never used by the collector (it uses the key); store it nowhere or in the
   team vault.
4. **System > REST API > Keys > Add**: the key belongs to the user who creates
   it, so create it while logged in as `trica` (this page needs a REST API
   settings privilege: grant it temporarily to the group, create the key, copy
   it, remove the privilege). Store the key in the secret file referenced by
   `password:`. It is shown once.

Verify (replace host and key):

```sh
curl -sk -H "X-API-Key: $KEY" https://fw/api/v2/system/version         # 200, version
curl -sk -H "X-API-Key: $KEY" https://fw/api/v2/firewall/rules         # 200
curl -sk -o /dev/null -w '%{http_code}\n' -X POST -H "X-API-Key: $KEY" \
     -H 'Content-Type: application/json' -d '{}' https://fw/api/v2/firewall/rule   # must be 403 or 405, never 200
```

A 403 on a GET names the missing privilege; add it, or put the section in
`options.skip` (`firewall_rules`, `nat`, `vpn`, `routes`) so that the run is
not reported partial forever. `skip` stops the objects of that section being
reported; with a full scope, those already known are then withdrawn.

## Options

| Key | Default | Meaning |
| --- | --- | --- |
| `skip` | none | sections not to read: `firewall_rules`, `nat`, `vpn`, `routes` |
| `base_path` | `/api/v2` | API prefix, for a reverse proxy |

Unknown keys are an error. `username` is ignored.

## Not collected, and why

| Data | Why |
| --- | --- |
| ARP table (`diagnostics/arp_table`), DHCP leases (`status/dhcp_server/leases`) | One entry per client device is churn, not infrastructure, and no type fits: neither an `ip_address` nor an `interface` of a firewall is the client. Never requested. (`interface.learned_macs` is for switch ports.) |
| IPsec phase 2, aliases, DHCP server settings, DNS, WireGuard, certificates, users | No type or attribute; out of the first version |
| Hardware UUID, base MAC | pfrest does not expose the SMBIOS UUID; interface MACs are not a chassis MAC. The device is identified by serial (when real), name and management IP |
| Rule source port, log flag, schedule, direction, gateway | No attribute on `firewall_rule`. Only the destination port is kept |
| Rule hit counters, states, traffic counters | Metrics, not inventory |
| CPU load, memory, disk, temperature | Metrics; no inventory use |
| Pagination | The collector sends no `limit`/`offset`; to confirm on a firewall with very many rules |
| LLDP | pfSense has no LLDP by default (an optional package) and the REST API has no endpoint for it |

# pfSense fixtures: synthesised, not recorded

Nothing in this directory was recorded from a firewall. They were written by
hand from knowledge of the pfSense REST API package v2 (pfrest,
<https://pfrest.org>) response format, then anonymised by construction:

- serial numbers and the Netgate ID are obviously fake (`FAKE-NG4100-0001`);
- MAC addresses are in the locally administered range (`02:00:00:...`);
- IP addresses come from 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24 and
  2001:db8::/32 only;
- names are generic (`fw-a1`, `fw-b2`, `example.test`); no secret appears.

Each file is a complete pfrest envelope
(`{"code":200,"status":"ok","response_id":"SUCCESS","message":"","data":...}`)
served by `FakePfSense` at `/api/v2/<path without .json>`.

| Directory | Models | Content |
| --- | --- | --- |
| `2.7.2/` | pfSense CE 2.7.2 with a REST API v2.x package | Netgate 4100 style appliance; WAN on DHCP, LAN, one VLAN, a disabled spare port; a dynamic gateway only the live state knows, a down static gateway, a disabled one; static routes (one disabled); filter rules (one disabled floating rule on two interfaces); a port forward, two outbound NAT rules (one `nonat`); an OpenVPN server and a disabled client; two IPsec phase 1 (one mobile) |
| `2.8.1/` | pfSense CE 2.8.1 patch 1 with a REST API v2.x package | virtual machine (no serial, `platform` is `pfSense`); static WAN with IPv6; no VLAN; one degraded default gateway; string trackers; a 1:1 NAT mapping; one IPsec tunnel without active SA |

The two directories differ in content (hardware, features in use, value
representations such as `"cpu_count": 4` as text), not in the REST API
version: the exact REST API package version running on the target site is not
known, and no difference between v2.x minor releases is modelled because none
could be documented with certainty.

## What must be checked against a real firewall

Every field name below is from memory of the pfrest v2 documentation and
OpenAPI schema; none was observed. In rough order of doubt:

1. `status/system`: the names `platform`, `serial`, `netgate_id`, `cpu_model`,
   `cpu_count`, and the type of `uptime` (modelled as the human-readable text
   pfSense prints; the parser also accepts a number of seconds). Whether the
   serial is exposed on non-Netgate hardware at all.
2. `status/interfaces`: `hwif` as the OS interface name, `macaddr`, `status`
   values (`up`, `no carrier`, `down`), `media` text, `subnet` as text or
   number, whether `name` is the pfSense id (`wan`) or the description.
3. `status/gateways`: the `status` vocabulary (modelled: `none`, `down`,
   `delay`, `loss`; pfSense itself uses `none` for "online"), and the `delay`
   and `loss` text formats (`1.234ms`, `0.0%`).
4. Rules: that a filter rule carries a `tracker`, that NAT rules carry
   `associated_rule_id` and `created_time` (used as the stable key when
   present), and the exact shape of `source`, `destination`,
   `destination_port` (modelled as strings such as `any`, `(self)`,
   `wan:ip`, `192.0.2.0/24`).
5. `vpn/openvpn/*`: `vpnid`, and the status vocabulary of
   `status/openvpn/servers` (modelled: `server_tls_listening`).
6. `status/ipsec/sas`: `con_id` (`con<ikeid>`) and `state` (`ESTABLISHED`).
7. Whether the plural endpoints return everything without `limit`/`offset`
   (the collector sends no query string).
8. The endpoint `firewall/nat/one_to_one/mappings` and the status endpoints
   `status/openvpn/clients`, which are not in the original endpoint list the
   collector was specified with.
9. A missing endpoint: pfrest answers 404; its envelope was modelled.

Run the collector against a real firewall, compare with the GUI, and correct
these fixtures before trusting the mapping.

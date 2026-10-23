# ADR-0050: A VLAN is identified within a layer-2 domain the operator names

- **Status**: accepted (2026-10)
- **Concerns**: `internal/collect` (`DefaultVLANDomain`, `VLANDomain`, `VLANKey`),
  `internal/collect/pfsense`, `internal/collect/arista`, `internal/collect/mikrotik`,
  `internal/collect/maas` (`options.vlan_domains`), `web/src/topology/vlans.ts`. Builds on
  ADR 0036 (identity scope).

## Context

A VLAN number only means something inside one layer-2 network. The `vlan` type is identified by
`key`, `"<domain>:<vid>"`. Arista and MikroTik built the domain from an option defaulting to
`default`; MAAS used the name of the fabric; pfSense used the name of its target
(`pfsense-1:201`). On a site where the firewall trunks the same VLANs as the switches, every
VLAN the firewall carries existed twice, `default:201` from the switches and `pfsense-1:201` from
the firewall: one network, two objects, two identical entries in the topology's VLAN filter
(whose label was the VID only).

## Decision

1. **A VLAN is identified by its layer-2 domain and its VID.** Every collector that reports
   VLANs builds the key with `collect.VLANKey(domain, vid)` and the domain from the target's
   configuration, through `collect.VLANDomain(option)`: surrounding spaces trimmed, empty means
   `collect.DefaultVLANDomain` (`default`), the case is kept. Two sources that report VLAN 201 of
   the same network name the same domain, and so report one object.
2. **pfSense takes `options.vlan_domain`, default `default`.** It no longer files its VLANs under
   the target name. This is a behaviour change: a deployment that ran a pfSense target before
   gets new VLAN objects at the next run (consequences below). The target name still scopes
   gateways and tunnels, which have no meaningful cross-source identity.
3. **MAAS maps fabrics to domains: `options.vlan_domains`** (`{fabric-0: default}`). A fabric
   that is not listed stays its own domain, named after the fabric, as before. MAAS fabrics are
   MAAS's own layer-2 domains, and nothing says which of them is the network the switches call
   `default`: the operator does.
4. **The UI tells VLANs apart, it never merges them.** The topology filter labels a VLAN with its
   VID and its name when the name adds something (`201 MGMT`), and with its domain when two
   entries would still read alike (`201 MGMT (lab)`). The labels come from the `vlan` objects'
   `name` and `domain` attributes.

Rejected: `identity_scope` (ADR 0036) on `vlan`. It stamps a domain on every source of a deployment
(the agent's `--domain`, the target's `domain`), which names a routing domain; a VLAN's layer-2
domain differs from it (several VLANs of one VRF, one VLAN trunked over several), and the option
already exists, with this meaning, in two collectors.

## Consequences

- Moving a pfSense target from the old default (its name) to `default`: at the next run the batch
  carries `default:<vid>`, which already exists when a switch reports it. The firewall's VLAN
  interfaces stop being members of `<target>:<vid>` (the relation closes in the history) and
  become members of `default:<vid>`. The old objects are no longer seen and become `absent` after
  the absence delay. Nothing is deleted and nothing needs a migration. To keep the old keys, set
  `options.vlan_domain` to the target's name.
- Two networks that are not connected but number their VLANs alike need distinct domains
  (`options.vlan_domain` per target, `vlan_domains` for MAAS); with the default they are one object.
- Accepted limits. A firewall carrying the same tag on two different layer-2 networks (one per
  parent interface) files both in its one domain; per-parent domains are not modelled. VLANs a
  firewall holds on its WAN interfaces (ISP VLANs) land in `default` too. MAAS's untagged
  (VID 0) VLAN of a fabric is a real VLAN of that fabric, which on a site is often a tagged VLAN
  of the switches: it is not mapped to its tagged twin (tracked as a finding).
- The domain `default` is a convention, not a discovery: two networks never named are one.

## Amendment: integration with the collector descriptors (ADR 0055) and the UniFi collectors (ADR 0057, 0060)

The options added here (`vlan_domain` on arista, mikrotik and pfSense, `vlan_domains` on MAAS)
carry the `default` and `doc` tags of the descriptors, and each collector's `Collects` still
covers the types its fake emits (`collecttest`). The UniFi collectors (`internal/collect/unifi`)
emit no `vlan` object, so the layer-2 domain rule does not apply to them; if a later `api` emits
VLANs, it must key them `<domain>:<vid>` with `collect.VLANKey`.

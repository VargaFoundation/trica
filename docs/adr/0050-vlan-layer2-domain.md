# ADR-0050: A VLAN is identified within a layer-2 domain the operator names

- **Status**: accepted (2026-10)
- **Concerns**: `internal/collect` (`DefaultVLANDomain`, `VLANDomain`, `VLANKey`),
  `internal/collect/pfsense`, `internal/collect/arista`, `internal/collect/mikrotik`,
  `internal/collect/maas` (`options.vlan_domains`, `options.untagged_vids`), `web/src/topology/vlans.ts`. Builds on
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
  of the switches: `options.untagged_vids` maps it to its tagged twin (amendment below, #148).
- The domain `default` is a convention, not a discovery: two networks never named are one.

## Amendment: integration with the collector descriptors (ADR 0055) and the UniFi collectors (ADR 0057, 0060)

The options added here (`vlan_domain` on arista, mikrotik and pfSense, `vlan_domains` on MAAS)
carry the `default` and `doc` tags of the descriptors, and each collector's `Collects` still
covers the types its fake emits (`collecttest`). The UniFi collectors (`internal/collect/unifi`)
emit no `vlan` object, so the layer-2 domain rule does not apply to them; if a later `api` emits
VLANs, it must key them `<domain>:<vid>` with `collect.VLANKey`.

## Amendment: MAAS's untagged VLAN (#148)

MAAS reports the VLAN an interface sits on without a tag as VID 0 of its fabric. On a site that
network is often a tagged VLAN of the switches (a PXE VLAN 203, say), so `vlan_domains` alone
leaves two objects, `default:203` from the switches and `default:0` from MAAS, and one empty
`<fabric>:0` per auto-created fabric.

Decisions:

1. **`options.untagged_vids`** on MAAS: a map from MAAS fabric name to a VID from 1 to 4094. That
   fabric's VID 0 VLAN is reported as `collect.VLANKey(domain, vid)`, the switches' object, with
   that `vid`. It is a separate option from `vlan_domains`, so that one keeps its shape and the
   descriptor (ADR 0055) can reflect both as flat maps; it is keyed by fabric, not by domain,
   because two fabrics of one domain can carry different untagged VIDs. Unset keeps `<domain>:0`.
2. **Membership stays untagged.** `member_of` keeps `tagged = (MAAS VID != 0)`: the interfaces
   join the mapped VLAN untagged. Only the VLAN's identity changes.
3. **The prefix follows**: its `vlan` attribute and `assigned_to` use the mapped VID.
4. **The name `untagged`** (MAAS's default, case-insensitive) is not set on a mapped VLAN, so it
   cannot compete with the switch's name. A name the operator set in MAAS is kept.
5. **Two guards.** A fabric that already has the VID as a tagged VLAN is not remapped (it would
   merge two of MAAS's layer-2 networks) and raises a warning; a fabric listed but unknown to MAAS
   raises a warning (a typo). Both checks are skipped when `fabrics/` could not be read; the
   mapping then still applies and the batch is partial anyway.
6. **Empty fabrics report no untagged VLAN.** The untagged VLAN appears only when an interface or
   a subnet is on it; tagged VLANs of `fabrics/` are still reported, as an operator's declaration.
   Existing member-less `<fabric>:0` objects, and `default:0` once the option is set, become
   `absent` after the absence delay. Nothing is deleted and nothing needs a migration; a partial
   batch withdraws nothing.

Rejected: no object for VID 0 (loses the membership of the interfaces and the prefix's VLAN);
`interface.native_vlan` (it describes a switchport's configuration, which MAAS does not observe,
and merges nothing); inferring the VID from a matching switch-side prefix (a guess, and domains
are named by the operator, not discovered).

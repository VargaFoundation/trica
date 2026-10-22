/** What the VLAN objects say about themselves, beyond the display the relations carry. */
export interface VlanInfo {
  name?: string;
  /** The layer-2 domain the VLAN is identified in (ADR 0050). */
  domain?: string;
}

/**
 * The label of each VLAN in the filter. A VLAN's display is its VID, so two
 * VLANs of different layer-2 domains with the same VID would be two identical
 * entries: the name is added when it says more than the VID ("201 MGMT"), and
 * the domain when two entries would still read alike ("201 MGMT (lab)"). The
 * UI never merges two VLAN objects; it only tells them apart. Without
 * information about a VLAN its display is the label.
 */
export function vlanLabels(
  displays: ReadonlyMap<string, string>,
  info: ReadonlyMap<string, VlanInfo> = new Map(),
): Map<string, string> {
  const base = new Map<string, string>();
  for (const [id, display] of displays) {
    const name = info.get(id)?.name?.trim();
    base.set(id, name && name !== display ? `${display} ${name}` : display);
  }
  const count = (labels: Iterable<string>) => {
    const n = new Map<string, number>();
    for (const l of labels) n.set(l, (n.get(l) ?? 0) + 1);
    return n;
  };
  const first = count(base.values());
  const withDomain = new Map<string, string>();
  for (const [id, label] of base) {
    const domain = info.get(id)?.domain?.trim();
    withDomain.set(id, (first.get(label) ?? 0) > 1 && domain ? `${label} (${domain})` : label);
  }
  // Still alike (same domain, or no domain to tell them apart): the id decides, so no two entries read the same
  // (a last resort: it is only seen when the VLAN objects were not read, or two share a domain and a name).
  const second = count(withDomain.values());
  const out = new Map<string, string>();
  for (const [id, label] of withDomain) out.set(id, (second.get(label) ?? 0) > 1 ? `${label} [${id}]` : label);
  return out;
}

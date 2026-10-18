// What an object is, for the eye: an icon, a family (its colour) and a word.
// It is read from the schema's own vocabulary only: the object's `type`, the
// `role` a device declares (an enum of the schema), and, for a type this file
// does not know (a custom type), the layer the schema puts it in. No device
// name, vendor or model is ever looked at: a server is a server because the
// inventory says role:server, not because it is called srv-something.
import type { IconName } from "../components/ui/icons";

export type Family = "network" | "compute" | "storage" | "power" | "security" | "other";

export interface Kind {
  /** The role of a device when it has one, the type otherwise. */
  key: string;
  icon: IconName;
  family: Family;
  /** Set when the key is a device role (its label is translated as role.<key>). */
  role?: string;
}

/** The values of the schema's device `role` enum, and how each is drawn. */
const ROLES: Record<string, { icon: IconName; family: Family }> = {
  server: { icon: "server", family: "compute" },
  switch: { icon: "switch", family: "network" },
  router: { icon: "router", family: "network" },
  firewall: { icon: "firewall", family: "security" },
  storage: { icon: "storage", family: "storage" },
  nas: { icon: "nas", family: "storage" },
  pdu: { icon: "pdu", family: "power" },
  ups: { icon: "ups", family: "power" },
  patch_panel: { icon: "patch_panel", family: "network" },
  sensor: { icon: "sensor", family: "other" },
  kvm: { icon: "kvm", family: "other" },
  other: { icon: "device", family: "other" },
};
export const KNOWN_ROLES = Object.keys(ROLES);

/** The built-in types of the schema (internal/schema/builtin.yaml). */
const TYPES: Record<string, { icon: IconName; family: Family }> = {
  site: { icon: "site", family: "other" },
  building: { icon: "building", family: "other" },
  room: { icon: "room", family: "other" },
  row: { icon: "rack", family: "other" },
  aisle: { icon: "room", family: "other" },
  rack: { icon: "rack", family: "other" },
  device_type: { icon: "document", family: "other" },
  device: { icon: "device", family: "other" },
  module: { icon: "module", family: "compute" },
  psu: { icon: "psu", family: "power" },
  fan: { icon: "fan", family: "other" },
  disk: { icon: "disk", family: "storage" },
  memory: { icon: "memory", family: "compute" },
  cpu: { icon: "cpu", family: "compute" },
  transceiver: { icon: "transceiver", family: "network" },
  sensor: { icon: "sensor", family: "other" },
  interface: { icon: "interface", family: "network" },
  front_port: { icon: "interface", family: "network" },
  rear_port: { icon: "interface", family: "network" },
  power_port: { icon: "psu", family: "power" },
  power_outlet: { icon: "psu", family: "power" },
  circuit: { icon: "psu", family: "power" },
  cable: { icon: "cable", family: "network" },
  vlan: { icon: "network", family: "network" },
  vrf: { icon: "network", family: "network" },
  prefix: { icon: "network", family: "network" },
  ip_address: { icon: "network", family: "network" },
  gateway: { icon: "router", family: "network" },
  internet_access: { icon: "network", family: "network" },
  tunnel: { icon: "network", family: "network" },
  firewall_rule: { icon: "firewall", family: "security" },
  vm: { icon: "vm", family: "compute" },
  k8s_cluster: { icon: "k8s", family: "compute" },
  k8s_node: { icon: "k8s", family: "compute" },
  k8s_service: { icon: "k8s", family: "compute" },
  namespace: { icon: "k8s", family: "compute" },
  workload: { icon: "k8s", family: "compute" },
  pod: { icon: "k8s", family: "compute" },
  ingress: { icon: "k8s", family: "network" },
  software: { icon: "application", family: "other" },
  version_policy: { icon: "document", family: "other" },
  service: { icon: "application", family: "other" },
  listener: { icon: "interface", family: "network" },
  application: { icon: "application", family: "other" },
  component: { icon: "application", family: "other" },
  component_instance: { icon: "application", family: "other" },
  endpoint: { icon: "network", family: "network" },
  environment: { icon: "application", family: "other" },
  team: { icon: "person", family: "other" },
  person: { icon: "person", family: "other" },
  vendor: { icon: "document", family: "other" },
  contract: { icon: "document", family: "other" },
  ticket: { icon: "document", family: "other" },
};

/** A custom type is drawn by its layer in the schema. */
const LAYERS: Record<string, { icon: IconName; family: Family }> = {
  location: { icon: "site", family: "other" },
  physical: { icon: "device", family: "other" },
  energy: { icon: "psu", family: "power" },
  cabling: { icon: "cable", family: "network" },
  network: { icon: "network", family: "network" },
  connection: { icon: "network", family: "network" },
  compute: { icon: "vm", family: "compute" },
  software: { icon: "application", family: "other" },
  application: { icon: "application", family: "other" },
  organisation: { icon: "person", family: "other" },
};

/**
 * The kind of an object from its type, its `role` attribute and the layer of
 * its type. A role the UI does not know (a newer schema) keeps its own word
 * and the generic device icon.
 */
export function kindOf(obj: { type: string; attributes?: Record<string, unknown> }, layer?: string): Kind {
  const role = obj.attributes?.role;
  if (typeof role === "string" && role !== "") {
    const r = ROLES[role] ?? { icon: "device" as const, family: "other" as const };
    return { key: role, role, ...r };
  }
  const t = TYPES[obj.type] ?? (layer ? LAYERS[layer] : undefined) ?? { icon: "object" as const, family: "other" as const };
  return { key: obj.type, ...t };
}

export function isKnownRole(role: string): boolean {
  return role in ROLES;
}

// Anonymised API answers for the tests: documentation addresses (192.0.2.0/24,
// 198.51.100.0/24), MAC addresses in the locally administered range (02:…),
// invented names (sw-a1, srv-01, paris-1). Each one is typed by the shapes
// generated from openapi.json, so a field the server renames breaks the build.
// They are written by hand, not recorded from a server: see docs/reference/ui.md.
import type {
  Change,
  Discrepancy,
  Me,
  ObjectView,
  Page,
  Proposal,
  Provenance,
  RelationLink,
  RelationView,
  SchemaDoc,
  SearchPage,
  Series,
  SourceHealth,
} from "../api/types";

export const ID = {
  site: "0a000000-0000-4000-8000-000000000001",
  rack: "0a000000-0000-4000-8000-000000000002",
  swA1: "0a000000-0000-4000-8000-0000000000a1",
  srv01: "0a000000-0000-4000-8000-0000000000b1",
};

export const page = <T>(items: T[], extra: Partial<Page<T>> = {}): Page<T> => ({
  total: items.length,
  limit: 50,
  offset: 0,
  items,
  ...extra,
});

export const meWith = (actions: string[]): Me => ({ subject: "reader-1", kind: "token", source: "ui-tests", actions });
export const READER = meWith(["read"]);
export const PROPOSER = meWith(["read", "propose"]);
export const APPROVER = meWith(["read", "propose", "approve"]);

export const swA1: ObjectView = {
  id: ID.swA1,
  type: "device",
  parent_id: ID.rack,
  site_id: ID.site,
  display: "sw-a1",
  status: "active",
  attributes: {
    hostname: "sw-a1",
    vendor: "arista",
    os_version: "4.28.3M",
    mgmt_ip: "192.0.2.10",
    power_w: 120,
    rack_unit: "U12",
  },
  created_at: "2026-03-01T08:00:00Z",
  first_seen: "2026-03-01T08:00:00Z",
  last_seen: "2026-10-12T06:30:00Z",
  observed: true,
};

export const srv01: ObjectView = {
  id: ID.srv01,
  type: "device",
  display: "srv-01",
  status: "active",
  attributes: {
    hostname: "srv-01",
    vendor: "dell",
    mgmt_ip: "198.51.100.21",
    role: "server",
    health: "warning",
    power_w: 412.5,
    inlet_temp_c: 23,
    outlet_temp_c: 38.5,
    uptime_s: 1_303_200,
    memory_bytes: 274_877_906_944,
    log_critical_count: 2,
  },
  created_at: "2026-03-02T08:00:00Z",
  last_seen: "2026-10-12T06:31:00Z",
  observed: true,
};

export const rackR12: ObjectView = {
  id: ID.rack,
  type: "rack",
  display: "r12",
  status: "active",
  attributes: { name: "r12" },
  created_at: "2026-03-01T08:00:00Z",
  observed: false,
};

export const siteParis: ObjectView = {
  id: ID.site,
  type: "site",
  display: "paris-1",
  status: "active",
  attributes: { name: "paris-1" },
  created_at: "2026-03-01T08:00:00Z",
  observed: false,
};

export const schema: SchemaDoc = {
  types: [
    {
      name: "device",
      layer: "physical",
      label: { en: "Device", fr: "Équipement" },
      description: "A network or compute device.",
      display: "hostname",
      parents: ["rack"],
      custom: false,
      identity: { strong: ["serial"], weak: ["hostname"] },
      attributes: [
        { name: "hostname", kind: "string", label: { en: "Hostname", fr: "Nom d'hôte" }, description: "", search: true, required: true, custom: false },
        { name: "vendor", kind: "string", label: { en: "Vendor", fr: "Constructeur" }, description: "", search: true, required: false, custom: false },
        { name: "os_version", kind: "string", label: { en: "OS version", fr: "Version de l'OS" }, description: "", search: true, required: false, custom: false },
        { name: "mgmt_ip", kind: "ip", label: { en: "Management address", fr: "Adresse de gestion" }, description: "", search: true, required: false, custom: false },
        { name: "power_w", kind: "number", label: { en: "Power draw", fr: "Consommation" }, description: "", unit: "W", search: false, required: false, custom: false, series: true },
        { name: "role", kind: "enum", label: { en: "Role", fr: "Rôle" }, description: "", search: false, required: false, custom: false, values: ["server", "switch", "router", "firewall", "storage", "nas", "pdu", "ups", "patch_panel", "sensor", "kvm", "other"] },
        { name: "health", kind: "enum", label: { en: "Health", fr: "Santé" }, description: "", search: false, required: false, custom: false, values: ["ok", "warning", "critical", "unknown"] },
        { name: "inlet_temp_c", kind: "float", label: { en: "Inlet temperature", fr: "Température d'entrée" }, description: "", search: false, required: false, custom: false, unit: "°C", series: true },
        { name: "outlet_temp_c", kind: "float", label: { en: "Outlet temperature", fr: "Température de sortie" }, description: "", search: false, required: false, custom: false, unit: "°C", series: true },
        { name: "uptime_s", kind: "int", label: { en: "Uptime", fr: "Durée de fonctionnement" }, description: "", search: false, required: false, custom: false, unit: "s" },
        { name: "memory_bytes", kind: "int", label: { en: "Memory", fr: "Mémoire" }, description: "", search: false, required: false, custom: false, unit: "B" },
        { name: "log_critical_count", kind: "int", label: { en: "Critical events", fr: "Événements critiques" }, description: "", search: false, required: false, custom: false },
      ],
    },
    {
      name: "psu",
      layer: "physical",
      label: { en: "Power supply", fr: "Alimentation" },
      description: "",
      display: "name",
      parents: ["device"],
      custom: false,
      identity: { strong: [], weak: ["name"] },
      attributes: [
        { name: "name", kind: "string", label: { en: "Name", fr: "Nom" }, description: "", search: false, required: false, custom: false, identity: "weak" },
        { name: "capacity_w", kind: "int", label: { en: "Capacity", fr: "Capacité" }, description: "", search: false, required: false, custom: false, unit: "W" },
        { name: "input_w", kind: "float", label: { en: "Input power", fr: "Puissance d'entrée" }, description: "", search: false, required: false, custom: false, unit: "W" },
        { name: "health", kind: "enum", label: { en: "Health", fr: "Santé" }, description: "", search: false, required: false, custom: false, values: ["ok", "warning", "critical", "unknown", "absent"] },
      ],
    },
    {
      name: "disk",
      layer: "physical",
      label: { en: "Disk", fr: "Disque" },
      description: "",
      display: "name",
      parents: ["device"],
      custom: false,
      identity: { strong: [], weak: ["name"] },
      attributes: [
        { name: "name", kind: "string", label: { en: "Name", fr: "Nom" }, description: "", search: false, required: false, custom: false, identity: "weak" },
        { name: "capacity_bytes", kind: "int", label: { en: "Capacity", fr: "Capacité" }, description: "", search: false, required: false, custom: false, unit: "B" },
        { name: "temperature_c", kind: "float", label: { en: "Temperature", fr: "Température" }, description: "", search: false, required: false, custom: false, unit: "°C" },
        { name: "health", kind: "enum", label: { en: "Health", fr: "Santé" }, description: "", search: false, required: false, custom: false, values: ["ok", "warning", "critical", "unknown", "absent"] },
      ],
    },
    {
      name: "rack",
      layer: "physical",
      label: { en: "Rack", fr: "Baie" },
      description: "",
      display: "name",
      parents: ["site"],
      custom: false,
      identity: { strong: [], weak: ["name"] },
      attributes: [],
    },
    {
      name: "site",
      layer: "physical",
      label: { en: "Site", fr: "Site" },
      description: "",
      display: "name",
      parents: [],
      custom: false,
      identity: { strong: [], weak: ["name"] },
      attributes: [],
    },
  ],
  relations: [
    {
      name: "mounted_in",
      label: { en: "mounted in", fr: "monté dans" },
      inverse: { en: "hosts", fr: "héberge" },
      description: "",
      from: ["device"],
      to: ["rack"],
      attributes: [],
      custom: false,
    },
    {
      name: "connected_to",
      label: { en: "connected to", fr: "connecté à" },
      inverse: { en: "connected from", fr: "connecté depuis" },
      description: "",
      from: ["device"],
      to: ["device"],
      attributes: [],
      custom: false,
    },
  ],
};

export const provenanceSwA1: Provenance = {
  object_id: ID.swA1,
  attributes: [
    { key: "hostname", value: "sw-a1", source: "arista:eos", confidence: "observed", valid_from: "2026-03-01T08:00:00Z", first_seen: "2026-03-01T08:00:00Z", last_seen: "2026-10-12T06:30:00Z" },
    { key: "vendor", value: "arista", source: "arista:eos", confidence: "observed", valid_from: "2026-03-01T08:00:00Z", first_seen: "2026-03-01T08:00:00Z", last_seen: "2026-10-12T06:30:00Z" },
    {
      key: "os_version",
      value: "4.28.3M",
      source: "arista:eos",
      confidence: "observed",
      valid_from: "2026-09-20T02:00:00Z",
      first_seen: "2026-09-20T02:00:00Z",
      last_seen: "2026-10-12T06:30:00Z",
      other_claims: [
        { key: "os_version", source: "declared:lab-file", value: "4.27.0M", confidence: "declared", first_seen: "2026-03-01T08:00:00Z", last_seen: "2026-10-11T00:00:00Z" },
      ],
    },
    { key: "mgmt_ip", value: "192.0.2.10", source: "snmp:core", confidence: "deduced", valid_from: "2026-03-01T08:00:00Z", first_seen: "2026-03-01T08:00:00Z", last_seen: "2026-10-12T06:25:00Z" },
    { key: "power_w", value: 120, source: "snmp:core", confidence: "observed", valid_from: "2026-10-01T00:00:00Z", first_seen: "2026-10-01T00:00:00Z", last_seen: "2026-10-12T06:25:00Z" },
    // rack_unit has no entry on purpose: the page must say so.
  ],
};

export const relationsSwA1: Page<RelationView> = page([
  {
    id: 1,
    type: "mounted_in",
    from_id: ID.swA1,
    to_id: ID.rack,
    source: "declared:lab-file",
    confidence: "declared",
    valid_from: "2026-03-01T08:00:00Z",
    direction: "out",
    other: { id: ID.rack, type: "rack", display: "r12" },
  },
  {
    id: 2,
    type: "connected_to",
    from_id: ID.srv01,
    to_id: ID.swA1,
    source: "lldp:core",
    confidence: "observed",
    valid_from: "2026-03-02T09:00:00Z",
    direction: "in",
    other: { id: ID.srv01, type: "device", display: "srv-01" },
  },
]);

export const timelineSwA1: Page<Change> = page([
  { at: "2026-09-20T02:00:00Z", object_id: ID.swA1, type: "device", display: "sw-a1", key: "os_version", before: "4.27.0M", after: "4.28.3M", source: "arista:eos", changeset_id: 41 },
  { at: "2026-03-02T09:00:00Z", object_id: ID.swA1, type: "device", display: "sw-a1", relation: "connected_to", other_id: ID.srv01, after: {}, source: "lldp:core", changeset_id: 7 },
  { at: "2026-03-01T08:00:00Z", object_id: ID.swA1, type: "device", display: "sw-a1", key: "hostname", after: "sw-a1", source: "arista:eos", changeset_id: 1 },
]);

export const searchSw: SearchPage = {
  summary: { query: "sw", total: 3, by_type: { device: 2, rack: 1 }, fuzzy: false, took_ms: 4 },
  total: 3,
  limit: 100,
  offset: 0,
  items: [swA1, { ...swA1, id: "0a000000-0000-4000-8000-0000000000a2", display: "sw-a2" }, rackR12],
};

const component = (n: number, type: string, display: string, attributes: Record<string, unknown>): ObjectView => ({
  id: `0a000000-0000-4000-8000-0000000c${n.toString(16).padStart(4, "0")}`,
  type,
  display,
  parent_id: ID.srv01,
  status: "active",
  attributes: { name: display, ...attributes },
  created_at: "2026-03-02T08:00:00Z",
  observed: true,
});

/** What srv-01 contains: two power supplies (one has failed) and a disk. */
export const componentsSrv01: ObjectView[] = [
  component(1, "psu", "PSU 1", { capacity_w: 800, input_w: 210.4, health: "ok" }),
  component(2, "psu", "PSU 2", { capacity_w: 800, input_w: 0, health: "critical" }),
  component(3, "disk", "nvme0n1", { capacity_bytes: 960_197_124_096, temperature_c: 31, health: "ok" }),
];

/** Where the devices are mounted (GET /relations?type=mounted_in). */
export const mountsLinks: Page<RelationLink> = page([
  {
    id: 1,
    type: "mounted_in",
    from_id: ID.swA1,
    to_id: ID.rack,
    from: { id: ID.swA1, type: "device", display: "sw-a1" },
    to: { id: ID.rack, type: "rack", display: "r12" },
    source: "declared:lab-file",
    confidence: "declared",
    valid_from: "2026-03-01T08:00:00Z",
    attributes: { position: 40, face: "front" },
  },
  {
    id: 2,
    type: "mounted_in",
    from_id: ID.srv01,
    to_id: ID.rack,
    from: { id: ID.srv01, type: "device", display: "srv-01" },
    to: { id: ID.rack, type: "rack", display: "r12" },
    source: "declared:lab-file",
    confidence: "declared",
    valid_from: "2026-03-01T08:00:00Z",
    attributes: { position: 10, face: "front" },
  },
]);

export const proposalPending: Proposal = {
  id: 12,
  created_at: "2026-10-12T07:00:00Z",
  source: "mcp:assistant",
  author: "agent-1",
  reason: "Record the rack unit of sw-a1",
  batch: null,
  status: "pending",
};

export const proposalApplied: Proposal = {
  id: 11,
  created_at: "2026-10-10T07:00:00Z",
  source: "mcp:assistant",
  author: "agent-1",
  reason: "Fix the hostname of srv-01",
  batch: null,
  status: "applied",
  decided_at: "2026-10-10T09:00:00Z",
  decided_by: "ops-1",
  changeset_id: 52,
};

export const proposalPendingDetail: Proposal = {
  ...proposalPending,
  preview: {
    created: 0,
    objects: {},
    relations_closed: 0,
    relations_opened: 0,
    values_changed: 1,
    changes: [{ op: "set_attr", object_id: ID.swA1, type: "device", key: "rack_unit", after: "U12" }],
  },
};

export const discrepancyMismatch: Discrepancy = {
  id: 5,
  kind: "value_mismatch",
  fingerprint: "fp-5",
  object_id: ID.swA1,
  key: "os_version",
  declared: "4.27.0M",
  observed: "4.28.3M",
  status: "open",
  opened_at: "2026-09-20T02:00:00Z",
  last_seen: "2026-10-12T06:30:00Z",
};

export const healthOk: SourceHealth = {
  source: "arista:eos",
  collector: "arista",
  target: "192.0.2.10",
  version: "0.3.0",
  ok: true,
  last_attempt: "2026-10-12T06:30:00Z",
  last_success: "2026-10-12T06:30:00Z",
  duration_ms: 840,
  items: 124,
  warnings: [],
  failures: 0,
};

export const healthFailing: SourceHealth = {
  source: "snmp:core",
  collector: "snmp",
  target: "198.51.100.1",
  ok: false,
  last_attempt: "2026-10-12T06:25:00Z",
  last_success: "2026-10-11T06:25:00Z",
  last_error: "timeout after 30s",
  duration_ms: 30000,
  items: 0,
  warnings: ["no ifAlias on 3 interfaces"],
  failures: 4,
};

/**
 * A measurement series over the range a call asks for, in 120 buckets: three
 * readings, a gap of two buckets, then a fourth. Power is around 400 W, the
 * temperatures around 25 °C.
 */
export function seriesOf(objectId: string, query: URLSearchParams): Series {
  const key = query.get("key") ?? "";
  const to = query.get("to") ?? "2026-10-12T06:00:00Z";
  const from = query.get("from") ?? new Date(Date.parse(to) - 86_400_000).toISOString();
  const n = Number(query.get("points") ?? "120");
  const step = Math.ceil((Date.parse(to) - Date.parse(from)) / n / 1000);
  const base = key === "power_w" ? 400 : 25;
  const at = (i: number) => new Date(Date.parse(from) + i * step * 1000).toISOString();
  const pt = (i: number, d: number) => ({ t: at(i), avg: base + d, min: base + d - 10, max: base + d + 10 });
  return {
    object_id: objectId,
    key,
    unit: key === "power_w" ? "W" : "°C",
    from,
    to,
    step_seconds: step,
    points: [pt(0, 0), pt(1, 10), pt(2, 20), pt(5, 5)],
    truncated: false,
  };
}

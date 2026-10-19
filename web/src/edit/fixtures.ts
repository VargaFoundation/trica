// The physical part of the schema as the server publishes it (GET /schema),
// copied from internal/schema/builtin.yaml for the edit tests: the forms read
// kinds, enum values, identifiers and parents from here, not from the code.
// Names and ids are invented (paris-1, r12, pdu-a).
import type { ApplyResult, AttributeDoc, ObjectView, RackItem, RelationView, SchemaDoc, TypeDoc } from "../api/types";
import { page } from "../test/fixtures";

const attr = (name: string, kind: string, en: string, fr: string, extra: Partial<AttributeDoc> = {}): AttributeDoc => ({
  name,
  kind,
  label: { en, fr },
  description: "",
  search: false,
  required: false,
  custom: false,
  ...extra,
});

const type = (name: string, en: string, fr: string, parents: string[], strong: string[], weak: string[], attributes: AttributeDoc[]): TypeDoc => ({
  name,
  layer: "location",
  label: { en, fr },
  description: "",
  display: "name",
  parents,
  custom: false,
  identity: { strong, weak },
  attributes,
});

const name = (en = "Name") => attr("name", "string", en, "Nom", { identity: "weak", required: true, search: true });

export const physicalSchema: SchemaDoc = {
  types: [
    type("site", "Site", "Site", [], ["code"], ["name"], [name(), attr("code", "string", "Code", "Code", { identity: "strong" }), attr("address", "text", "Address", "Adresse"), attr("timezone", "string", "Time zone", "Fuseau horaire")]),
    type("building", "Building", "Bâtiment", ["site"], [], ["name"], [name()]),
    type("room", "Room", "Salle", ["site", "building"], [], ["name"], [
      name(),
      attr("width_mm", "int", "Width", "Largeur", { unit: "mm" }),
      attr("depth_mm", "int", "Depth", "Profondeur", { unit: "mm" }),
      attr("tile_mm", "int", "Floor tile", "Carreau", { unit: "mm" }),
    ]),
    type("row", "Row", "Rangée", ["room"], [], ["name"], [name()]),
    type("rack", "Rack", "Baie", ["site", "room", "row"], ["serial", "asset_tag"], ["name"], [
      name(),
      attr("serial", "string", "Serial number", "Numéro de série", { identity: "strong" }),
      attr("asset_tag", "string", "Asset tag", "Étiquette d'inventaire", { identity: "strong" }),
      attr("u_height", "int", "Height", "Hauteur", { unit: "U" }),
      attr("depth_mm", "int", "Depth", "Profondeur", { unit: "mm" }),
      attr("width_mm", "int", "Width", "Largeur", { unit: "mm" }),
      attr("facing", "enum", "Front faces", "Face avant vers", { values: ["north", "east", "south", "west"] }),
      attr("front_aisle", "enum", "Front aisle", "Couloir côté avant", { values: ["cold", "hot"] }),
      attr("floor_x", "int", "Floor X", "Position X"),
      attr("floor_y", "int", "Floor Y", "Position Y"),
      attr("lifecycle", "enum", "Lifecycle", "Cycle de vie", { values: ["planned", "active", "decommissioned"] }),
    ]),
    type("device_type", "Device model", "Modèle d'équipement", [], ["slug"], ["model"], [attr("u_height", "float", "Height", "Hauteur", { unit: "U" }), attr("full_depth", "bool", "Full depth", "Pleine profondeur")]),
    type("device", "Device", "Équipement", [], ["serial", "asset_tag"], ["name"], [
      attr("name", "string", "Name", "Nom", { identity: "weak", search: true }),
      attr("serial", "string", "Serial number", "Numéro de série", { identity: "strong" }),
      attr("asset_tag", "string", "Asset tag", "Étiquette d'inventaire", { identity: "strong" }),
      attr("role", "enum", "Role", "Rôle", { values: ["server", "switch", "router", "firewall", "storage", "pdu", "ups", "patch_panel", "sensor", "nas", "kvm", "other"] }),
      attr("vendor", "string", "Vendor", "Constructeur"),
      attr("model", "string", "Model", "Modèle"),
      attr("part_number", "string", "Part number", "Référence"),
      attr("mgmt_ip", "ip", "Management IP", "IP d'administration"),
      attr("description", "text", "Description", "Description"),
      attr("u_height", "float", "Height", "Hauteur", { unit: "U" }),
    ]),
  ],
  relations: [
    {
      name: "mounted_in",
      label: { en: "is mounted in", fr: "est monté dans" },
      inverse: { en: "holds", fr: "accueille" },
      description: "",
      from: ["device"],
      to: ["rack"],
      attributes: [
        attr("position", "float", "Position", "Position", { unit: "U" }),
        attr("face", "enum", "Face", "Face", { values: ["front", "rear"] }),
      ],
      custom: false,
    },
    {
      name: "instance_of",
      label: { en: "is a", fr: "est un" },
      inverse: { en: "has instances", fr: "a pour instances" },
      description: "",
      from: ["device"],
      to: ["device_type"],
      attributes: [],
      custom: false,
    },
  ],
};

/** The same schema with whole-unit positions (an overlay that makes position an int). */
export const wholeUnitSchema: SchemaDoc = {
  ...physicalSchema,
  relations: physicalSchema.relations.map((r) =>
    r.name === "mounted_in" ? { ...r, attributes: r.attributes.map((a) => (a.name === "position" ? { ...a, kind: "int" } : a)) } : r,
  ),
};

export const EID = {
  site: "0e000000-0000-4000-8000-000000000001",
  room: "0e000000-0000-4000-8000-000000000010",
  rack: "0e000000-0000-4000-8000-000000000020",
  rack2: "0e000000-0000-4000-8000-000000000021",
  sw: "0e000000-0000-4000-8000-0000000000a1",
  srv: "0e000000-0000-4000-8000-0000000000b1",
  pdu: "0e000000-0000-4000-8000-0000000000d1",
  model1u: "0e000000-0000-4000-8000-0000000000c1",
};

export const obj = (id: string, t: string, display: string, attributes: Record<string, unknown> = {}, extra: Partial<ObjectView> = {}): ObjectView => ({
  id,
  type: t,
  display,
  status: "active",
  attributes,
  created_at: "2026-03-01T08:00:00Z",
  observed: false,
  ...extra,
});

export const site = obj(EID.site, "site", "paris-1", { name: "paris-1" });
export const room = obj(EID.room, "room", "salle-1", { name: "salle-1", width_mm: 3000, depth_mm: 1800 }, { parent_id: EID.site, site_id: EID.site });
/** A 12 U rack on tile (1, 1) of salle-1. */
export const rack = obj(EID.rack, "rack", "r12", { name: "r12", u_height: 12, floor_x: 1, floor_y: 1, facing: "south" }, { parent_id: EID.room, site_id: EID.site });
export const rack2 = obj(EID.rack2, "rack", "r13", { name: "r13", u_height: 42, floor_x: 3, floor_y: 1 }, { parent_id: EID.room, site_id: EID.site });
export const sw = obj(EID.sw, "device", "sw-a1", { name: "sw-a1", role: "switch" }, { observed: true });
export const srv = obj(EID.srv, "device", "srv-01", { name: "srv-01", role: "server" }, { observed: true });
export const pdu = obj(EID.pdu, "device", "pdu-a", { name: "pdu-a", role: "pdu", u_height: 1 });
export const model1u = obj(EID.model1u, "device_type", "ex-1u", { slug: "ex-1u", u_height: 1, full_depth: true });

/** r12 holds srv-01 at U 10-11 on the front (2 U model) and pdu-a at U 1 on the rear. */
export const rackContents = page<RackItem>([
  { device: { id: EID.srv, type: "device", display: "srv-01" }, position: 10, face: "front", u_height: 2, full_depth: false },
  { device: { id: EID.pdu, type: "device", display: "pdu-a" }, position: 1, face: "rear", u_height: 1, full_depth: false },
]);

/** sw-a1: mounted in r13 by a collector, a 1 U full-depth model. */
export const swRelations = page<RelationView>([
  {
    id: 1,
    type: "mounted_in",
    from_id: EID.sw,
    to_id: EID.rack2,
    source: "arista:sw-a1",
    confidence: "observed",
    valid_from: "2026-03-01T08:00:00Z",
    direction: "out",
    attributes: { position: 30, face: "front" },
    other: { id: EID.rack2, type: "rack", display: "r13" },
  },
  {
    id: 2,
    type: "instance_of",
    from_id: EID.sw,
    to_id: EID.model1u,
    source: "deduce:devicetypes",
    confidence: "deduced",
    valid_from: "2026-03-01T08:00:00Z",
    direction: "out",
    other: { id: EID.model1u, type: "device_type", display: "ex-1u" },
  },
]);

/** pdu-a: mounted in r12 by the signed-in person (ui-tests is the token's source). */
export const pduRelations = page<RelationView>([
  {
    id: 3,
    type: "mounted_in",
    from_id: EID.pdu,
    to_id: EID.rack,
    source: "ui-tests",
    confidence: "declared",
    valid_from: "2026-03-01T08:00:00Z",
    direction: "out",
    attributes: { position: 1, face: "rear" },
    other: { id: EID.rack, type: "rack", display: "r12" },
  },
]);

export const roomContentsRacks = page<ObjectView>([rack, rack2]);

/** A preview as the engine answers one: a new device created and mounted. */
export const previewNewDevice: ApplyResult = {
  created: 1,
  values_changed: 3,
  relations_opened: 1,
  relations_closed: 0,
  objects: { device: "0e000000-0000-4000-8000-0000000000f1" },
  changes: [
    { op: "create", object_id: "0e000000-0000-4000-8000-0000000000f1", type: "device" },
    { op: "set", object_id: "0e000000-0000-4000-8000-0000000000f1", type: "device", key: "_status", after: "active" },
    { op: "set", object_id: "0e000000-0000-4000-8000-0000000000f1", type: "device", key: "role", after: "pdu" },
    { op: "relate", object_id: "0e000000-0000-4000-8000-0000000000f1", type: "device", key: "mounted_in", other_id: EID.rack, after: { position: 5, face: "front" } },
  ],
};

export const emptyResult: ApplyResult = { created: 0, values_changed: 0, relations_opened: 0, relations_closed: 0, objects: {} };

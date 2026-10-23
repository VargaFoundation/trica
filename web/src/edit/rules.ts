// What the schema registry (GET /schema) says about the physical layout, read
// once for the forms. Nothing here restates the schema: a face, a role or a
// facing the registry does not list is not offered, an attribute it does not
// have is not asked for, and half units are offered when mounted_in.position
// is a float (internal/schema/builtin.yaml).
import type { AttributeDoc, SchemaDoc, TypeDoc } from "../api/types";

export interface EditRules {
  /** mounted_in.face values, in schema order. */
  faces: string[];
  /** mounted_in.position takes half units. */
  halfUnits: boolean;
  /** The schema has mounted_in from device to rack at all. */
  canMount: boolean;
  /** The types located_in leads to (site, building, room), as the schema lists them: where a device in no rack may be placed. Empty when the schema has no such relation. */
  locationTypes: string[];
  /** device.role values. */
  roles: string[];
  /** The device type has its own u_height (for devices whose model is unknown). */
  deviceHeight: boolean;
  type: (name: string) => TypeDoc | undefined;
  attribute: (type: string, key: string) => AttributeDoc | undefined;
  /** Whether key is an identifier of type (strong or weak). */
  isIdentity: (type: string, key: string) => boolean;
}

export function editRules(doc: SchemaDoc | undefined): EditRules {
  const types = new Map((doc?.types ?? []).map((t) => [t.name, t]));
  const mounted = (doc?.relations ?? []).find((r) => r.name === "mounted_in");
  const located = (doc?.relations ?? []).find((r) => r.name === "located_in");
  const relAttr = (key: string) => mounted?.attributes.find((a) => a.name === key);
  const attribute = (type: string, key: string) => types.get(type)?.attributes.find((a) => a.name === key);
  return {
    faces: relAttr("face")?.values ?? [],
    halfUnits: relAttr("position")?.kind === "float",
    canMount: !!mounted && mounted.from.includes("device") && mounted.to.includes("rack") && !!relAttr("position"),
    locationTypes: located && located.from.includes("device") ? located.to.filter((t) => types.has(t)) : [],
    roles: attribute("device", "role")?.values ?? [],
    deviceHeight: !!attribute("device", "u_height"),
    type: (name) => types.get(name),
    attribute,
    isIdentity: (type, key) => {
      const a = attribute(type, key);
      if (a?.identity) return true;
      const id = types.get(type)?.identity;
      return !!id && ((id.strong ?? []).includes(key) || (id.weak ?? []).includes(key));
    },
  };
}

/**
 * The attributes a person declares on each physical container, in the order of
 * the form. Only those the schema has are shown; the labels, kinds, units and
 * enum values come from the schema.
 */
export const CONTAINER_FIELDS: Record<string, readonly string[]> = {
  site: ["name", "code", "address", "timezone"],
  building: ["name"],
  room: ["name", "width_mm", "depth_mm", "tile_mm"],
  row: ["name"],
  rack: ["name", "u_height", "floor_x", "floor_y", "facing", "depth_mm", "width_mm", "serial", "asset_tag", "lifecycle"],
};

/** What a person says about a device no collector sees (a PDU, a UPS, a patch panel, a shelf). */
export const NEW_DEVICE_FIELDS: readonly string[] = ["name", "role", "vendor", "model", "part_number", "serial", "asset_tag", "description"];

export const CONTAINER_TYPES = Object.keys(CONTAINER_FIELDS);

// Batches the UI declares. A person cannot write the inventory: the page builds
// an sdk.Batch (pkg/sdk) and hands it to POST /batches, where the reconcile
// engine applies it or queues it as a proposal (ADR 0005, ADR 0039). Kept free
// of React so that the shape sent to the server is tested on its own.
//
// Every batch is partial: a person adds or withdraws claims, never says "this
// is everything I know" (a full batch would withdraw every other claim the
// person made). Source and confidence are the server's: it overwrites the
// source with "user:<subject>" (or the token's source) and forces "declared".
import type { Batch, BatchEndpoint, BatchObject, BatchRelation } from "../api/types";

export type Attrs = Record<string, unknown>;

/** An existing object, designated by its trica id (sdk.IDKey). */
export const byId = (type: string, id: string): BatchEndpoint => ({ type, identity: { id } });
export const byRef = (ref: string): BatchEndpoint => ({ ref });

export function emptyBatch(now: Date = new Date()): Batch {
  return { source: "ui", scope: "partial", observed_at: now.toISOString(), confidence: "declared", objects: [], relations: [] };
}

/** Drops empty values: an empty field is "not said", never "said empty" (sdk Object.Set). */
export function cleanAttrs(attrs: Attrs): Attrs {
  const out: Attrs = {};
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === null || v === "" || (typeof v === "number" && !Number.isFinite(v))) continue;
    out[k] = v;
  }
  return out;
}

const objectOf = (ref: string, type: string, identity: Record<string, string>, attributes: Attrs, parent?: string): BatchObject => {
  const o: BatchObject = { ref, type, identity };
  const a = cleanAttrs(attributes);
  if (Object.keys(a).length > 0) o.attributes = a;
  if (parent) o.parent = parent;
  return o;
};

export interface NewObject {
  type: string;
  /** Identifiers (name, serial, code…): what the engine recognises the object by. */
  identity: Record<string, string>;
  attributes: Attrs;
  /** The container, an existing object: weak identifiers (a name) are unique inside it. */
  parent?: { type: string; id: string };
}

/** Creates an object, or, when its identifiers match one already there, completes that one (the preview says which). */
export function createObject(o: NewObject, now?: Date): Batch {
  const b = emptyBatch(now);
  const identity = Object.fromEntries(Object.entries(o.identity).filter(([, v]) => v.trim() !== "").map(([k, v]) => [k, v.trim()]));
  const objects: BatchObject[] = [];
  if (o.parent) objects.push(objectOf("parent", o.parent.type, { id: o.parent.id }, {}));
  objects.push(objectOf("new", o.type, identity, o.attributes, o.parent ? "parent" : undefined));
  b.objects = objects;
  return b;
}

/** Sets attributes of an existing object: a rename is a new name. Withdrawn keys drop this caller's claim on them. */
export function updateObject(type: string, id: string, attributes: Attrs, withdraw: string[] = [], now?: Date): Batch {
  const b = emptyBatch(now);
  const o = objectOf("obj", type, { id }, attributes);
  if (withdraw.length > 0) o.withdraw = [...withdraw];
  b.objects = [o];
  return b;
}

export interface Placement {
  rackId: string;
  /** Lowest U occupied, 1 at the bottom. */
  position: number;
  face?: string;
}

export type DeviceChoice = { id: string } | { create: { identity: Record<string, string>; attributes: Attrs } };

export interface PlaceDevice {
  device: DeviceChoice;
  placement: Placement;
  /** The device's own height, said when its model does not say (device.u_height). */
  heightU?: number;
  /** Racks the device is mounted in now and leaves: this caller's claim on each is withdrawn. */
  leave?: string[];
}

/** Mounts a device (existing or declared here) in a rack at a U and a face. */
export function placeDevice(p: PlaceDevice, now?: Date): Batch {
  const b = emptyBatch(now);
  const objects: BatchObject[] = [];
  let from: BatchEndpoint;
  if ("create" in p.device) {
    const identity = Object.fromEntries(Object.entries(p.device.create.identity).filter(([, v]) => v.trim() !== "").map(([k, v]) => [k, v.trim()]));
    objects.push(objectOf("device", "device", identity, { ...p.device.create.attributes, u_height: p.heightU }));
    from = byRef("device");
  } else if (p.heightU !== undefined) {
    objects.push(objectOf("device", "device", { id: p.device.id }, { u_height: p.heightU }));
    from = byRef("device");
  } else {
    from = byId("device", p.device.id);
  }
  b.objects = objects;
  const mount: BatchRelation = {
    type: "mounted_in",
    from,
    to: byId("rack", p.placement.rackId),
    attributes: cleanAttrs({ position: p.placement.position, face: p.placement.face }),
  };
  b.relations = [mount];
  const leave = (p.leave ?? []).filter((r) => r !== p.placement.rackId);
  if (leave.length > 0) b.unrelations = leave.map((rackId) => ({ type: "mounted_in", from: { ...from }, to: byId("rack", rackId) }));
  return b;
}

/** Takes a device out of a rack: withdraws this caller's claim that it is mounted there. */
export function unplaceDevice(deviceId: string, rackId: string, now?: Date): Batch {
  const b = emptyBatch(now);
  b.unrelations = [{ type: "mounted_in", from: byId("device", deviceId), to: byId("rack", rackId) }];
  return b;
}

export interface ChooseModel {
  deviceId: string;
  /** The library slug of the chosen model: the engine recognises the device_type by it, or creates it bare (the library describes it). */
  slug: string;
  /** Models (device_type ids) this caller declared before and replaces: this caller's claim on each is withdrawn. */
  withdraw?: string[];
}

/** Declares which model (chassis variant) a device is: a declared instance_of, which wins over the library's match by name (ADR 0048). */
export function chooseModel(c: ChooseModel, now?: Date): Batch {
  const b = emptyBatch(now);
  b.objects = [objectOf("model", "device_type", { slug: c.slug }, {})];
  b.relations = [{ type: "instance_of", from: byId("device", c.deviceId), to: byRef("model") }];
  const withdraw = (c.withdraw ?? []).filter((id) => id !== "");
  if (withdraw.length > 0) b.unrelations = withdraw.map((id) => ({ type: "instance_of", from: byId("device", c.deviceId), to: byId("device_type", id) }));
  return b;
}

/** The source a batch from this caller is attributed to (internal/api Principal.BatchSource). */
export function callerSource(me: { kind: string; subject: string; source?: string } | undefined): string | undefined {
  if (!me) return undefined;
  return me.kind === "user" ? `user:${me.subject}` : me.source || undefined;
}

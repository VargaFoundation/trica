import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { callerSource, chooseModel, cleanAttrs, createObject, declareFrontAisle, locateDevice, placeDevice, unlocateDevice, unplaceDevice, updateObject } from "./batch";

const AT = new Date("2026-03-01T08:00:00Z");

/**
 * The batches the forms build, in one file the Go API tests replay against the
 * real engine (internal/api/ui_batches_test.go): ids are "@name" placeholders
 * the Go side replaces. A builder that changes shape fails here until the file
 * is regenerated (TRICA_UPDATE_UI_BATCHES=1 npx vitest run src/edit), and the
 * Go test then says whether the engine still accepts it.
 */
function uiBatches() {
  return {
    createRoom: createObject(
      { type: "room", identity: { name: "salle-2" }, attributes: { width_mm: 6000, depth_mm: 3600, tile_mm: 600 }, parent: { type: "site", id: "@paris" } },
      AT,
    ),
    createRack: createObject(
      { type: "rack", identity: { name: "r14" }, attributes: { u_height: 42, floor_x: 2, floor_y: 1, facing: "south" }, parent: { type: "room", id: "@room" } },
      AT,
    ),
    placeNewPdu: placeDevice(
      {
        device: { create: { identity: { name: "pdu-r14-a" }, attributes: { role: "pdu", vendor: "Example" } } },
        placement: { rackId: "@rack", position: 1.5, face: "rear" },
        heightU: 1,
      },
      AT,
    ),
    moveWithin: placeDevice({ device: { id: "@pdu" }, placement: { rackId: "@rack", position: 7, face: "rear" } }, AT),
    newNamedLikeExisting: placeDevice(
      { device: { create: { identity: { name: "sw-a1" }, attributes: { role: "switch" } } }, placement: { rackId: "@rack", position: 20, face: "front" } },
      AT,
    ),
    moveObserved: placeDevice({ device: { id: "@sw" }, placement: { rackId: "@rack", position: 40, face: "front" }, leave: ["@rack-p"] }, AT),
    moveOwn: placeDevice({ device: { id: "@pdu" }, placement: { rackId: "@rack-p", position: 3, face: "front" }, heightU: 2, leave: ["@rack"] }, AT),
    unplace: unplaceDevice("@pdu", "@rack-p", AT),
    renameRack: updateObject("rack", "@rack", { name: "r14b", u_height: 48 }, [], AT),
    badFace: placeDevice({ device: { id: "@pdu" }, placement: { rackId: "@rack", position: 5, face: "side" } }, AT),
    chooseVariant: chooseModel({ deviceId: "@srv-v", slug: "acme-srv2-8sff" }, AT),
    changeVariant: chooseModel({ deviceId: "@srv-v", slug: "acme-srv2-12lff", withdraw: ["@model-8sff"] }, AT),
    frontAisleCold: declareFrontAisle("@rack", "cold", AT),
    frontAisleHot: declareFrontAisle("@rack", "hot", AT),
    frontAisleUnset: declareFrontAisle("@rack", null, AT),
    locateApInParisRoom: locateDevice("@ap", { type: "room", id: "@room-p" }, AT),
    locateApInLyonRoom: locateDevice("@ap", { type: "room", id: "@room-l" }, AT),
    locateApInSite: locateDevice("@ap", { type: "site", id: "@paris" }, AT),
    unlocateApInLyon: unlocateDevice("@ap", { type: "room", id: "@room-l" }, AT),
  };
}

const FILE = resolve(import.meta.dirname, "testdata/ui-batches.json");

describe("the batches the forms send", () => {
  it("are the ones the Go API tests replay (testdata/ui-batches.json)", () => {
    const text = `${JSON.stringify(uiBatches(), null, 2)}\n`;
    if (process.env.TRICA_UPDATE_UI_BATCHES === "1") writeFileSync(FILE, text);
    expect(readFileSync(FILE, "utf8")).toBe(text);
  });

  it("are partial and declared: a person never withdraws by omission", () => {
    for (const b of Object.values(uiBatches())) {
      expect(b.scope).toBe("partial");
      expect(b.confidence).toBe("declared");
    }
  });

  it("racking a device never touches its located_in: a rack wins in the engine, the place stays in the history (ADR 0058)", () => {
    const b = uiBatches();
    for (const batch of [b.placeNewPdu, b.moveWithin, b.moveObserved, b.moveOwn, b.unplace]) {
      for (const r of [...(batch.relations ?? []), ...(batch.unrelations ?? [])]) expect(r.type).toBe("mounted_in");
    }
  });

  it("puts a new container in its parent, designated by id, with only its identifiers as identity", () => {
    const b = uiBatches().createRack;
    expect(b.objects).toEqual([
      { ref: "parent", type: "room", identity: { id: "@room" } },
      { ref: "new", type: "rack", identity: { name: "r14" }, attributes: { u_height: 42, floor_x: 2, floor_y: 1, facing: "south" }, parent: "parent" },
    ]);
  });

  it("says the rack's front aisle on the rack by id, and withdraws only this caller's claim for \"not set\"", () => {
    expect(declareFrontAisle("r1", "cold", AT).objects).toEqual([{ ref: "obj", type: "rack", identity: { id: "r1" }, attributes: { front_aisle: "cold" } }]);
    expect(declareFrontAisle("r1", null, AT).objects).toEqual([{ ref: "obj", type: "rack", identity: { id: "r1" }, withdraw: ["front_aisle"] }]);
  });

  it("mounts an existing device by id, with no object when nothing about the device changes", () => {
    const b = placeDevice({ device: { id: "d1" }, placement: { rackId: "r1", position: 12, face: "front" } }, AT);
    expect(b.objects).toEqual([]);
    expect(b.relations).toEqual([{ type: "mounted_in", from: { type: "device", identity: { id: "d1" } }, to: { type: "rack", identity: { id: "r1" } }, attributes: { position: 12, face: "front" } }]);
    expect(b.unrelations).toBeUndefined();
  });

  it("says the device's own height when given, and withdraws only the racks it leaves", () => {
    const b = placeDevice({ device: { id: "d1" }, placement: { rackId: "r1", position: 12 }, heightU: 0.5, leave: ["r1", "r2"] }, AT);
    expect(b.objects).toEqual([{ ref: "device", type: "device", identity: { id: "d1" }, attributes: { u_height: 0.5 } }]);
    expect(b.relations?.[0]?.attributes).toEqual({ position: 12 });
    // Staying in r1 (a new U) is an update of the same relation, not a withdrawal.
    expect(b.unrelations).toEqual([{ type: "mounted_in", from: { ref: "device" }, to: { type: "rack", identity: { id: "r2" } } }]);
  });

  it("drops empty identifiers and attributes: an empty field is not a value", () => {
    const b = createObject({ type: "site", identity: { name: " lyon-2 ", code: "  " }, attributes: { address: "", timezone: undefined, x: Number.NaN } }, AT);
    expect(b.objects).toEqual([{ ref: "new", type: "site", identity: { name: "lyon-2" } }]);
    expect(cleanAttrs({ a: 0, b: false, c: null })).toEqual({ a: 0, b: false });
  });

  it("withdraws the attributes a person empties", () => {
    expect(updateObject("rack", "r1", {}, ["facing"], AT).objects).toEqual([{ ref: "obj", type: "rack", identity: { id: "r1" }, withdraw: ["facing"] }]);
  });

  it("declares a chosen model by its slug, and withdraws only the models it replaces", () => {
    const b = chooseModel({ deviceId: "d1", slug: "dell-poweredge-r330-8sff", withdraw: ["m0", ""] }, AT);
    expect(b.objects).toEqual([{ ref: "model", type: "device_type", identity: { slug: "dell-poweredge-r330-8sff" } }]);
    expect(b.relations).toEqual([{ type: "instance_of", from: { type: "device", identity: { id: "d1" } }, to: { ref: "model" } }]);
    expect(b.unrelations).toEqual([{ type: "instance_of", from: { type: "device", identity: { id: "d1" } }, to: { type: "device_type", identity: { id: "m0" } } }]);
    expect(chooseModel({ deviceId: "d1", slug: "x" }, AT).unrelations).toBeUndefined();
  });

  it("places a device by id in a room, with nothing else: the engine closes the caller's previous place", () => {
    const b = locateDevice("d1", { type: "room", id: "r1" }, AT);
    expect(b.objects).toEqual([]);
    expect(b.relations).toEqual([{ type: "located_in", from: { type: "device", identity: { id: "d1" } }, to: { type: "room", identity: { id: "r1" } }, attributes: {} }]);
    expect(b.unrelations).toBeUndefined();
  });

  it("withdraws only the caller's claim on the place it names", () => {
    const b = unlocateDevice("d1", { type: "building", id: "b1" }, AT);
    expect(b.relations).toEqual([]);
    expect(b.unrelations).toEqual([{ type: "located_in", from: { type: "device", identity: { id: "d1" } }, to: { type: "building", identity: { id: "b1" } } }]);
  });

  it("knows the source the server will attribute the batch to", () => {
    expect(callerSource({ kind: "user", subject: "carol" })).toBe("user:carol");
    expect(callerSource({ kind: "token", subject: "x", source: "mcp:assistant" })).toBe("mcp:assistant");
    expect(callerSource({ kind: "token", subject: "x" })).toBeUndefined();
    expect(callerSource(undefined)).toBeUndefined();
  });
});

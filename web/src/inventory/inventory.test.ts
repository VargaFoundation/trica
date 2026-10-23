import type { AttributeDoc, ObjectView, RelationLink } from "../api/types";
import { formatBytes, formatDuration, formatQuantity } from "./format";
import { countHealth, FOLD_AFTER, foldTiles, groupTiles, mountsOf, placesOf, powerTotal, type Tile } from "./Fleet";
import { healthOf } from "./health";
import { KNOWN_ROLES, kindOf } from "./kind";
import { en } from "../i18n/en";
import { fr } from "../i18n/fr";
import { columnsOf, groupOf, isHealthEnum } from "../pages/ObjectOverview";

const W = { d: "d", h: "h", min: "min", s: "s" };
const dev = (role: string | undefined, name = "x") => ({ type: "device", attributes: role === undefined ? { name } : { name, role } });

describe("what an object is", () => {
  it.each([
    ["server", "server", "compute"],
    ["switch", "switch", "network"],
    ["router", "router", "network"],
    ["firewall", "firewall", "security"],
    ["nas", "nas", "storage"],
    ["storage", "storage", "storage"],
    ["pdu", "pdu", "power"],
    ["ups", "ups", "power"],
    ["access_point", "router", "network"],
    ["access_control", "firewall", "security"],
    ["camera", "device", "security"],
  ])("a device with role %s is drawn as %s, in the %s family", (role, icon, family) => {
    expect(kindOf(dev(role))).toEqual({ key: role, role, icon, family });
  });

  it("tells a server, a switch, a NAS and a PDU apart: four icons, four words", () => {
    const kinds = ["server", "switch", "nas", "pdu"].map((r) => kindOf(dev(r)));
    expect(new Set(kinds.map((k) => k.icon)).size).toBe(4);
  });

  it("knows the roles a UniFi estate brings, with a word each in both languages", () => {
    for (const role of ["access_point", "access_control", "camera"]) {
      expect(kindOf(dev(role)).role).toBe(role);
      expect(KNOWN_ROLES).toContain(role);
      expect(en[`role.${role}` as keyof typeof en]).toBeTruthy();
      expect(fr[`role.${role}` as keyof typeof fr]).toBeTruthy();
    }
  });

  it("reads the role, never the name", () => {
    expect(kindOf(dev("server", "switch-01")).icon).toBe("server");
    expect(kindOf(dev(undefined, "fw-01")).icon).toBe("device");
  });

  it("keeps the word of a role it does not know, with the generic icon", () => {
    expect(kindOf(dev("spine"))).toEqual({ key: "spine", role: "spine", icon: "device", family: "other" });
  });

  it("draws a component by its type", () => {
    expect(kindOf({ type: "psu" }).icon).toBe("psu");
    expect(kindOf({ type: "fan" }).icon).toBe("fan");
    expect(kindOf({ type: "disk" }).icon).toBe("disk");
    expect(kindOf({ type: "memory" }).icon).toBe("memory");
    expect(kindOf({ type: "k8s_node" }).icon).toBe("k8s");
  });

  it("draws a custom type by its layer, and anything else as an object", () => {
    expect(kindOf({ type: "busbar" }, "energy")).toMatchObject({ icon: "psu", family: "power" });
    expect(kindOf({ type: "mystery" }).icon).toBe("object");
  });
});

describe("health", () => {
  it("is the health attribute, unknown when missing or not a health word, absent when the object is", () => {
    expect(healthOf({ status: "active", attributes: { health: "critical" } })).toBe("critical");
    expect(healthOf({ status: "active", attributes: {} })).toBe("unknown");
    expect(healthOf({ status: "active", attributes: { health: "degraded" } })).toBe("unknown");
    expect(healthOf({ status: "absent", attributes: { health: "ok" } })).toBe("absent");
  });
});

describe("quantities", () => {
  it("bytes in binary multiples", () => {
    expect(formatBytes(512, "en")).toBe("512 B");
    expect(formatBytes(1536, "en")).toBe("1.5 KiB");
    expect(formatBytes(274_877_906_944, "en")).toBe("256 GiB");
    expect(formatBytes(960_197_124_096, "fr")).toBe("894 GiB");
  });

  it("durations, the two largest parts", () => {
    expect(formatDuration(93_784, "en", W)).toBe("1 d 2 h");
    expect(formatDuration(3_660, "en", W)).toBe("1 h 1 min");
    expect(formatDuration(0, "en", W)).toBe("0 s");
    expect(formatDuration(1_303_200, "fr", { d: "j", h: "h", min: "min", s: "s" })).toBe("15 j 2 h");
  });

  it("formats by the schema's unit, in the reader's locale", () => {
    expect(formatQuantity(120, "W", "en", W)).toBe("120 W");
    expect(formatQuantity(12_500, "W", "en", W)).toBe("12.5 kW");
    expect(formatQuantity(23.25, "°C", "fr", W)).toBe("23,3 °C");
    expect(formatQuantity(25_000, "Mb/s", "en", W)).toBe("25 Gb/s");
    expect(formatQuantity(100, "Mb/s", "en", W)).toBe("100 Mb/s");
    expect(formatQuantity(6000, "mm", "en", W)).toBe("6,000 mm");
  });

  it("leaves a number without a unit and anything that is not a number alone", () => {
    expect(formatQuantity(1001, undefined, "en", W)).toBeUndefined();
    expect(formatQuantity("4.28.3M", "W", "en", W)).toBeUndefined();
  });
});

const obj = (id: string, display: string, attributes: Record<string, unknown> = {}): ObjectView => ({
  id,
  type: "device",
  display,
  status: "active",
  attributes,
  created_at: "2026-01-01T00:00:00Z",
  observed: true,
});
const tile = (o: ObjectView, health: Tile["health"], mount?: Tile["mount"], place?: Tile["place"]): Tile => ({ obj: o, kind: kindOf(o), kindLabel: String(o.attributes.role ?? "Device"), health, mount, place });

describe("the fleet", () => {
  it("says what its power total covers: devices measured, and supply output apart", () => {
    const total = powerTotal([
      tile(obj("s1", "s1", { role: "server", power_w: 150, power_basis: "input" }), "ok"),
      tile(obj("s2", "s2", { role: "server", power_w: 120 }), "ok"),
      tile(obj("w1", "w1", { role: "switch", power_w: 96.5, power_basis: "output" }), "ok"),
      tile(obj("w2", "w2", { role: "switch" }), "ok"),
      tile(obj("f1", "f1", { role: "firewall" }), "unknown"),
      tile(obj("p1", "p1", { role: "patch_panel" }), "unknown"),
      tile({ ...obj("old", "old", { role: "server", power_w: 300 }), status: "absent" }, "absent"),
    ]);
    // The absent server and the patch panel are not owed a reading.
    expect(total).toEqual({ watts: 366.5, measured: 3, of: 5, outputWatts: 96.5 });
  });

  it("knows where each device is mounted", () => {
    const link = {
      id: 1,
      type: "mounted_in",
      from_id: "d1",
      to_id: "r1",
      from: { id: "d1", type: "device", display: "d1" },
      to: { id: "r1", type: "rack", display: "rack 1" },
      source: "s",
      confidence: "declared",
      valid_from: "2026-01-01T00:00:00Z",
      attributes: { position: 12, face: "rear" },
    } as RelationLink;
    expect(mountsOf([link]).get("d1")).toEqual({ rackId: "r1", rack: "rack 1", position: 12, face: "rear" });
  });

  it("groups by rack, top of the rack first, devices in no rack last", () => {
    const groups = groupTiles(
      [
        tile(obj("a", "a"), "ok", { rackId: "r2", rack: "rack 10", position: 1 }),
        tile(obj("b", "b"), "ok", { rackId: "r1", rack: "rack 2", position: 3 }),
        tile(obj("c", "c"), "critical"),
        tile(obj("d", "d"), "ok", { rackId: "r1", rack: "rack 2", position: 40 }),
      ],
      "rack",
      "Not in a rack",
    );
    expect(groups.map((g) => g.title)).toEqual(["rack 2", "rack 10", "Not in a rack"]);
    expect(groups[0]?.tiles.map((x) => x.obj.id)).toEqual(["d", "b"]);
  });

  it("groups a device in no rack under the place it stands in, between the racks and the devices that are nowhere", () => {
    const groups = groupTiles(
      [
        tile(obj("nowhere", "nowhere"), "ok"),
        tile(obj("ap", "ap"), "ok", undefined, { id: "room1", name: "salle-1" }),
        tile(obj("sw", "sw"), "ok", { rackId: "r1", rack: "rack 1", position: 3 }),
        // A rack wins over a place: this device is drawn in its rack.
        tile(obj("both", "both"), "ok", { rackId: "r1", rack: "rack 1", position: 4 }, { id: "room1", name: "salle-1" }),
      ],
      "rack",
      "Not in a rack",
    );
    expect(groups.map((g) => g.title)).toEqual(["rack 1", "salle-1", "Not in a rack"]);
    expect(groups[0]?.tiles.map((x) => x.obj.id)).toEqual(["both", "sw"]);
    expect(groups[1]).toMatchObject({ placeId: "room1" });
    expect(groups[1]?.tiles.map((x) => x.obj.id)).toEqual(["ap"]);
  });

  it("knows where each device not in a rack stands, the first place said when sources disagree", () => {
    const link = (type: string, from: string, to: string, name: string) =>
      ({ id: 1, type, from_id: from, to_id: to, from: { id: from, type: "device", display: from }, to: { id: to, type: "room", display: name }, source: "s", confidence: "declared", valid_from: "2026-01-01T00:00:00Z" }) as RelationLink;
    const m = placesOf([link("mounted_in", "d0", "r0", "rack"), link("located_in", "d1", "room1", "salle-1"), link("located_in", "d1", "room2", "salle-2")]);
    expect([...m.keys()]).toEqual(["d1"]);
    expect(m.get("d1")).toEqual({ id: "room1", name: "salle-1" });
  });

  it("groups by kind, the group with the worst health first, the worst device first inside", () => {
    const groups = groupTiles(
      [
        tile(obj("s1", "s1", { role: "switch" }), "ok"),
        tile(obj("v1", "v1", { role: "server" }), "ok"),
        tile(obj("v2", "v2", { role: "server" }), "critical"),
      ],
      "kind",
      "",
    );
    expect(groups.map((g) => g.key)).toEqual(["server", "switch"]);
    expect(groups[0]?.tiles.map((x) => x.obj.id)).toEqual(["v2", "v1"]);
  });

  it("folds a large group to the devices that need attention, then healthy ones up to the limit", () => {
    const many = Array.from({ length: 30 }, (_, i) => tile(obj(`d${i}`, `d${i}`), i % 10 === 0 ? "critical" : "ok"));
    const shown = foldTiles(many);
    expect(shown).toHaveLength(FOLD_AFTER);
    expect(shown.filter((x) => x.health === "critical")).toHaveLength(3);
    const allBad = Array.from({ length: 20 }, (_, i) => tile(obj(`b${i}`, `b${i}`), "warning"));
    expect(foldTiles(allBad)).toHaveLength(20);
    expect(foldTiles(many.slice(0, 5))).toHaveLength(5);
  });

  it("counts health", () => {
    expect(countHealth([tile(obj("a", "a"), "ok"), tile(obj("b", "b"), "ok"), tile(obj("c", "c"), "warning")])).toEqual({ ok: 2, warning: 1, critical: 0, unknown: 0, absent: 0 });
  });
});

const doc = (name: string, kind: string, extra: Partial<AttributeDoc> = {}): AttributeDoc => ({ name, kind, label: { en: name, fr: name }, description: "", search: false, required: false, custom: false, ...extra });

describe("the object page's grouping", () => {
  it("groups attributes by what the schema says they are", () => {
    expect(groupOf(doc("serial", "string", { identity: "strong" }), "x")).toBe("identity");
    expect(groupOf(doc("mgmt_ip", "ip"), "x")).toBe("network");
    expect(groupOf(doc("power_w", "float", { unit: "W" }), 1)).toBe("measurements");
    expect(groupOf(doc("health", "enum"), "ok")).toBe("state");
    expect(groupOf(doc("warranty_end", "time"), "x")).toBe("dates");
    expect(groupOf(doc("provisioning_tags", "list"), [])).toBe("lists");
    expect(groupOf(doc("description", "text"), "x")).toBe("details");
    expect(groupOf(undefined, 3)).toBe("measurements");
  });

  it("shows an enum of health words as a health pill", () => {
    expect(isHealthEnum(doc("battery_health", "enum", { values: ["ok", "warning", "critical", "unknown", "absent"] }))).toBe(true);
    expect(isHealthEnum(doc("oper_status", "enum", { values: ["up", "down"] }))).toBe(false);
  });

  it("gives a component table the type's attributes in schema order, the ones present only, at most six", () => {
    const docs = ["name", "serial", "model", "capacity_w", "input_w", "health", "feed", "airflow", "firmware_version"].map((n) => doc(n, "string"));
    const items = [obj("p1", "PSU 1", { name: "PSU 1", model: "m", input_w: 1, health: "ok", capacity_w: 2, feed: "A", airflow: "x", firmware_version: "1", zzz: 1 })];
    expect(columnsOf(items, docs)).toEqual(["model", "capacity_w", "input_w", "feed", "airflow", "firmware_version"]);
  });
});

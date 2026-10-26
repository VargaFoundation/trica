import { describe, expect, it } from "vitest";
import type { MapEdge } from "../../api/types";
import { edgeUtilClass, linkState, MAX_LANES, MAX_PARTICLES, PARTICLE_SECONDS, particleCount, planFlows, speedClass, STATE_DASH } from "./flow";

const edge = (id: string, ab: number | undefined, ba: number | undefined, extra: Partial<MapEdge> = {}): MapEdge => ({
  id,
  a: "a",
  b: "b",
  medium: "physical",
  kinds: { cable: 0, cabled_to: 1, neighbor_of: 0, tunnel: 0, uplink: 0, uplinked_to: 0 },
  links: 1,
  confidence: "declared",
  capacity_partial: false,
  a_to_b_bps: ab,
  b_to_a_bps: ba,
  oper: "up",
  measured: true,
  members: [],
  members_truncated: false,
  ...extra,
});
const ON = { on: true, reduced: false, hidden: false };

describe("what a line says of its link", () => {
  it.each([
    ["up", "up", undefined],
    ["partial", "up", undefined],
    ["down", "down", "6 4"],
    ["unknown", "unknown", "1.5 5"],
    ["anything else", "unknown", "1.5 5"],
  ])("oper %s is %s, drawn with dashes %s", (oper, state, dash) => {
    expect(linkState({ oper })).toBe(state);
    expect(STATE_DASH[linkState({ oper })]).toBe(dash);
  });
});

describe("particles of a lane", () => {
  it("are one at least, more on a longer line and a busier one, never more than the cap", () => {
    expect(particleCount(5, 1)).toBe(1);
    expect(particleCount(300, 3)).toBeGreaterThan(particleCount(100, 3));
    expect(particleCount(300, 5)).toBeGreaterThan(particleCount(300, 2));
    expect(particleCount(1e5, 5)).toBe(MAX_PARTICLES);
  });
  it("cross the line faster as the rate grows", () => {
    const secs = ([1, 2, 3, 4, 5] as const).map((c) => PARTICLE_SECONDS[c]);
    expect([...secs].sort((a, b) => b - a)).toEqual(secs);
  });
});

describe("speed classes", () => {
  it.each([
    [0, 1],
    [999_999, 1],
    [1e6, 2],
    [5e7, 3],
    [5e8, 4],
    [1e9, 5],
    [4e11, 5],
  ])("%d b/s is class %d", (bps, cls) => expect(speedClass(bps)).toBe(cls));
});

describe("which lanes move", () => {
  it("runs one lane per direction that carries traffic", () => {
    const lanes = planFlows([edge("e1", 5e8, 2e6), edge("e2", 3e6, 0)], ON);
    expect(lanes.map((l) => `${l.edge}:${l.dir}:${l.cls}`)).toEqual(["e1:ab:4", "e2:ab:2", "e1:ba:2"]);
  });

  it("moves nothing when the switch is off, the system asks for less motion, or the page is hidden", () => {
    const e = [edge("e1", 5e8, 2e6)];
    expect(planFlows(e, { ...ON, on: false })).toEqual([]);
    expect(planFlows(e, { ...ON, reduced: true })).toEqual([]);
    expect(planFlows(e, { ...ON, hidden: true })).toEqual([]);
    expect(planFlows(e, ON)).toHaveLength(2);
  });

  it("leaves out what is not measured, down, or silent", () => {
    const lanes = planFlows([edge("u", 9e9, 9e9, { measured: false }), edge("d", 9e9, 9e9, { oper: "down" }), edge("z", 0, undefined)], ON);
    expect(lanes).toEqual([]);
  });

  it(`moves at most ${MAX_LANES} lanes, the busiest`, () => {
    const many = Array.from({ length: 400 }, (_, i) => edge(`e${String(i).padStart(3, "0")}`, (i + 1) * 1e6, undefined));
    const lanes = planFlows(many, ON);
    expect(lanes).toHaveLength(MAX_LANES);
    expect(lanes[0]?.edge).toBe("e399");
    expect(lanes.at(-1)?.edge).toBe("e250");
  });
});

describe("the colour class of a line", () => {
  it("reads the same thresholds as the network view", () => {
    expect(edgeUtilClass(edge("e", 1, 1, { util: 0.2 }))).toBe("normal");
    expect(edgeUtilClass(edge("e", 1, 1, { util: 0.5 }))).toBe("warn");
    expect(edgeUtilClass(edge("e", 1, 1, { util: 0.8 }))).toBe("crit");
    expect(edgeUtilClass(edge("e", 1, 1, { oper: "down" }))).toBe("down");
    expect(edgeUtilClass(edge("e", 1, 1, { oper: "partial", util: 0.1 }))).toBe("warn");
    expect(edgeUtilClass(edge("e", undefined, undefined, { measured: false }))).toBe("unknown");
  });
});

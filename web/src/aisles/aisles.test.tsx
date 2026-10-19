import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { AirflowCheck, Batch, DeviceAirflow, ObjectView, RackItem } from "../api/types";
import { physicalSchema } from "../edit/fixtures";
import { expectNoA11yViolations } from "../test/axe";
import { page, PROPOSER, READER } from "../test/fixtures";
import { defaultRoutes, mockApi, renderApp, type Call, type Handler } from "../test/support";
import { aisleOf, aisleTiles, conflictingRacks, faceAisle, neighbour, statusOf, summarize } from "./aisles";

describe("aisles, pure", () => {
  it("reads a rack's front aisle and the aisle of each face", () => {
    expect(aisleOf("cold")).toBe("cold");
    expect(aisleOf("lukewarm")).toBeUndefined();
    expect(faceAisle("cold", "front")).toBe("cold");
    expect(faceAisle("cold", "rear")).toBe("hot");
    expect(faceAisle("hot", "rear")).toBe("cold");
    expect(faceAisle(undefined, "front")).toBeUndefined();
  });

  it("counts the checks by status, the missing aisle apart", () => {
    const c = (status: string, reason: string): AirflowCheck => ({ status, reason });
    expect(summarize([c("ok", "intake_on_cold_aisle"), c("mismatch", "intake_on_hot_aisle"), c("unknown", "aisle_not_set"), undefined, c("weird", "x")])).toEqual({
      ok: 1,
      mismatch: 1,
      unknown: 3,
      noAisle: 1,
    });
    expect(statusOf(undefined)).toBe("unknown");
  });

  it("draws the aisles of a room from its racks, and flags the tile two racks disagree about", () => {
    expect(neighbour(2, 2, "north")).toEqual({ x: 2, y: 1 });
    expect(neighbour(2, 2, "west")).toEqual({ x: 1, y: 2 });
    // Two racks face each other across row 2: a shared cold aisle; their backs give two hot aisles.
    const racks = [
      { id: "a", name: "a", x: 1, y: 1, facing: "south" as const, clash: false, frontAisle: "cold" as const },
      { id: "b", name: "b", x: 1, y: 3, facing: "north" as const, clash: false, frontAisle: "cold" as const },
      // c says its front is on the hot aisle, d says the same tile is cold.
      { id: "c", name: "c", x: 2, y: 1, facing: "south" as const, clash: false, frontAisle: "hot" as const },
      { id: "d", name: "d", x: 2, y: 3, facing: "north" as const, clash: false, frontAisle: "cold" as const },
      // No facing, or no aisle: nothing drawn.
      { id: "e", name: "e", x: 4, y: 1, clash: false, frontAisle: "cold" as const },
      { id: "f", name: "f", x: 5, y: 1, facing: "east" as const, clash: false },
    ];
    const tiles = aisleTiles(racks);
    const at = (x: number, y: number) => tiles.find((t) => t.x === x && t.y === y);
    expect(at(1, 2)).toMatchObject({ kind: "cold", racks: ["a", "b"] });
    expect(at(1, 0)).toMatchObject({ kind: "hot", racks: ["a"] });
    expect(at(1, 4)).toMatchObject({ kind: "hot" });
    expect(at(2, 2)).toMatchObject({ kind: "conflict", racks: ["c", "d"] });
    expect(at(2, 0)).toMatchObject({ kind: "cold" });
    expect(tiles.some((t) => t.racks.includes("e") || t.racks.includes("f"))).toBe(false);
    expect([...conflictingRacks(tiles)].sort()).toEqual(["c", "d"]);
    // A tile with a rack on it is not an aisle, nor a tile outside the room.
    expect(aisleTiles([{ id: "g", name: "g", x: 0, y: 0, facing: "north", clash: false, frontAisle: "cold" }, { id: "h", name: "h", x: 0, y: 1, facing: "west", clash: false, frontAisle: "cold" }])).toEqual([
      { x: 1, y: 1, kind: "hot", racks: ["h"] },
    ]);
  });
});

// ------------------------------------------------------------------ the pages

const RACK = "0a000000-0000-4000-8000-0000000000a0";
const dev = (n: number) => `0a000000-0000-4000-8000-0000000002${String(n).padStart(2, "0")}`;
const rackObj = (attributes: Record<string, unknown>): ObjectView => ({
  id: RACK,
  type: "rack",
  display: "r30",
  status: "active",
  attributes: { name: "r30", u_height: 12, ...attributes },
  created_at: "2026-03-01T08:00:00Z",
  observed: false,
});

const item = (n: number, name: string, position: number, face: string, airflow: AirflowCheck): RackItem => ({
  device: { id: dev(n), type: "device", display: name },
  position,
  face,
  u_height: 1,
  full_depth: false,
  role: n === 1 ? "server" : "switch",
  airflow,
});

const items = (cold: boolean): RackItem[] => [
  item(1, "srv-30", 2, "front", cold ? { status: "ok", reason: "intake_on_cold_aisle", airflow: "front_to_rear", airflow_from: "model", intake: "front", cold_face: "front" } : { status: "unknown", reason: "aisle_not_set", intake: "front" }),
  item(2, "sw-30f", 10, "rear", cold ? { status: "mismatch", reason: "intake_on_hot_aisle", airflow: "front_to_rear", airflow_from: "model", intake: "rear", cold_face: "front" } : { status: "unknown", reason: "aisle_not_set", intake: "rear" }),
  item(3, "sw-30r", 11, "rear", cold ? { status: "ok", reason: "intake_on_cold_aisle", airflow: "rear_to_front", airflow_from: "model", intake: "front", cold_face: "front" } : { status: "unknown", reason: "aisle_not_set", intake: "front" }),
];

function rackRoutes(rack: ObjectView, extra: Record<string, Handler> = {}, me: Handler = PROPOSER): Record<string, Handler> {
  const cold = rack.attributes.front_aisle === "cold";
  return {
    ...defaultRoutes(me),
    "GET /schema": physicalSchema,
    [`GET /objects/${RACK}`]: rack,
    [`GET /objects/${RACK}/provenance`]: { object_id: RACK, attributes: cold ? [{ key: "front_aisle", value: "cold", source: "user:alice", confidence: "declared", valid_from: "2026-10-01T00:00:00Z" }] : [] },
    [`GET /racks/${RACK}/contents`]: page(items(cold)),
    "POST /batches/preview": (c: Call) => ({
      source: "ui-tests",
      result: { created: 0, values_changed: 1, relations_opened: 0, relations_closed: 0, objects: { obj: RACK }, changes: [{ op: "set_attr", object_id: RACK, type: "rack", key: "front_aisle", after: (c.body as Batch).objects?.[0]?.attributes?.front_aisle ?? null }] },
    }),
    ...extra,
  };
}

describe("rack page: aisles and airflow", () => {
  it("names the aisle of each face, draws the airflow, and says which devices take their air in from the hot aisle", async () => {
    mockApi(rackRoutes(rackObj({ front_aisle: "cold" })));
    const { container } = renderApp({ at: `/racks/${RACK}` });
    await screen.findByRole("heading", { level: 2, name: "Aisles and airflow" });
    expect(await screen.findByText("Front on the cold aisle, rear on the hot aisle.")).toBeInTheDocument();
    expect(await screen.findByText(/Said by user:alice/)).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /in from the cold aisle at the front, out to the hot aisle at the rear/ })).toBeInTheDocument();
    // Captions of the elevations.
    await waitFor(() => expect(container.querySelector('svg[data-face="rear"]')).not.toBeNull());
    const captions = [...container.querySelectorAll(".rack-face figcaption")].map((c) => c.textContent);
    expect(captions).toEqual(["FrontCold aisle", "RearHot aisle"]);
    // The summary, with the reversed switch named.
    const summary = await screen.findByText("1 device takes its air in from the hot aisle");
    const box = summary.closest(".alert") as HTMLElement;
    expect(within(box).getByRole("link", { name: "sw-30f" })).toBeInTheDocument();
    expect(screen.getByText("All 2 devices whose airflow is known take their air in from the cold aisle.")).toBeInTheDocument();
    // The elevation outlines it and says it in its accessible name.
    const slot = container.querySelector(`svg[data-face="rear"] [data-id="${dev(2)}"]`) as SVGElement;
    expect(slot.getAttribute("data-airflow")).toBe("mismatch");
    expect(slot.getAttribute("class")).toContain("rack-airflow-mismatch");
    expect(slot.getAttribute("aria-label")).toContain("airflow: Reversed");
    expect(slot.querySelector(".rack-airflow-strip")).not.toBeNull();
    expect(container.querySelector(`[data-id="${dev(3)}"]`)?.getAttribute("class")).not.toContain("rack-airflow-mismatch");
    // The table says it in a word.
    const row = screen.getByRole("rowheader", { name: "sw-30f" }).closest("tr") as HTMLElement;
    expect(within(row).getByText("Reversed")).toBeInTheDocument();
    await expectNoA11yViolations();
  });

  it("asks for the aisle when nobody said it", async () => {
    const api = mockApi(rackRoutes(rackObj({}), {}, READER));
    const { container } = renderApp({ at: `/racks/${RACK}` });
    expect(await screen.findByText(/Nobody has said which aisle this rack faces/)).toBeInTheDocument();
    expect(await screen.findByText("Say which aisle the front of this rack faces to check the airflow of its devices.")).toBeInTheDocument();
    expect(container.querySelector(".airflow-diagram")).toBeNull();
    expect(container.querySelector(".rack-face figcaption .aisle-chip")).toBeNull();
    // A reader is offered no control, and the provenance is not read for nothing.
    expect(screen.queryByRole("button", { name: "Say which aisle it faces" })).toBeNull();
    expect(api.calls.some((c) => c.path.endsWith("/provenance"))).toBe(false);
  });

  it("declares the front aisle in one choice, previewed then submitted, as a partial batch on the rack by id", async () => {
    const user = userEvent.setup();
    const api = mockApi(
      rackRoutes(rackObj({}), {
        "POST /batches": { source: "user:e2e", status: "applied", result: { created: 0, values_changed: 1, relations_opened: 0, relations_closed: 0, objects: {}, changes: [] } },
      }),
    );
    renderApp({ at: `/racks/${RACK}` });
    await user.click(await screen.findByRole("button", { name: "Say which aisle it faces" }));
    const group = screen.getByRole("group", { name: "The front of this rack faces" });
    // Nothing chosen yet: nothing to preview.
    expect(screen.getByRole("button", { name: "Preview the change" })).toBeDisabled();
    await user.click(within(group).getByRole("radio", { name: "the cold aisle" }));
    await user.click(screen.getByRole("button", { name: "Preview the change" }));
    await user.click(await screen.findByRole("button", { name: "Submit" }));
    await screen.findByText(/Applied: the inventory now says this/);
    const sent = api.calls.filter((c) => c.method === "POST").map((c) => c.body as Batch);
    expect(sent).toHaveLength(2);
    for (const b of sent) {
      expect(b.scope).toBe("partial");
      expect(b.objects).toEqual([{ ref: "obj", type: "rack", identity: { id: RACK }, attributes: { front_aisle: "cold" } }]);
    }
  });

  it("withdraws only the caller's own claim for \"not set\", and says when the value is someone else's", async () => {
    const user = userEvent.setup();
    const api = mockApi(rackRoutes(rackObj({ front_aisle: "cold" })));
    renderApp({ at: `/racks/${RACK}` });
    await screen.findByText(/Said by user:alice/);
    await user.click(screen.getByRole("button", { name: "Change the aisles" }));
    await user.click(screen.getByRole("radio", { name: "not set" }));
    // The caller (ui-tests) said nothing: there is nothing of theirs to withdraw.
    expect(screen.getByText(/You have said nothing about this rack's aisles/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Preview the change" })).toBeDisabled();
    await user.click(screen.getByRole("radio", { name: "the hot aisle" }));
    await user.click(screen.getByRole("button", { name: "Preview the change" }));
    await screen.findByRole("button", { name: "Submit" });
    expect((api.calls.find((c) => c.method === "POST")?.body as Batch).objects?.[0]?.attributes).toEqual({ front_aisle: "hot" });
  });
});

describe("room page: the aisles of the floor plan", () => {
  it("draws the aisle tiles from the racks and flags racks that disagree", async () => {
    const ROOM = "0a000000-0000-4000-8000-0000000000b0";
    const rack = (n: number, name: string, x: number, y: number, facing: string, front_aisle?: string): ObjectView => ({
      id: `0a000000-0000-4000-8000-0000000003${String(n).padStart(2, "0")}`,
      type: "rack",
      display: name,
      status: "active",
      parent_id: ROOM,
      attributes: { name, floor_x: x, floor_y: y, facing, ...(front_aisle ? { front_aisle } : {}) },
      created_at: "2026-03-01T08:00:00Z",
      observed: false,
    });
    const racks = [rack(1, "r30", 1, 1, "south", "cold"), rack(2, "r31", 1, 3, "north", "cold"), rack(3, "r32", 2, 1, "south", "hot"), rack(4, "r33", 2, 3, "north", "cold")];
    mockApi({
      ...defaultRoutes(READER),
      [`GET /objects/${ROOM}`]: { id: ROOM, type: "room", display: "room-b", status: "active", attributes: { name: "room-b", width_mm: 2400, depth_mm: 3000 }, created_at: "2026-03-01T08:00:00Z", observed: false },
      "GET /objects": (c: Call) => (c.query.get("type") === "rack" ? page(racks) : page([])),
    });
    const { container } = renderApp({ at: `/rooms/${ROOM}` });
    await screen.findByRole("rowheader", { name: "r33" });
    const kinds = Object.fromEntries([...container.querySelectorAll("[data-tile].floor-aisle-tile")].map((g) => [g.getAttribute("data-tile"), g.getAttribute("data-aisle")]));
    expect(kinds).toMatchObject({ "1,2": "cold", "1,0": "hot", "1,4": "hot", "2,2": "conflict" });
    expect(container.querySelector('[data-tile="2,2"] title')?.textContent).toBe("Racks disagree about this aisle: r32, r33");
    const r32 = screen.getByRole("rowheader", { name: "r32" }).closest("tr") as HTMLElement;
    expect(within(r32).getByText("Hot aisle")).toBeInTheDocument();
    expect(within(r32).getByText(/Opens on an aisle another rack says is the other kind/)).toBeInTheDocument();
    expect(screen.getByText(/A blue tile is a cold aisle/)).toBeInTheDocument();
    await expectNoA11yViolations();
  });
});

describe("device page: the airflow card", () => {
  it("says what was read and why the check fails, with the contradiction of the chosen model", async () => {
    const SW = "0a000000-0000-4000-8000-0000000002ff";
    const sw: ObjectView = { id: SW, type: "device", display: "sw-30x", status: "active", attributes: { name: "sw-30x", role: "switch" }, created_at: "2026-03-01T08:00:00Z", observed: true };
    const air: DeviceAirflow = {
      device: { id: SW, type: "device", display: "sw-30x" },
      rack: { id: RACK, type: "rack", display: "r30" },
      face: "rear",
      front_aisle: "cold",
      model: { id: "0a000000-0000-4000-8000-0000000004ff", type: "device_type", display: "SW48-R" },
      model_airflow: "rear_to_front",
      model_declared: true,
      device_airflow: "front_to_rear",
      parts: [{ part: { id: "0a000000-0000-4000-8000-0000000005ff", type: "psu", display: "PSU1" }, airflow: "front_to_rear" }],
      check: {
        status: "mismatch",
        reason: "intake_on_hot_aisle",
        airflow: "front_to_rear",
        airflow_from: "device",
        intake: "rear",
        cold_face: "front",
        warnings: [{ code: "airflow_contradicts_model", airflow: "front_to_rear", from: "device", model: "rear_to_front", model_declared: true }],
      },
    };
    mockApi({
      ...defaultRoutes(READER),
      [`GET /objects/${SW}`]: sw,
      [`GET /objects/${SW}/relations`]: page([]),
      [`GET /objects/${SW}/provenance`]: { object_id: SW, attributes: [] },
      [`GET /objects/${SW}/faces`]: { faces: [], parts: [] },
      [`GET /objects/${SW}/device-type-candidates`]: { device: { id: SW, type: "device", display: "sw-30x" }, certain: true, reason: "declared", declarations: [], candidates: [], can_choose: false },
      [`GET /objects/${SW}/airflow`]: air,
    });
    renderApp({ at: `/objects/${SW}` });
    const card = (await screen.findByRole("heading", { level: 2, name: "Airflow" })).closest("section") as HTMLElement;
    expect(within(card).getByText("Reversed")).toBeInTheDocument();
    expect(within(card).getByText(/Takes its air in from the hot aisle/)).toBeInTheDocument();
    expect(within(card).getByText(/The device says front to rear \(said by the device\), its model says rear to front\. The model was chosen by hand/)).toBeInTheDocument();
    expect(within(card).getByText("Hot aisle")).toBeInTheDocument();
    expect(within(card).getByText("at the rack's rear")).toBeInTheDocument();
    expect(within(card).getByRole("link", { name: "PSU1" })).toBeInTheDocument();
    await expectNoA11yViolations();
  });

  it("says nothing for a device in no rack with no airflow, nor when the server has no such endpoint", async () => {
    mockApi({ ...defaultRoutes(READER) });
    renderApp({ at: "/objects/0a000000-0000-4000-8000-0000000000a1" });
    await screen.findByRole("heading", { level: 1 });
    await waitFor(() => expect(screen.queryByRole("heading", { level: 2, name: "Airflow" })).toBeNull());
  });
});

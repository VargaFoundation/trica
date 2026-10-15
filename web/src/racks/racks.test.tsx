import { render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import type { ObjectView, RackItem } from "../api/types";
import { Providers } from "../App";
import { expectNoA11yViolations } from "../test/axe";
import { ID, page, rackR12 } from "../test/fixtures";
import { defaultRoutes, mockApi, renderApp, type Call, type Handler } from "../test/support";
import { RackElevation } from "./RackElevation";
import { layoutRack, type Mounted } from "./layout";

const obj = (id: string, type: string, display: string, attributes: Record<string, unknown> = {}, extra: Partial<ObjectView> = {}): ObjectView => ({
  id,
  type,
  display,
  status: "active",
  attributes,
  created_at: "2026-03-01T08:00:00Z",
  observed: false,
  ...extra,
});

const T_1U = "0a000000-0000-4000-8000-0000000000c1";
const T_2U = "0a000000-0000-4000-8000-0000000000c2";
const T_FULL = "0a000000-0000-4000-8000-0000000000c3";
const deviceTypes = [
  obj(T_1U, "device_type", "ex-1u", { u_height: 1 }),
  obj(T_2U, "device_type", "ex-2u", { u_height: 2 }),
  obj(T_FULL, "device_type", "ex-server", { u_height: 2, full_depth: true }),
];

const dev = (n: number) => `0a000000-0000-4000-8000-0000000001${String(n).padStart(2, "0")}`;

/** What the server answers for one mounted device; the model comes from deviceTypes. */
const rackItem = (device: string, name: string, position: number | undefined, face: string | undefined, type?: string): RackItem => {
  const t = deviceTypes.find((d) => d.id === type);
  const height = t?.attributes.u_height;
  return {
    device: { id: device, type: "device", display: name },
    ...(position === undefined ? {} : { position }),
    ...(face === undefined ? {} : { face }),
    ...(t ? { model: { id: t.id, type: "device_type", display: t.display } } : {}),
    ...(typeof height === "number" ? { u_height: height } : {}),
    full_depth: t?.attributes.full_depth === true,
  };
};

interface Mount {
  n: number;
  name: string;
  position?: number;
  face?: string;
  type?: string;
}

/** Routes for a 12 U rack r12 holding the given devices. */
function rackRoutes(mounts: Mount[], rack: ObjectView = obj(ID.rack, "rack", "r12", { u_height: 12 }), extra: Record<string, Handler> = {}): Record<string, Handler> {
  const routes: Record<string, Handler> = {
    ...defaultRoutes(),
    [`GET /objects/${ID.rack}`]: rack,
    [`GET /racks/${ID.rack}/contents`]: page(mounts.map((m) => rackItem(dev(m.n), m.name, m.position, m.face, m.type))),
    ...extra,
  };
  return routes;
}

const faceSvg = (face: "front" | "rear") => document.querySelector(`svg[data-face="${face}"]`) as SVGSVGElement;
const slotRect = (face: "front" | "rear", id: string) => faceSvg(face).querySelector(`[data-id="${id}"] rect`) as SVGRectElement;

describe("rack elevation page", () => {
  it("draws each device on its face at the right height, as a link to the device", async () => {
    mockApi(
      rackRoutes([
        { n: 1, name: "sw-a1", position: 10, face: "front", type: T_2U },
        { n: 2, name: "pdu-a", position: 1, face: "rear", type: T_1U },
      ]),
    );
    renderApp({ at: `/racks/${ID.rack}` });
    await screen.findByRole("heading", { level: 1, name: "Rack r12" });
    await waitFor(() => expect(faceSvg("front").querySelector(`[data-id="${dev(1)}"]`)).not.toBeNull());
    // 12 U, 22 px per U, 6 px padding: the top edge of U11 (the top of a 2 U device at U10) is at 6 + (12 - 11) * 22.
    expect(slotRect("front", dev(1)).getAttribute("y")).toBe("29");
    expect(slotRect("front", dev(1)).getAttribute("height")).toBe("42");
    // U1 is the bottom row: 6 + 11 * 22.
    expect(slotRect("rear", dev(2)).getAttribute("y")).toBe("249");
    // The front face holds sw-a1 only, the rear face pdu-a only.
    expect(faceSvg("front").querySelector(`[data-id="${dev(2)}"]`)).toBeNull();
    expect(faceSvg("rear").querySelector(`[data-id="${dev(1)}"]`)).toBeNull();
    expect(within(faceSvg("front") as unknown as HTMLElement).getByRole("link", { name: "sw-a1, U10-U11" })).toHaveAttribute("href", `/objects/${dev(1)}`);
  });

  it("has a table with the same information, for people who do not use the drawing", async () => {
    mockApi(rackRoutes([{ n: 1, name: "sw-a1", position: 10, face: "front", type: T_2U }]));
    renderApp({ at: `/racks/${ID.rack}` });
    const row = (await screen.findByRole("rowheader", { name: "sw-a1" })).closest("tr") as HTMLElement;
    expect(row.querySelector('[data-label="Position"]')).toHaveTextContent("U10-U11");
    expect(row.querySelector('[data-label="Face"]')).toHaveTextContent("Front");
    expect(row.querySelector('[data-label="Height"]')).toHaveTextContent("2 U");
    expect(row.querySelector('[data-label="Model"]')).toHaveTextContent("ex-2u");
    expect(row.querySelector('[data-label="Notes"]')).toHaveTextContent("—");
  });

  it("reports the free units of each face", async () => {
    mockApi(rackRoutes([{ n: 1, name: "sw-a1", position: 10, face: "front", type: T_2U }]));
    renderApp({ at: `/racks/${ID.rack}` });
    const front = (await screen.findByText("Free units, front")).nextElementSibling;
    expect(front).toHaveTextContent("10");
    expect(screen.getByText("Free units, rear").nextElementSibling).toHaveTextContent("12");
  });

  it("shows a full-depth device on both faces, linking from the face it is mounted on only", async () => {
    mockApi(rackRoutes([{ n: 3, name: "srv-01", position: 4, face: "rear", type: T_FULL }]));
    renderApp({ at: `/racks/${ID.rack}` });
    await waitFor(() => expect(faceSvg("rear").querySelector(`a[data-id="${dev(3)}"]`)).not.toBeNull());
    expect(faceSvg("front").querySelector(`a[data-id="${dev(3)}"]`)).toBeNull();
    expect(faceSvg("front").querySelector(".rack-shadow")).not.toBeNull();
    // the shadow is not a second device for a screen reader
    expect(faceSvg("front").querySelector(".rack-shadow")?.getAttribute("aria-hidden")).toBe("true");
    expect(screen.getAllByRole("rowheader", { name: "srv-01" })).toHaveLength(1);
  });

  it("flags overlapping devices in the drawing and in the table", async () => {
    mockApi(
      rackRoutes([
        { n: 1, name: "sw-a1", position: 5, face: "front", type: T_2U },
        { n: 2, name: "sw-a2", position: 6, face: "front", type: T_1U },
      ]),
    );
    renderApp({ at: `/racks/${ID.rack}` });
    await waitFor(() => expect(faceSvg("front").querySelectorAll(".rack-problem")).toHaveLength(2));
    const rows = within(await screen.findByRole("table")).getAllByRole("row");
    expect(rows.filter((r) => r.textContent?.includes("Overlaps another device"))).toHaveLength(2);
  });

  it("flags a device that sticks out of the rack", async () => {
    mockApi(rackRoutes([{ n: 1, name: "sw-a1", position: 12, face: "front", type: T_2U }]));
    renderApp({ at: `/racks/${ID.rack}` });
    expect(await screen.findByText("Outside the rack")).toBeInTheDocument();
  });

  it("lists devices mounted without a position instead of drawing them", async () => {
    mockApi(
      rackRoutes([
        { n: 1, name: "sw-a1", position: 3, face: "front", type: T_1U },
        { n: 2, name: "sw-a2", type: T_1U },
      ]),
    );
    renderApp({ at: `/racks/${ID.rack}` });
    const h = await screen.findByRole("heading", { level: 2, name: "1 device is mounted without a position" });
    expect(within(h.parentElement as HTMLElement).getByRole("link", { name: "sw-a2" })).toHaveAttribute("href", `/objects/${dev(2)}`);
    expect(faceSvg("front").querySelector(`[data-id="${dev(2)}"]`)).toBeNull();
  });

  it("says when a height or a face is not known and what was assumed", async () => {
    mockApi(rackRoutes([{ n: 1, name: "sw-a1", position: 3 }]));
    renderApp({ at: `/racks/${ID.rack}` });
    expect(await screen.findByText(/Height unknown, drawn as 1 U/)).toBeInTheDocument();
    expect(screen.getByText(/Face unknown, drawn on the front/)).toBeInTheDocument();
  });

  it("assumes 42 U when the rack has no height, and says so", async () => {
    mockApi(rackRoutes([], obj(ID.rack, "rack", "r12", {})));
    renderApp({ at: `/racks/${ID.rack}` });
    expect(await screen.findByText("The inventory has no height for this rack: 42 U is assumed.")).toBeInTheDocument();
    await waitFor(() => expect(faceSvg("front")).not.toBeNull());
    expect(faceSvg("front").querySelectorAll("text.rack-u")).toHaveLength(42);
  });

  it("says when the rack is empty", async () => {
    mockApi(rackRoutes([]));
    renderApp({ at: `/racks/${ID.rack}` });
    expect(await screen.findByText("Nothing is mounted in this rack.")).toBeInTheDocument();
  });

  it("warns when the API holds more mounted devices than it returned", async () => {
    mockApi(rackRoutes([{ n: 1, name: "sw-a1", position: 1, face: "front", type: T_1U }], undefined, { [`GET /racks/${ID.rack}/contents`]: page([rackItem(dev(1), "sw-a1", 1, "front", T_1U)], { total: 1500 }) }));
    renderApp({ at: `/racks/${ID.rack}` });
    expect(await screen.findByText(/Only the first 1 mounted devices are shown/)).toBeInTheDocument();
  });

  it("reads the rack's contents in one call, whatever the number of devices (reads, never writes)", async () => {
    const api = mockApi(
      rackRoutes([
        { n: 1, name: "sw-a1", position: 1, face: "front", type: T_1U },
        { n: 2, name: "sw-a2", position: 2, face: "front", type: T_1U },
        { n: 3, name: "srv-a3", position: 3, face: "front", type: T_FULL },
      ]),
    );
    renderApp({ at: `/racks/${ID.rack}` });
    await screen.findByRole("rowheader", { name: "srv-a3" });
    expect(api.calls.every((c) => c.method === "GET")).toBe(true);
    expect(api.calls.filter((c) => c.path === `/racks/${ID.rack}/contents`)).toHaveLength(1);
    // No request per device, and no device type list: the server joins them (finding #42).
    expect(api.calls.filter((c) => c.path.startsWith("/objects/0a000000-0000-4000-8000-0000000001"))).toEqual([]);
    expect(api.calls.some((c) => c.path === "/objects" && c.query.get("type") === "device_type")).toBe(false);
    expect(api.unmatched).toEqual([]);
  });

  it("is not found for an object that is not a rack, and for an unknown id", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: `/racks/${ID.swA1}` });
    expect(await screen.findByRole("heading", { level: 1, name: /not found/i })).toBeInTheDocument();
  });

  it("is reachable from the rack's own page", async () => {
    mockApi({ ...defaultRoutes(), [`GET /objects/${ID.rack}/provenance`]: { attributes: [] }, [`GET /objects/${ID.rack}/relations`]: page([]), [`GET /objects/${ID.rack}/timeline`]: page([]) });
    renderApp({ at: `/objects/${rackR12.id}` });
    expect(await screen.findByRole("link", { name: "Open the rack elevation" })).toHaveAttribute("href", `/racks/${ID.rack}`);
  });

  it("is accessible, with a drawing and a table", async () => {
    mockApi(
      rackRoutes([
        { n: 1, name: "sw-a1", position: 10, face: "front", type: T_2U },
        { n: 2, name: "sw-a2", position: 11, face: "front", type: T_1U },
        { n: 3, name: "srv-01", position: 4, face: "rear", type: T_FULL },
        { n: 4, name: "lost", type: T_1U },
      ]),
    );
    renderApp({ at: `/racks/${ID.rack}` });
    await screen.findByRole("rowheader", { name: "srv-01" });
    await expectNoA11yViolations();
  });

  it("speaks French", async () => {
    mockApi(rackRoutes([{ n: 1, name: "sw-a1", position: 10, face: "front", type: T_2U }]));
    renderApp({ at: `/racks/${ID.rack}`, locale: "fr" });
    expect(await screen.findByRole("heading", { level: 1, name: "Baie r12" })).toBeInTheDocument();
    expect(await screen.findByRole("heading", { level: 2, name: "Équipements montés" })).toBeInTheDocument();
    expect(faceSvg("front").getAttribute("aria-label")).toBe("Avant de la baie r12, 12 U");
  });
});

describe("rack render time", () => {
  const FULL: Mounted[] = [
    ...Array.from({ length: 48 }, (_, i) => ({ id: `f${i}`, name: `sw-front-${String(i + 1).padStart(2, "0")}`, position: i + 1, face: "front" as const, heightU: 1, typeName: "ex-1u" })),
    ...Array.from({ length: 48 }, (_, i) => ({ id: `r${i}`, name: `pdu-rear-${String(i + 1).padStart(2, "0")}`, position: i + 1, face: "rear" as const, heightU: 1, typeName: "ex-1u" })),
  ];

  it("draws a 48 U rack, both faces fully populated, in well under a second", () => {
    const layout = layoutRack(48, FULL);
    expect(layout.front).toHaveLength(48);
    expect(layout.rear).toHaveLength(48);
    const t0 = performance.now();
    const { container } = render(
      <Providers locale="en">
        <MemoryRouter>
          <RackElevation rack="r48" uHeight={48} face="front" slots={layout.front} />
          <RackElevation rack="r48" uHeight={48} face="rear" slots={layout.rear} />
        </MemoryRouter>
      </Providers>,
    );
    const elapsed = performance.now() - t0;
    expect(container.querySelectorAll("a.rack-slot")).toHaveLength(96);
    // Reported in the pull request; the budget is 1 s.
    console.info(`RACK-RENDER-MS ${elapsed.toFixed(1)} (48 U, 96 devices, both faces, jsdom)`);
    expect(elapsed).toBeLessThan(1000);
  });

  it("lays out a 48 U rack with 96 devices in well under a tenth of a second", () => {
    const t0 = performance.now();
    for (let i = 0; i < 20; i++) layoutRack(48, FULL);
    expect((performance.now() - t0) / 20).toBeLessThan(100);
  });
});

describe("rooms", () => {
  const ROOM = "0a000000-0000-4000-8000-0000000000d1";
  const ROW = "0a000000-0000-4000-8000-0000000000d2";
  const rack = (n: number, name: string, parent: string | undefined, attrs: Record<string, unknown>) =>
    obj(`0a000000-0000-4000-8000-00000000e0${String(n).padStart(2, "0")}`, "rack", name, attrs, { parent_id: parent });
  const room = obj(ROOM, "room", "dc-1a", { width_mm: 3000, depth_mm: 2400 }, { site_id: ID.site });

  const roomRoutes = (racks: ObjectView[], r: ObjectView = room): Record<string, Handler> => ({
    ...defaultRoutes(),
    [`GET /objects/${ROOM}`]: r,
    "GET /objects": (c: Call) => {
      const type = c.query.get("type");
      if (type === "room") return page([r]);
      if (type === "row") return page([obj(ROW, "row", "row-a", {}, { parent_id: ROOM })]);
      if (type === "rack") return page(racks);
      return page([]);
    },
  });

  it("lists rooms, each linking to its floor plan", async () => {
    mockApi(roomRoutes([]));
    renderApp({ at: "/rooms" });
    expect(await screen.findByRole("link", { name: "dc-1a" })).toHaveAttribute("href", `/rooms/${ROOM}`);
    expect(screen.getByText("3,000 × 2,400 mm")).toBeInTheDocument();
    await expectNoA11yViolations();
  });

  it("is in the main navigation", async () => {
    mockApi(roomRoutes([]));
    renderApp({ at: "/" });
    expect(await screen.findByRole("link", { name: "Rooms" })).toHaveAttribute("href", "/rooms");
  });

  it("says when there is no room", async () => {
    mockApi({ ...roomRoutes([]), "GET /objects": page([]) });
    renderApp({ at: "/rooms" });
    expect(await screen.findByText("No room is recorded yet.")).toBeInTheDocument();
  });

  it("draws the racks of the room, directly or through a row, on their tiles, and nobody else's", async () => {
    const mine = rack(1, "r01", ROOM, { floor_x: 0, floor_y: 0, facing: "south", u_height: 42 });
    const inRow = rack(2, "r02", ROW, { floor_x: 2, floor_y: 1, facing: "north", u_height: 48 });
    const elsewhere = rack(3, "r99", ID.site, { floor_x: 1, floor_y: 1 });
    mockApi(roomRoutes([mine, inRow, elsewhere]));
    renderApp({ at: `/rooms/${ROOM}` });
    await screen.findByRole("heading", { level: 1, name: "Floor plan of dc-1a" });
    await waitFor(() => expect(document.querySelectorAll("a.floor-rack")).toHaveLength(2));
    const rect = (id: string) => document.querySelector(`a.floor-rack[data-id="${id}"] rect`) as SVGRectElement;
    // 64 px tiles, 8 px padding, 3 px inset: tile (2, 1) starts at 8 + 2 * 64 and 8 + 64.
    expect(rect(inRow.id).getAttribute("x")).toBe("139");
    expect(rect(inRow.id).getAttribute("y")).toBe("75");
    expect(rect(mine.id).getAttribute("x")).toBe("11");
    expect(rect(mine.id).getAttribute("y")).toBe("11");
    expect(document.querySelector('a[data-id="' + elsewhere.id + '"]')).toBeNull();
    expect(screen.getByRole("link", { name: "r01, South" })).toHaveAttribute("href", `/racks/${mine.id}`);
    // 3000 x 2400 mm in 600 mm tiles.
    expect(document.querySelector(".floor-room")?.getAttribute("width")).toBe(String(5 * 64));
    expect(document.querySelector(".floor-room")?.getAttribute("height")).toBe(String(4 * 64));
    await expectNoA11yViolations();
  });

  it("counts floor positions in the room's own tile_mm, and says so", async () => {
    const small = obj(ROOM, "room", "dc-1a", { width_mm: 3000, depth_mm: 2400, tile_mm: 300 }, { site_id: ID.site });
    // Tile (9, 7) is inside a 3000 x 2400 mm room of 300 mm tiles, outside one of 600 mm tiles.
    mockApi(roomRoutes([rack(1, "r01", ROOM, { floor_x: 9, floor_y: 7 })], small));
    renderApp({ at: `/rooms/${ROOM}` });
    expect(await screen.findByText("One square is a 300 mm floor tile. The bar on a rack is its front.")).toBeInTheDocument();
    expect(document.querySelector(".floor-room")?.getAttribute("width")).toBe(String(10 * 64));
    expect(document.querySelector(".floor-room")?.getAttribute("height")).toBe(String(8 * 64));
    expect(screen.queryByText(/outside the size recorded/)).toBeNull();
  });

  it("marks the front of a rack on the side it faces", async () => {
    const mine = rack(1, "r01", ROOM, { floor_x: 0, floor_y: 0, facing: "east" });
    mockApi(roomRoutes([mine]));
    renderApp({ at: `/rooms/${ROOM}` });
    await waitFor(() => expect(document.querySelectorAll(".floor-front")).toHaveLength(1));
    const line = document.querySelector(".floor-front") as SVGLineElement;
    expect(line.getAttribute("x1")).toBe(line.getAttribute("x2")); // a vertical bar
    expect(line.getAttribute("x1")).toBe("70"); // the right edge of tile 0: 8 + 64 - 2
  });

  it("lists racks that have no floor position and flags two racks on one tile", async () => {
    const a = rack(1, "r01", ROOM, { floor_x: 1, floor_y: 1 });
    const b = rack(2, "r02", ROOM, { floor_x: 1, floor_y: 1 });
    const c = rack(3, "r03", ROOM, {});
    mockApi(roomRoutes([a, b, c]));
    renderApp({ at: `/rooms/${ROOM}` });
    const rows = within(await screen.findByRole("table")).getAllByRole("row");
    expect(rows.filter((r) => r.textContent?.includes("Shares its tile with another rack"))).toHaveLength(2);
    expect(rows.filter((r) => r.textContent?.includes("No floor position"))).toHaveLength(1);
    expect(document.querySelectorAll("a.floor-rack")).toHaveLength(2);
  });

  it("fits the grid to the racks when the room has no size, and says so", async () => {
    const a = rack(1, "r01", ROOM, { floor_x: 3, floor_y: 0 });
    mockApi(roomRoutes([a], obj(ROOM, "room", "dc-1a", {})));
    renderApp({ at: `/rooms/${ROOM}` });
    expect(await screen.findByText(/no size for this room/)).toBeInTheDocument();
    expect(document.querySelector(".floor-room")?.getAttribute("width")).toBe(String(4 * 64));
  });

  it("says when the room holds no rack", async () => {
    mockApi(roomRoutes([]));
    renderApp({ at: `/rooms/${ROOM}` });
    expect(await screen.findByText("No rack is recorded in this room.")).toBeInTheDocument();
  });

  it("is not found for something that is not a room", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: `/rooms/${ID.swA1}` });
    expect(await screen.findByRole("heading", { level: 1, name: /not found/i })).toBeInTheDocument();
  });

  it("speaks French", async () => {
    mockApi(roomRoutes([rack(1, "r01", ROOM, { floor_x: 0, floor_y: 0, facing: "south" })]));
    renderApp({ at: `/rooms/${ROOM}`, locale: "fr" });
    expect(await screen.findByRole("heading", { level: 1, name: "Plan de la salle dc-1a" })).toBeInTheDocument();
    expect(await screen.findByRole("link", { name: "r01, Sud" })).toBeInTheDocument();
  });
});

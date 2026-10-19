import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { DeviceFaces, FacePart, ObjectView } from "../api/types";
import { clearFaceImages } from "../racks/faceImages";
import { ID } from "../test/fixtures";
import { expectNoA11yViolations } from "../test/axe";
import { defaultRoutes, mockApi, problem, renderApp, type Handler } from "../test/support";
import { partState, placeSpots, unplaced, worst } from "./spots";

let n = 0;
const part = (type: string, display: string, attributes: Record<string, unknown>): ObjectView => ({
  id: `0a000000-0000-4000-8000-0000000f${(++n).toString(16).padStart(4, "0")}`,
  type,
  display,
  parent_id: ID.srv01,
  status: "active",
  attributes: { name: display, ...attributes },
  created_at: "2026-03-02T08:00:00Z",
  observed: true,
});

const disk1 = part("disk", "Disk 1", { health: "ok", model: "MZ7KM480", capacity_bytes: 480_103_981_056, temperature_c: 29 });
const disk2 = part("disk", "Disk 2", { health: "critical", model: "MZ7KM480" });
const disk3 = part("disk", "Disk 3", { health: "ok" });
const psu1 = part("psu", "PSU 0", { health: "ok", capacity_w: 800, input_w: 182 });
const psu2 = part("psu", "PSU 1", { health: "warning", capacity_w: 800 });
const ilo = part("interface", "iLO", { kind: "bmc", oper_status: "up", speed_mbps: 1000 });
const eno1 = part("interface", "eno1", { oper_status: "down" });

const placed = (p: ObjectView, kind: string, face?: string, spot?: string): FacePart => ({ part: p, kind, face, spot });

const IMG = "/api/v1/device-types/acme-srv/front";
const faces: DeviceFaces = {
  model: { id: "0a000000-0000-4000-8000-0000000000d1", type: "device_type", display: "acme-srv" },
  model_name: "SRV 360",
  faces: [
    {
      face: "front",
      image: IMG,
      width: 482.6,
      height: 42.8,
      layout_origin: "trica",
      spots: [
        { kind: "bay", name: "1:1", x: 10, y: 5, w: 30, h: 8 },
        { kind: "bay", name: "1:2", x: 10, y: 15, w: 30, h: 8 },
        { kind: "bay", name: "1:3", x: 10, y: 25, w: 30, h: 8 },
      ],
    },
    {
      face: "rear",
      image: "/api/v1/device-types/acme-srv/rear",
      width: 482.6,
      height: 42.8,
      layout_origin: "trica",
      spots: [
        { kind: "psu", name: "1", x: 380, y: 5, w: 40, h: 30 },
        { kind: "psu", name: "2", x: 430, y: 5, w: 40, h: 30 },
        { kind: "port", name: "bmc", x: 100, y: 20, w: 10, h: 8 },
        { kind: "port", name: "nic1", x: 120, y: 20, w: 10, h: 8 },
      ],
    },
  ],
  parts: [
    placed(disk1, "bay", "front", "1:1"),
    placed(disk2, "bay", "front", "1:2"),
    placed(disk3, "bay"),
    placed(psu1, "psu", "rear", "1"),
    placed(psu2, "psu", "rear", "2"),
    placed(ilo, "port", "rear", "bmc"),
    placed(eno1, "port", "rear", "nic1"),
  ],
  truncated: false,
};

function routes(body: Handler = faces) {
  return { ...defaultRoutes(), [`GET /objects/${ID.srv01}/faces`]: body };
}

async function card() {
  return (await screen.findByRole("heading", { name: "Front and rear", level: 2 })).closest("section") as HTMLElement;
}
const drawing = (face: string) => document.querySelector(`.device-face[data-face="${face}"] svg`) as SVGSVGElement;
const spot = (face: string, name: string) => drawing(face).querySelector(`[data-spot="${name}"]`) as SVGElement;
const details = () => document.querySelector(".faces-details") as HTMLElement;

describe("spots", () => {
  it("take the worst state of their parts, health before link state", () => {
    expect(partState(disk2)).toBe("critical");
    expect(partState(ilo)).toBe("up");
    expect(partState(eno1)).toBe("down");
    expect(partState(part("interface", "x", {}))).toBe("unknown");
    expect(partState({ ...ilo, attributes: { ...ilo.attributes, health: "warning" } })).toBe("warning");
    expect(worst(["up", "critical", "ok"])).toBe("critical");
    expect(worst(["up", "down"])).toBe("down");
    expect(worst([])).toBe("empty");
  });

  it("are read kind by kind, in the order a person counts", () => {
    const face = {
      face: "front",
      image: IMG,
      width: 100,
      height: 10,
      spots: ["Ethernet10", "Ethernet2", "Ethernet1"].map((name, i) => ({ kind: "port", name, x: i * 10, y: 0, w: 5, h: 5 })).concat([{ kind: "psu", name: "1", x: 90, y: 0, w: 5, h: 5 }]),
    };
    const lanes = [1, 2].map((l) => placed(part("interface", `Ethernet2/${l}`, { oper_status: l === 1 ? "up" : "down" }), "port", "front", "Ethernet2"));
    const spots = placeSpots(face, [...lanes, placed(eno1, "port", "rear", "Ethernet1")], "en");
    expect(spots.map((s) => s.spot.name)).toEqual(["1", "Ethernet1", "Ethernet2", "Ethernet10"]);
    // Two lanes of one port share it; the down one colours it. A part of the other face stays there.
    expect(spots[2]?.parts).toHaveLength(2);
    expect(spots[2]?.state).toBe("down");
    expect(spots[1]?.state).toBe("empty");
    expect(unplaced(faces.parts).map((p) => p.part.display)).toEqual(["Disk 3"]);
  });
});

describe("the faces of a device", () => {
  beforeEach(() => clearFaceImages());
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("draws each part in its place, coloured and named by its state", async () => {
    mockApi(routes());
    renderApp({ at: `/objects/${ID.srv01}` });
    const section = await card();
    expect(section.querySelector(".faces-summary")).toHaveTextContent("7 places on the faces · 6 with a part reported · 2 needing attention.");
    expect(within(section).getByRole("link", { name: "SRV 360" })).toHaveAttribute("href", `/objects/${faces.model?.id}`);

    const bay = spot("front", "1:2");
    expect(bay.getAttribute("class")).toContain("face-spot-critical");
    expect(bay).toHaveAttribute("href", `/objects/${disk2.id}`);
    expect(bay).toHaveAttribute("aria-label", "Bay 1:2: Disk 2, Critical");
    expect(spot("rear", "1")).toHaveAttribute("aria-label", "Power supply 1: PSU 0, OK");
    expect(spot("rear", "2").getAttribute("class")).toContain("face-spot-warning");
    expect(spot("rear", "bmc")).toHaveAttribute("aria-label", "Port bmc: iLO, Link up");
    expect(spot("rear", "nic1").getAttribute("class")).toContain("face-spot-down");
    // An empty bay is drawn dashed, and is not a link.
    const empty = spot("front", "1:3");
    expect(empty.tagName.toLowerCase()).toBe("g");
    expect(empty.getAttribute("class")).toContain("face-spot-empty");
    // No image decoder in the test browser: the spots are drawn over a plain plate.
    expect(drawing("front").getAttribute("data-drawn")).toBe("plate");
    expect(within(section).getByRole("list", { name: "Legend" })).toHaveTextContent(/Critical.*Warning.*OK.*Link up.*Link down.*Nothing reported/);
  });

  it("gives a part's details when a pointer is over it", async () => {
    mockApi(routes());
    renderApp({ at: `/objects/${ID.srv01}` });
    await card();
    expect(details()).toHaveTextContent("Point at a part");
    await userEvent.hover(spot("front", "1:1"));
    expect(within(details()).getByRole("heading", { name: "Bay 1:1 Front" })).toBeInTheDocument();
    expect(within(details()).getByRole("link", { name: "Disk 1" })).toHaveAttribute("href", `/objects/${disk1.id}`);
    expect(within(details()).getByText("Capacity").nextElementSibling).toHaveTextContent("447 GiB");
    expect(within(details()).getByText("Temperature").nextElementSibling).toHaveTextContent("29 °C");
    expect(details()).toHaveTextContent("OK");
    await userEvent.hover(spot("rear", "1"));
    expect(within(details()).getByText("Input power").nextElementSibling).toHaveTextContent("182 W");
    await userEvent.hover(spot("front", "1:3"));
    expect(details()).toHaveTextContent("Bay 1:3 Front");
    expect(details()).toHaveTextContent("No source reports a part in this place.");
  });

  it("is one tab stop per face, the arrow keys going from part to part", async () => {
    mockApi(routes());
    renderApp({ at: `/objects/${ID.srv01}` });
    await card();
    const front = within(drawing("front") as unknown as HTMLElement).getAllByRole("link");
    expect(front.map((a) => a.getAttribute("tabindex"))).toEqual(["0", "-1"]);
    act(() => front[0]?.focus());
    expect(within(details()).getByRole("link", { name: "Disk 1" })).toBeInTheDocument();
    expect(front[0]).toHaveAttribute("aria-describedby", details().id);
    await userEvent.keyboard("{ArrowRight}");
    expect(document.activeElement).toBe(front[1]);
    expect(front.map((a) => a.getAttribute("tabindex"))).toEqual(["-1", "0"]);
    expect(within(details()).getByRole("link", { name: "Disk 2" })).toBeInTheDocument();
    // The empty bay is skipped, and the ends hold.
    await userEvent.keyboard("{ArrowRight}");
    expect(document.activeElement).toBe(front[1]);
    await userEvent.keyboard("{Home}");
    expect(document.activeElement).toBe(front[0]);
    // The rear is its own stop: power supplies first, then ports.
    const rear = within(drawing("rear") as unknown as HTMLElement).getAllByRole("link");
    expect(rear.map((a) => a.getAttribute("aria-label"))).toEqual(["Power supply 1: PSU 0, OK", "Power supply 2: PSU 1, Warning", "Port bmc: iLO, Link up", "Port nic1: eno1, Link down"]);
    act(() => rear[0]?.focus());
    await userEvent.keyboard("{End}");
    expect(document.activeElement).toBe(rear[3]);
  });

  it("lists the parts it could not place", async () => {
    mockApi(routes());
    renderApp({ at: `/objects/${ID.srv01}` });
    const section = await card();
    const loose = within(section).getByRole("heading", { name: "1 part not placed" }).parentElement as HTMLElement;
    expect(within(loose).getByRole("link", { name: "Disk 3" })).toHaveAttribute("href", `/objects/${disk3.id}`);
    expect(loose).toHaveTextContent("trica does not guess");
  });

  it("paints the model's image under the spots, fetched with the token", async () => {
    const bitmap = { width: 1200, height: 120, close: () => undefined } as unknown as ImageBitmap;
    const drawn: unknown[][] = [];
    vi.stubGlobal("createImageBitmap", vi.fn(() => Promise.resolve(bitmap)));
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(() => ({ clearRect: () => undefined, drawImage: (...a: unknown[]) => drawn.push(a) }) as never);
    const api = mockApi({ ...routes(), "GET /device-types/acme-srv/front": "png", "GET /device-types/acme-srv/rear": "png" });
    const { container } = renderApp({ at: `/objects/${ID.srv01}` });
    await card();
    await waitFor(() => expect(drawing("front").getAttribute("data-drawn")).toBe("image"));
    expect(drawing("front").getAttribute("viewBox")).toBe("0 0 482.6 42.8");
    expect(drawn.some((a) => a[0] === bitmap)).toBe(true);
    expect(api.calls.find((c) => c.path === "/device-types/acme-srv/front")?.headers.get("Authorization")).toBe("Bearer trica_test_token");
    expect(container.innerHTML).not.toMatch(/blob:|data:/);
  });

  it("shows a face without a layout as its image alone", async () => {
    const bitmap = { width: 1200, height: 120, close: () => undefined } as unknown as ImageBitmap;
    vi.stubGlobal("createImageBitmap", vi.fn(() => Promise.resolve(bitmap)));
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(() => ({ clearRect: () => undefined, drawImage: () => undefined }) as never);
    const photo: DeviceFaces = { faces: [{ face: "front", image: IMG, width: 0, height: 0, spots: [] }], parts: [], truncated: false };
    mockApi({ ...routes(photo), "GET /device-types/acme-srv/front": "png" });
    renderApp({ at: `/objects/${ID.srv01}` });
    const section = await card();
    expect(section).toHaveTextContent("The library has no layout for this model");
    await waitFor(() => expect(drawing("front").getAttribute("viewBox")).toBe("0 0 1200 120"));
    expect(drawing("front").querySelectorAll(".face-spot")).toHaveLength(0);
    expect(document.querySelector(".faces-details")).toBeNull();
  });

  it("is not drawn without a face, and not asked for an object that is not a device", async () => {
    mockApi(routes({ faces: [], parts: [], truncated: false }));
    renderApp({ at: `/objects/${ID.srv01}` });
    await screen.findByRole("heading", { name: "srv-01", level: 1 });
    expect(screen.queryByRole("heading", { name: "Front and rear" })).toBeNull();

    const rack = mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.rack}` });
    await screen.findByRole("heading", { name: "r12", level: 1 });
    expect(rack.calls.some((c) => c.path.endsWith("/faces"))).toBe(false);
  });

  it("says when the faces cannot be read, and when the device has too many parts", async () => {
    mockApi(routes(problem(500, "internal")));
    const view = renderApp({ at: `/objects/${ID.srv01}` });
    expect(within(await card()).getByRole("alert")).toBeInTheDocument();
    view.unmount();
    mockApi(routes({ ...faces, truncated: true }));
    renderApp({ at: `/objects/${ID.srv01}` });
    expect(await card()).toHaveTextContent("some are missing from it");
  });

  it("has no accessibility violation, in either theme", async () => {
    for (const theme of ["light", "dark"] as const) {
      mockApi(routes());
      const view = renderApp({ at: `/objects/${ID.srv01}`, theme });
      await card();
      await userEvent.hover(spot("front", "1:2"));
      await expectNoA11yViolations();
      view.unmount();
    }
  });

  it("speaks French", async () => {
    mockApi(routes());
    renderApp({ at: `/objects/${ID.srv01}`, locale: "fr" });
    const section = (await screen.findByRole("heading", { name: "Faces avant et arrière", level: 2 })).closest("section") as HTMLElement;
    expect(spot("front", "1:2")).toHaveAttribute("aria-label", "Logement 1:2 : Disk 2, Critique");
    expect(spot("rear", "nic1")).toHaveAttribute("aria-label", "Port nic1 : eno1, Lien inactif");
    expect(within(section).getByRole("heading", { name: "1 composant non placé" })).toBeInTheDocument();
  });
});


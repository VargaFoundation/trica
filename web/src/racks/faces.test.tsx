import { screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ObjectView, RackItem } from "../api/types";
import { drawRackSheet, HEALTH_INK, type Ctx } from "../export/rackCanvas";
import { expectNoA11yViolations } from "../test/axe";
import { ID, page } from "../test/fixtures";
import { defaultRoutes, mockApi, renderApp } from "../test/support";
import { clearFaceImages, isFaceImagePath, loadFaceImage } from "./faceImages";
import { faceKind, genericFace, type FaceKind } from "./faces";
import { layoutRack, overallHealth, type Mounted } from "./layout";

const rack: ObjectView = {
  id: ID.rack,
  type: "rack",
  display: "r12",
  status: "active",
  attributes: { u_height: 12 },
  created_at: "2026-03-01T08:00:00Z",
  observed: false,
};
const dev = (n: number) => `0a000000-0000-4000-8000-0000000002${String(n).padStart(2, "0")}`;
const IMG = "/api/v1/device-types/acme-srv-2u/front";
const REAR = "/api/v1/device-types/acme-srv-2u/rear";

const items: RackItem[] = [
  // A model with a front and a rear image, full depth: its rear is the image too, faded.
  {
    device: { id: dev(1), type: "device", display: "srv-01" },
    position: 10,
    face: "front",
    model: { id: dev(90), type: "device_type", display: "Acme 2U" },
    model_name: "Acme Server 2U",
    u_height: 2,
    full_depth: true,
    role: "server",
    health: "ok",
    parts_health: "critical",
    front_image: IMG,
    rear_image: REAR,
  },
  // No image: a generic switch face.
  { device: { id: dev(2), type: "device", display: "sw-01" }, position: 12, face: "front", u_height: 1, full_depth: false, role: "switch", health: "warning" },
  // No role, no health: a blank plate, health unknown.
  { device: { id: dev(3), type: "device", display: "box-01" }, position: 1, face: "front", u_height: 1, full_depth: false },
  { device: { id: dev(4), type: "device", display: "pdu-01" }, position: 3, face: "rear", u_height: 1, full_depth: false, role: "pdu", health: "ok" },
];

/** The text of a blob (jsdom's has no text()). */
// The body of a fetched image is Node's Blob on some versions and jsdom's on others: jsdom's
// FileReader refuses the first, and jsdom's Blob has no text(). Use what the Blob offers.
const blobText = (b: Blob): Promise<string> => {
  if (typeof b.text === "function") return b.text();
  if (typeof b.arrayBuffer === "function") return b.arrayBuffer().then((a) => new TextDecoder().decode(a));
  return new Promise<string>((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(r.error);
    r.readAsText(b);
  });
};
const faceSvg = (face: "front" | "rear") => document.querySelector(`svg[data-face="${face}"]`) as SVGSVGElement;
const slot = (face: "front" | "rear", id: string) => faceSvg(face).querySelector(`[data-id="${id}"]`) as SVGElement;

describe("health", () => {
  it("is the worst of the device's own and its parts', unknown when nobody says", () => {
    const cases: [Mounted["health"], Mounted["partsHealth"], string][] = [
      ["ok", undefined, "ok"],
      ["ok", "critical", "critical"],
      ["critical", "ok", "critical"],
      ["warning", "ok", "warning"],
      [undefined, "warning", "warning"],
      ["unknown", "ok", "ok"],
      [undefined, undefined, "unknown"],
      ["unknown", undefined, "unknown"],
      ["bogus", undefined, "unknown"],
    ];
    for (const [health, partsHealth, want] of cases) expect(overallHealth({ health, partsHealth }), `${health}/${partsHealth}`).toBe(want);
  });
});

describe("generic faces", () => {
  it("maps every role of the schema to a face, an unknown role to a blank plate", () => {
    expect(faceKind("nas")).toBe("storage");
    expect(faceKind("patch_panel")).toBe("patch_panel");
    expect(faceKind("sensor")).toBe("other");
    expect(faceKind(undefined)).toBe("other");
  });

  it("draws each role differently, inside its box", () => {
    const kinds: FaceKind[] = ["server", "switch", "router", "firewall", "storage", "pdu", "ups", "patch_panel", "kvm", "other"];
    const signatures = new Set<string>();
    for (const kind of kinds) {
      for (const h of [20, 42]) {
        const prims = genericFace(kind, 278, h);
        expect(prims[0]).toMatchObject({ k: "rect", c: "plate", w: 278, h });
        for (const p of prims) {
          const [x0, y0, x1, y1] =
            p.k === "rect" ? [p.x, p.y, p.x + p.w, p.y + p.h] : p.k === "circle" ? [p.x - p.r, p.y - p.r, p.x + p.r, p.y + p.r] : [p.x1, p.y1, p.x2, p.y2];
          expect(Math.min(x0, x1), `${kind} ${h}`).toBeGreaterThanOrEqual(0);
          expect(Math.max(x0, x1), `${kind} ${h}`).toBeLessThanOrEqual(278);
          expect(Math.min(y0, y1), `${kind} ${h}`).toBeGreaterThanOrEqual(0);
          expect(Math.max(y0, y1), `${kind} ${h}`).toBeLessThanOrEqual(h);
        }
        if (h === 20) {
          const count = (c: string) => prims.filter((p) => p.c === c).length;
          signatures.add(["bay", "port", "led", "vent", "accent"].map(count).join(","));
        }
      }
    }
    // No two roles look the same.
    expect(signatures.size).toBe(kinds.length);
    // A 2 U server has more drive bays than a 1 U one.
    const bays = (h: number) => genericFace("server", 278, h).filter((p) => p.c === "bay").length;
    expect(bays(42)).toBeGreaterThan(bays(20));
  });
});

describe("face images", () => {
  const bitmap = { width: 1000, height: 180, close: () => undefined } as unknown as ImageBitmap;
  let drawn: unknown[][];

  beforeEach(() => {
    clearFaceImages();
    drawn = [];
    vi.stubGlobal("createImageBitmap", vi.fn(() => Promise.resolve(bitmap)));
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(
      () => ({ clearRect: () => undefined, drawImage: (...a: unknown[]) => drawn.push(a) }) as never,
    );
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("only fetches the API's image paths, so the token goes nowhere else", async () => {
    for (const ok of [IMG, REAR]) expect(isFaceImagePath(ok)).toBe(true);
    for (const bad of ["https://evil.example/api/v1/device-types/a/front", "/api/v1/device-types/a/top", "/api/v1/objects", "//evil.example/x", "/api/v1/device-types/A B/front"]) {
      expect(isFaceImagePath(bad), bad).toBe(false);
    }
    const api = mockApi({});
    expect(await loadFaceImage("https://evil.example/x.png")).toBeNull();
    expect(api.calls).toHaveLength(0);
  });

  it("draws the model's image, else the role's face, with the health on the left edge and in words", async () => {
    const api = mockApi({
      ...defaultRoutes(),
      [`GET /objects/${ID.rack}`]: rack,
      [`GET /racks/${ID.rack}/contents`]: page(items),
      "GET /device-types/acme-srv-2u/front": "png bytes",
      "GET /device-types/acme-srv-2u/rear": "png bytes",
    });
    const { container } = renderApp({ at: `/racks/${ID.rack}` });
    await screen.findByRole("table");
    // The image is fetched with the session's token, then painted on a canvas: no blob: or data: URL.
    await waitFor(() => expect(slot("front", dev(1)).getAttribute("data-face-drawn")).toBe("image"));
    const img = api.calls.find((c) => c.path === "/device-types/acme-srv-2u/front");
    expect(img?.headers.get("Authorization")).toBe("Bearer trica_test_token");
    expect(slot("front", dev(1)).querySelector("foreignObject canvas")).not.toBeNull();
    expect(drawn.some((a) => a[0] === bitmap)).toBe(true);
    expect(container.innerHTML).not.toMatch(/blob:|data:/);
    // The rear of the full-depth server is its rear image, faded, not a link.
    await waitFor(() => expect(faceSvg("rear").querySelector(".rack-shadow foreignObject")).not.toBeNull());

    // Healthy itself, a critical part: critical, in colour and in words.
    const srv = slot("front", dev(1));
    expect(srv.getAttribute("class")).toContain("rack-health-critical");
    expect(srv.getAttribute("aria-label")).toBe("srv-01, U10-U11, Health: Critical");
    expect(srv.querySelector("title")?.textContent).toBe("srv-01 (U10-U11) · Acme Server 2U · Health: Critical");
    expect(srv.querySelector(".rack-health-strip")).not.toBeNull();

    // No image: the face of its role.
    const sw = slot("front", dev(2));
    expect(sw.getAttribute("data-face-drawn")).toBe("generic");
    expect(sw.querySelector(".rack-face-switch")).not.toBeNull();
    expect(sw.getAttribute("data-health")).toBe("warning");
    expect(slot("rear", dev(4)).querySelector(".rack-face-pdu")).not.toBeNull();
    // Nothing known: a blank plate, health unknown, and the name alone for a screen reader.
    const box = slot("front", dev(3));
    expect(box.querySelector(".rack-face-other")).not.toBeNull();
    expect(box.getAttribute("data-health")).toBe("unknown");
    expect(box.getAttribute("aria-label")).toBe("box-01, U1");

    // The table says it too.
    const row = within(screen.getByRole("table")).getByRole("rowheader", { name: "srv-01" }).closest("tr") as HTMLTableRowElement;
    expect(row.querySelector('[data-label="Health"]')).toHaveTextContent("Critical");
    expect(row.querySelector('[data-label="Model"]')).toHaveTextContent("Acme Server 2U");
    await expectNoA11yViolations();
  });

  it("draws a rear-mounted device's front on the rear elevation and its rear on the front (#134)", async () => {
    const SW = "acme-sw-1u";
    const EDGE = "acme-edge-1u";
    const mounted = (n: number, name: string, fullDepth: boolean, model: string): RackItem => ({
      device: { id: dev(n), type: "device", display: name },
      position: 5 + (n - 5) * 2,
      face: "rear",
      u_height: 1,
      full_depth: fullDepth,
      role: "switch",
      front_image: `/api/v1/device-types/${model}/front`,
      rear_image: `/api/v1/device-types/${model}/rear`,
    });
    const rearItems = [mounted(5, "sw-r1", true, SW), mounted(6, "edge-r1", false, EDGE)];
    // Each bitmap says which image it is, and each canvas records the last one painted on it.
    const painted = new Map<HTMLCanvasElement, string>();
    vi.stubGlobal(
      "createImageBitmap",
      vi.fn(async (b: Blob) => ({ width: 1000, height: 180, close: () => undefined, tag: JSON.parse(await blobText(b)) as string })),
    );
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(function (this: HTMLCanvasElement) {
      return { clearRect: () => undefined, drawImage: (b: { tag: string }) => painted.set(this, b.tag) } as never;
    });
    const api = mockApi({
      ...defaultRoutes(),
      [`GET /objects/${ID.rack}`]: rack,
      [`GET /racks/${ID.rack}/contents`]: page(rearItems),
      [`GET /racks/${ID.rack}/removed`]: page([]),
      [`GET /device-types/${SW}/front`]: `front:${SW}`,
      [`GET /device-types/${SW}/rear`]: `rear:${SW}`,
      [`GET /device-types/${EDGE}/front`]: `front:${EDGE}`,
      [`GET /device-types/${EDGE}/rear`]: `rear:${EDGE}`,
    });
    renderApp({ at: `/racks/${ID.rack}` });
    await screen.findByRole("table");
    const tagOf = (el: Element | null) => painted.get(el?.querySelector("canvas") as HTMLCanvasElement);
    // The elevation it is mounted on shows its front (the ports), shallow or not.
    await waitFor(() => expect(tagOf(slot("rear", dev(5)))).toBe(`front:${SW}`));
    await waitFor(() => expect(tagOf(slot("rear", dev(6)))).toBe(`front:${EDGE}`));
    // The other elevation shows a full-depth device's rear, faded.
    await waitFor(() => expect(tagOf(faceSvg("front").querySelector(".rack-shadow:not(.rack-behind)"))).toBe(`rear:${SW}`));
    // A shallow device is only an outline from behind: no image on it, and its rear never fetched.
    const behind = faceSvg("front").querySelector(`[data-behind="${dev(6)}"]`);
    expect(behind).not.toBeNull();
    expect(behind?.querySelector("canvas")).toBeNull();
    expect(api.count(`GET /device-types/${EDGE}/rear`)).toBe(0);
  });

  it("falls back on the role's face when the image cannot be had", async () => {
    mockApi({
      ...defaultRoutes(),
      [`GET /objects/${ID.rack}`]: rack,
      [`GET /racks/${ID.rack}/contents`]: page(items),
      // No route for the images: 404.
    });
    renderApp({ at: `/racks/${ID.rack}` });
    await screen.findByRole("table");
    await waitFor(() => expect(slot("front", dev(1)).querySelector(".rack-face-server")).not.toBeNull());
    expect(slot("front", dev(1)).querySelector("canvas")).toBeNull();
  });
});

describe("a desktop device on a shelf (ADR 0068)", () => {
  const NAS = "/api/v1/device-types/acme-tower-nas/front";
  const nas = (desktop: boolean): RackItem => ({
    device: { id: dev(7), type: "device", display: "nas-01" },
    position: 5,
    face: "front",
    u_height: 4,
    full_depth: false,
    desktop,
    role: "nas",
    front_image: NAS,
  });

  beforeEach(() => {
    clearFaceImages();
    vi.stubGlobal("createImageBitmap", vi.fn(() => Promise.resolve({ width: 1000, height: 484, close: () => undefined })));
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(() => ({ clearRect: () => undefined, drawImage: () => undefined }) as never);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  const draw = async (desktop: boolean) => {
    mockApi({
      ...defaultRoutes(),
      [`GET /objects/${ID.rack}`]: rack,
      [`GET /racks/${ID.rack}/contents`]: page([nas(desktop)]),
      [`GET /racks/${ID.rack}/removed`]: page([]),
      "GET /device-types/acme-tower-nas/front": "png bytes",
    });
    renderApp({ at: `/racks/${ID.rack}` });
    await screen.findByRole("table");
    await waitFor(() => expect(slot("front", dev(7)).querySelector("foreignObject")).not.toBeNull());
    const fo = slot("front", dev(7)).querySelector("foreignObject") as SVGForeignObjectElement;
    return { w: Number(fo.getAttribute("width")), h: Number(fo.getAttribute("height")), x: Number(fo.getAttribute("x")) };
  };

  it("keeps the proportions of its face inside the shelf, centred, instead of stretching it over 4 U", async () => {
    // 4 U = 86 px high in a 278 px wide slot; a 1000 x 484 picture fits 86 high, 177.7 wide.
    const { w, h, x } = await draw(true);
    expect(h).toBeCloseTo(86, 5);
    expect(w).toBeCloseTo(86 * (1000 / 484), 3);
    expect(x).toBeGreaterThan(1 + 1);
  });

  it("still stretches the face of a rack-mounted device over its slot", async () => {
    const { w, h } = await draw(false);
    expect(w).toBe(278);
    expect(h).toBe(86);
  });

  it("paints it with its own proportions on the export sheet too", () => {
    const img = { width: 1000, height: 484 } as unknown as CanvasImageSource;
    const calls: unknown[][] = [];
    const state: Record<string, unknown> = {};
    const ctx = new Proxy(state, {
      get: (t, k) => (k in t ? t[k as string] : (...args: unknown[]) => (k === "drawImage" ? calls.push(args) : undefined)),
      set: (t, k, v) => ((t[k as string] = v), true),
    }) as unknown as Ctx;
    const layout = layoutRack(12, [{ id: "n", name: "nas-01", position: 5, face: "front", heightU: 4, desktop: true, frontImage: NAS }]);
    drawRackSheet(ctx, { title: "Rack r12", uHeight: 12, faces: [{ caption: "Front", slots: layout.front }], footer: "x", images: new Map([[NAS, img]]) });
    expect(calls).toHaveLength(1);
    const [, , , w, h] = calls[0] as number[];
    expect(h).toBeCloseTo(86, 5);
    expect(w).toBeCloseTo(86 * (1000 / 484), 3);
  });
});

describe("the export sheet", () => {
  /** A context that records the calls the faces make. */
  function recorder() {
    const calls: { op: string; args: unknown[]; fill: unknown }[] = [];
    const state: Record<string, unknown> = {};
    const ctx = new Proxy(state, {
      get: (t, k) => (k in t ? t[k as string] : (...args: unknown[]) => calls.push({ op: String(k), args, fill: t.fillStyle })),
      set: (t, k, v) => ((t[k as string] = v), true),
    }) as unknown as Ctx;
    return { ctx, calls };
  }

  it("paints the same images, faces and health as the screen", () => {
    const layout = layoutRack(
      12,
      items.map((it) => ({
        id: it.device.id,
        name: it.device.display,
        position: it.position,
        face: it.face as "front" | "rear",
        heightU: it.u_height,
        fullDepth: it.full_depth,
        role: it.role,
        health: it.health,
        partsHealth: it.parts_health,
        frontImage: it.front_image,
        rearImage: it.rear_image,
      })),
    );
    const front = { width: 1000, height: 180 } as unknown as CanvasImageSource;
    const { ctx, calls } = recorder();
    drawRackSheet(ctx, {
      title: "Rack r12",
      uHeight: 12,
      faces: [
        { caption: "Front", slots: layout.front },
        { caption: "Rear", slots: layout.rear },
      ],
      footer: "x",
      images: new Map([[IMG, front]]),
    });
    // The front image is drawn once; the rear image was not loaded, so the shadow stays a plain block.
    expect(calls.filter((c) => c.op === "drawImage").map((c) => c.args[0])).toEqual([front]);
    // The health strips: critical, warning, unknown on the front, ok for the PDU at the rear.
    const strips = calls.filter((c) => c.op === "fillRect" && c.args[2] === 5).map((c) => c.fill);
    expect(strips).toEqual(expect.arrayContaining([HEALTH_INK.critical, HEALTH_INK.warning, HEALTH_INK.unknown, HEALTH_INK.ok]));
    // Generic faces for the devices without an image: their LEDs are arcs.
    expect(calls.some((c) => c.op === "arc")).toBe(true);
    expect(calls.filter((c) => c.op === "fillText").map((c) => c.args[0])).toEqual(expect.arrayContaining(["srv-01  U10-U11", "sw-01  U12"]));
  });

  it("paints a rear-mounted device's front on the rear sheet and its rear on the front (#134)", () => {
    const SW_F = "/api/v1/device-types/acme-sw-1u/front";
    const SW_R = "/api/v1/device-types/acme-sw-1u/rear";
    const F = { tag: "front" } as unknown as CanvasImageSource;
    const R = { tag: "rear" } as unknown as CanvasImageSource;
    const layout = layoutRack(12, [{ id: "sw", name: "sw-r1", position: 5, face: "rear", heightU: 1, fullDepth: true, frontImage: SW_F, rearImage: SW_R }]);
    const { ctx, calls } = recorder();
    drawRackSheet(ctx, {
      title: "Rack r12",
      uHeight: 12,
      faces: [
        { caption: "Front", slots: layout.front },
        { caption: "Rear", slots: layout.rear },
      ],
      footer: "x",
      images: new Map([
        [SW_F, F],
        [SW_R, R],
      ]),
    });
    // The front sheet is drawn first and holds the shadow, the rear image; the rear sheet holds the front image.
    expect(calls.filter((c) => c.op === "drawImage").map((c) => c.args[0])).toEqual([R, F]);
  });
});

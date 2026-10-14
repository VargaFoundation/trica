import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Providers } from "../App";
import type { Slot } from "../racks/layout";
import { blobBytes, fileSlug } from "./download";
import { ExportButtons } from "./ExportButtons";
import { jpegInfo, MAX_PAGE_PT, pdfFromJpeg, pdfTitle } from "./pdf";
import { drawRackSheet, rackRaster, SHEET, sheetSize, type Ctx, type RackSheet } from "./rackCanvas";

/** The smallest byte string jpegInfo accepts: start of image, a quantisation table to skip, start of frame. */
function fakeJpeg(width: number, height: number, components = 3, before: number[] = []): Uint8Array {
  const comps = components === 1 ? [1, 0x11, 0] : [1, 0x22, 0, 2, 0x11, 1, 3, 0x11, 1];
  const len = 8 + comps.length;
  return new Uint8Array([
    0xff, 0xd8,
    ...before,
    0xff, 0xc0, len >> 8, len & 0xff, 8, height >> 8, height & 0xff, width >> 8, width & 0xff, components, ...comps,
    0xff, 0xd9,
  ]);
}

const latin1 = (b: Uint8Array) => new TextDecoder("latin1").decode(b);

describe("jpegInfo", () => {
  it("reads the size and the number of components", () => {
    expect(jpegInfo(fakeJpeg(1280, 720))).toEqual({ width: 1280, height: 720, components: 3 });
    expect(jpegInfo(fakeJpeg(40, 30, 1))).toEqual({ width: 40, height: 30, components: 1 });
  });

  it("skips the segments before the frame, a Huffman table included", () => {
    // APP0 of 6 bytes (length 6), then DHT (0xc4, length 5): its marker is in the 0xc0-0xcf range but is not a frame.
    const before = [0xff, 0xe0, 0, 6, 1, 2, 3, 4, 0xff, 0xc4, 0, 5, 9, 9, 9];
    expect(jpegInfo(fakeJpeg(321, 123, 3, before))).toMatchObject({ width: 321, height: 123 });
  });

  it("refuses what is not a JPEG", () => {
    expect(() => jpegInfo(new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0, 0, 0, 0]))).toThrow("not a JPEG");
    expect(() => jpegInfo(new Uint8Array([]))).toThrow("not a JPEG");
  });

  it("refuses a JPEG with no frame header, a zero size or four components", () => {
    expect(() => jpegInfo(new Uint8Array([0xff, 0xd8, 0xff, 0xd9, 0, 0, 0, 0, 0, 0, 0, 0]))).toThrow("no frame header");
    expect(() => jpegInfo(fakeJpeg(0, 10))).toThrow("unsupported");
    const cmyk = fakeJpeg(10, 10);
    cmyk[11] = 4; // number of components, in the frame header
    expect(() => jpegInfo(cmyk)).toThrow("unsupported");
  });
});

describe("pdfFromJpeg", () => {
  const jpeg = fakeJpeg(1000, 500);
  const pdf = pdfFromJpeg(jpeg, { title: "Rack r12", widthPt: 300 });
  const text = latin1(pdf);

  it("is a PDF: header, end marker", () => {
    expect(text.startsWith("%PDF-1.4\n")).toBe(true);
    expect(text.endsWith("%%EOF\n")).toBe(true);
  });

  it("has one page, as wide as asked and as tall as the picture", () => {
    expect(text).toContain("/Count 1");
    expect(text).toContain("/MediaBox [0 0 300 150]");
    expect(text).toContain("300 0 0 150 0 0 cm /Im0 Do");
  });

  it("embeds the JPEG untouched, and says its size and length", () => {
    expect(text).toContain("/Width 1000 /Height 500");
    expect(text).toContain("/Filter /DCTDecode");
    expect(text).toContain(`/Length ${jpeg.length} >>\nstream\n`);
    const start = text.indexOf("stream\n", text.indexOf("/DCTDecode")) + "stream\n".length;
    expect(Array.from(pdf.slice(start, start + jpeg.length))).toEqual(Array.from(jpeg));
    expect(text.slice(start + jpeg.length, start + jpeg.length + 14)).toBe("\nendstream\nend");
  });

  it("has a cross-reference table whose offsets point at the objects", () => {
    const startxref = Number(/startxref\n(\d+)\n%%EOF/.exec(text)?.[1]);
    expect(text.slice(startxref, startxref + 4)).toBe("xref");
    const entries = text.slice(startxref).split("\n").slice(3, 9); // after "xref", "0 7" and the free entry
    expect(entries).toHaveLength(6);
    entries.forEach((e, i) => {
      expect(e).toMatch(/^\d{10} 00000 n $/);
      const offset = Number(e.slice(0, 10));
      expect(text.slice(offset, offset + `${i + 1} 0 obj`.length)).toBe(`${i + 1} 0 obj`);
    });
    expect(text).toContain("/Size 7");
  });

  it("stores the title as UTF-16, whatever it holds", () => {
    expect(pdfTitle("Aé")).toBe("<FEFF004100E9>");
    expect(text).toContain(`/Title ${pdfTitle("Rack r12")}`);
    expect(latin1(pdfFromJpeg(jpeg, { title: "a) /Root (b \\", widthPt: 100 }))).not.toContain("a) /Root");
  });

  it("uses a grey colour space for a grey JPEG", () => {
    expect(latin1(pdfFromJpeg(fakeJpeg(10, 10, 1), { title: "t", widthPt: 100 }))).toContain("/ColorSpace /DeviceGray");
    expect(text).toContain("/ColorSpace /DeviceRGB");
  });

  it("keeps a page inside what viewers accept", () => {
    const big = latin1(pdfFromJpeg(fakeJpeg(100, 400), { title: "t", widthPt: 20000 }));
    const box = /MediaBox \[0 0 ([\d.]+) ([\d.]+)\]/.exec(big);
    expect(Number(box?.[2])).toBeLessThanOrEqual(MAX_PAGE_PT);
    expect(Number(box?.[1])).toBeCloseTo(Number(box?.[2]) / 4, 1);
  });
});

describe("fileSlug", () => {
  it("keeps names usable as file names", () => {
    expect(fileSlug("r12")).toBe("r12");
    expect(fileSlug("Rack A/1 (Paris)")).toBe("rack-a-1-paris");
    expect(fileSlug("../../etc")).toBe("etc");
    expect(fileSlug("///")).toBe("export");
  });
});

// ---- the canvas ----

const slot = (name: string, bottom: number, height: number, o: Partial<Slot> = {}): Slot => ({
  item: { id: `id-${name}`, name },
  face: "front",
  shadow: false,
  bottom,
  height,
  top: bottom + height - 1,
  issues: [],
  overlaps: [],
  ...o,
});

interface Rec {
  calls: [string, ...unknown[]][];
  ctx: Ctx;
}

function recorder(): Rec {
  const calls: Rec["calls"] = [];
  const state = { fillStyle: "", strokeStyle: "", lineWidth: 1, font: "", textAlign: "left" as CanvasTextAlign };
  const rec =
    (name: string) =>
    (...a: unknown[]) => {
      calls.push([name, ...a, { fill: state.fillStyle, stroke: state.strokeStyle, width: state.lineWidth, font: state.font }]);
    };
  const ctx = Object.assign(state, {
    fillRect: rec("fillRect"),
    strokeRect: rec("strokeRect"),
    beginPath: rec("beginPath"),
    moveTo: rec("moveTo"),
    lineTo: rec("lineTo"),
    stroke: rec("stroke"),
    fillText: rec("fillText"),
    setLineDash: rec("setLineDash"),
    save: rec("save"),
    restore: rec("restore"),
    translate: rec("translate"),
  }) as unknown as Ctx;
  return { calls, ctx };
}

const sheet = (front: Slot[], rear: Slot[] = [], uHeight = 4): RackSheet => ({
  title: "Rack r12",
  uHeight,
  faces: [
    { caption: "Front", slots: front },
    { caption: "Rear", slots: rear },
  ],
  footer: "Exported from trica on 2026-10-14",
});

type Meta = { fill: string; stroke: string; width: number; font: string };
const texts = (r: Rec) => r.calls.filter((c) => c[0] === "fillText").map((c) => c[1] as string);
const meta = (c: Rec["calls"][number]) => c[c.length - 1] as Meta;

describe("the rack sheet", () => {
  it("is white, as big as its two faces, with the title, both captions and the footer", () => {
    const s = sheet([slot("sw-a1", 2, 1)]);
    const r = recorder();
    drawRackSheet(r.ctx, s);
    const { width, height } = sheetSize(s);
    expect(r.calls[0]?.slice(0, 5)).toEqual(["fillRect", 0, 0, width, height]);
    expect(meta(r.calls[0] as Rec["calls"][number]).fill).toBe(SHEET.paper);
    expect(texts(r)).toEqual(expect.arrayContaining(["Rack r12", "Front", "Rear", "Exported from trica on 2026-10-14"]));
  });

  it("grows with the rack and with the number of faces", () => {
    const one = sheetSize({ ...sheet([]), uHeight: 10 });
    const two = sheetSize({ ...sheet([]), uHeight: 20 });
    expect(two.height).toBeGreaterThan(one.height);
    expect(sheetSize({ ...sheet([]), faces: [{ caption: "Front", slots: [] }] }).width).toBeLessThan(one.width);
  });

  it("numbers every unit and draws a rule under each, on each face", () => {
    const r = recorder();
    drawRackSheet(r.ctx, sheet([], [], 5));
    const nums = texts(r).filter((t) => /^\d+$/.test(t));
    expect(nums).toEqual(["1", "2", "3", "4", "5", "1", "2", "3", "4", "5"]);
    expect(r.calls.filter((c) => c[0] === "lineTo")).toHaveLength(10);
  });

  it("draws the second face beside the first, not on top of it", () => {
    const r = recorder();
    drawRackSheet(r.ctx, sheet([]));
    const shifts = r.calls.filter((c) => c[0] === "translate" && (c[1] as number) !== 0).map((c) => c[1] as number);
    expect(shifts).toHaveLength(2);
    expect(shifts[1]).toBeGreaterThan(shifts[0] as number);
  });

  it("names a device with its position", () => {
    const r = recorder();
    drawRackSheet(r.ctx, sheet([slot("sw-a1", 2, 2)]));
    expect(texts(r)).toContain("sw-a1  U2-U3");
  });

  it("puts a device at its height: U1 is the bottom, so a higher unit is drawn higher", () => {
    const r = recorder();
    drawRackSheet(r.ctx, sheet([slot("low", 1, 1), slot("high", 4, 1)]));
    const blocks = r.calls.filter((c) => c[0] === "strokeRect" && (meta(c).stroke === SHEET.slotInk));
    expect(blocks).toHaveLength(2);
    expect(blocks[0]?.[2] as number).toBeGreaterThan(blocks[1]?.[2] as number); // y of "low" is below "high"
  });

  it("draws the other face of a full-depth device dashed, and a problem heavier and red", () => {
    const r = recorder();
    drawRackSheet(
      r.ctx,
      sheet([slot("shadowed", 1, 1, { shadow: true }), slot("clash", 3, 1, { issues: ["overlap"] }), slot("assumed", 4, 1, { issues: ["assumedHeight"] }), slot("stray", 5, 1, { issues: ["outOfRange"] })]),
    );
    const dash = r.calls.findIndex((c) => c[0] === "setLineDash" && (c[1] as number[]).length === 2);
    expect(dash).toBeGreaterThan(-1);
    expect(r.calls[dash]?.[1]).toEqual([4, 3]);
    expect(meta(r.calls[dash + 1] ?? ["x"]).stroke).toBe(SHEET.shadowInk);
    const clash = r.calls.filter((c) => c[0] === "strokeRect" && meta(c).stroke === SHEET.problemInk);
    // Two problems: an overlap and a device outside the rack's units.
    expect(clash).toHaveLength(2);
    expect(meta(clash[0] as Rec["calls"][number]).width).toBe(2);
    expect(texts(r)).toContain("! clash  U3");
    expect(texts(r)).toContain("! stray  U5");
    // Only a problem is flagged: an assumed height is a note, not an error.
    expect(texts(r).some((t) => t.startsWith("assumed"))).toBe(true);
    expect(texts(r)).not.toContain("! assumed  U4");
  });

  it("cuts a name that would run out of the block", () => {
    const r = recorder();
    drawRackSheet(r.ctx, sheet([slot("x".repeat(80), 1, 1)]));
    const t = texts(r).find((x) => x.startsWith("xxx")) ?? "";
    expect(t).toContain("…");
    expect(t.length).toBeLessThan(80);
  });
});

describe("rackRaster", () => {
  afterEach(() => vi.restoreAllMocks());

  it("encodes the canvas in the format asked for, at twice the sheet's size", async () => {
    const r = recorder();
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(() => Object.assign(r.ctx, { scale: vi.fn() }) as never);
    const asked: (string | undefined)[] = [];
    vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation((cb, type) => {
      asked.push(type);
      cb(new Blob(["x"], { type }));
    });
    const s = sheet([slot("sw-a1", 1, 1)]);
    const png = await rackRaster(s, "png");
    const jpg = await rackRaster(s, "jpeg");
    expect(asked).toEqual(["image/png", "image/jpeg"]);
    const { width, height } = sheetSize(s);
    expect(png).toMatchObject({ width: width * 2, height: height * 2, ratio: 2 });
    expect(jpg.ratio).toBe(2);
    expect(texts(r)).toContain("sw-a1  U1");
  });

  it("fails clearly when the browser has no canvas or cannot encode", async () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
    await expect(rackRaster(sheet([]), "png")).rejects.toThrow("canvas is not available");
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(() => Object.assign(recorder().ctx, { scale: vi.fn() }) as never);
    vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation((cb) => cb(null));
    await expect(rackRaster(sheet([]), "png")).rejects.toThrow("could not encode");
  });
});

// ---- the buttons ----

describe("ExportButtons", () => {
  const saved: { name: string; href: string }[] = [];
  const blobs: Blob[] = [];

  beforeEach(() => {
    saved.length = 0;
    blobs.length = 0;
    URL.createObjectURL = vi.fn((b: Blob) => {
      blobs.push(b);
      return `blob:test/${blobs.length}`;
    });
    URL.revokeObjectURL = vi.fn();
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      saved.push({ name: this.download, href: this.href });
    });
  });
  afterEach(() => vi.restoreAllMocks());

  const raster = (w = 1000, h = 500) => ({ blob: new Blob([fakeJpeg(w, h) as BlobPart], { type: "image/jpeg" }), width: w, height: h, ratio: 2 });
  const show = (renderFn: Parameters<typeof ExportButtons>[0]["render"]) =>
    render(
      <Providers locale="en">
        <ExportButtons basename="trica-rack-r12" title="Rack r12" render={renderFn} />
      </Providers>,
    );

  it("saves a PNG under its name", async () => {
    const renderFn = vi.fn(() => Promise.resolve({ blob: new Blob(["png"], { type: "image/png" }), width: 10, height: 10, ratio: 1 }));
    show(renderFn);
    await userEvent.setup().click(screen.getByRole("button", { name: "Export PNG" }));
    await waitFor(() => expect(saved).toHaveLength(1));
    expect(renderFn).toHaveBeenCalledExactlyOnceWith("png");
    expect(saved[0]).toEqual({ name: "trica-rack-r12.png", href: "blob:test/1" });
    expect(blobs[0]?.type).toBe("image/png");
    expect(await screen.findByText("PNG file saved.")).toBeDefined();
  });

  it("saves a PDF built from a JPEG, a page as wide as the picture is in print points", async () => {
    const renderFn = vi.fn(() => Promise.resolve(raster(1000, 500)));
    show(renderFn);
    await userEvent.setup().click(screen.getByRole("button", { name: "Export PDF" }));
    await waitFor(() => expect(saved).toHaveLength(1));
    expect(renderFn).toHaveBeenCalledExactlyOnceWith("jpeg");
    expect(saved[0]?.name).toBe("trica-rack-r12.pdf");
    const pdf = blobs[0] as Blob;
    expect(pdf.type).toBe("application/pdf");
    const text = latin1(await blobBytes(pdf));
    expect(text.startsWith("%PDF-1.4")).toBe(true);
    // 1000 px drawn at ratio 2 are 500 CSS px, which are 375 pt.
    expect(text).toContain("/MediaBox [0 0 375 187.5]");
    expect(await screen.findByText("PDF file saved.")).toBeDefined();
  });

  it("says what failed, and can be tried again", async () => {
    const renderFn = vi.fn().mockRejectedValueOnce(new Error("canvas is full")).mockResolvedValue(raster());
    show(renderFn);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Export PNG" }));
    expect(await screen.findByText("The PNG export failed: canvas is full")).toBeDefined();
    expect(saved).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "Export PDF" }));
    await waitFor(() => expect(saved).toHaveLength(1));
    expect(screen.queryByText(/failed/)).toBeNull();
  });

  it("says a PDF failed when the picture is not a JPEG", async () => {
    show(() => Promise.resolve({ blob: new Blob(["not an image"]), width: 10, height: 10, ratio: 1 }));
    await userEvent.setup().click(screen.getByRole("button", { name: "Export PDF" }));
    expect(await screen.findByText("The PDF export failed: not a JPEG")).toBeDefined();
    expect(saved).toHaveLength(0);
  });

  it("is busy while it draws: the buttons are off, a status says so", async () => {
    let finish: (r: ReturnType<typeof raster>) => void = () => undefined;
    show(() => new Promise((res) => (finish = res)));
    await userEvent.setup().click(screen.getByRole("button", { name: "Export PDF" }));
    expect(await screen.findByText("Preparing the file…")).toBeDefined();
    expect((screen.getByRole("button", { name: "Export PNG" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Export PDF" }) as HTMLButtonElement).disabled).toBe(true);
    finish(raster());
    await waitFor(() => expect((screen.getByRole("button", { name: "Export PNG" }) as HTMLButtonElement).disabled).toBe(false));
  });
});

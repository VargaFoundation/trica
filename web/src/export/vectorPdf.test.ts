import { describe, expect, it } from "vitest";
import { MAX_PAGE_PT, pdfTitle } from "./pdf";
import { latin1Text, pdfFromVector, pdfString, textWidth, type Figure } from "./vectorPdf";

const latin1 = (b: Uint8Array) => new TextDecoder("latin1").decode(b);

const solid = { color: "#1f2328", width: 2 };
const figure: Figure = {
  width: 400,
  height: 200,
  shapes: [
    { kind: "line", from: { x: 20, y: 20 }, to: { x: 380, y: 20 }, stroke: solid },
    { kind: "line", from: { x: 20, y: 100 }, to: { x: 380, y: 100 }, via: { x: 200, y: 180 }, stroke: { color: "#9a5b00", width: 2, dash: [6, 4] } },
    { kind: "box", x: 100, y: 40, w: 80, h: 26, radius: 6, fill: "#ddf4ff", stroke: { color: "#0550ae", width: 1.5 } },
    { kind: "text", at: { x: 140, y: 53 }, text: "sw-a1", size: 12, color: "#1f2328" },
    { kind: "text", at: { x: 200, y: 140 }, text: "deduced, high", size: 10, color: "#1f2328", background: "#ffffff" },
  ],
};
const pdf = pdfFromVector(figure, { title: "Topology" });
const text = latin1(pdf);

describe("pdfFromVector", () => {
  it("is a PDF of one page, as large as the figure is in print points", () => {
    expect(text.startsWith("%PDF-1.4\n")).toBe(true);
    expect(text.endsWith("%%EOF\n")).toBe(true);
    expect(text).toContain("/Count 1");
    // 400 x 200 CSS px are 300 x 150 pt.
    expect(text).toContain("/MediaBox [0 0 300 150]");
    expect(text).toContain(`/Title ${pdfTitle("Topology")}`);
  });

  it("holds no picture: the text is text, in a font every viewer has", () => {
    expect(text).not.toContain("/XObject");
    expect(text).not.toContain("/DCTDecode");
    expect(text).toContain("/BaseFont /Helvetica /Encoding /WinAnsiEncoding");
    expect(text).toContain("(sw-a1) Tj");
    expect(text).toContain("(deduced, high) Tj");
  });

  it("has a cross-reference table whose offsets point at the objects", () => {
    const startxref = Number(/startxref\n(\d+)\n%%EOF/.exec(text)?.[1]);
    expect(text.slice(startxref, startxref + 4)).toBe("xref");
    const entries = text.slice(startxref).split("\n").slice(3, 9);
    entries.forEach((e, i) => {
      const offset = Number(e.slice(0, 10));
      expect(text.slice(offset, offset + `${i + 1} 0 obj`.length)).toBe(`${i + 1} 0 obj`);
    });
    expect(text).toContain("/Size 7");
    // The stream's length is its length in bytes.
    const len = Number(/<< \/Length (\d+) >>\nstream\n/.exec(text)?.[1]);
    const start = text.indexOf("stream\n") + "stream\n".length;
    expect(text.slice(start + len, start + len + 10)).toBe("\nendstream");
  });

  it("turns the page's y upwards: what is at the top of the figure is at the top of the page", () => {
    // The first line, 20 px from the top, is 15 pt from the top of a 150 pt page.
    expect(text).toContain("15 135 m 285 135 l S");
  });

  it("draws a curve through its control point, a dash when asked, and a solid line otherwise", () => {
    // From (15,75) to (285,75) bent towards (150,15): cubic control points two thirds of the way.
    expect(text).toContain("15 75 m 105 35 195 35 285 75 c S");
    expect(text).toContain("[4.5 3] 0 d");
    expect(text).toMatch(/1\.5 w \[\] 0 d/);
  });

  it("fills and outlines a box with rounded corners, in the colours given", () => {
    expect(text).toContain("0.87 0.96 1 rg");
    expect(text).toContain("0.02 0.31 0.68 RG");
    expect(text).toMatch(/h B/);
  });

  it("centres the text on its point, with the font's own widths", () => {
    // "sw-a1" at 12 px: (500+722+333+556+556)/1000*12 = 32 px wide, so it starts 16 px left of the centre.
    expect(textWidth("sw-a1", 12)).toBeCloseTo(32.004, 3);
    // x: (140 - 16) px = 93 pt; y: the baseline 0.359 of the size below the centre, (53 + 4.31) px from the top = 107.02 pt from the bottom.
    expect(text).toContain("BT /F1 9 Tf 0.12 0.14 0.16 rg 93 107.02 Td (sw-a1) Tj ET");
  });

  it("puts a label on its background, the background first", () => {
    const bg = text.indexOf("1 1 1 rg");
    expect(bg).toBeGreaterThan(-1);
    expect(bg).toBeLessThan(text.indexOf("(deduced, high) Tj"));
  });

  it("escapes what a PDF string gives a meaning to, and writes '?' for what the font cannot", () => {
    expect(pdfString("a(b)c\\")).toBe("(a\\(b\\)c\\\\)");
    expect(latin1Text("é × ✓ 日")).toBe("é × ? ?");
    expect(latin1(pdfFromVector({ width: 10, height: 10, shapes: [{ kind: "text", at: { x: 5, y: 5 }, text: "a) Tj (b", size: 10, color: "#000000" }] }, { title: "t" }))).toContain(
      "(a\\) Tj \\(b) Tj",
    );
  });

  it("writes the Latin-1 characters it keeps as one byte each", () => {
    const one = pdfFromVector({ width: 10, height: 10, shapes: [{ kind: "text", at: { x: 5, y: 5 }, text: "é", size: 10, color: "#000000" }] }, { title: "t" });
    const at = latin1(one).indexOf("(é) Tj");
    expect(at).toBeGreaterThan(-1);
    expect(one[at + 1]).toBe(0xe9);
  });

  it("keeps a page inside what viewers accept", () => {
    const big = latin1(pdfFromVector({ width: 40000, height: 10000, shapes: [] }, { title: "t" }));
    const box = /MediaBox \[0 0 ([\d.]+) ([\d.]+)\]/.exec(big);
    expect(Number(box?.[1])).toBeLessThanOrEqual(MAX_PAGE_PT);
    expect(Number(box?.[2])).toBeCloseTo(MAX_PAGE_PT / 4, 0);
  });

  it("refuses a colour it cannot write", () => {
    expect(() => pdfFromVector({ width: 10, height: 10, shapes: [{ kind: "text", at: { x: 0, y: 0 }, text: "a", size: 1, color: "red" }] }, { title: "t" })).toThrow(
      "not a colour: red",
    );
  });
});

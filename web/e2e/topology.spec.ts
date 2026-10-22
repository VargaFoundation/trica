import { readFileSync } from "node:fs";
import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { expectAccessible, ID, oid, signInWithToken } from "./support";

// The topology page and the exports, in a real browser, behind the real CSP
// (script-src 'self', style-src 'self': cytoscape, ELK and the canvas
// exports must work without loosening it). The mock inventory holds a small
// network in paris-1 and lyon-1 with every kind of link, and a synthetic
// leaf-spine fabric that brings the picture to 200 linked devices.

const RACK_LYON = oid(6);
const VLAN_10 = oid(0x30);
const SW_L1 = oid(0xe1);
const SITE_LYON = oid(5);

/** Content-security-policy violations the page reports, collected from the first script on. */
async function watchCsp(page: Page): Promise<() => Promise<string[]>> {
  await page.addInitScript(() => {
    const w = window as unknown as { __csp: string[] };
    w.__csp = [];
    document.addEventListener("securitypolicyviolation", (e) =>
      w.__csp.push(`${e.violatedDirective} ${e.blockedURI}`),
    );
  });
  const consoleErrors: string[] = [];
  page.on("console", (m) => {
    if (
      m.type() === "error" &&
      /Content Security Policy|Refused to/i.test(m.text())
    )
      consoleErrors.push(m.text());
  });
  return async () => [
    ...consoleErrors,
    ...(await page.evaluate(
      () => (window as unknown as { __csp: string[] }).__csp,
    )),
  ];
}

// The 200-device picture is budgeted by the benchmark below (8 s), not by the
// functional tests: they wait as long as the benchmark does, not Playwright's 5 s.
const LAYOUT_200 = { timeout: 30_000 };

const ready = (page: Page, nodes?: number) =>
  page.locator(
    `.topology-canvas[data-state="ready"]${nodes === undefined ? "" : `[data-nodes="${nodes}"]`}`,
  );

async function downloadOf(
  page: Page,
  button: string,
): Promise<{ name: string; bytes: Buffer }> {
  const [d] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", { name: button }).click(),
  ]);
  const path = await d.path();
  return { name: d.suggestedFilename(), bytes: readFileSync(path) };
}

/** Width, height and the number of non-white pixels of a PNG, decoded in a blank page (the app's CSP forbids data: images). */
async function inspectPng(
  context: BrowserContext,
  png: Buffer,
): Promise<{
  width: number;
  height: number;
  inked: number;
  opaque: number;
  colours: number;
}> {
  const blank = await context.newPage();
  try {
    await blank.goto("about:blank");
    return await blank.evaluate(async (b64) => {
      const img = new Image();
      img.src = `data:image/png;base64,${b64}`;
      await img.decode();
      const c = document.createElement("canvas");
      c.width = img.width;
      c.height = img.height;
      const g = c.getContext("2d");
      if (!g) throw new Error("no 2d context");
      g.drawImage(img, 0, 0);
      const d = g.getImageData(0, 0, c.width, c.height).data;
      let inked = 0;
      let opaque = 0;
      const seen = new Set<number>();
      for (let i = 0; i < d.length; i += 4) {
        if (d[i + 3] === 255) opaque++;
        const r = d[i] ?? 0;
        const gr = d[i + 1] ?? 0;
        const b = d[i + 2] ?? 0;
        if (r < 240 || gr < 240 || b < 240) {
          inked++;
          if (seen.size < 64) seen.add((r << 16) | (gr << 8) | b);
        }
      }
      return {
        width: img.width,
        height: img.height,
        inked,
        opaque,
        colours: seen.size,
      };
    }, png.toString("base64"));
  } finally {
    await blank.close();
  }
}

const dims = (png: Buffer) => ({
  width: png.readUInt32BE(16),
  height: png.readUInt32BE(20),
});
const PNG_SIGNATURE = Buffer.from([
  0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
]);

/** A one-page PDF: a picture (the rack sheet) or lines and text (the topology). */
function expectPdf(bytes: Buffer, kind: "picture" | "vector"): string {
  const text = bytes.toString("latin1");
  expect(text.startsWith("%PDF-1.")).toBe(true);
  expect(text.trimEnd().endsWith("%%EOF")).toBe(true);
  expect(text).toMatch(/\/MediaBox \[0 0 \d+(\.\d+)? \d+(\.\d+)?\]/);
  if (kind === "picture") {
    expect(text).toContain("/DCTDecode");
  } else {
    expect(text).not.toContain("/DCTDecode");
    expect(text).toContain("/BaseFont /Helvetica");
  }
  return text;
}

test("the topology shows every kind of link, apart, and is accessible", async ({
  page,
}) => {
  const csp = await watchCsp(page);
  await signInWithToken(page, `/topology?vlan=${VLAN_10}`);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Topology");
  await expect(ready(page, 3)).toBeVisible();
  await expect(page.locator("p.summary")).toHaveText("2 links · 3 devices");
  const rows = page.getByRole("table").locator("tbody tr");
  await expect(rows).toHaveCount(2);
  // The declared cable names its cable; the deduced one says how sure the deducer is, and on what evidence.
  await expect(
    rows.filter({ has: page.getByRole("link", { name: "C-0001" }) }),
  ).toHaveAttribute("data-class", "declared");
  const deduced = page.locator('tbody tr[data-class="deduced"]');
  await expect(deduced).toContainText("deduced, high");
  await expect(deduced).toContainText("(LLDP)");
  await expect(
    page.getByRole("list", { name: "Legend" }).getByRole("listitem"),
  ).toHaveCount(3);
  await expectAccessible(page);
  expect(await csp()).toEqual([]);
});

test("the French topology page is accessible too", async ({
  browser,
  baseURL,
}) => {
  const context = await browser.newContext({
    locale: "fr-FR",
    ignoreHTTPSErrors: true,
    baseURL,
  });
  const page = await context.newPage();
  await signInWithToken(page, `/topology?rack=${RACK_LYON}`);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Topologie");
  await expect(ready(page)).toBeVisible();
  await expectAccessible(page);
  await context.close();
});

test("the filters narrow the picture and the address keeps them", async ({
  page,
}) => {
  await signInWithToken(page, "/topology?view=network");
  await expect(ready(page, 200)).toBeVisible(LAYOUT_200);
  const summary = page.locator("p.summary");
  await page.getByLabel("Rack").selectOption({ label: "rl1" });
  await expect(ready(page, 3)).toBeVisible();
  await expect(summary).toHaveText("2 links · 3 devices");
  await expect(page).toHaveURL(new RegExp(`rack=${RACK_LYON}`));
  // Take the deduced links away: the cross-site link goes, the observed cable in Lyon stays.
  await page.getByRole("checkbox", { name: "Deduced" }).click();
  await expect(ready(page, 2)).toBeVisible();
  await expect(summary).toHaveText("1 link · 2 devices");
  await expect(
    page.getByRole("checkbox", { name: "Deduced" }),
  ).not.toBeChecked();
  await page.reload();
  await expect(ready(page, 2)).toBeVisible();
  await page.getByLabel("Rack").selectOption("");
  await page.getByLabel("Site").selectOption({ label: "lyon-1" });
  await expect(page).toHaveURL(new RegExp(`site=${SITE_LYON}`));
  await page.getByLabel("VLAN").selectOption({ label: "vlan-20" });
  // vlan-20 holds one port, on a deduced link: with deduced links hidden nothing is left, and the page says why.
  await expect(summary).toHaveText("0 links · 0 devices");
  await expect(page.getByText("No link matches these filters.")).toBeVisible();
  await page.getByRole("checkbox", { name: "Deduced" }).click();
  await expect(summary).toHaveText("1 link · 2 devices");
  // A value that matches nothing is ignored, not obeyed.
  await page.goto(`/topology?site=nowhere&classes=nonsense`);
  await expect(ready(page, 200)).toBeVisible(LAYOUT_200);
});

test("the table of links is the keyboard way in: every device and port opens its page", async ({
  page,
}) => {
  await signInWithToken(page, `/topology?rack=${RACK_LYON}`);
  await expect(ready(page, 3)).toBeVisible();
  const link = page
    .getByRole("table")
    .getByRole("link", { name: "sw-l1" })
    .first();
  await link.focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(new RegExp(`/objects/${SW_L1}$`));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("sw-l1");
});

test("the topology exports a PNG and a PDF under the page's policy", async ({
  page,
  context,
}) => {
  const csp = await watchCsp(page);
  await signInWithToken(page, `/topology?vlan=${VLAN_10}`);
  await expect(ready(page, 3)).toBeVisible();

  const png = await downloadOf(page, "Export PNG");
  expect(png.name).toMatch(/^trica-topology-\d{4}-\d{2}-\d{2}\.png$/);
  expect(png.bytes.subarray(0, 8).equals(PNG_SIGNATURE)).toBe(true);
  const seen = await inspectPng(context, png.bytes);
  expect(dims(png.bytes)).toEqual({ width: seen.width, height: seen.height });
  expect(seen.width).toBeGreaterThan(100);
  expect(seen.height).toBeGreaterThan(100);
  // Not a blank sheet: the nodes and the three kinds of edge are inked, in more than one colour.
  expect(seen.inked).toBeGreaterThan(2000);
  expect(seen.colours).toBeGreaterThan(3);
  // On white paper, every pixel opaque: a transparent background would print black or disappear in a dark viewer.
  expect(seen.opaque).toBe(seen.width * seen.height);
  await expect(page.getByText("PNG file saved.")).toBeVisible();

  const pdf = await downloadOf(page, "Export PDF");
  expect(pdf.name).toMatch(/^trica-topology-\d{4}-\d{2}-\d{2}\.pdf$/);
  // The labels are text in the file, so they can be selected and searched.
  const drawn = expectPdf(pdf.bytes, "vector");
  expect(drawn).toContain("(deduced, high) Tj");
  expect((drawn.match(/ re f|h B/g) ?? []).length).toBeGreaterThanOrEqual(3);
  await expect(page.getByText("PDF file saved.")).toBeVisible();
  expect(await csp()).toEqual([]);
});

test("the PNG has the same size on a high-density screen", async ({
  browser,
  browserName,
}) => {
  // Playwright's Firefox kept devicePixelRatio at 1 for a context asked for 2
  // (seen with Firefox 1543), so the comparison would prove nothing there.
  test.skip(browserName === "firefox", "the device scale factor does not apply");
  // Cytoscape multiplies an export's scale by the screen's pixel ratio; the
  // page asks for a size instead, so a Retina display does not get four times
  // the pixels (which Safari refuses for the 200-device picture).
  const sizes: { width: number; height: number }[] = [];
  for (const deviceScaleFactor of [1, 2]) {
    const context = await browser.newContext({
      deviceScaleFactor,
      ignoreHTTPSErrors: true,
    });
    const page = await context.newPage();
    await signInWithToken(page, `/topology?vlan=${VLAN_10}`);
    await expect(ready(page, 3)).toBeVisible();
    expect(await page.evaluate(() => window.devicePixelRatio)).toBe(
      deviceScaleFactor,
    );
    sizes.push(dims((await downloadOf(page, "Export PNG")).bytes));
    await context.close();
  }
  expect(sizes[1]).toEqual(sizes[0]);
});

test("the rack elevation exports a PNG and a PDF under the page's policy", async ({
  page,
  context,
}) => {
  const csp = await watchCsp(page);
  await signInWithToken(page, `/racks/${ID.r12}`);
  await expect(page.locator('svg[data-face="front"] a.rack-slot')).toHaveCount(
    3,
  );

  const started = Date.now();
  const png = await downloadOf(page, "Export PNG");
  const ms = Date.now() - started;
  test
    .info()
    .annotations.push({ type: "rack-export-ms", description: String(ms) });
  console.log(`rack r12 PNG export, click to file: ${ms} ms`);
  expect(png.name).toBe("trica-rack-r12.png");
  expect(png.bytes.subarray(0, 8).equals(PNG_SIGNATURE)).toBe(true);
  const seen = await inspectPng(context, png.bytes);
  // A 42 U rack, front and rear side by side, drawn at twice the screen resolution.
  expect(seen.height).toBeGreaterThan(42 * 8);
  expect(seen.width).toBeGreaterThan(seen.height / 4);
  expect(seen.inked).toBeGreaterThan(5000);
  expect(seen.colours).toBeGreaterThan(3);

  const pdf = await downloadOf(page, "Export PDF");
  expect(pdf.name).toBe("trica-rack-r12.pdf");
  expectPdf(pdf.bytes, "picture");
  expect(await csp()).toEqual([]);
});

test("a 200-device topology is laid out and drawn in a few seconds", async ({
  page,
}) => {
  const csp = await watchCsp(page);
  await signInWithToken(page, "/");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Inventory");
  // From the click on the menu to the last node placed: the bulk reads, the model, the ELK layout, the paint.
  const started = Date.now();
  await page.getByRole("link", { name: "Topology" }).click();
  // The map is the default view; the graph is the Network tab.
  await page.getByRole("tab", { name: "Network" }).click();
  const canvas = ready(page, 200);
  await expect(canvas).toBeVisible(LAYOUT_200);
  const total = Date.now() - started;
  const layout = Number(await canvas.getAttribute("data-layout-ms"));
  const edges = Number(await canvas.getAttribute("data-edges"));
  test.info().annotations.push({
    type: "topology-200-total-ms",
    description: String(total),
  });
  test.info().annotations.push({
    type: "topology-200-layout-ms",
    description: String(layout),
  });
  console.log(
    `200-device topology (${edges} edges): menu click to drawn ${total} ms, of which layout and paint ${layout} ms`,
  );
  expect(edges).toBeGreaterThan(400);
  expect(total).toBeLessThan(8000);
  expect(layout).toBeLessThan(6000);
  await expectAccessible(page);
  expect(await csp()).toEqual([]);

  // The whole figure exports too (bounded to 16 M pixels whatever its size).
  const exportStarted = Date.now();
  const png = await downloadOf(page, "Export PNG");
  const exportMs = Date.now() - exportStarted;
  console.log(
    `200-device topology PNG export, click to file: ${exportMs} ms (${png.bytes.length} bytes)`,
  );
  test.info().annotations.push({
    type: "topology-200-export-ms",
    description: String(exportMs),
  });
  expect(png.bytes.subarray(0, 8).equals(PNG_SIGNATURE)).toBe(true);
  const { width, height } = dims(png.bytes);
  expect(width * height).toBeLessThanOrEqual(16_000_000);
});

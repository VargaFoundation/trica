import { readFileSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { expectAccessible, ID, signInWithToken } from "./support";

test("a rack draws both faces from the schema's position attributes", async ({
  page,
}) => {
  await signInWithToken(page, `/racks/${ID.r12}`);
  const front = page.locator('svg[data-face="front"]');
  const rear = page.locator('svg[data-face="rear"]');
  await expect(front.locator("a.rack-slot")).toHaveCount(3); // sw-a1, sw-a2, srv-01
  await expect(rear.locator("a.rack-slot")).toHaveCount(1); // pdu-a
  // srv-01 is full depth, mounted at the front: its rear is drawn as a shadow, not as a device.
  await expect(rear.locator("g.rack-shadow")).toHaveCount(1);
  // sw-a1 sits above sw-a2 (U40 over U39): higher U, smaller y.
  const y = async (name: string) =>
    Number(
      await front
        .locator(`a.rack-slot[aria-label^="${name},"] rect`)
        .first()
        .getAttribute("y"),
    );
  expect(await y("sw-a1")).toBeLessThan(await y("sw-a2"));
  await expectAccessible(page);
});

test("a rack with an overlap and a device without a position says so", async ({
  page,
}) => {
  await signInWithToken(page, `/racks/${ID.r13}`);
  await expect(page.locator("svg[data-face=front] .rack-problem")).toHaveCount(
    2,
  );
  await expect(
    page.getByRole("heading", {
      name: /1 device is mounted without a position/,
    }),
  ).toBeVisible();
});

test("the floor plan links each rack to its elevation", async ({ page }) => {
  await signInWithToken(page, `/rooms/${ID.room}`);
  const plan = page.locator("svg.floor-svg");
  await expect(plan.locator("a.floor-rack")).toHaveCount(3);
  await plan.locator(`a.floor-rack[data-id="${ID.r12}"]`).click();
  await expect(page).toHaveURL(new RegExp(`/racks/${ID.r12}$`));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Rack r12");
});

// The rack budget is lot 1's acceptance criterion ("rack displayed in < 1 s"). One wall-clock
// sample on a shared runner flakes at the margin (1047 ms on #47, finding #48), so the budget
// holds the median of RENDERS renders after a warm-up render, which loads the page's code.
const RENDERS = 5;

test("a fully populated 48 U rack (96 devices, both faces) renders in under a second", async ({
  page,
}) => {
  await signInWithToken(page, `/rooms/${ID.room}`);
  const floor = page.locator("svg.floor-svg a.floor-rack");
  await expect(floor).toHaveCount(3);
  const samples: number[] = [];
  for (let i = 0; i <= RENDERS; i++) {
    if (i > 0) {
      // A reload empties the client's query cache: every render fetches the rack again.
      await page.goto(`/rooms/${ID.room}`);
      await expect(floor).toHaveCount(3);
    }
    // From the click to the last slot painted: the rack and its contents from the mock API (one
    // call each since finding #42, 97 requests before), the layout and the SVG.
    const started = Date.now();
    await floor.and(page.locator(`[data-id="${ID.r48}"]`)).click();
    await expect(
      page.locator('svg[data-face="front"] a.rack-slot'),
    ).toHaveCount(48);
    await expect(page.locator('svg[data-face="rear"] a.rack-slot')).toHaveCount(
      48,
    );
    if (i > 0) samples.push(Date.now() - started);
  }
  const median = rackMedian(samples);
  test
    .info()
    .annotations.push({ type: "rack-render-ms", description: String(median) });
  console.log(
    `48 U rack, 96 devices, click to fully drawn: median ${median} ms of ${samples.join(", ")} ms`,
  );
  expect(median).toBeLessThan(1000);
  await expectAccessible(page);
});

function rackMedian(samples: number[]): number {
  const sorted = [...samples].sort((a, b) => a - b);
  return sorted[Math.floor(sorted.length / 2)] ?? Number.POSITIVE_INFINITY; // no sample: no proof
}

/** Policy violations, from the first script on. */
async function cspViolations(page: Page): Promise<() => Promise<string[]>> {
  await page.addInitScript(() => {
    const w = window as unknown as { __csp: string[] };
    w.__csp = [];
    document.addEventListener("securitypolicyviolation", (e) => w.__csp.push(`${e.violatedDirective} ${e.blockedURI}`));
  });
  const errors: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error" && /Content Security Policy|Refused to/i.test(m.text())) errors.push(m.text());
  });
  return async () => [...errors, ...(await page.evaluate(() => (window as unknown as { __csp: string[] }).__csp))];
}

// r20 holds library models with images (Arista 7050SX-64-R and 7280SE-64-F, HPE DL380 Gen10 and
// DL360 Gen9), library models without (CCR1036-8G-2S+, the pfSense appliance, DL380 Gen9), devices with
// no model at all, and every health. The images come from the device-type library through the API,
// with the token, under img-src 'self': no blob: or data: URL.
test("devices show their model's picture, else a face drawn for their role, and their health", async ({ page }) => {
  const csp = await cspViolations(page);
  await signInWithToken(page, `/racks/${ID.r20}`);
  const front = page.locator('svg[data-face="front"]');
  const device = (name: string) => front.locator(`a.rack-slot[aria-label^="${name},"]`);
  for (const name of ["sw-f1", "sw-f2", "srv-f10", "srv-f12"]) {
    await expect(device(name)).toHaveAttribute("data-face-drawn", "image");
  }
  // The picture is really painted: the canvas holds opaque pixels.
  const painted = await device("srv-f10")
    .locator("canvas")
    .evaluate((c: HTMLCanvasElement) => {
      const d = (c.getContext("2d") as CanvasRenderingContext2D).getImageData(0, 0, c.width, c.height).data;
      let opaque = 0;
      for (let i = 3; i < d.length; i += 4) if ((d[i] ?? 0) > 0) opaque++;
      return opaque / (c.width * c.height);
    });
  expect(painted).toBeGreaterThan(0.9);
  for (const [name, kind] of [
    ["sw-f3", "switch"],
    ["fw-f1", "firewall"],
    ["srv-f11", "server"],
    ["pp-f1", "patch_panel"],
    ["nas-f1", "storage"],
    ["ups-f1", "ups"],
    ["kvm-f1", "kvm"],
  ] as const) {
    await expect(device(name)).toHaveAttribute("data-face-drawn", "generic");
    await expect(device(name).locator(`.rack-face-${kind}`)).toHaveCount(1);
  }
  await expect(page.locator('svg[data-face="rear"] a.rack-slot[aria-label^="pdu-f1,"] .rack-face-pdu')).toHaveCount(1);
  // The rear of a full-depth device with a rear image is that image, faded (srv-f10, srv-f12, sw-f2).
  await expect(page.locator('svg[data-face="rear"] .rack-shadow foreignObject')).toHaveCount(3);
  // Health in colour and in words: srv-f10 is healthy, its power supply is not.
  await expect(device("srv-f10")).toHaveAttribute("data-health", "critical");
  await expect(device("srv-f10")).toHaveAttribute("aria-label", "srv-f10, U14-U15, Health: Critical");
  await expect(device("sw-f2")).toHaveAttribute("data-health", "warning");
  await expect(device("nas-f1")).toHaveAttribute("data-health", "unknown");
  await expectAccessible(page);

  // The export paints the same pictures, from the same origin.
  const [d] = await Promise.all([page.waitForEvent("download"), page.getByRole("button", { name: "Export PNG" }).click()]);
  expect(d.suggestedFilename()).toBe("trica-rack-r20.png");
  expect(readFileSync(await d.path()).subarray(0, 4).toString("latin1")).toBe("\x89PNG");
  expect(await csp()).toEqual([]);
});

// r21 holds one device per face the project drew (internal/devicetypes/library/drawings): every
// one is painted from its picture, front and, for the full-depth ones, rear. With
// TRICA_E2E_SHOTS=<dir> the rack is also saved as a picture, in both themes.
const SHOTS = process.env.TRICA_E2E_SHOTS;
const drawn = ["sw-d1", "sw-d2", "sw-d3", "srv-d1", "srv-d2", "srv-d3", "srv-d4", "srv-d5", "srv-d6", "srv-d7", "srv-d8", "srv-d9"];
for (const theme of ["dark", "light"] as const) {
  test.describe(`${theme} theme`, () => {
    test.use({ colorScheme: theme, viewport: { width: 1400, height: 1200 } });
    test("the faces the project drew are painted, front and rear", async ({ page }) => {
      await signInWithToken(page, `/racks/${ID.r21}`);
      const front = page.locator('svg[data-face="front"]');
      for (const name of drawn) {
        const slot = front.locator(`a.rack-slot[aria-label^="${name},"]`);
        await expect(slot).toHaveAttribute("data-face-drawn", "image");
        const painted = await slot.locator("canvas").evaluate((c: HTMLCanvasElement) => {
          const d = (c.getContext("2d") as CanvasRenderingContext2D).getImageData(0, 0, c.width, c.height).data;
          let opaque = 0;
          for (let i = 3; i < d.length; i += 4) if ((d[i] ?? 0) > 0) opaque++;
          return opaque / (c.width * c.height);
        });
        expect(painted, name).toBeGreaterThan(0.9);
      }
      // Ten are full depth with a rear drawing; the 7050SX-64 and the CRS354 are not full depth.
      await expect(page.locator('svg[data-face="rear"] .rack-shadow foreignObject')).toHaveCount(10);
      if (SHOTS) {
        await page.waitForTimeout(300);
        await page.screenshot({ path: `${SHOTS}/rack-drawn-faces-${theme}.png`, fullPage: true });
        await front.screenshot({ path: `${SHOTS}/rack-drawn-faces-front-${theme}.png`, scale: "device" });
      }
    });
  });
}

// r23 holds a sample of the device-type catalogue (docs/reference/devicetypes.md, "Catalogue"):
// upstream photographs and faces the project drew, servers, switches, a firewall, storage and
// power. Every front is painted from its picture. With TRICA_E2E_SHOTS=<dir> the rack is saved
// as a picture, in both themes.
const catalogue = ["sw-c1", "sw-c2", "sw-c3", "sw-c4", "sw-c5", "fw-c1", "srv-c1", "srv-c2", "srv-c3", "srv-c4", "srv-c5", "srv-c6", "srv-c7", "srv-c8", "srv-c9", "nas-c1", "nas-c2", "pdu-c1", "ups-c1", "ups-c2"];
for (const theme of ["dark", "light"] as const) {
  test.describe(`${theme} theme`, () => {
    test.use({ colorScheme: theme, viewport: { width: 1400, height: 1600 } });
    test("a sample of the catalogue is painted from its faces", async ({ page }) => {
      await signInWithToken(page, `/racks/${ID.r23}`);
      const front = page.locator('svg[data-face="front"]');
      for (const name of catalogue) {
        const slot = front.locator(`a.rack-slot[aria-label^="${name},"]`);
        await expect(slot, name).toHaveAttribute("data-face-drawn", "image");
        const painted = await slot.locator("canvas").evaluate((c: HTMLCanvasElement) => {
          const d = (c.getContext("2d") as CanvasRenderingContext2D).getImageData(0, 0, c.width, c.height).data;
          let opaque = 0;
          for (let i = 3; i < d.length; i += 4) if ((d[i] ?? 0) > 0) opaque++;
          return opaque / (c.width * c.height);
        });
        expect(painted, name).toBeGreaterThan(0.8);
      }
      if (SHOTS) {
        await page.waitForTimeout(300);
        await page.screenshot({ path: `${SHOTS}/rack-catalogue-${theme}.png`, fullPage: true });
        await front.screenshot({ path: `${SHOTS}/rack-catalogue-front-${theme}.png`, scale: "device" });
        await page.locator('svg[data-face="rear"]').screenshot({ path: `${SHOTS}/rack-catalogue-rear-${theme}.png`, scale: "device" });
      }
    });
  });
}

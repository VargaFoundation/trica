import { expect, test, type Page } from "@playwright/test";
import { expectAccessible, oid, signInWithToken } from "./support";

// Hot and cold aisles and the airflow check (ADR 0049), in a real browser,
// under the real CSP, against the mock API, whose rack contents and device
// airflow come from the real check (internal/airflow). room-b holds r30 and
// r31 around a cold aisle, and r32 / r33 that disagree about theirs; r30
// holds a server, an Arista -F switch mounted on the rear (reversed), its -R
// twin (correct), a -R switch that reports forward airflow, and a -F switch
// with -R fan trays. r34 to r36 say nothing yet: each browser engine declares
// the aisle of its own (they share the server).
const SHOTS = process.env.TRICA_E2E_SHOTS;
const roomB = oid(0x800);
const r30 = oid(0x801);
const dev = { srv30: oid(0x811), sw30f: oid(0x813), sw30r: oid(0x815), sw30x: oid(0x817), sw30p: oid(0x818) };
const chromium = { rack: oid(0x805), name: "r34", srv: "srv-34" };
const byEngine: Record<string, { rack: string; name: string; srv: string }> = {
  chromium,
  firefox: { rack: oid(0x806), name: "r35", srv: "srv-35" },
  webkit: { rack: oid(0x807), name: "r36", srv: "srv-36" },
};

async function watchCsp(page: Page): Promise<() => Promise<string[]>> {
  await page.addInitScript(() => {
    const w = window as unknown as { __csp: string[] };
    w.__csp = [];
    document.addEventListener("securitypolicyviolation", (e) => w.__csp.push(`${e.violatedDirective} ${e.blockedURI}`));
  });
  return () => page.evaluate(() => (window as unknown as { __csp: string[] }).__csp);
}

const slot = (page: Page, face: "front" | "rear", id: string) => page.locator(`svg[data-face="${face}"] a.rack-slot[data-id="${id}"]`);

test("a rack says which aisle each face is on, and which devices take their air in from the hot aisle", async ({ page }) => {
  const csp = await watchCsp(page);
  await signInWithToken(page, `/racks/${r30}`);
  const card = page.getByRole("region", { name: "Aisles and airflow" });
  await expect(card).toContainText("Front on the cold aisle, rear on the hot aisle.");
  await expect(card.getByRole("img", { name: /in from the cold aisle at the front, out to the hot aisle at the rear/ })).toBeVisible();
  await expect(page.locator(".rack-face figcaption").nth(0)).toContainText("Cold aisle");
  await expect(page.locator(".rack-face figcaption").nth(1)).toContainText("Hot aisle");

  // The -F switch on the rear takes its air in at the port side: on the hot aisle.
  await expect(slot(page, "rear", dev.sw30f)).toHaveAttribute("data-airflow", "mismatch");
  await expect(slot(page, "rear", dev.sw30f)).toHaveAttribute("aria-label", /airflow: Reversed/);
  // Its -R twin on the rear takes its air in at the rack's front: correct.
  await expect(slot(page, "rear", dev.sw30r)).toHaveAttribute("data-airflow", "ok");
  await expect(slot(page, "front", dev.srv30)).toHaveAttribute("data-airflow", "ok");
  // Its model says -R, the switch says forward: the switch is believed.
  await expect(slot(page, "rear", dev.sw30x)).toHaveAttribute("data-airflow", "mismatch");
  // -F on the front, but fan trays turned the other way.
  await expect(slot(page, "front", dev.sw30p)).toHaveAttribute("data-airflow", "mismatch");

  const summary = card.getByRole("note");
  await expect(summary).toContainText("3 devices take their air in from the hot aisle");
  await expect(summary.getByRole("link", { name: "sw-30f" })).toBeVisible();
  await expect(summary).toContainText("Turned another way than the device: FanTray1 (rear to front), FanTray2 (rear to front).");
  await expect(card).toContainText("All 2 devices whose airflow is known take their air in from the cold aisle.");
  await expect(card).toContainText("1 device cannot be checked.");
  const row = page.getByRole("row", { name: /sw-30r/ });
  await expect(row.getByText("Correct")).toBeVisible();
  await expectAccessible(page);
  expect(await csp()).toEqual([]);
});

test("the aisle is declared in one choice on the rack page, previewed then submitted", async ({ page }, info) => {
  const mine = byEngine[info.project.name] ?? chromium;
  const csp = await watchCsp(page);
  await signInWithToken(page, `/racks/${mine.rack}`);
  const card = page.getByRole("region", { name: "Aisles and airflow" });
  await expect(card).toContainText("Nobody has said which aisle this rack faces");
  await expect(page.getByRole("row", { name: new RegExp(mine.srv) }).getByText("Not checked")).toBeVisible();
  await card.getByRole("button", { name: "Say which aisle it faces" }).click();
  await card.getByRole("group", { name: "The front of this rack faces" }).getByRole("radio", { name: "the cold aisle" }).check();
  await expectAccessible(page);
  await card.getByRole("button", { name: "Preview the change" }).click();
  await expect(card.getByRole("region", { name: "What the inventory would change" })).toContainText("Front aisle");
  await card.getByRole("button", { name: "Submit" }).click();
  await expect(card.getByRole("status")).toContainText("Applied");
  await card.getByRole("button", { name: "Close" }).click();
  await expect(card).toContainText("Front on the cold aisle, rear on the hot aisle.");
  await expect(page.getByRole("row", { name: new RegExp(mine.srv) }).getByText("Correct")).toBeVisible();
  expect(await csp()).toEqual([]);
});

test("the floor plan draws the aisles from the racks and flags the racks that disagree", async ({ page }) => {
  await signInWithToken(page, `/rooms/${roomB}`);
  await expect(page.locator('[data-tile="1,2"].floor-aisle-tile')).toHaveAttribute("data-aisle", "cold");
  await expect(page.locator('[data-tile="1,0"].floor-aisle-tile')).toHaveAttribute("data-aisle", "hot");
  await expect(page.locator('[data-tile="2,2"].floor-aisle-tile')).toHaveAttribute("data-aisle", "conflict");
  await expect(page.getByRole("row", { name: /r32/ })).toContainText("Opens on an aisle another rack says is the other kind");
  await expectAccessible(page);
});

test("the device page says why the check fails and that the switch contradicts its model", async ({ page }) => {
  await signInWithToken(page, `/objects/${dev.sw30x}`);
  const card = page.getByRole("region", { name: "Airflow" });
  await expect(card).toContainText("Reversed");
  await expect(card).toContainText("Takes its air in from the hot aisle");
  await expect(card).toContainText("The device says front to rear (said by the device), its model says rear to front.");
  await expectAccessible(page);
});

for (const theme of ["dark", "light"] as const) {
  test.describe(`${theme} theme`, () => {
    test.use({ colorScheme: theme, viewport: { width: 1400, height: 1500 } });
    test("pictures of the aisles", async ({ page }) => {
      await signInWithToken(page, `/racks/${r30}`);
      await expect(slot(page, "rear", dev.sw30f)).toHaveAttribute("data-airflow", "mismatch");
      await expectAccessible(page);
      if (SHOTS) {
        await page.getByRole("region", { name: "Aisles and airflow" }).screenshot({ path: `${SHOTS}/aisles-card-${theme}.png` });
        await page.locator(".rack-faces").screenshot({ path: `${SHOTS}/aisles-elevation-${theme}.png` });
        await page.screenshot({ path: `${SHOTS}/aisles-rack-page-${theme}.png`, fullPage: true });
      }
      await page.goto(`/rooms/${roomB}`);
      await expect(page.locator('[data-tile="2,2"].floor-aisle-tile')).toBeVisible();
      if (SHOTS) await page.locator(".floor-plan").screenshot({ path: `${SHOTS}/aisles-floor-${theme}.png` });
      await page.goto(`/objects/${dev.sw30x}`);
      const card = page.getByRole("region", { name: "Airflow" });
      await expect(card).toContainText("Reversed");
      if (SHOTS) await card.screenshot({ path: `${SHOTS}/aisles-device-${theme}.png` });
    });
  });
}

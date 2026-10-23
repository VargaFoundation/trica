import { expect, test, type Page } from "@playwright/test";
import { expectAccessible, ID, oid, signInWithToken } from "./support";

// The isometric topology map (ADR 0051) in a real browser, under the real CSP,
// against the mock API, which serves the map through the real topomap package.
// With TRICA_E2E_SHOTS=<dir> every level is also saved as a picture.
const SHOTS = process.env.TRICA_E2E_SHOTS;
const drawn = '.iso-svg[data-state="drawn"]';

async function watchCsp(page: Page): Promise<() => Promise<string[]>> {
  await page.addInitScript(() => {
    const w = window as unknown as { __csp: string[] };
    w.__csp = [];
    document.addEventListener("securitypolicyviolation", (e) => w.__csp.push(`${e.violatedDirective} ${e.blockedURI}`));
  });
  return () => page.evaluate(() => (window as unknown as { __csp: string[] }).__csp);
}

const levels: { name: string; path: string; level: string }[] = [
  { name: "global", path: "/topology", level: "global" },
  { name: "site", path: `/topology?level=site&id=${ID.site}`, level: "site" },
  { name: "site-floor", path: `/topology?level=room&id=${ID.site}`, level: "room" },
  { name: "room", path: `/topology?level=room&id=${ID.room}`, level: "room" },
  { name: "rack", path: `/topology?level=rack&id=${ID.r12}`, level: "rack" },
  { name: "rack-full", path: `/topology?level=rack&id=${ID.r48}`, level: "rack" },
  { name: "unplaced", path: "/topology?level=unplaced", level: "unplaced" },
];

for (const theme of ["dark", "light"] as const) {
  test.describe(`${theme} theme`, () => {
    test.use({ colorScheme: theme });
    for (const l of levels) {
      test(`the ${l.name} level draws, is accessible and breaks no CSP rule`, async ({ page }) => {
        const csp = await watchCsp(page);
        await signInWithToken(page, l.path);
        const svg = page.locator(drawn);
        await expect(svg).toBeVisible();
        await expect(svg).toHaveAttribute("data-level", l.level);
        expect(Number(await svg.getAttribute("data-items"))).toBeGreaterThan(0);
        if (SHOTS) {
          await page.waitForTimeout(900); // the entry animation
          await page.screenshot({ path: `${SHOTS}/map-${l.name}-${theme}.png`, fullPage: true });
        }
        await expectAccessible(page);
        expect(await csp()).toEqual([]);
      });
    }
  });
}

test("the world is walked down to a device with the mouse, and back with the keyboard", async ({ page }) => {
  await signInWithToken(page, "/topology");
  await expect(page.locator(drawn)).toHaveAttribute("data-level", "global");
  await page.locator('.iso-item[data-kind="site"][aria-label*="paris-1"]').click();
  await expect(page.locator(drawn)).toHaveAttribute("data-level", "site");
  await page.locator('.iso-item[data-kind="room"]').first().click();
  await expect(page.locator(drawn)).toHaveAttribute("data-level", "room");
  await page.locator('.iso-item[data-kind="rack"]').first().click();
  await expect(page.locator(drawn)).toHaveAttribute("data-level", "rack");
  await page.keyboard.press("Escape");
  await expect(page.locator(drawn)).toHaveAttribute("data-level", "room");
  await page.goBack();
  await expect(page.locator(drawn)).toHaveAttribute("data-level", "rack");
});

test("a place is opened with the keyboard alone", async ({ page }) => {
  await signInWithToken(page, "/topology");
  await expect(page.locator(drawn)).toBeVisible();
  await page.locator('.iso-item[data-kind="site"][aria-label*="paris-1"]').focus();
  await page.keyboard.press("Enter");
  await expect(page.locator(drawn)).not.toHaveAttribute("data-level", "global");
});

test("the traffic moves along measured links, and stands still when less motion is asked", async ({ browser }) => {
  const url = `/topology?level=room&id=${ID.room}`;
  const flowing = async (reducedMotion: "reduce" | "no-preference") => {
    const ctx = await browser.newContext({ reducedMotion });
    const page = await ctx.newPage();
    await signInWithToken(page, url);
    await expect(page.locator(drawn)).toBeVisible();
    await page.waitForTimeout(500);
    const names = await page.evaluate(() => document.getAnimations().map((a) => (a as CSSAnimation).animationName));
    const lanes = await page.locator(".iso-flow").count();
    await ctx.close();
    return { moving: names.filter((n) => n === "iso-flow").length, lanes };
  };
  const on = await flowing("no-preference");
  expect(on.lanes).toBeGreaterThan(0);
  expect(on.moving).toBeGreaterThan(0);
  const off = await flowing("reduce");
  expect(off.lanes).toBe(0);
  expect(off.moving).toBe(0);
});

test("a rack of 96 devices is drawn in under a second", async ({ page }) => {
  await signInWithToken(page, `/topology?level=rack&id=${ID.r48}`);
  const svg = page.locator(drawn);
  await expect(svg).toBeVisible();
  expect(Number(await svg.getAttribute("data-drawn-ms"))).toBeLessThan(1000);
});

test("the Network view stays one tab away, and the old addresses open it", async ({ page }) => {
  await signInWithToken(page, `/topology?site=${oid(1)}`);
  await expect(page.getByRole("tab", { name: "Network" })).toHaveAttribute("aria-selected", "true");
  await expect(page.locator(".topology-canvas canvas").first()).toBeVisible({ timeout: 30_000 });
  await page.getByRole("tab", { name: "Map" }).click();
  await expect(page.locator(drawn)).toBeVisible();
});

test("a device uplink is drawn between places with an arrow, named in the legend and the table", async ({ page }) => {
  await signInWithToken(page, "/topology");
  await expect(page.locator(drawn)).toBeVisible();
  const uplink = page.locator('.iso-edge[data-medium="uplinked_to"]');
  await expect(uplink).toHaveCount(1);
  await expect(uplink.locator(".iso-edge-arrow")).toHaveCount(1);
  await expect(uplink.locator(".iso-edge-base")).toHaveAttribute("stroke-dasharray", "10 3 2 3 2 3");
  await expect(page.locator(".iso-legend")).toContainText("Uplink: a controller names the device");
  const row = page.locator(`tr[data-id="${(await uplink.getAttribute("data-id")) ?? ""}"]`);
  await expect(row).toContainText("Uplink (port unknown)");
  await row.getByRole("button").click();
  await expect(page.locator(".iso-panel")).toContainText("nothing to measure");

  // The paris-1 floor: srv-f001 to spine-01 is drawn (no cable joins them),
  // leaf-01 to spine-01 is not (its cable already does).
  // Already signed in: navigate, the token form is not shown again.
  await page.goto(`/topology?level=room&id=${ID.site}`);
  await expect(page.locator(drawn)).toHaveAttribute("data-level", "room");
  await expect(page.locator('.iso-edge[data-medium="uplinked_to"]')).toHaveCount(1);
});

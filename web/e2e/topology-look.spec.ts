import { expect, test } from "@playwright/test";
import { ID, signInWithToken } from "./support";

// The look of the map (ADR 0066) in a real browser, on the e2e inventory's rack r12:
// a firewall, two switches and servers, linked by a LAG and cables with live rates, shown as they stand.
const drawn = '.iso-svg[data-state="drawn"]';

test("a linked rack is shown as it stands: one row per linked device, highest unit first, a dot per link, and traffic", async ({ page }) => {
  await signInWithToken(page, `/topology?level=rack&id=${ID.r12}`);
  await expect(page.locator(drawn)).toBeVisible();

  const rows = page.locator(".iso-item.has-card");
  expect(await rows.count()).toBeGreaterThanOrEqual(4);
  const units = await page.evaluate(() =>
    [...document.querySelectorAll<SVGGElement>(".iso-item.has-card")]
      .map((g) => ({ u: Number((g.querySelector(".iso-card-u")?.textContent ?? "U0").slice(1)), y: g.querySelector(".iso-card-box")?.getBoundingClientRect().top ?? 0 }))
      .sort((a, b) => a.y - b.y)
      .map((r) => r.u),
  );
  expect(units).toEqual([...units].sort((a, b) => b - a));

  expect(await page.locator(".iso-edge-dot").count()).toBeGreaterThan(0);
  await expect(page.locator(".iso-badge-bundle").first()).toContainText("×");
  expect(await page.locator(".iso-flow animate").count()).toBeGreaterThan(0);
});

test("pointing at a link's dot shows its ports, and clicking it opens the link", async ({ page }) => {
  await signInWithToken(page, `/topology?level=rack&id=${ID.r12}`);
  await expect(page.locator(drawn)).toBeVisible();
  const dot = page.locator(".iso-edge-dot").filter({ has: page.locator(".iso-dot") }).first();
  const id = await dot.getAttribute("data-for");
  await dot.hover();
  await expect(page.locator(`.iso-edge-badges.is-hot[data-for="${id}"] .iso-badge-port`).first()).toBeVisible();
  await dot.click();
  await expect(page.locator(".iso-side")).toBeVisible();
});

test("a cabinet of a room shows its status LEDs, which blink only for a warning or worse", async ({ page }) => {
  await signInWithToken(page, `/topology?level=room&id=${ID.room}`);
  await expect(page.locator(drawn)).toBeVisible();
  expect(await page.locator(".iso-door .iso-led").count()).toBeGreaterThan(0);
  expect(await page.locator(".iso-led.is-blink:not(.l-warning):not(.l-critical)").count()).toBe(0);
  const ok = page.locator(".iso-led.l-ok").first();
  if ((await ok.count()) > 0) expect(await ok.evaluate((el) => getComputedStyle(el).animationName)).toBe("none");
});

test("the LEDs stand still when less motion is asked", async ({ browser }) => {
  const ctx = await browser.newContext({ reducedMotion: "reduce" });
  const page = await ctx.newPage();
  await signInWithToken(page, `/topology?level=room&id=${ID.room}`);
  await expect(page.locator(drawn)).toBeVisible();
  const names = await page.locator(".iso-led").evaluateAll((els) => els.map((el) => getComputedStyle(el).animationName));
  expect(names.every((n) => n === "none")).toBe(true);
  await ctx.close();
});

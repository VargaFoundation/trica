import { expect, test } from "@playwright/test";
import { ID, signInWithToken } from "./support";

// The look of the map (ADR 0066) in a real browser, on the e2e inventory's rack r12:
// a firewall, two switches and servers, linked by a LAG and cables with live rates.
const drawn = '.iso-svg[data-state="drawn"]';

test("a linked rack is a tree of cards with the gateway on top, bundles, port badges and traffic", async ({ page }) => {
  await signInWithToken(page, `/topology?level=rack&id=${ID.r12}`);
  await expect(page.locator(drawn)).toBeVisible();

  const cards = page.locator(".iso-item.has-card");
  expect(await cards.count()).toBeGreaterThanOrEqual(4);
  const tops = await page.evaluate(() =>
    [...document.querySelectorAll<SVGGElement>(".iso-item.has-card")].map((g) => ({
      name: g.getAttribute("aria-label") ?? "",
      y: g.querySelector(".iso-card-box")?.getBoundingClientRect().top ?? 0,
    })),
  );
  const first = [...tops].sort((a, b) => a.y - b.y)[0];
  expect(first?.name).toContain("fw-paris");

  await expect(page.locator(".iso-badge-bundle").first()).toContainText("×");
  expect(await page.locator(".iso-edge-badges .iso-badge-port").count()).toBeGreaterThan(0);
  expect(await page.locator(".iso-flow .iso-sheen-flow animate").count()).toBeGreaterThan(0);
  await expect(page.locator(".iso-edge .iso-edge-base").first()).toHaveAttribute("stroke-width", /\d/);
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

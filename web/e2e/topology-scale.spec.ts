import { expect, test, type Page } from "@playwright/test";
import { ID, signInWithToken } from "./support";

// The names on the map keep one size on screen whatever the zoom, and a level of one device does not
// blow it up to fill the stage (ADR 0066, amendment "visual language").
const drawn = '.iso-svg[data-state="drawn"]';

/** The height on screen of the first name shown. */
const nameHeight = (page: Page) => page.locator(".iso-labels:not(.is-hidden) .iso-label").first().evaluate((el) => el.getBoundingClientRect().height);

test("a name keeps its size on screen when the view is zoomed in or out", async ({ page }) => {
  await signInWithToken(page, `/topology?level=room&id=${ID.room}`);
  await expect(page.locator(drawn)).toBeVisible();
  const at1 = await nameHeight(page);
  expect(at1).toBeGreaterThan(9);
  for (const name of ["Zoom in", "Zoom in", "Zoom in"]) await page.getByRole("button", { name }).click();
  await page.waitForTimeout(300);
  const zoomedIn = await nameHeight(page);
  for (const name of ["Zoom out", "Zoom out", "Zoom out", "Zoom out", "Zoom out"]) await page.getByRole("button", { name }).click();
  await page.waitForTimeout(300);
  const zoomedOut = await nameHeight(page);
  // Without the text scale, three steps in make names about twice as tall, and five out about a third as tall.
  for (const h of [zoomedIn, zoomedOut]) {
    expect(h / at1).toBeGreaterThan(0.78);
    expect(h / at1).toBeLessThan(1.28);
  }
});

test("a card of a rack's tree is never drawn bigger than its design size, and fits when zoomed out", async ({ page }) => {
  await signInWithToken(page, `/topology?level=rack&id=${ID.r12}`);
  await expect(page.locator(drawn)).toBeVisible();
  const card = () => page.locator(".iso-card-box .iso-card-bg").first().evaluate((el) => el.getBoundingClientRect().height);
  const zoom = async (name: string, n: number) => {
    for (let i = 0; i < n; i++) await page.getByRole("button", { name }).click();
    await page.waitForTimeout(300);
  };
  const atFit = await card();
  expect(atFit).toBeGreaterThan(20);
  await zoom("Zoom in", 3);
  const in3 = await card();
  await zoom("Zoom in", 3);
  const in6 = await card();
  // Zoomed in, a card keeps its design size (46 px) on screen: without the text scale, three more steps in
  // make it about twice as tall.
  for (const h of [in3, in6]) expect(h).toBeLessThan(46 * 1.15);
  expect(in6 / in3).toBeGreaterThan(0.85);
  expect(in6 / in3).toBeLessThan(1.18);
  // Zoomed out past the fit it shrinks with the scene, so that the whole tree stays in view.
  await zoom("Zoom out", 9);
  expect(await card()).toBeLessThan(atFit);
});

test("a level of one device draws it at its size, not across the stage", async ({ page }) => {
  await signInWithToken(page, "/topology?level=unplaced");
  await expect(page.locator(drawn)).toBeVisible();
  const stage = await page.locator(".iso-svg").evaluate((el) => el.getBoundingClientRect().width);
  const block = await page.locator(".iso-item .iso-lift").first().evaluate((el) => el.getBoundingClientRect().width);
  expect(block / stage).toBeLessThan(0.25);
  expect(await nameHeight(page)).toBeLessThan(20);
});

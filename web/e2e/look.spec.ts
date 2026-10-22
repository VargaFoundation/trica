import { expect, test, type Page } from "@playwright/test";
import { expectAccessible, ID, signInWithToken } from "./support";

// The console's look (ADR 0040) in a real browser, in both themes: the page is
// dark by default, light on request, every page passes axe (which computes the
// contrast because it paints), and the pieces that tell equipment apart are
// there. With TRICA_E2E_SHOTS=<dir> each page is also saved as a picture.
const SHOTS = process.env.TRICA_E2E_SHOTS;

const views: { name: string; path: string; ready: (p: Page) => Promise<void> }[] = [
  { name: "home", path: "/", ready: (p) => expect(p.locator("li.tile").first()).toBeVisible() },
  { name: "health", path: "/health", ready: (p) => expect(p.getByText("arista:eos")).toBeVisible() },
  { name: "object-server", path: `/objects/${ID.srv01}`, ready: (p) => expect(p.locator(".attr-list").first()).toBeVisible() },
  { name: "object-server-components", path: `/objects/${ID.srv01}?tab=components`, ready: (p) => expect(p.getByRole("table", { name: /Power supply of srv-01/ })).toBeVisible() },
  { name: "object-switch", path: `/objects/${ID.swA1}`, ready: (p) => expect(p.locator(".attr-list").first()).toBeVisible() },
  { name: "rack", path: `/racks/${ID.r12}`, ready: (p) => expect(p.locator(".rack-svg").first()).toBeVisible() },
  { name: "topology-map", path: "/topology", ready: (p) => expect(p.locator('.iso-svg[data-state="drawn"] .iso-item').first()).toBeVisible() },
  { name: "topology", path: "/topology?view=network", ready: (p) => expect(p.locator(".topology-canvas canvas").first()).toBeVisible({ timeout: 30_000 }) },
];

for (const theme of ["dark", "light"] as const) {
  test.describe(`${theme} theme`, () => {
    test.use({ colorScheme: theme });
    for (const v of views) {
      test(`${v.name} reads well`, async ({ page }) => {
        await signInWithToken(page, v.path);
        await v.ready(page);
        const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
        expect(bg).toBe(theme === "dark" ? "rgb(17, 17, 19)" : "rgb(252, 252, 253)");
        if (SHOTS) {
          await page.waitForTimeout(v.name === "topology" ? 1500 : 300);
          await page.screenshot({ path: `${SHOTS}/${v.name}-${theme}.png`, fullPage: v.name !== "topology" });
        }
        if (v.name !== "topology") await expectAccessible(page);
      });
    }
  });
}

// Dark without any preference is the bare :root block (src/styles/tokens.test.ts):
// Chromium has no "no preference" to emulate, so this checks the rest.
test("System follows the system's scheme, and the selector overrides it", async ({ page }) => {
  const bg = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  await page.emulateMedia({ colorScheme: "dark" });
  await signInWithToken(page, "/");
  await expect(page.locator("li.tile").first()).toBeVisible();
  expect(await bg()).toBe("rgb(17, 17, 19)");
  await page.emulateMedia({ colorScheme: "light" });
  expect(await bg()).toBe("rgb(252, 252, 253)");
  await page.getByRole("combobox", { name: "Theme" }).selectOption("dark");
  expect(await bg()).toBe("rgb(17, 17, 19)");
  await page.emulateMedia({ colorScheme: "dark" });
  await page.getByRole("combobox", { name: "Theme" }).selectOption("light");
  expect(await bg()).toBe("rgb(252, 252, 253)");
});

test("a server, a switch, a PDU and a NAS each have their own icon", async ({ page }) => {
  await signInWithToken(page, "/");
  const icon = (name: string) => page.locator("li.tile", { has: page.getByRole("link", { name, exact: true }) }).locator("[data-icon]");
  await expect(icon("srv-01")).toHaveAttribute("data-icon", "server");
  await expect(icon("sw-a1")).toHaveAttribute("data-icon", "switch");
  await expect(icon("pdu-a")).toHaveAttribute("data-icon", "pdu");
  await expect(icon("srv-03")).toHaveAttribute("data-icon", "nas");
  await expect(icon("srv-04")).toHaveAttribute("data-icon", "firewall");
});

test("the topology canvas follows the theme", async ({ page }) => {
  await signInWithToken(page, "/topology?view=network");
  const canvas = page.locator(".topology-canvas");
  await expect(canvas.locator("canvas").first()).toBeVisible({ timeout: 30_000 });
  await page.getByRole("combobox", { name: "Theme" }).selectOption("dark");
  await expect.poll(() => canvas.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe("rgb(24, 25, 27)");
  await page.getByRole("combobox", { name: "Theme" }).selectOption("light");
  await expect.poll(() => canvas.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe("rgb(255, 255, 255)");
});

test("the sidebar folds into a rail and stays folded", async ({ page }) => {
  await signInWithToken(page, "/health");
  await page.getByRole("button", { name: "Collapse sidebar" }).click();
  await expect(page.locator(".shell")).toHaveAttribute("data-sidebar", "rail");
  await page.reload();
  await expect(page.getByRole("button", { name: "Expand sidebar" })).toBeVisible();
  await expectAccessible(page);
});

test("a phone gets the whole page without sideways scrolling", async ({ browser, baseURL }) => {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 }, ignoreHTTPSErrors: true, baseURL, colorScheme: "dark" });
  const page = await context.newPage();
  await signInWithToken(page, `/objects/${ID.srv01}`);
  await expect(page.getByRole("heading", { level: 1, name: "srv-01" })).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/object-server-phone-dark.png`, fullPage: true });
  await context.close();
});

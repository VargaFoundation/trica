import { expect, test, type Page } from "@playwright/test";
import { expectAccessible, oid, signInWithToken } from "./support";

// Choosing the chassis of a device the library cannot tell (ADR 0048), in a
// real browser, under the real CSP, against the mock API: r22 holds four Dell
// PowerEdge R330 the library matched to the generic R330 only. The mock keeps
// the one declaration the chooser sends, so the rack can show the result;
// each browser engine chooses for its own device (they share the server).
const SHOTS = process.env.TRICA_E2E_SHOTS;
const r22 = oid(9);
const chromium = { id: oid(0x505), name: "srv-v1" };
const byEngine: Record<string, { id: string; name: string }> = {
  chromium,
  firefox: { id: oid(0x506), name: "srv-v2" },
  webkit: { id: oid(0x507), name: "srv-v3" },
};
const pictured = oid(0x508); // srv-v4, never chosen

async function watchCsp(page: Page): Promise<() => Promise<string[]>> {
  await page.addInitScript(() => {
    const w = window as unknown as { __csp: string[] };
    w.__csp = [];
    document.addEventListener("securitypolicyviolation", (e) => w.__csp.push(`${e.violatedDirective} ${e.blockedURI}`));
  });
  return () => page.evaluate(() => (window as unknown as { __csp: string[] }).__csp);
}

test("an uncertain chassis is chosen on the device page, and the rack draws the chosen face", async ({ page }, info) => {
  const dev = byEngine[info.project.name] ?? chromium;
  const csp = await watchCsp(page);
  await signInWithToken(page, `/racks/${r22}`);
  const slot = page.locator(`svg[data-face="front"] a.rack-slot[aria-label^="${dev.name},"]`);
  // The generic R330 has no picture: a generic face, and the rack says the variant is not certain.
  await expect(slot).toHaveAttribute("data-variant-uncertain", "true");
  await expect(slot).toHaveAttribute("data-face-drawn", "generic");
  await expect(slot.locator("title")).toContainText("variant not certain");
  await page.getByRole("link", { name: new RegExp(`Choose the chassis\\s*\\(${dev.name}\\)`) }).click();
  await expect(page).toHaveURL(new RegExp(`/objects/${dev.id}#device-model$`));

  const section = page.getByRole("region", { name: "Device model" });
  await expect(section.getByRole("note")).toContainText("Variant not certain — choose the chassis");
  await expect(page.locator(".object-head").getByText("Variant not certain")).toBeVisible();
  const group = section.getByRole("group", { name: "Choose the chassis" });
  await expect(group.getByRole("radio")).toHaveCount(4);
  // The three chassis have their front painted from the API (the generic one has none).
  await expect(group.locator('canvas[data-drawn="image"]')).toHaveCount(3);
  await group.getByRole("radio", { name: /4 x 3\.5in cabled/ }).check();
  await expect(section.locator(".variant-preview canvas")).toHaveCount(2); // front and rear
  await expectAccessible(page);

  await section.getByRole("button", { name: "Preview the change" }).click();
  await expect(section.getByRole("region", { name: "What the inventory would change" })).toBeVisible();
  await section.getByRole("button", { name: "Submit" }).click();
  await expect(section.getByRole("status")).toContainText("Applied");
  await expect(section.locator(".variant-decided")).toContainText("PowerEdge R330 4 x 3.5in cabled. Declared by e2e.");
  await expect(page.locator(".object-head").getByText("Variant not certain")).toHaveCount(0);

  // The rack now draws the chosen chassis.
  await page.goto(`/racks/${r22}`);
  await expect(slot).toHaveAttribute("data-face-drawn", "image");
  await expect(slot).not.toHaveAttribute("data-variant-uncertain", "true");
  await expect(slot.locator("title")).toContainText("PowerEdge R330 4 x 3.5in cabled");
  expect(await csp()).toEqual([]);
});

for (const theme of ["dark", "light"] as const) {
  test.describe(`${theme} theme`, () => {
    test.use({ colorScheme: theme, viewport: { width: 1400, height: 1400 } });
    test("the chooser shows the candidate fronts side by side", async ({ page }) => {
      await signInWithToken(page, `/objects/${pictured}`);
      const section = page.getByRole("region", { name: "Device model" });
      const group = section.getByRole("group", { name: "Choose the chassis" });
      await expect(group.locator('canvas[data-drawn="image"]')).toHaveCount(3);
      await group.getByRole("radio", { name: /8 x 2\.5in hot-plug/ }).check();
      await expect(section.locator(".variant-preview canvas")).toHaveCount(2);
      await expectAccessible(page);
      if (SHOTS) await section.screenshot({ path: `${SHOTS}/variant-chooser-${theme}.png` });
    });
  });
}

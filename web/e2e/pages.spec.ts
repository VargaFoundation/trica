import { expect, test } from "@playwright/test";
import { expectAccessible, ID, signInWithToken } from "./support";

// Every page of the application, in a real browser, behind the real CSP:
// it renders, shows what the mock inventory holds, and has no WCAG 2.2 A/AA
// violation. A page that is added to App.tsx without a row here is not covered.
const pages: { name: string; path: string; heading: RegExp; also?: RegExp }[] = [
  { name: "home", path: "/", heading: /^Inventory$/, also: /srv-01/ },
  { name: "search", path: "/search?q=sw", heading: /^Search: sw$/, also: /sw-a1/ },
  { name: "object", path: `/objects/${ID.swA1}`, heading: /^sw-a1$/, also: /192\.0\.2\.10/ },
  { name: "object components", path: `/objects/${ID.srv01}?tab=components`, heading: /^srv-01$/, also: /PSU 2/ },
  { name: "object provenance", path: `/objects/${ID.swA1}?tab=provenance`, heading: /^sw-a1$/, also: /declared:lab-file/ },
  { name: "object relations", path: `/objects/${ID.swA1}?tab=relations`, heading: /^sw-a1$/, also: /r12/ },
  { name: "object history", path: `/objects/${ID.swA1}?tab=history`, heading: /^sw-a1$/, also: /Hostname set to sw-a1/ },
  { name: "discrepancies", path: "/discrepancies", heading: /^Discrepancies$/, also: /os_version|OS version/ },
  { name: "proposals", path: "/proposals", heading: /^Proposals$/ },
  { name: "proposal", path: "/proposals/12", heading: /./, also: /Record the rack unit of sw-a1/ },
  { name: "health", path: "/health", heading: /^Health$/, also: /arista:eos/ },
  { name: "rooms", path: "/rooms", heading: /^Rooms$/, also: /salle-1/ },
  { name: "room floor plan", path: `/rooms/${ID.room}`, heading: /^Floor plan of salle-1$/, also: /r12/ },
  { name: "rack elevation", path: `/racks/${ID.r12}`, heading: /^Rack r12$/, also: /sw-a1/ },
  { name: "rack with problems", path: `/racks/${ID.r13}`, heading: /^Rack r13$/, also: /Overlaps another device/ },
  { name: "not found", path: "/nowhere", heading: /^Page not found$/ },
];

for (const p of pages) {
  test(`${p.name} renders and is accessible`, async ({ page }) => {
    await signInWithToken(page, p.path);
    await expect(page.getByRole("heading", { level: 1 }).first()).toHaveText(p.heading);
    if (p.also) await expect(page.locator("main").getByText(p.also).filter({ visible: true }).first()).toBeVisible();
    await expectAccessible(page);
  });
}

test("the sign-in page is accessible", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Sign in");
  await expectAccessible(page);
});

test("the French catalogue is accessible too", async ({ browser, baseURL }) => {
  const context = await browser.newContext({ locale: "fr-FR", ignoreHTTPSErrors: true, baseURL });
  const page = await context.newPage();
  await signInWithToken(page, `/racks/${ID.r12}`);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Baie r12");
  await expectAccessible(page);
  await context.close();
});

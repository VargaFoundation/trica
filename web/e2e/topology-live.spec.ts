import { expect, test, type Page } from "@playwright/test";
import { expectAccessible, ID, signInWithToken } from "./support";

// The live overlay (ADR 0041) in a real browser, against the e2e mock's fake
// live source: the age of the data, the legend, the figures in the table, the
// details beside the drawing, under the page's policy and axe, in both
// themes. With TRICA_E2E_SHOTS=<dir> the rack's topology is also saved as a
// picture, with and without the details open.
const SHOTS = process.env.TRICA_E2E_SHOTS;

async function watchCsp(page: Page) {
  const seen: string[] = [];
  page.on("console", (m) => {
    if (/Content Security Policy|Refused to/i.test(m.text()))
      seen.push(m.text());
  });
  return () => seen;
}

for (const theme of ["dark", "light"] as const) {
  test.describe(`${theme} theme`, () => {
    test.use({ colorScheme: theme });

    test("the topology shows live traffic, and a link's details beside it", async ({
      page,
    }) => {
      const csp = await watchCsp(page);
      await signInWithToken(page, `/topology?rack=${ID.r12}`);
      const canvas = page.locator(
        '.topology-canvas[data-state="ready"][data-live="on"]',
      );
      await expect(canvas).toBeVisible({ timeout: 30_000 });
      await expect(page.locator(".live-age")).toHaveText(/Live data \d+ s ago/);
      await expect(
        page.getByRole("list", { name: "Live state" }).getByRole("listitem"),
      ).toHaveCount(6);
      await expect(
        page.getByRole("columnheader", { name: "Live" }),
      ).toBeVisible();
      // The aggregate's two members, between the MLAG peers.
      const lag = page.locator("tbody tr", { hasText: "Ethernet5" });
      await expect(lag).toHaveCount(1);
      await expect(lag.locator("td").nth(6)).toHaveText(/↑ .*b\/s/);
      if (SHOTS) {
        await page.waitForTimeout(1500);
        await canvas.screenshot({
          path: `${SHOTS}/topology-live-${theme}.png`,
        });
      }
      await lag
        .getByRole("button", { name: /Show the details of sw-a/ })
        .click();
      const panel = page.getByRole("complementary", { name: "Details" });
      await expect(panel).toBeVisible();
      await expect(
        panel.getByText(/Aggregate Port-Channel11 \(2×10G\)/),
      ).toBeVisible();
      await expect(
        panel.getByRole("button", { name: "Show its member links" }),
      ).toBeVisible();
      if (SHOTS) {
        await page.waitForTimeout(500);
        await page
          .locator(".topology-stage")
          .screenshot({ path: `${SHOTS}/topology-live-panel-${theme}.png` });
      }
      await panel
        .getByRole("button", { name: "Show its member links" })
        .click();
      await expect(
        page.locator('.topology-canvas[data-state="ready"][data-live="on"]'),
      ).toBeVisible({ timeout: 30_000 });
      await expect(
        panel.getByRole("button", { name: "Fold into Port-Channel11" }),
      ).toBeVisible();
      await expectAccessible(page);
      expect(csp()).toEqual([]);
      // The icons come from the application itself, never from elsewhere.
      const icon = await page.request.get(
        `/topology-icons/${theme}/switch.svg`,
      );
      expect(icon.status()).toBe(200);
    });
  });
}

test.describe("reduced motion", () => {
  test.use({ reducedMotion: "reduce" });
  test("a reader who asks for reduced motion gets no animation switch to turn on", async ({
    page,
  }) => {
    await signInWithToken(page, `/topology?rack=${ID.r12}`);
    await expect(
      page.locator('.topology-canvas[data-state="ready"][data-live="on"]'),
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      page.getByRole("checkbox", { name: "Animate the traffic" }),
    ).toBeDisabled();
    await expect(
      page.getByText("Animation is off: your system asks for reduced motion."),
    ).toBeVisible();
  });
});

import { expect, test, type Page } from "@playwright/test";
import { expectAccessible, ID, signInWithToken } from "./support";

// srv-p1 is a DL360 Gen9 8SFF the project drew, in no rack: its object page
// draws both faces from the library with its disks, power supplies and ports
// in their places (GET /objects/{id}/faces), in a real browser, behind the
// real CSP. With TRICA_E2E_SHOTS=<dir> the section is saved as a picture.
const SHOTS = process.env.TRICA_E2E_SHOTS;

async function watchCsp(page: Page): Promise<() => Promise<string[]>> {
  await page.addInitScript(() => {
    const w = window as unknown as { __csp: string[] };
    w.__csp = [];
    document.addEventListener("securitypolicyviolation", (e) => w.__csp.push(`${e.violatedDirective} ${e.blockedURI}`));
  });
  return () => page.evaluate(() => (window as unknown as { __csp: string[] }).__csp);
}

for (const theme of ["dark", "light"] as const) {
  test.describe(`${theme} theme`, () => {
    test.use({ colorScheme: theme, viewport: { width: 1400, height: 1200 } });

    test("a server's object page draws its parts in place", async ({ page }) => {
      const csp = await watchCsp(page);
      await signInWithToken(page, `/objects/${ID.srvP1}`);
      const section = page.locator(".faces-card");
      const front = section.locator('figure[data-face="front"] svg');
      const rear = section.locator('figure[data-face="rear"] svg');
      await expect(front).toHaveAttribute("data-drawn", "image");
      await expect(rear).toHaveAttribute("data-drawn", "image");

      // The picture is really painted, from the API with the token.
      const painted = await front.locator("canvas").evaluate((c: HTMLCanvasElement) => {
        const d = (c.getContext("2d") as CanvasRenderingContext2D).getImageData(0, 0, c.width, c.height).data;
        let opaque = 0;
        for (let i = 3; i < d.length; i += 4) if ((d[i] ?? 0) > 0) opaque++;
        return opaque / (c.width * c.height);
      });
      expect(painted).toBeGreaterThan(0.9);

      // Five disks in bays 1:1 to 1:5, three bays empty; the states in colour and words.
      await expect(front.locator("a.face-spot[data-kind=bay]")).toHaveCount(5);
      await expect(front.locator("g.face-spot-empty[data-kind=bay]")).toHaveCount(3);
      await expect(front.locator('a[data-spot="1:3"]')).toHaveClass(/face-spot-critical/);
      await expect(front.locator('a[data-spot="1:3"]')).toHaveAttribute("aria-label", "Bay 1:3: Disk 3, Critical");
      await expect(front.locator('a[data-spot="1:5"]')).toHaveClass(/face-spot-warning/);
      await expect(rear.locator('a[data-kind=psu][data-spot="2"]')).toHaveClass(/face-spot-critical/);
      await expect(rear.locator('a[data-spot="nic1"]')).toHaveClass(/face-spot-up/);
      await expect(rear.locator('a[data-spot="nic3"]')).toHaveClass(/face-spot-down/);
      await expect(rear.locator('a[data-spot="bmc"]')).toHaveClass(/face-spot-up/);

      // Hovering a power supply gives its details beside the drawing.
      await rear.locator('a[data-kind=psu][data-spot="1"]').hover();
      const details = section.locator(".faces-details");
      await expect(details.getByRole("heading", { name: /Power supply 1/ })).toBeVisible();
      await expect(details).toContainText("164 W");

      // The keyboard: one stop per face, the arrows move between the spots.
      await front.locator('a[data-spot="1:1"]').focus();
      await page.keyboard.press("ArrowRight");
      await expect(front.locator('a[data-spot="1:2"]')).toBeFocused();
      await expect(details.getByRole("heading", { name: /Bay 1:2/ })).toBeVisible();
      await page.keyboard.press("End");
      await expect(front.locator('a[data-spot="1:5"]')).toBeFocused();

      // The disk whose name does not say its bay is listed, not guessed.
      await expect(section.getByRole("heading", { name: /1 part not placed/ })).toBeVisible();
      await expect(section.locator(".faces-unplaced")).toContainText("sda");

      if (SHOTS) {
        await page.mouse.move(0, 0);
        await page.waitForTimeout(300);
        await section.screenshot({ path: `${SHOTS}/object-faces-${theme}.png`, scale: "device" });
      }
      await expectAccessible(page);
      expect(await csp()).toEqual([]);
    });

    test("a DL380 Gen10 with NVMe drives in Box 3 shows them there, both supplies, each part once", async ({ page }) => {
      const csp = await watchCsp(page);
      await signInWithToken(page, `/objects/${ID.srvP2}`);
      const section = page.locator(".faces-card");
      const front = section.locator('figure[data-face="front"] svg');
      const rear = section.locator('figure[data-face="rear"] svg');
      await expect(front).toHaveAttribute("data-drawn", "image");
      await expect(rear).toHaveAttribute("data-drawn", "image");
      await expect(section.getByRole("link", { name: "ProLiant DL380 Gen10 8SFF" })).toBeVisible();

      // The drives iLO calls 3:1 to 3:5 in Box 3; the three other bays of the box empty; no bay elsewhere.
      await expect(front.locator("a.face-spot[data-kind=bay]")).toHaveCount(5);
      await expect(front.locator("g.face-spot-empty[data-kind=bay]")).toHaveCount(3);
      for (const b of [1, 2, 3, 4, 5]) await expect(front.locator(`a[data-spot="3:${b}"]`)).toHaveClass(/face-spot-ok/);
      // The OS's name of a drive shares its bay: one bay, two names.
      await expect(front.locator('a[data-spot="3:1"]')).toHaveAttribute("aria-label", "Bay 3:1: 3:1, nvme0n1, OK");
      // Both power supplies, iLO's 0 in bay 1.
      await expect(rear.locator('a[data-kind=psu][data-spot="1"]')).toHaveAttribute("aria-label", "Power supply 1: HpeServerPowerSupply 0, OK");
      await expect(rear.locator('a[data-kind=psu][data-spot="2"]')).toHaveAttribute("aria-label", "Power supply 2: HpeServerPowerSupply 1, OK");
      await expect(rear.locator('a[data-spot="nic2"]')).toHaveAttribute("aria-label", "Port nic2: eno2, 2, Link down");

      // Not placed: the system disks and a port, grouped, each part once.
      await expect(section.getByRole("heading", { name: /3 parts not placed/ })).toBeVisible();
      const os = section.getByRole("region", { name: "Known only to the operating system" });
      await expect(os.getByRole("listitem")).toHaveCount(3);
      await expect(os.getByRole("listitem").nth(0)).toContainText("sda");
      await expect(os.getByRole("listitem").nth(0)).toContainText("SYNTH-SATA-480");
      await expect(os.getByRole("listitem").nth(2)).toContainText("also seen as 5");

      if (SHOTS) {
        await page.mouse.move(0, 0);
        await page.waitForTimeout(300);
        await section.screenshot({ path: `${SHOTS}/object-faces-dl380-gen10-${theme}.png`, scale: "device" });
      }
      await expectAccessible(page);
      expect(await csp()).toEqual([]);
    });
  });
}

test("clicking a spot opens the part", async ({ page }) => {
  await signInWithToken(page, `/objects/${ID.srvP1}`);
  await page.locator('.faces-card a[data-spot="1:3"]').click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Disk 3");
});

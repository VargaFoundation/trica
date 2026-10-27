import { expect, test, type Page } from "@playwright/test";
import { ID, signInWithToken } from "./support";

// Nothing written over anything on the topology map (ADR 0066, amendment): at every level of the e2e
// inventory, in both themes, no two labels (pills, cards, port and bundle badges) intersect, and no
// pill or card is drawn over a block (a building, a room, a rack, a device, a gate, a cabinet's frame).
// The boxes are the ones the browser draws. With TRICA_E2E_SHOTS=<dir> every level is also saved as a picture.
const SHOTS = process.env.TRICA_E2E_SHOTS;
const drawn = '.iso-svg[data-state="drawn"]';

const levels: { name: string; path: string; labelled?: false }[] = [
  { name: "global", path: "/topology" },
  { name: "site", path: `/topology?level=site&id=${ID.site}` },
  { name: "site-floor", path: `/topology?level=room&id=${ID.site}` },
  { name: "room", path: `/topology?level=room&id=${ID.room}` },
  { name: "rack", path: `/topology?level=rack&id=${ID.r12}` },
  { name: "rack-full", path: `/topology?level=rack&id=${ID.r48}`, labelled: false }, // 96 unlinked devices: nothing is written until one is pointed at
  { name: "unplaced", path: "/topology?level=unplaced" },
];

interface Box {
  name: string;
  /** The item a pill or a block belongs to; a pill may touch its own block. */
  of?: string;
  x0: number;
  y0: number;
  x1: number;
  y1: number;
}

/** What the browser draws: the labels that are shown, and the blocks. */
async function boxes(page: Page): Promise<{ labels: Box[]; blocks: Box[] }> {
  return page.evaluate(() => {
    const r = (name: string, el: Element, of?: string) => {
      const b = el.getBoundingClientRect();
      return { name, of, x0: b.left, y0: b.top, x1: b.right, y1: b.bottom };
    };
    const labels = [
      ...[...document.querySelectorAll(".iso-labels:not(.is-hidden)")].map((g) => r(`pill ${g.getAttribute("data-for")}`, g.querySelector(".iso-pill") as Element, g.getAttribute("data-for") ?? undefined)),
      ...[...document.querySelectorAll(".iso-item.has-card")].map((g) => r(`card ${g.getAttribute("data-id")}`, g.querySelector(".iso-card-box") as Element, g.getAttribute("data-id") ?? undefined)),
      ...[...document.querySelectorAll(".iso-edge-badges.is-shown .iso-badge-port:not(.is-dropped)")].map((g, i) => r(`port badge ${g.textContent} #${i}`, g.querySelector("rect") as Element)),
      ...[...document.querySelectorAll(".iso-badge-bundle")].map((g, i) => r(`bundle badge ${g.textContent} #${i}`, g.querySelector("rect") as Element)),
    ];
    const blocks = [
      ...[...document.querySelectorAll(".iso-item")].map((g) => r(`block ${g.getAttribute("aria-label")}`, g.querySelector(".iso-lift") as Element, g.getAttribute("data-id") ?? undefined)),
      ...[...document.querySelectorAll(".iso-frame")].map((g, i) => r(`frame #${i}`, g)),
    ];
    return { labels, blocks };
  });
}

const hit = (a: Box, b: Box, tol = 0.5) => a.x0 < b.x1 - tol && b.x0 < a.x1 - tol && a.y0 < b.y1 - tol && b.y0 < a.y1 - tol;

/** The pairs that intersect: two labels, or a pill or card over a block that is not its own. */
function collisions(labels: Box[], blocks: Box[]): string[] {
  const out: string[] = [];
  labels.forEach((a, i) => {
    for (const b of labels.slice(i + 1)) if (hit(a, b)) out.push(`${a.name} / ${b.name}`);
    if (a.name.startsWith("port badge") || a.name.startsWith("bundle badge")) return; // a badge sits on its own line, which joins blocks
    for (const k of blocks) if (k.of !== a.of && hit(a, k)) out.push(`${a.name} / ${k.name}`);
  });
  return out;
}

for (const theme of ["dark", "light"] as const) {
  test.describe(`${theme} theme`, () => {
    test.use({ colorScheme: theme });
    for (const l of levels) {
      test(`at the ${l.name} level no label is written over another label or over a block`, async ({ page }) => {
        await signInWithToken(page, l.path);
        await expect(page.locator(drawn)).toBeVisible();
        await page.waitForTimeout(900); // the entry animation and the labels' fade
        const { labels, blocks } = await boxes(page);
        if (l.labelled !== false) expect(labels.length, "some labels are shown").toBeGreaterThan(0);
        expect(collisions(labels, blocks)).toEqual([]);
        if (SHOTS) await page.screenshot({ path: `${SHOTS}/map-${l.name}-${theme}.png`, fullPage: true });
      });
    }
  });
}

test("at the rack level the Internet and the other places stand right of the cards, with their links round them", async ({ page }) => {
  await signInWithToken(page, `/topology?level=rack&id=${ID.r12}`);
  await expect(page.locator(drawn)).toBeVisible();
  await page.waitForTimeout(900);
  const got = await page.evaluate(() => {
    const box = (el: Element | null) => {
      const b = (el as Element).getBoundingClientRect();
      return { x0: b.left, x1: b.right, y0: b.top, y1: b.bottom };
    };
    return {
      gates: [...document.querySelectorAll(".iso-item.iso-internet .iso-lift, .iso-item.iso-portal .iso-lift")].map(box),
      cards: [...document.querySelectorAll(".iso-card-box")].map(box),
      frame: box(document.querySelector(".iso-frame-back")),
    };
  });
  expect(got.gates.length).toBeGreaterThan(0);
  const rightOfCards = Math.max(...got.cards.map((c) => c.x1));
  for (const g of got.gates) expect(g.x0, "a gate is right of the cards").toBeGreaterThan(rightOfCards);
  expect(Math.min(...got.gates.map((g) => g.x0))).toBeGreaterThan(got.frame.x1);
});

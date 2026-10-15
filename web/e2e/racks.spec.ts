import { expect, test } from "@playwright/test";
import { expectAccessible, ID, signInWithToken } from "./support";

test("a rack draws both faces from the schema's position attributes", async ({
  page,
}) => {
  await signInWithToken(page, `/racks/${ID.r12}`);
  const front = page.locator('svg[data-face="front"]');
  const rear = page.locator('svg[data-face="rear"]');
  await expect(front.locator("a.rack-slot")).toHaveCount(3); // sw-a1, sw-a2, srv-01
  await expect(rear.locator("a.rack-slot")).toHaveCount(1); // pdu-a
  // srv-01 is full depth, mounted at the front: its rear is drawn as a shadow, not as a device.
  await expect(rear.locator("g.rack-shadow")).toHaveCount(1);
  // sw-a1 sits above sw-a2 (U40 over U39): higher U, smaller y.
  const y = async (name: string) =>
    Number(
      await front
        .locator(`a.rack-slot[aria-label^="${name},"] rect`)
        .first()
        .getAttribute("y"),
    );
  expect(await y("sw-a1")).toBeLessThan(await y("sw-a2"));
  await expectAccessible(page);
});

test("a rack with an overlap and a device without a position says so", async ({
  page,
}) => {
  await signInWithToken(page, `/racks/${ID.r13}`);
  await expect(page.locator("svg[data-face=front] .rack-problem")).toHaveCount(
    2,
  );
  await expect(
    page.getByRole("heading", {
      name: /1 device is mounted without a position/,
    }),
  ).toBeVisible();
});

test("the floor plan links each rack to its elevation", async ({ page }) => {
  await signInWithToken(page, `/rooms/${ID.room}`);
  const plan = page.locator("svg.floor-svg");
  await expect(plan.locator("a.floor-rack")).toHaveCount(3);
  await plan.locator(`a.floor-rack[data-id="${ID.r12}"]`).click();
  await expect(page).toHaveURL(new RegExp(`/racks/${ID.r12}$`));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Rack r12");
});

// The rack budget is lot 1's acceptance criterion ("rack displayed in < 1 s"). One wall-clock
// sample on a shared runner flakes at the margin (1047 ms on #47, finding #48), so the budget
// holds the median of RENDERS renders after a warm-up render, which loads the page's code.
const RENDERS = 5;

test("a fully populated 48 U rack (96 devices, both faces) renders in under a second", async ({
  page,
}) => {
  await signInWithToken(page, `/rooms/${ID.room}`);
  const floor = page.locator("svg.floor-svg a.floor-rack");
  await expect(floor).toHaveCount(3);
  const samples: number[] = [];
  for (let i = 0; i <= RENDERS; i++) {
    if (i > 0) {
      // A reload empties the client's query cache: every render fetches the rack again.
      await page.goto(`/rooms/${ID.room}`);
      await expect(floor).toHaveCount(3);
    }
    // From the click to the last slot painted: the rack and its contents from the mock API (one
    // call each since finding #42, 97 requests before), the layout and the SVG.
    const started = Date.now();
    await floor.and(page.locator(`[data-id="${ID.r48}"]`)).click();
    await expect(
      page.locator('svg[data-face="front"] a.rack-slot'),
    ).toHaveCount(48);
    await expect(page.locator('svg[data-face="rear"] a.rack-slot')).toHaveCount(
      48,
    );
    if (i > 0) samples.push(Date.now() - started);
  }
  const median = rackMedian(samples);
  test
    .info()
    .annotations.push({ type: "rack-render-ms", description: String(median) });
  console.log(
    `48 U rack, 96 devices, click to fully drawn: median ${median} ms of ${samples.join(", ")} ms`,
  );
  expect(median).toBeLessThan(1000);
  await expectAccessible(page);
});

function rackMedian(samples: number[]): number {
  const sorted = [...samples].sort((a, b) => a - b);
  return sorted[Math.floor(sorted.length / 2)] ?? Number.POSITIVE_INFINITY; // no sample: no proof
}

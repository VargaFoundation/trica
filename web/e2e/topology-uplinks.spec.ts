import { readFileSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { expectAccessible, signInWithToken } from "./support";

// Uplinks on the topology (ADR 0062) in a real browser, behind the real CSP.
// The mock inventory has two: the first server to spine-01, which no cable
// reaches it through (a line of its own), and the first leaf to spine-01,
// which its cable already joins it to (carried by that line). The VLAN filter
// of the other topology specs drops uplinks, so they are not affected.

const LAYOUT_200 = { timeout: 30_000 };

async function watchCsp(page: Page): Promise<() => string[]> {
  const seen: string[] = [];
  page.on("console", (m) => {
    if (/Content Security Policy|Refused to/i.test(m.text())) seen.push(m.text());
  });
  return () => seen;
}

test("an uplink is a line of its own in the table and the legend, and is accessible", async ({ page }) => {
  const csp = await watchCsp(page);
  await signInWithToken(page, "/topology?view=network");
  await expect(page.locator('.topology-canvas[data-state="ready"][data-nodes="200"]')).toBeVisible(LAYOUT_200);
  const row = page.locator("tbody tr", { hasText: "uplink (port unknown)" }).filter({ has: page.getByRole("link", { name: "srv-f001" }) });
  await expect(row).toHaveCount(1);
  await expect(row).toContainText("port unknown");
  await expect(row).toContainText("unifi:e2e");
  await expect(row).toHaveAttribute("data-class", "observed");
  // Both uplinks are in the table, the covered one too.
  await expect(page.locator("tbody tr", { hasText: "uplink (port unknown)" })).toHaveCount(2);
  await expect(page.getByRole("list", { name: "Legend" }).getByRole("listitem")).toHaveCount(4);
  await expect(page.getByRole("list", { name: "Legend" })).toContainText("Uplink:");
  // Its details say what is known and that nothing is measured.
  await row.getByRole("button", { name: /Show the details of the uplink srv-f001/ }).click();
  const panel = page.getByRole("complementary", { name: "Details" });
  await expect(panel).toContainText("srv-f001 reaches the network through spine-01");
  await expect(panel).toContainText("nothing to measure");
  await expectAccessible(page);
  expect(csp()).toEqual([]);
});

test("the PDF carries the uplink's label, and the PNG is exported with it", async ({ page }) => {
  const csp = await watchCsp(page);
  await signInWithToken(page, "/topology?view=network");
  await expect(page.locator('.topology-canvas[data-state="ready"][data-nodes="200"]')).toBeVisible(LAYOUT_200);
  const [d] = await Promise.all([page.waitForEvent("download"), page.getByRole("button", { name: "Export PDF" }).click()]);
  const text = readFileSync((await d.path()) as string).toString("latin1");
  // The label is text in the file, so it can be selected and searched.
  expect(text).toContain("(uplink \\(port unknown\\)) Tj");
  await expect(page.getByText("PDF file saved.")).toBeVisible();
  const [png] = await Promise.all([page.waitForEvent("download"), page.getByRole("button", { name: "Export PNG" }).click()]);
  expect(readFileSync((await png.path()) as string).subarray(0, 4).toString("hex")).toBe("89504e47");
  expect(csp()).toEqual([]);
});

test("a VLAN filter leaves the uplinks out: a VLAN is a property of ports", async ({ page }) => {
  await signInWithToken(page, "/topology?view=network");
  await expect(page.locator('.topology-canvas[data-state="ready"][data-nodes="200"]')).toBeVisible(LAYOUT_200);
  await page.getByLabel("VLAN").selectOption({ label: "vlan-10" });
  await expect(page.locator("tbody tr", { hasText: "uplink (port unknown)" })).toHaveCount(0);
  await expect(page.getByRole("list", { name: "Legend" }).getByRole("listitem")).toHaveCount(3);
});

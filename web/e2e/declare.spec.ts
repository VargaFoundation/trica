import { expect, test } from "@playwright/test";
import { expectAccessible, ID, signInWithToken } from "./support";

// Declaring the physical layout from the UI, in a real browser against the
// mock API: it previews a batch and "queues" a submitted one as proposal 12,
// never applying anything, so these tests leave the inventory as they found it.

test("a free unit of a rack opens the placement form; a declared PDU is previewed and sent for approval", async ({ page }) => {
  await signInWithToken(page, `/racks/${ID.r12}`);
  const front = page.locator('svg[data-face="front"]');
  await expect(front.locator("a.rack-slot")).toHaveCount(3);
  // U 20 of the front is free: a pointer target (sw-a1 at U 40 is a link, not a target).
  await front.locator('rect.rack-free[data-u="20"]').click();
  const panel = page.getByRole("region", { name: "Place a device in rack r12" });
  await expect(panel.getByLabel("Position (lowest U)")).toHaveValue("20");
  await expect(panel.getByLabel("Face")).toHaveValue("front");

  await panel.getByRole("radio", { name: /no collector sees/ }).check();
  await panel.getByLabel(/^Name/).fill("pdu-e2e");
  await panel.getByLabel("Role").selectOption("pdu");
  await panel.getByLabel(/^Height/).fill("1");
  await panel.getByRole("button", { name: "Preview the change" }).click();
  const preview = panel.getByRole("region", { name: "What the inventory would change" });
  await expect(preview).toContainText("New Device:");
  await expect(preview).toContainText("pdu-e2e");
  await expectAccessible(page);

  await panel.getByRole("button", { name: "Submit" }).click();
  await expect(panel.getByRole("status")).toContainText("Sent for approval");
  await panel.getByRole("link", { name: "Proposal #12" }).click();
  await expect(page).toHaveURL(/\/proposals\/12$/);
});

test("a position outside the rack is refused before anything is sent", async ({ page }) => {
  await signInWithToken(page, `/racks/${ID.r13}`);
  await page.getByRole("button", { name: "Place a device" }).click();
  const panel = page.getByRole("region", { name: "Place a device in rack r13" });
  await panel.getByRole("radio", { name: /no collector sees/ }).check();
  await panel.getByLabel(/^Name/).fill("shelf-e2e");
  await panel.getByLabel(/^Height/).fill("2");
  await panel.getByLabel("Position (lowest U)").fill("12");
  await expect(panel.getByText("The device would stick out of the rack: its top would be U13, the rack has 12 U.")).toBeVisible();
  await expect(panel.getByRole("button", { name: "Preview the change" })).toBeDisabled();
  await expectAccessible(page);
});

test("a free floor tile opens 'add a rack' with its coordinates, and a taken tile is said", async ({ page }) => {
  await signInWithToken(page, `/rooms/${ID.room}`);
  await page.locator('svg.floor-svg rect.floor-free[data-tile="0,0"]').click();
  const panel = page.getByRole("region", { name: "Add a rack in salle-1" });
  await expect(panel.getByLabel("Floor X")).toHaveValue("0");
  await panel.getByLabel(/^Name/).fill("r14");
  await panel.getByLabel("Floor X").fill("1");
  await panel.getByLabel("Floor Y").fill("1");
  await expect(panel.getByText("This tile already holds r12.")).toBeVisible();
  await panel.getByRole("button", { name: "Preview the change" }).click();
  await expect(panel.getByRole("region", { name: "What the inventory would change" })).toContainText("New Rack:");
  await expectAccessible(page);
});

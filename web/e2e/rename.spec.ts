import { expect, test } from "@playwright/test";
import { expectAccessible, ID, signInWithToken } from "./support";

// Renaming a rack from its page, in a real browser against the read-only mock API: the batch is
// previewed and "queued" (never applied), so these tests leave the inventory as they found it.
// What the server does with the batch (same id, no duplicate, history kept, name taken refused)
// is proven by the Go integration tests, not here: the mock cannot show the new name.

test("a rack is renamed from its page: the dialog designates it by id, previews and sends the new name", async ({
  page,
}) => {
  await signInWithToken(page, `/racks/${ID.r12}`);
  await page.getByRole("button", { name: "Rename", exact: true }).click();
  const panel = page.getByRole("region", { name: "Rename r12" });
  await expect(panel.getByLabel("New name")).toHaveValue("r12");
  await expect(
    panel.getByRole("button", { name: "Preview the change" }),
  ).toBeDisabled();
  await panel.getByLabel("New name").fill("r12-renamed");
  await panel.getByRole("button", { name: "Preview the change" }).click();
  const preview = panel.getByRole("region", {
    name: "What the inventory would change",
  });
  await expect(preview).toBeVisible();
  await expectAccessible(page);
  await panel.getByRole("button", { name: "Submit" }).click();
  await expect(panel.getByRole("status")).toContainText("Sent for approval");
});

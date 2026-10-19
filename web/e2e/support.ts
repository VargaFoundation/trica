import AxeBuilder from "@axe-core/playwright";
import { expect, type Page } from "@playwright/test";

/** The API token the mock server accepts in the pasted-token form. */
export const API_TOKEN = "e2e-api-token";

export const oid = (n: number): string => `0a000000-0000-4000-8000-${n.toString(16).padStart(12, "0")}`;
export const ID = { site: oid(1), r12: oid(2), r13: oid(3), r48: oid(4), r20: oid(7), r21: oid(8), room: oid(16), swA1: oid(0xa1), srv01: oid(0xb1) };

/** Opens `path` and signs in with the pasted API token; the address is kept, so the page asked for opens. */
export async function signInWithToken(page: Page, path = "/"): Promise<void> {
  await page.goto(path);
  await page.locator("#token").fill(API_TOKEN); // by id: the label is translated
  await page.locator("form button[type=submit]").click();
}

/**
 * axe-core in the real browser, WCAG 2.0 to 2.2 levels A and AA. Unlike the
 * jsdom tests this one computes colour contrast, because it paints.
 */
export async function expectAccessible(page: Page): Promise<void> {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
    .analyze();
  const text = results.violations
    .map((v) => `${v.id} (${v.impact}): ${v.help}\n${v.nodes.map((n) => `    ${n.target.join(" ")}  ${n.html.slice(0, 140)}`).join("\n")}`)
    .join("\n");
  expect(results.violations, text).toEqual([]);
}

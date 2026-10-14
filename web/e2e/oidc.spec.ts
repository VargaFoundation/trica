import { expect, test } from "@playwright/test";
import { expectAccessible, ID } from "./support";

// The browser half of ADR 0019, against a fake provider that checks what a real
// one checks: the PKCE S256 verifier against the challenge, the redirect_uri,
// no client_secret, a single-use code. The API accepts only tokens that
// provider issued, so landing on a page proves the whole chain.

test("signing in with SSO lands on the page that was asked for", async ({ page }) => {
  await page.goto(`/racks/${ID.r12}`);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Sign in");
  await expectAccessible(page);

  const authorize = page.waitForRequest((r) => r.url().includes("/idp/authorize"));
  const token = page.waitForRequest((r) => r.url().includes("/idp/token"));
  await page.getByRole("button", { name: "Sign in with SSO" }).click();

  const url = new URL((await authorize).url());
  expect(url.searchParams.get("response_type")).toBe("code");
  expect(url.searchParams.get("code_challenge_method")).toBe("S256");
  expect(url.searchParams.get("redirect_uri")).toBe(`${new URL(page.url()).origin}/oidc/callback`);
  expect(url.searchParams.get("code_challenge")).toMatch(/^[A-Za-z0-9_-]{43}$/);

  const body = new URLSearchParams((await token).postData() ?? "");
  expect(body.get("client_secret")).toBeNull();
  expect(body.get("code_verifier")).toMatch(/^[A-Za-z0-9_-]{43,128}$/);

  await expect(page).toHaveURL(new RegExp(`/racks/${ID.r12}$`));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Rack r12");
  await expectAccessible(page);
});

test("the access token of the provider is not left in the address or in local storage", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sign in with SSO" }).click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Inventory");
  expect(page.url()).not.toContain("code=");
  expect(page.url()).not.toContain("state=");
  expect(await page.evaluate(() => JSON.stringify(window.localStorage))).toBe("{}");
});

test("a callback nobody started is refused and says why", async ({ page }) => {
  await page.goto("/oidc/callback?code=forged&state=forged");
  await expect(page.getByRole("alert")).toContainText("not started from this tab");
  await expect(page.getByRole("link", { name: "Back to the sign-in page" })).toBeVisible();
  await expectAccessible(page);
});

test("a refusal from the provider is shown", async ({ page }) => {
  await page.goto("/oidc/callback?error=access_denied&error_description=Denied%20by%20policy");
  await expect(page.getByRole("alert")).toContainText("refused the sign-in");
  await expect(page.getByRole("alert")).toContainText("Denied by policy");
});

test("the pasted-token form still works next to SSO, and a bad token is refused", async ({ page }) => {
  await page.goto("/");
  await page.locator("#token").fill("not-a-token");
  await page.locator("form button[type=submit]").click();
  await expect(page.getByRole("alert")).toContainText("refused this token");
  await page.locator("#token").fill("e2e-api-token");
  await page.locator("form button[type=submit]").click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Inventory");
});

test("the page serves its configuration, and the CSP lets it reach the provider only", async ({ page, request }) => {
  const res = await request.get("/ui-config.json");
  expect(res.headers()["cache-control"]).toBe("no-store");
  const cfg = (await res.json()) as { oidc: { issuer: string; client_id: string } };
  expect(cfg.oidc.client_id).toBe("trica-ui-e2e");
  const home = await page.goto("/");
  const csp = home?.headers()["content-security-policy"] ?? "";
  expect(csp).toContain(`connect-src 'self' ${new URL(cfg.oidc.issuer).origin}`);
});

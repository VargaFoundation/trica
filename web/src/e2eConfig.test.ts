import { afterEach, describe, expect, it, vi } from "vitest";

// The browser tests' engines (ADR 0038): Chromium on every pull request,
// Firefox and WebKit added only when TRICA_E2E_BROWSERS=all asks for them.
async function projects(browsers?: string): Promise<string[]> {
  vi.resetModules();
  if (browsers !== undefined) vi.stubEnv("TRICA_E2E_BROWSERS", browsers);
  const config = (await import("../playwright.config")).default;
  return (config.projects ?? []).map((p) => p.name ?? "");
}

describe("the browser test configuration", () => {
  afterEach(() => vi.unstubAllEnvs());

  it("runs Chromium only by default", async () => {
    expect(await projects()).toEqual(["chromium"]);
  });

  it("adds Firefox and WebKit, as desktop browsers, when all are asked for", async () => {
    expect(await projects("all")).toEqual(["chromium", "firefox", "webkit"]);
    const config = (await import("../playwright.config")).default;
    const engines = (config.projects ?? []).map((p) => p.use?.defaultBrowserType);
    expect(engines).toEqual(["chromium", "firefox", "webkit"]);
  });

  it("ignores any other value", async () => {
    expect(await projects("firefox")).toEqual(["chromium"]);
  });
});

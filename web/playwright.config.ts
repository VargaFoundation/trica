import { defineConfig, devices } from "@playwright/test";

// The browser tests run the built UI (internal/web/dist, embedded with
// -tags webui) behind cmd/trica-e2e-server: the real internal/web handler, a
// read-only mock of /api/v1 and a fake OpenID provider, over self-signed TLS
// (the page's OIDC code accepts https only). Chromium by default: one engine
// keeps the pull request job fast, and the accessibility rules do not depend
// on it. TRICA_E2E_BROWSERS=all adds Firefox and WebKit (the e2e workflow's
// browsers job, and `make web-e2e-browsers`).
const PORT = Number(process.env.TRICA_E2E_PORT ?? 18443);
const ORIGIN = `https://127.0.0.1:${PORT}`;

export default defineConfig({
  testDir: "e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: 0, // a flaky test is a bug to fix, not to retry
  workers: process.env.CI ? 2 : undefined,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: ORIGIN,
    ignoreHTTPSErrors: true,
    trace: "retain-on-failure",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    ...(process.env.TRICA_E2E_BROWSERS === "all"
      ? [
          { name: "firefox", use: { ...devices["Desktop Firefox"] } },
          { name: "webkit", use: { ...devices["Desktop Safari"] } },
        ]
      : []),
  ],
  webServer: {
    command: `go run -tags webui ./cmd/trica-e2e-server -addr 127.0.0.1:${PORT}`,
    cwd: "..",
    url: `${ORIGIN}/ui-config.json`,
    ignoreHTTPSErrors: true,
    reuseExistingServer: false,
    timeout: 180_000,
    stdout: "pipe",
    stderr: "pipe",
  },
});

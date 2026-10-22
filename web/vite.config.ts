/// <reference types="vitest/config" />
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The build goes straight into internal/web/dist, which the Go server embeds
// (`go build -tags webui`, ADR 0018). It is a build output: not committed.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
    sourcemap: false,
    // The server sends a CSP with img-src 'self': nothing may be inlined as a data: URL.
    assetsInlineLimit: 0,
    cssCodeSplit: false,
  },
  server: {
    // `npm run dev` talks to a trica server started separately.
    proxy: {
      "/api": process.env.TRICA_API ?? "http://127.0.0.1:8080",
      // The theme (ADR 0054) is served by the Go server, before sign-in.
      "/theme": process.env.TRICA_API ?? "http://127.0.0.1:8080",
    },
  },
  test: {
    // The Playwright specs in e2e/ run with `npm run e2e`, not here.
    include: ["src/**/*.test.{ts,tsx}"],
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    css: false,
    restoreMocks: true,
    env: { TZ: "UTC" },
    coverage: { provider: "v8", include: ["src/**"], exclude: ["src/**/*.gen.ts", "src/test/**"] },
  },
});

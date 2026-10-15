# ADR-0038: The browser tests run in Firefox and WebKit too, on demand

- **Status**: accepted (2026-10)
- **Concerns**: `web/playwright.config.ts`, `Makefile` (`web-e2e-browsers`), `web/package.json`
  (`e2e:install-browsers`), the `browsers` job of `.github/workflows/e2e.yml`. Amends the sentence
  "Chromium only" of ADR 0019. Issue #49, point 2.

## Context

ADR 0019 ran Playwright in Chromium only, to keep the pull request job short, on the argument
that the accessibility rules and the layout do not differ by engine. The argument was never
tested. Running the same specs in Firefox and WebKit showed that it holds for the pages and the
accessibility checks, and not for the exports: cytoscape multiplies an export's `scale` by the
screen's pixel ratio, and Playwright's "Desktop Safari" device has a ratio of 2, like a Retina
display. The 200-device topology came out at 56 million pixels against the 16 million the page
budgets for, a size Safari refuses for a canvas. Chromium's device has a ratio of 1, so the bug
was invisible there.

## Decision

**Chromium stays the only engine on pull requests.** Its job is unchanged.

**`TRICA_E2E_BROWSERS=all` adds a Firefox and a WebKit project** (Playwright's "Desktop Firefox"
and "Desktop Safari" devices) to the same configuration, so the specs are the same files with no
per-engine branch. `make web-e2e-browsers` runs the three; `npm --prefix web run
e2e:install-browsers` installs them with their system libraries.

**The `browsers` job of the `e2e` workflow runs them on demand only** (`workflow_dispatch`:
Actions > e2e > Run workflow). Not nightly and not on pull requests: each engine adds minutes to
the organisation's Actions budget, which is spent elsewhere, and scheduling it is a cost decision
for the maintainers, not a test decision.

**The pixel-ratio dependence is tested in Chromium**, where it runs on every pull request: a spec
exports the same topology from a context with a device scale factor of 1 and one of 2 and expects
the same image size. The export asks cytoscape for `maxWidth`/`maxHeight`, which it does not
multiply by the pixel ratio, instead of `scale`.

## Consequences

- A regression specific to Firefox or WebKit is only seen when someone runs the job or the
  target. Moving it to a schedule is a one-line change to the job's `if:`.
- WebKit needs system libraries (GTK 4, GStreamer) that a developer machine may lack; the
  `mcr.microsoft.com/playwright` image of the same version has them.
- The timing budgets (rack < 1 s, 200-device topology < 8 s) apply to every engine; WebKit is the
  slowest of the three and closest to them.

import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RelationLink } from "../api/types";
import { expectNoA11yViolations } from "../test/axe";
import { page, READER } from "../test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp, type Call, type Handler } from "../test/support";
import { DEVICES, L, T, lagContext, lagLinks, sampleContext, sampleLinks, sampleLive } from "./fixtures";
import type * as GraphModule from "./graph";
import type { Drawing } from "./graph";
import { graphEnv } from "./graph";
import { LIVE_REFRESH_MS, liveInterval } from "./useLiveTopology";

// The drawing the page made last, to click in it as a reader would.
const drawn = vi.hoisted(() => ({ last: null as Drawing | null }));
vi.mock("./graph", async (original) => {
  const mod: typeof GraphModule = await original();
  return {
    ...mod,
    draw: (...args: Parameters<typeof mod.draw>) => {
      const d = mod.draw(...args);
      drawn.last = d;
      return d;
    },
  };
});

beforeEach(() => {
  graphEnv.headless = true;
  graphEnv.layout = "preset";
  vi.useFakeTimers({ shouldAdvanceTime: true, now: Date.parse("2026-10-18T10:00:12Z") });
});
afterEach(() => {
  graphEnv.headless = false;
  graphEnv.layout = "elk";
  vi.useRealTimers();
});

const obj = (id: string, type: string, display: string, extra: Record<string, unknown> = {}) => ({
  id,
  type,
  display,
  status: "active",
  attributes: {},
  created_at: "2026-03-01T08:00:00Z",
  observed: true,
  ...extra,
});

function routes(live: Handler): Record<string, Handler> {
  const links: RelationLink[] = [...sampleLinks(), ...lagLinks()];
  const context: RelationLink[] = [...sampleContext(), ...lagContext()];
  return {
    ...defaultRoutes(READER),
    "GET /relations": (c: Call) => page((c.query.get("type") ?? "").includes("cabled_to") ? links : context, { limit: 1000 }),
    "GET /objects": (c: Call) => {
      switch (c.query.get("type")) {
        case "device":
          return page(
            DEVICES.map((d) =>
              obj(d.id, "device", d.name, {
                site_id: d.siteId,
                attributes: { role: d.role, ...(d.id === T.swA1 || d.id === T.swA2 ? { mlag_domain: "pair-1", health: "ok" } : {}), ...(d.id === T.srv01 ? { health: "ok", log_critical_count: 2 } : {}) },
              }),
            ),
            { limit: 1000 },
          );
        case "psu":
          return page([obj("p-psu-1", "psu", "PSU 1", { parent_id: T.srv01, attributes: { health: "critical" } })], { limit: 1000 });
        case "site":
          return page([obj(T.paris, "site", "paris-1"), obj(T.lyon, "site", "lyon-1")], { limit: 1000 });
        default:
          return page([], { limit: 1000 });
      }
    },
    "GET /topology/live": live,
  };
}

async function open(live: Handler, locale: "en" | "fr" = "en") {
  const api = mockApi(routes(live));
  renderApp({ at: "/topology", locale });
  await screen.findByRole("table");
  return api;
}

const row = (text: string) =>
  within(screen.getByRole("table"))
    .getAllByRole("row")
    .filter((r) => within(r).queryByText(text) !== null);

describe("the live topology", () => {
  it("adds nothing and asks nothing more when the server has no live overlay", async () => {
    const api = await open({ enabled: false, stale: false, refresh_seconds: 0, links: {}, sources: [] });
    await waitFor(() => expect(api.count("GET /topology/live")).toBe(1));
    expect(screen.queryByRole("columnheader", { name: "Live" })).toBeNull();
    expect(screen.queryByRole("list", { name: "Live state" })).toBeNull();
    expect(screen.queryByText(/Live data/)).toBeNull();
    await act(() => vi.advanceTimersByTimeAsync(3 * LIVE_REFRESH_MS));
    expect(api.count("GET /topology/live")).toBe(1);
  });

  it("treats an older server without the endpoint as no overlay", async () => {
    const api = await open(problem(404, "not-found"));
    await waitFor(() => expect(api.count("GET /topology/live")).toBe(1));
    expect(screen.queryByRole("columnheader", { name: "Live" })).toBeNull();
  });

  it("says each link's traffic and state in the table, the age of the data, and has a legend", async () => {
    await open(sampleLive());
    expect(await screen.findByRole("columnheader", { name: "Live" })).toBeDefined();
    expect(screen.getByText("Live data 12 s ago")).toBeDefined();
    const eno1 = row("eno1")[0]!;
    expect(within(eno1).getByText("↑ 3.2 Gb/s ↓ 1.1 Gb/s · 32%")).toBeDefined();
    expect(within(row("sw-l1")[0]!).getByText("down")).toBeDefined();
    expect(within(screen.getByRole("list", { name: "Live state" })).getAllByRole("listitem")).toHaveLength(6);
    expect(screen.getByRole("img").getAttribute("data-live")).toBe("on");
    await expectNoA11yViolations();
  });

  it("asks again every 30 s while enabled", async () => {
    const api = await open(sampleLive());
    await waitFor(() => expect(api.count("GET /topology/live")).toBe(1));
    await act(() => vi.advanceTimersByTimeAsync(LIVE_REFRESH_MS + 100));
    await waitFor(() => expect(api.count("GET /topology/live")).toBe(2));
    expect(liveInterval({ enabled: true } as never)).toBe(LIVE_REFRESH_MS);
    expect(liveInterval({ enabled: false } as never)).toBe(false);
    expect(liveInterval(undefined)).toBe(false);
  });

  it("says when the data is stale", async () => {
    await open(sampleLive({ stale: true }));
    expect(await screen.findByText("Live data stale: last update 12 s ago")).toBeDefined();
  });

  it("opens a line's details: both ends, speed, traffic each way, utilisation, source; an aggregate unfolds", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await open(sampleLive());
    await screen.findByRole("columnheader", { name: "Live" });
    const cy = drawn.last!.cy;
    const lag = cy.edges().filter((e) => e.data("lag") !== "").first();
    act(() => void lag.emit("tap"));
    const panel = await screen.findByRole("complementary", { name: "Details" });
    expect(within(panel).getByText("Aggregate Port-Channel11 (2×10G)")).toBeDefined();
    expect(within(panel).getAllByText("Ethernet5").length).toBeGreaterThan(0);
    expect(within(panel).getAllByText("75%").length).toBe(1);
    expect(within(panel).getAllByText("arista").length).toBeGreaterThan(0);
    await expectNoA11yViolations();
    const edges = cy.edges().length;
    await user.click(within(panel).getByRole("button", { name: "Show its member links" }));
    await waitFor(() => expect(drawn.last!.cy.edges().length).toBe(edges + 1));
    await user.click(within(panel).getByRole("button", { name: "Close the details" }));
    expect(screen.queryByRole("complementary", { name: "Details" })).toBeNull();
  });

  it("opens the same details from the table, for the keyboard, and takes the reader there", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await open(sampleLive());
    await screen.findByRole("columnheader", { name: "Live" });
    await user.click(screen.getByRole("button", { name: "Show the details of sw-a1 / Ethernet2 ↔ srv-01 / eno1" }));
    const panel = await screen.findByRole("complementary", { name: "Details" });
    expect(document.activeElement).toBe(within(panel).getByRole("heading", { level: 2 }));
    expect(within(panel).getAllByText("3.2 Gb/s").length).toBe(1);
    expect(within(panel).getByText("10G")).toBeDefined();
    expect(within(panel).getByText("32%")).toBeDefined();
    expect(drawn.last!.cy.$(":selected").length).toBe(1);
  });

  it("opens a device's health: the worst of its parts, its critical events, a link to its page", async () => {
    await open(sampleLive(), "fr");
    await screen.findByRole("columnheader", { name: "En direct" });
    act(() => void drawn.last!.cy.getElementById(T.srv01).emit("tap"));
    const panel = await screen.findByRole("complementary", { name: "Détail" });
    expect(within(panel).getByText("Critique")).toBeDefined();
    expect(within(panel).getByText("2")).toBeDefined();
    expect(within(panel).getByRole("link", { name: "Ouvrir la page de srv-01" }).getAttribute("href")).toBe(`/objects/${T.srv01}`);
    expect(L.a1e5).toBeDefined();
  });
});

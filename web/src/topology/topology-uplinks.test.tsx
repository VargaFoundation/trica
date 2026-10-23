// The topology page with uplinks (ADR 0062): the table, the legend, the
// details panel, the live overlay, in both languages.
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RelationLink } from "../api/types";
import { expectNoA11yViolations } from "../test/axe";
import { page, READER } from "../test/fixtures";
import { defaultRoutes, mockApi, renderApp, type Call, type Handler } from "../test/support";
import { sampleContext, sampleLinks, sampleLive, T } from "./fixtures";
import { DEVICES_WITH_AP, sampleUplinks, UPLINK } from "./fixtures-uplinks";
import type * as GraphModule from "./graph";
import type { Drawing } from "./graph";
import { graphEnv } from "./graph";

// The drawing the page made last, to look at its lines as a reader would see them.
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
});
afterEach(() => {
  graphEnv.headless = false;
  graphEnv.layout = "elk";
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

function routes(links: RelationLink[], live?: Handler): Record<string, Handler> {
  return {
    ...defaultRoutes(READER),
    "GET /relations": (c: Call) => page((c.query.get("type") ?? "").includes("cabled_to") ? links : sampleContext(), { limit: 1000 }),
    "GET /objects": (c: Call) => {
      switch (c.query.get("type")) {
        case "device":
          return page(
            DEVICES_WITH_AP.map((d) => obj(d.id, "device", d.name, { site_id: d.siteId, attributes: { role: d.role } })),
            { limit: 1000 },
          );
        case "site":
          return page([obj(T.paris, "site", "paris-1"), obj(T.lyon, "site", "lyon-1")], { limit: 1000 });
        default:
          return page([], { limit: 1000 });
      }
    },
    ...(live ? { "GET /topology/live": live } : {}),
  };
}

async function open(links: RelationLink[], locale: "en" | "fr" = "en", live?: Handler) {
  const api = mockApi(routes(links, live));
  renderApp({ at: "/topology?view=network", locale });
  await screen.findByRole("table");
  return api;
}

const withUplinks = () => [...sampleLinks(), ...sampleUplinks()];
const tableRows = () => within(screen.getByRole("table")).getAllByRole("row").slice(1);
const uplinkRows = (kind: string) => tableRows().filter((r) => within(r).queryAllByText(kind).length > 0);

describe("uplinks on the topology page", () => {
  it("asks for them with the cables, and lists each one with its ports said unknown", async () => {
    const api = await open(withUplinks());
    expect(api.calls.some((c) => c.path.includes("/relations") && (c.query.get("type") ?? "").split(",").includes("uplinked_to"))).toBe(true);
    const rows = uplinkRows("uplink (port unknown)");
    expect(rows).toHaveLength(3);
    const ap = rows.find((r) => within(r).queryByRole("link", { name: "ap-01" }));
    expect(ap).toBeDefined();
    // No port to link to: the device, and the words.
    expect(within(ap!).getAllByText("port unknown").length).toBe(2);
    expect(within(ap!).getByText("unifi:office-lan")).toBeDefined();
    expect(within(ap!).getAllByRole("link").map((a) => a.getAttribute("href"))).toEqual([`/objects/${UPLINK.ap01}`, `/objects/${T.swA2}`]);
    expect(screen.getByText("7 links · 5 devices")).toBeDefined();
  });

  it("draws a line for the two uplinks no cable covers, not for the one a cable covers", async () => {
    await open(withUplinks());
    const canvas = screen.getByRole("img");
    expect(canvas.getAttribute("data-edges")).toBe(String(drawn.last!.cy.edges().length));
    expect(drawn.last!.cy.edges().filter((e) => e.data("uplink") === true).length).toBe(2);
  });

  it("explains the line in the legend only when one is drawn", async () => {
    await open(withUplinks());
    const legend = screen.getByRole("list", { name: "Legend" });
    expect(within(legend).getAllByRole("listitem")).toHaveLength(4);
    expect(within(legend).getByText(/Uplink: a controller names the device/)).toBeDefined();
  });

  it("has no uplink entry in the legend when no uplink is drawn", async () => {
    await open(sampleLinks());
    expect(within(screen.getByRole("list", { name: "Legend" })).getAllByRole("listitem")).toHaveLength(3);
  });

  it("opens the uplink's details from the table: who reaches the network through whom, and why nothing is measured", async () => {
    await open(withUplinks());
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Show the details of the uplink ap-01 → sw-a2" }));
    const panel = await screen.findByRole("complementary", { name: "Details" });
    expect(document.activeElement).toBe(within(panel).getByRole("heading", { level: 2 }));
    expect(within(panel).getByRole("heading", { level: 3, name: "ap-01 → sw-a2 (uplink)" })).toBeDefined();
    expect(within(panel).getByText("ap-01 reaches the network through sw-a2, as unifi:office-lan reports it; the ports are not known.")).toBeDefined();
    expect(within(panel).getByText("An uplink names no port: there is nothing to measure.")).toBeDefined();
    expect(drawn.last!.cy.$(":selected").length).toBe(1);
    expect(drawn.last!.cy.$(":selected").data("uplink")).toBe(true);
  });

  it("opens the cable's details from the row of an uplink it covers, and says the uplink there", async () => {
    await open(withUplinks());
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Show the details of the uplink sw-a2 → sw-a1" }));
    const panel = await screen.findByRole("complementary", { name: "Details" });
    expect(within(panel).getByText("Also reported as an uplink: sw-a2 → sw-a1 (unifi:office-lan)")).toBeDefined();
    expect(drawn.last!.cy.$(":selected").data("uplink")).toBeUndefined();
  });

  it("is in French as well, and passes the accessibility checks in both languages", async () => {
    await open(withUplinks(), "fr");
    expect(uplinkRows("remontée (port inconnu)")).toHaveLength(3);
    expect(within(screen.getByRole("list", { name: "Légende" })).getAllByRole("listitem")).toHaveLength(4);
    await expectNoA11yViolations();
  });

  it("passes the accessibility checks in English, with the details of an uplink open", async () => {
    await open(withUplinks());
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Show the details of the uplink ap-01 → sw-a2" }));
    await screen.findByRole("complementary", { name: "Details" });
    await expectNoA11yViolations();
  });
});

describe("uplinks and the live overlay", () => {
  it("leaves an uplink without a live style, while the cables get theirs", async () => {
    await open(withUplinks(), "en", sampleLive());
    await screen.findByRole("columnheader", { name: "Live" });
    await waitFor(() => expect(drawn.last!.cy.edges(".live").length).toBeGreaterThan(0));
    const uplinks = drawn.last!.cy.edges().filter((e) => e.data("uplink") === true);
    expect(uplinks.length).toBe(2);
    expect(uplinks.filter((e) => e.hasClass("live") || e.hasClass("live-unknown")).length).toBe(0);
    const cables = drawn.last!.cy.edges().filter((e) => e.data("uplink") !== true);
    expect(cables.filter((e) => e.hasClass("live")).length).toBe(cables.length);
    // In the table, an uplink has no figure.
    const row = uplinkRows("uplink (port unknown)")[0]!;
    expect(within(row).getAllByText("—").length).toBeGreaterThan(0);
  });

  it("opens an uplink's details without live figures when the overlay is on", async () => {
    await open(withUplinks(), "en", sampleLive());
    await screen.findByRole("columnheader", { name: "Live" });
    const ap = drawn.last!.cy.edges().filter((e) => e.data("uplink") === true).first();
    act(() => void ap.emit("tap"));
    const panel = await screen.findByRole("complementary", { name: "Details" });
    expect(within(panel).queryByText("Speed")).toBeNull();
    expect(within(panel).getByText("An uplink names no port: there is nothing to measure.")).toBeDefined();
  });
});

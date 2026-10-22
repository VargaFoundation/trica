import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { READER } from "../../test/fixtures";
import { defaultRoutes, mockApi, renderApp, type Call } from "../../test/support";
import { MAPS, withLive } from "./testdata/maps";

vi.setConfig({ testTimeout: 20_000 });
const NOW = Date.parse("2026-03-01T08:00:05Z");

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true, now: NOW });
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function serve(live = true) {
  return mockApi({
    ...defaultRoutes(READER),
    "GET /topology/map": (c: Call) => {
      const m = MAPS[(c.query.get("level") ?? "global") as keyof typeof MAPS];
      return withLive(m, live);
    },
  });
}
const levelsAsked = (api: ReturnType<typeof serve>) =>
  api.calls.filter((c) => c.path.endsWith("/topology/map")).map((c) => `${c.query.get("level")}:${c.query.get("id") ?? ""}`);
const scene = () => document.querySelector<SVGSVGElement>(".iso-svg");
const ready = () => waitFor(() => expect(scene()?.getAttribute("data-state")).toBe("drawn"), { timeout: 8000 });
const item = (name: RegExp) => screen.getByRole("button", { name });

describe("the topology map", () => {
  it("opens on the world, with the Map tab selected and the sites as buttons", async () => {
    const api = serve();
    renderApp({ at: "/topology" });
    await ready();
    expect(screen.getByRole("tab", { name: "Map" })).toHaveAttribute("aria-selected", "true");
    expect(scene()).toHaveAttribute("data-level", "global");
    expect(item(/^Site lyon-1/)).toBeInTheDocument();
    expect(item(/^Site paris-1/)).toBeInTheDocument();
    expect(levelsAsked(api)).toEqual(["global:"]);
  });

  it("drills down by click, then by Enter, and goes back up with Escape", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const api = serve();
    renderApp({ at: "/topology" });
    await ready();
    await user.click(item(/^Site paris-1/));
    await waitFor(() => expect(levelsAsked(api)).toContain("site:<paris>"));
    await waitFor(() => expect(scene()).toHaveAttribute("data-level", "site"));
    await ready();

    item(/^Room A1/).focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(levelsAsked(api)).toContain("room:<room>"));
    await waitFor(() => expect(scene()).toHaveAttribute("data-level", "room"));
    await ready();

    await user.keyboard("{Escape}");
    await waitFor(() => expect(scene()).toHaveAttribute("data-level", "site"));
  });

  it("selects an item with Space and shows its panel without leaving the level", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    serve();
    renderApp({ at: "/topology?level=room&id=%3Croom%3E" });
    await ready();
    item(/^Rack R1/).focus();
    await user.keyboard(" ");
    const panel = await screen.findByRole("heading", { name: "R1", level: 3 });
    expect(panel).toBeInTheDocument();
    expect(scene()).toHaveAttribute("data-level", "room");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("heading", { name: "R1", level: 3 })).toBeNull());
    expect(scene()).toHaveAttribute("data-level", "room");
  });

  it("lists the links of the level and opens one from the table", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    serve();
    renderApp({ at: "/topology?level=room&id=%3Croom%3E" });
    await ready();
    const table = await screen.findByRole("table");
    const buttons = within(table).getAllByRole("button", { name: /Details of the link/ });
    expect(buttons.length).toBe(MAPS.room.edges.length);
    await user.click(buttons[0] as HTMLElement);
    expect(await screen.findByRole("button", { name: "Close the details" })).toBeInTheDocument();
  });

  it("lists at most 50 links, and says how many are left out", async () => {
    mockApi({
      ...defaultRoutes(READER),
      "GET /topology/map": () => {
        const base = MAPS.room;
        const [a, b] = base.nodes.filter((n) => n.kind === "rack");
        const edges = Array.from({ length: 70 }, (_, i) => ({ ...(base.edges[0] as (typeof base.edges)[number]), id: `${a?.id}~${b?.id}~${i}` , a: a?.id ?? "", b: b?.id ?? "" }));
        return { ...base, edges };
      },
    });
    renderApp({ at: "/topology?level=room&id=%3Croom%3E" });
    await ready();
    const table = await screen.findByRole("table");
    expect(within(table).getAllByRole("button", { name: /Details of the link/ })).toHaveLength(50);
    expect(screen.getByText(/first 50 of 70 links/)).toBeInTheDocument();
  });

  it("asks again every 30 s while live data are on, and never when they are off", async () => {
    const on = serve(true);
    const view = renderApp({ at: "/topology" });
    await ready();
    expect(levelsAsked(on)).toHaveLength(1);
    await act(() => vi.advanceTimersByTimeAsync(30_000));
    await waitFor(() => expect(levelsAsked(on).length).toBeGreaterThanOrEqual(2));
    view.unmount();

    const off = serve(false);
    renderApp({ at: "/topology" });
    await ready();
    await act(() => vi.advanceTimersByTimeAsync(95_000));
    expect(levelsAsked(off)).toHaveLength(1);
  });

  it("moves the traffic of measured lines, and nothing when the system asks for less motion", async () => {
    serve();
    renderApp({ at: "/topology?level=room&id=%3Croom%3E" });
    await ready();
    await waitFor(() => expect(document.querySelectorAll(".iso-flow").length).toBeGreaterThan(0));
    document.body.innerHTML = "";

    serve();
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: q.includes("reduce"), media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }));
    renderApp({ at: "/topology?level=room&id=%3Croom%3E" });
    await ready();
    expect(document.querySelectorAll(".iso-flow")).toHaveLength(0);
    expect(scene()).toHaveAttribute("data-lanes", "0");
    expect(screen.getByRole("checkbox", { name: /animate the traffic/i })).toBeDisabled();
  });

  it("falls back to the world with a notice when the address is unusable", async () => {
    serve();
    renderApp({ at: "/topology?level=site" });
    await ready();
    expect(scene()).toHaveAttribute("data-level", "global");
    expect(screen.getByText(/names no place the map can show/)).toBeInTheDocument();
  });

  it.each(["site=paris", "rack=r12", "vlan=10", "classes=cable"])("opens the Network view for the old address ?%s", async (q) => {
    serve();
    renderApp({ at: `/topology?${q}` });
    expect(await screen.findByRole("tab", { name: "Network" })).toHaveAttribute("aria-selected", "true");
    expect(scene()).toBeNull();
  });

  it("switches to the Network tab and back", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    serve();
    renderApp({ at: "/topology" });
    await ready();
    await user.click(screen.getByRole("tab", { name: "Network" }));
    await waitFor(() => expect(screen.getByRole("tab", { name: "Network" })).toHaveAttribute("aria-selected", "true"));
    await user.click(screen.getByRole("tab", { name: "Map" }));
    await ready();
  });
});

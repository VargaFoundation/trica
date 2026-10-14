import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { rackR12, searchSw, swA1 } from "../test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp } from "../test/support";

describe("search results", () => {
  it("are grouped by type, largest group first, with the type counts of the server", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: "/search?q=sw" });

    const device = await screen.findByRole("heading", { level: 2, name: /Device/ });
    const rack = screen.getByRole("heading", { level: 2, name: /Rack/ });
    expect(device).toHaveTextContent("Device (2)");
    expect(rack).toHaveTextContent("Rack (1)");
    // Document order is the display order: 2 devices before 1 rack.
    expect(device.compareDocumentPosition(rack) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    const deviceGroup = device.closest("section") as HTMLElement;
    expect(within(deviceGroup).getByRole("link", { name: "sw-a1" })).toHaveAttribute("href", `/objects/${swA1.id}`);
    expect(within(deviceGroup).getByRole("link", { name: "sw-a2" })).toBeInTheDocument();
    expect(within(deviceGroup).queryByRole("link", { name: "r12" })).not.toBeInTheDocument();
    const rackGroup = rack.closest("section") as HTMLElement;
    expect(within(rackGroup).getByRole("link", { name: "r12" })).toBeInTheDocument();

    const call = api.calls.find((c) => c.path === "/search");
    expect(call?.query.get("q")).toBe("sw");
    expect(call?.query.get("limit")).toBe("100");
    expect(screen.getByRole("status")).toHaveTextContent("3 results in 4 ms");
  });

  it("puts the larger group first even when its name sorts later", async () => {
    mockApi({
      ...defaultRoutes(),
      "GET /search": {
        ...searchSw,
        total: 3,
        summary: { ...searchSw.summary, by_type: { device: 1, rack: 2 } },
        items: [swA1, rackR12, { ...rackR12, id: "0a000000-0000-4000-8000-0000000000b2", display: "r13" }],
      },
    });
    renderApp({ at: "/search?q=sw" });
    const rack = await screen.findByRole("heading", { level: 2, name: /Rack/ });
    const device = screen.getByRole("heading", { level: 2, name: /Device/ });
    expect(rack.compareDocumentPosition(device) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("offers every type that has results, even when its objects are not in the first page", async () => {
    mockApi({
      ...defaultRoutes(),
      "GET /search": { ...searchSw, total: 9, summary: { ...searchSw.summary, total: 9, by_type: { device: 2, rack: 1, vlan: 6 } } },
    });
    renderApp({ at: "/search?q=sw" });
    const vlan = await screen.findByRole("heading", { level: 2, name: /vlan/ });
    const group = vlan.closest("section") as HTMLElement;
    expect(within(group).queryByRole("table")).not.toBeInTheDocument();
    expect(within(group).getByRole("link", { name: "Show all 6 vlan" })).toHaveAttribute("href", "/search?q=sw&type=vlan");
  });

  it("link to the whole type when only part of it is shown", async () => {
    mockApi({
      ...defaultRoutes(),
      "GET /search": { ...searchSw, summary: { ...searchSw.summary, by_type: { device: 40, rack: 1 } } },
    });
    renderApp({ at: "/search?q=sw" });
    const link = await screen.findByRole("link", { name: "Show all 40 Device" });
    expect(link).toHaveAttribute("href", "/search?q=sw&type=device");
    expect(screen.getByText("2 of 40 shown")).toBeInTheDocument();
  });

  it("a type view is an address of its own: it asks /objects for that type and pages through it", async () => {
    const api = mockApi({
      ...defaultRoutes(),
      "GET /objects": (c) => ({
        summary: { query: "sw", total: 120, by_type: { device: 120 }, fuzzy: false, took_ms: 2 },
        total: 120,
        limit: 50,
        offset: Number(c.query.get("offset") ?? 0),
        items: [swA1],
      }),
    });
    renderApp({ at: "/search?q=sw&type=device&offset=50" });

    await screen.findByRole("link", { name: "sw-a1" });
    const call = api.calls.find((c) => c.path === "/objects");
    expect(call?.query.get("type")).toBe("device");
    expect(call?.query.get("q")).toBe("sw");
    expect(call?.query.get("offset")).toBe("50");
    expect(api.count("GET /search")).toBe(0);

    const pager = screen.getByRole("navigation", { name: "Pagination" });
    expect(within(pager).getByText("51–100 of 120")).toBeInTheDocument();
    expect(within(pager).getByRole("link", { name: "Previous" })).toHaveAttribute("href", "/search?q=sw&type=device");
    expect(within(pager).getByRole("link", { name: "Next" })).toHaveAttribute("href", "/search?q=sw&type=device&offset=100");
    expect(screen.getByRole("link", { name: "All types" })).toHaveAttribute("href", "/search?q=sw");
  });

  it("say when the match is approximate", async () => {
    mockApi({ ...defaultRoutes(), "GET /search": { ...searchSw, summary: { ...searchSw.summary, fuzzy: true } } });
    renderApp({ at: "/search?q=swa-1" });
    expect(await screen.findByText(/showing approximate matches for swa-1/)).toBeInTheDocument();
  });

  it("say when nothing matches", async () => {
    mockApi({
      ...defaultRoutes(),
      "GET /search": { summary: { query: "zzz", total: 0, by_type: {}, fuzzy: false, took_ms: 1 }, total: 0, limit: 100, offset: 0, items: [] },
    });
    renderApp({ at: "/search?q=zzz" });
    expect(await screen.findByText("Nothing matches zzz.")).toBeInTheDocument();
  });

  it("show the server's reason when the query is not understood", async () => {
    mockApi({ ...defaultRoutes(), "GET /search": problem(400, "bad-query", "unexpected ) at position 7") });
    renderApp({ at: "/search?q=type:(" });
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The query was not understood");
    expect(alert).toHaveTextContent("unexpected ) at position 7");
  });

  it("without a query, do not call the server", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: "/search" });
    await screen.findByText(/Press \/ or Ctrl\+K/);
    expect(api.count("GET /search")).toBe(0);
  });

  it("the search bar submits to a deep link and keeps the query visible", async () => {
    const api = mockApi({ ...defaultRoutes(), "GET /search": { ...searchSw, items: [swA1, rackR12], total: 2 } });
    renderApp();
    const user = userEvent.setup();
    await user.type(await screen.findByRole("searchbox", { name: "Search the inventory" }), "type:device sw-a1{Enter}");
    expect(await screen.findByRole("heading", { level: 1, name: "Search: type:device sw-a1" })).toBeInTheDocument();
    expect(api.calls.find((c) => c.path === "/search")?.query.get("q")).toBe("type:device sw-a1");
    expect(screen.getByRole("searchbox")).toHaveValue("type:device sw-a1");
  });
});

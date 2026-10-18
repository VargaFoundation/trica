import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ID, page, srv01, swA1 } from "../test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp } from "../test/support";

const fleet = () => ({ ...defaultRoutes(), "GET /objects": (c: { query: URLSearchParams }) => (c.query.get("type") === "device" ? page([swA1, srv01]) : page([])) });

function tileOf(name: string): HTMLElement {
  return screen.getByRole("link", { name }).closest("li.tile") as HTMLElement;
}

describe("the overview: the physical estate at a glance", () => {
  it("asks for every device and where it is mounted", async () => {
    const api = mockApi(fleet());
    renderApp({ at: "/" });
    await screen.findByRole("link", { name: "srv-01" });
    const devices = api.calls.find((c) => c.path === "/objects");
    expect(devices?.query.get("type")).toBe("device");
    expect(devices?.query.get("limit")).toBe("1000");
    expect(api.calls.find((c) => c.path === "/relations")?.query.get("type")).toBe("mounted_in");
  });

  it("shows one tile per device with its kind, health, power, temperatures and critical events", async () => {
    mockApi(fleet());
    renderApp({ at: "/" });
    await screen.findByRole("link", { name: "srv-01" });
    const srv = tileOf("srv-01");
    expect(srv).toHaveAttribute("data-health", "warning");
    expect(srv).toHaveAttribute("data-kind", "server");
    expect(within(srv).getByText("Server · U10")).toBeInTheDocument();
    expect(within(srv).getByText("Warning")).toBeInTheDocument();
    expect(within(srv).getByText("413 W")).toBeInTheDocument();
    expect(within(srv).getByText("23 °C")).toBeInTheDocument();
    expect(within(srv).getByText("38.5 °C")).toBeInTheDocument();
    expect(within(srv).getByText("2 critical events")).toBeInTheDocument();
    expect(within(srv).getByRole("link", { name: "srv-01" })).toHaveAttribute("href", `/objects/${ID.srv01}`);
    // A device that reports no health is said to be unknown, not ok.
    expect(tileOf("sw-a1")).toHaveAttribute("data-health", "unknown");
  });

  it("counts the devices by health and adds up their power", async () => {
    mockApi(fleet());
    renderApp({ at: "/" });
    const stats = await screen.findByRole("list", { name: "Equipment health in figures" });
    const stat = (id: string) => stats.querySelector(`[data-stat="${id}"] .stat-value`)?.textContent;
    await waitFor(() => expect(stat("total")).toBe("2"));
    expect(stat("ok")).toBe("0");
    expect(stat("warning")).toBe("1");
    expect(stat("unknown")).toBe("1");
    expect(stat("power")).toBe("533 W");
  });

  it("groups the devices by rack, top first, with a link to the elevation", async () => {
    mockApi(fleet());
    renderApp({ at: "/" });
    const rack = await screen.findByRole("link", { name: "Open the elevation of r12" });
    expect(rack).toHaveAttribute("href", `/racks/${ID.rack}`);
    const group = rack.closest(".health-group") as HTMLElement;
    expect(within(group).getAllByRole("link", { name: /^(sw-a1|srv-01)$/ }).map((l) => l.textContent)).toEqual(["sw-a1", "srv-01"]);
    expect(within(group).getByRole("group", { name: "Health of this group" })).toHaveTextContent("1 warning");
  });

  it("can show only what needs attention", async () => {
    mockApi(fleet());
    renderApp({ at: "/" });
    await screen.findByRole("link", { name: "sw-a1" });
    await userEvent.setup().click(screen.getByRole("checkbox", { name: "Only what needs attention" }));
    expect(screen.queryByRole("link", { name: "sw-a1" })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "srv-01" })).toBeInTheDocument();
  });

  it("can group by kind instead", async () => {
    mockApi(fleet());
    renderApp({ at: "/" });
    await screen.findByRole("link", { name: "sw-a1" });
    await userEvent.setup().selectOptions(screen.getByRole("combobox", { name: "Group by" }), "kind");
    expect(screen.getByRole("heading", { level: 3, name: "Server" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 3, name: "Device" })).toBeInTheDocument();
  });

  it("falls back to kinds, and says so, when the rack locations cannot be loaded", async () => {
    mockApi({ ...fleet(), "GET /relations": problem(500, "internal") });
    renderApp({ at: "/" });
    expect(await screen.findByText("Rack locations could not be loaded: devices are grouped by kind.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "srv-01" })).toBeInTheDocument();
  });

  it("folds the healthy devices of a large group, and unfolds them on request", async () => {
    const many = Array.from({ length: 20 }, (_, i) => ({ ...srv01, id: `dev-${i}`, display: `node-${String(i).padStart(2, "0")}`, attributes: { role: "server", health: i === 19 ? "critical" : "ok" } }));
    mockApi({ ...fleet(), "GET /objects": page(many) });
    renderApp({ at: "/" });
    await screen.findByRole("link", { name: "node-19" });
    expect(document.querySelectorAll("li.tile")).toHaveLength(12);
    const more = screen.getByRole("button", { name: "Show all 20 devices (8 more, all ok)" });
    await userEvent.setup().click(more);
    expect(document.querySelectorAll("li.tile")).toHaveLength(20);
    expect(screen.getByRole("button", { name: "Fold the healthy devices" })).toHaveAttribute("aria-expanded", "true");
  });

  it("says when the inventory has no device", async () => {
    mockApi({ ...defaultRoutes(), "GET /objects": page([]) });
    renderApp({ at: "/" });
    expect(await screen.findByText("No device in the inventory yet.")).toBeInTheDocument();
  });

  it("says when only part of the inventory is drawn", async () => {
    mockApi({ ...fleet(), "GET /objects": page([swA1, srv01], { total: 1500 }) });
    renderApp({ at: "/" });
    expect(await screen.findByText("Only the first 2 devices are shown: the inventory holds more.")).toBeInTheDocument();
  });

  it("is in French too", async () => {
    mockApi(fleet());
    renderApp({ at: "/", locale: "fr" });
    await screen.findByRole("link", { name: "srv-01" });
    expect(within(tileOf("srv-01")).getByText("Serveur · U10")).toBeInTheDocument();
    expect(within(tileOf("srv-01")).getByText("2 événements critiques")).toBeInTheDocument();
  });
});

describe("the health page", () => {
  it("shows the equipment, grouped by kind, then the collection", async () => {
    mockApi(fleet());
    renderApp({ at: "/health" });
    expect(await screen.findByRole("heading", { level: 2, name: "Equipment" })).toBeInTheDocument();
    expect(await screen.findByRole("heading", { level: 3, name: "Server" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Collection" })).toBeInTheDocument();
    expect(await screen.findByText("2 sources, 1 failing")).toBeInTheDocument();
  });
});

describe("the object page header", () => {
  it("says what the object is: icon, role and type, health", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.srv01}` });
    await screen.findByRole("heading", { level: 1, name: "srv-01" });
    expect(document.querySelector(".object-head [data-kind]")).toHaveAttribute("data-kind", "server");
    expect(document.querySelector(".object-head [data-icon]")).toHaveAttribute("data-icon", "server");
    expect(await screen.findByText("Server")).toBeInTheDocument();
    expect(document.querySelector(".object-head .health-pill")).toHaveAttribute("data-value", "warning");
  });

  it("a switch is not drawn like a server", async () => {
    mockApi({ ...defaultRoutes(), [`GET /objects/${ID.swA1}`]: { ...swA1, attributes: { ...swA1.attributes, role: "switch" } } });
    renderApp({ at: `/objects/${ID.swA1}` });
    await screen.findByRole("heading", { level: 1, name: "sw-a1" });
    await waitFor(() => expect(document.querySelector(".object-head [data-icon]")).toHaveAttribute("data-icon", "switch"));
    expect(screen.getByText("Switch")).toBeInTheDocument();
  });

  it("puts the key facts forward, with their units formatted", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.srv01}` });
    await screen.findByRole("heading", { level: 1, name: "srv-01" });
    const dl = document.querySelector("dl.facts") as HTMLElement;
    await waitFor(() => expect(dl.querySelector('[data-fact="uptime_s"] dd')).toHaveTextContent("15 d 2 h"));
    expect(dl.querySelector('[data-fact="power_w"] dd')).toHaveTextContent("413 W");
    expect(dl.querySelector('[data-fact="mgmt_ip"] dd')).toHaveTextContent("198.51.100.21");
    expect(dl.querySelector('[data-fact="vendor"] dd')).toHaveTextContent("dell");
  });

  it("gives the location from the mounted_in relation, with the position", async () => {
    mockApi({
      ...defaultRoutes(),
      [`GET /objects/${ID.srv01}/relations`]: page([
        { id: 9, type: "mounted_in", from_id: ID.srv01, to_id: ID.rack, source: "s", confidence: "declared", valid_from: "2026-03-01T08:00:00Z", direction: "out", other: { id: ID.rack, type: "rack", display: "r12" }, attributes: { position: 10, face: "rear" } },
      ]),
    });
    renderApp({ at: `/objects/${ID.srv01}` });
    await screen.findByRole("heading", { level: 1, name: "srv-01" });
    await waitFor(() => expect(document.querySelector('[data-fact="location"]')).toHaveTextContent("r12 U10, rear"));
  });

  it("sets the breadcrumb trail: overview, site, parent, object", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.swA1}` });
    const trail = await screen.findByRole("navigation", { name: "Breadcrumb" });
    await waitFor(() => expect(within(trail).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["Overview", "paris-1", "r12", "sw-a1"]));
    expect(within(trail).getByRole("link", { name: "paris-1" })).toHaveAttribute("href", `/objects/${ID.site}`);
    expect(within(trail).getByText("sw-a1")).toHaveAttribute("aria-current", "page");
  });
});

describe("the object page tabs", () => {
  it("opens on the overview, attributes grouped by what they are", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.srv01}` });
    expect(await screen.findByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true");
    const panel = screen.getByRole("tabpanel");
    const measurements = await within(panel).findByRole("heading", { level: 3, name: "Measurements" });
    const group = measurements.closest(".card") as HTMLElement;
    expect(within(group).getByText("Memory").nextElementSibling).toHaveTextContent("256 GiB");
    expect(within(group).getByText("Inlet temperature").nextElementSibling).toHaveTextContent("23 °C");
    const state = within(panel).getByRole("heading", { level: 3, name: "State" }).closest(".card") as HTMLElement;
    expect(state.querySelector(".health-pill")).toHaveAttribute("data-value", "warning");
  });

  it("the arrow keys move between tabs and the address keeps the tab", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.srv01}` });
    const user = userEvent.setup();
    const overview = await screen.findByRole("tab", { name: "Overview" });
    overview.focus();
    await user.keyboard("{ArrowRight}");
    const components = screen.getByRole("tab", { name: /^Components/ });
    expect(components).toHaveFocus();
    expect(components).toHaveAttribute("aria-selected", "true");
    expect(await screen.findByRole("table", { name: "Power supply of srv-01" })).toBeInTheDocument();
    await user.keyboard("{End}");
    expect(screen.getByRole("tab", { name: "Provenance" })).toHaveAttribute("aria-selected", "true");
  });

  it("lists the components by type, each with its health, the worst first", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.srv01}?tab=components` });
    const psus = await screen.findByRole("table", { name: "Power supply of srv-01" });
    expect(api.calls.find((c) => c.path === "/search")?.query.get("q")).toBe(`parent:"${ID.srv01}"`);
    const rows = within(psus).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.getAttribute("data-health"))).toEqual(["critical", "ok"]);
    expect(within(rows[0]!).getByRole("link", { name: "PSU 2" })).toBeInTheDocument();
    expect(within(psus).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Health", "Capacity", "Input power"]);
    expect(within(rows[1]!).getByText("210 W")).toBeInTheDocument();
    const disks = screen.getByRole("table", { name: "Disk of srv-01" });
    expect(within(disks).getByText("894 GiB")).toBeInTheDocument();
    expect(screen.getByText("3 components, 1 needing attention")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /^Components/ })).toHaveTextContent("3");
  });

  it("keeps only the components really inside the object", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.swA1}?tab=components` });
    expect(await screen.findByText("No component recorded inside this object.")).toBeInTheDocument();
  });
});

describe("the sidebar", () => {
  it("folds into a rail of icons, keeps the names for a screen reader, and remembers it", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/proposals" });
    const user = userEvent.setup();
    const toggle = await screen.findByRole("button", { name: "Collapse sidebar" });
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    await user.click(toggle);
    expect(document.querySelector(".shell")).toHaveAttribute("data-sidebar", "rail");
    expect(localStorage.getItem("trica.sidebar")).toBe("rail");
    expect(screen.getByRole("button", { name: "Expand sidebar" })).toHaveAttribute("aria-expanded", "false");
    const nav = screen.getByRole("navigation", { name: "Main" });
    expect(within(nav).getByRole("link", { name: "Proposals" })).toHaveAttribute("aria-current", "page");
    expect(within(nav).getByRole("link", { name: "Health" })).toHaveAttribute("title", "Health");
  });

  it("starts folded when it was left folded", async () => {
    localStorage.setItem("trica.sidebar", "rail");
    mockApi(defaultRoutes());
    renderApp({ at: "/proposals" });
    expect(await screen.findByRole("button", { name: "Expand sidebar" })).toBeInTheDocument();
  });

  it("lists every section", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/proposals" });
    const nav = await screen.findByRole("navigation", { name: "Main" });
    expect(within(nav).getAllByRole("link").map((l) => [l.textContent, l.getAttribute("href")])).toEqual([
      ["Overview", "/"],
      ["Health", "/health"],
      ["Search", "/search"],
      ["Rooms and racks", "/rooms"],
      ["Topology", "/topology"],
      ["Discrepancies", "/discrepancies"],
      ["Proposals", "/proposals"],
    ]);
  });
});

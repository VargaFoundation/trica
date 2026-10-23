import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { discrepancyLocation, discrepancyLocationRack, discrepancyMismatch, healthFailing, healthOk, ID, locationConflictIDs, page } from "../test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp } from "../test/support";

describe("discrepancies", () => {
  it("lists the open ones by default, declared against observed, with a link to the object", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: "/discrepancies" });
    const row = (await screen.findByText("value_mismatch")).closest("tr") as HTMLElement;
    expect(api.calls.find((c) => c.path === "/discrepancies")?.query.get("status")).toBe("open");
    expect(row.querySelector('td[data-label="Declared"]')).toHaveTextContent("4.27.0M");
    expect(row.querySelector('td[data-label="Observed"]')).toHaveTextContent("4.28.3M");
    expect(row.querySelector('td[data-label="Attribute"]')).toHaveTextContent("os_version");
    expect(row.querySelector('td[data-label="Status"] .badge')).toHaveAttribute("data-value", "open");
    expect(await within(row).findByRole("link", { name: "sw-a1" })).toHaveAttribute("href", `/objects/${ID.swA1}`);
  });

  it("takes its filters from the address", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: "/discrepancies?status=resolved&kind=value_mismatch&offset=50" });
    await screen.findByRole("heading", { level: 1, name: "Discrepancies" });
    await waitFor(() => expect(api.count("GET /discrepancies")).toBe(1));
    const q = api.calls.find((c) => c.path === "/discrepancies")?.query;
    expect(q?.get("status")).toBe("resolved");
    expect(q?.get("kind")).toBe("value_mismatch");
    expect(q?.get("offset")).toBe("50");
    expect(screen.getByRole("combobox", { name: "Status" })).toHaveValue("resolved");
    expect(screen.getByRole("combobox", { name: "Kind" })).toHaveValue("value_mismatch");
  });

  it("keeps a kind from the address that this build does not know selectable", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/discrepancies?kind=future_kind" });
    await screen.findByText("value_mismatch");
    const select = screen.getByRole("combobox", { name: "Kind" });
    expect(select).toHaveValue("future_kind");
    expect(within(select).getByRole("option", { name: "future_kind" })).toBeInTheDocument();
  });

  it("names every kind, and filters by the chosen one", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: "/discrepancies" });
    const user = userEvent.setup();
    await screen.findByText("value_mismatch");
    const select = screen.getByRole("combobox", { name: "Kind" });
    expect(within(select).getAllByRole("option").map((o) => o.textContent)).toEqual([
      "All kinds",
      "Value mismatch",
      "Unknown neighbour",
      "Unresolved reference",
      "Cable not observed",
      "Cable not declared",
      "Cable conflict",
      "Places in different sites",
    ]);
    await user.selectOptions(select, "location_conflict");
    await user.click(screen.getByRole("button", { name: "Filter" }));
    await waitFor(() => expect(api.calls.some((c) => c.path === "/discrepancies" && c.query.get("kind") === "location_conflict")).toBe(true));
  });

  it("a location conflict lists its places, their sites and who said so", async () => {
    mockApi({ ...defaultRoutes(), "GET /discrepancies": page([discrepancyLocation, discrepancyLocationRack]) });
    renderApp({ at: "/discrepancies?kind=location_conflict" });
    const rows = await screen.findAllByText("location_conflict");
    const row = rows[0]?.closest("tr") as HTMLElement;
    expect(within(row).getByText("Places in different sites")).toBeInTheDocument();
    const declared = row.querySelector('td[data-label="Declared"]') as HTMLElement;
    const places = within(declared).getAllByRole("listitem");
    expect(places).toHaveLength(2);
    expect(within(places[0] as HTMLElement).getAllByRole("link")[0]).toHaveAttribute("href", `/objects/${locationConflictIDs.roomA}`);
    expect(within(places[0] as HTMLElement).getAllByRole("link")[1]).toHaveAttribute("href", `/objects/${ID.site}`);
    expect(places[1]).toHaveTextContent("(user:dan, declaration:lab)");
    const rackRow = rows[1]?.closest("tr") as HTMLElement;
    const rackDeclared = rackRow.querySelector('td[data-label="Declared"]') as HTMLElement;
    expect(rackDeclared).toHaveTextContent("Mounted in");
    expect(rackDeclared).toHaveTextContent("the rack decides the site");
    expect(within(rackDeclared).getAllByRole("link")[0]).toHaveAttribute("href", `/objects/${ID.rack}`);
  });

  it("falls back to the plain value when the details are not what the engine writes", async () => {
    mockApi({ ...defaultRoutes(), "GET /discrepancies": page([{ ...discrepancyLocation, details: { reason: "other" }, declared: "plain" }]) });
    renderApp({ at: "/discrepancies" });
    const row = (await screen.findByText("location_conflict")).closest("tr") as HTMLElement;
    expect(row.querySelector('td[data-label="Declared"]')).toHaveTextContent("plain");
  });

  it("names the kinds in French", async () => {
    mockApi({ ...defaultRoutes(), "GET /discrepancies": page([discrepancyLocation]) });
    renderApp({ at: "/discrepancies", locale: "fr" });
    const row = (await screen.findByText("location_conflict")).closest("tr") as HTMLElement;
    expect(within(row).getByText("Emplacements dans des sites différents")).toBeInTheDocument();
    expect(within(row).getAllByText(/dans le site/)).toHaveLength(2);
  });

  it("all means no status filter", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: "/discrepancies?status=all" });
    await screen.findByText("value_mismatch");
    expect(api.calls.find((c) => c.path === "/discrepancies")?.query.has("status")).toBe(false);
  });

  it("the filter form moves to a new address and asks again", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: "/discrepancies" });
    const user = userEvent.setup();
    await screen.findByText("value_mismatch");
    await user.selectOptions(screen.getByRole("combobox", { name: "Status" }), "accepted");
    await user.selectOptions(screen.getByRole("combobox", { name: "Kind" }), "cable_conflict");
    await user.click(screen.getByRole("button", { name: "Filter" }));
    await waitFor(() => expect(api.calls.some((c) => c.path === "/discrepancies" && c.query.get("status") === "accepted" && c.query.get("kind") === "cable_conflict")).toBe(true));
  });

  it("pages through a long list", async () => {
    mockApi({ ...defaultRoutes(), "GET /discrepancies": page([discrepancyMismatch], { total: 120, offset: 50 }) });
    renderApp({ at: "/discrepancies?offset=50" });
    const pager = await screen.findByRole("navigation", { name: "Pagination" });
    expect(within(pager).getByRole("link", { name: "Next" })).toHaveAttribute("href", "/discrepancies?offset=100");
    expect(within(pager).getByRole("link", { name: "Previous" })).toHaveAttribute("href", "/discrepancies");
  });

  it("says when there is none", async () => {
    mockApi({ ...defaultRoutes(), "GET /discrepancies": page([]) });
    renderApp({ at: "/discrepancies" });
    expect(await screen.findByText("No discrepancy matches.")).toBeInTheDocument();
  });

  it("shows an error with a retry, not an empty list", async () => {
    let fail = true;
    mockApi({ ...defaultRoutes(), "GET /discrepancies": () => (fail ? problem(500, "internal", "database is down") : page([discrepancyMismatch])) });
    renderApp({ at: "/discrepancies" });
    expect(await screen.findByRole("alert")).toHaveTextContent("database is down");
    expect(screen.queryByText("No discrepancy matches.")).not.toBeInTheDocument();
    fail = false;
    await userEvent.setup().click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("value_mismatch")).toBeInTheDocument();
  });
});

describe("collection health", () => {
  it("puts the failing sources first and says how many fail", async () => {
    mockApi({ ...defaultRoutes(), "GET /health/collection": page([healthOk, healthFailing]) });
    renderApp({ at: "/health" });
    expect(await screen.findByText("2 sources, 1 failing")).toBeInTheDocument();
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows[0]).toHaveAttribute("data-ok", "false");
    expect(rows[0]).toHaveTextContent("snmp:core");
    expect(rows[1]).toHaveTextContent("arista:eos");
  });

  it("shows why a source fails and when it last worked", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/health" });
    const row = (await screen.findByText("snmp:core")).closest("tr") as HTMLElement;
    expect(row).toHaveTextContent("timeout after 30s");
    expect(row.querySelector(".badge")).toHaveTextContent("Failing");
    expect(row.querySelector('td[data-label="Last success"] time')).toHaveAttribute("datetime", "2026-10-11T06:25:00Z");
    expect(row.querySelector('td[data-label="Failures"]')).toHaveTextContent("4");
    const ok = screen.getByText("arista:eos").closest("tr") as HTMLElement;
    expect(ok.querySelector(".badge")).toHaveTextContent("OK");
  });

  it("links a source its module describes to its instance, and leaves the others as text", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/health" });
    const link = await screen.findByRole("link", { name: "arista:eos" });
    expect(link).toHaveAttribute("href", "/collectors/arista/eos");
    expect(screen.getByText("snmp:core").closest("a")).toBeNull();
    expect(screen.getByRole("link", { name: "Collectors and their configuration" })).toHaveAttribute("href", "/collectors");
  });

  it("says never when a source has never succeeded", async () => {
    mockApi({ ...defaultRoutes(), "GET /health/collection": page([{ ...healthFailing, last_success: undefined }]) });
    renderApp({ at: "/health" });
    const row = (await screen.findByText("snmp:core")).closest("tr") as HTMLElement;
    expect(row.querySelector('td[data-label="Last success"]')).toHaveTextContent("never");
  });

  it("says when no source has reported", async () => {
    mockApi({ ...defaultRoutes(), "GET /health/collection": page([]) });
    renderApp({ at: "/health" });
    expect(await screen.findByText("No source has reported yet.")).toBeInTheDocument();
  });
});

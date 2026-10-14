import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { discrepancyMismatch, healthFailing, healthOk, ID, page } from "../test/fixtures";
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
    expect(screen.getByRole("textbox", { name: "Kind" })).toHaveValue("value_mismatch");
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
    await user.type(screen.getByRole("textbox", { name: "Kind" }), "missing_object");
    await user.click(screen.getByRole("button", { name: "Filter" }));
    await waitFor(() => expect(api.calls.some((c) => c.path === "/discrepancies" && c.query.get("status") === "accepted" && c.query.get("kind") === "missing_object")).toBe(true));
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

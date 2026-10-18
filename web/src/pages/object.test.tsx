import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ID, page, provenanceSwA1, timelineSwA1 } from "../test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp } from "../test/support";

const at = `/objects/${ID.swA1}`;

async function attributeRow(name: RegExp): Promise<HTMLElement> {
  // The provenance table is under its tab: the tests that use this helper open the page there.
  const table = await screen.findByRole("table", { name: /Attributes of sw-a1/ });
  return within(table).getByRole("row", { name });
}

/** The ISO value of the <time> element in the cell of a row, by column title. */
function cellTime(row: HTMLElement, column: string): string | null | undefined {
  return row.querySelector(`td[data-label="${column}"] time`)?.getAttribute("datetime");
}

describe("object page header", () => {
  it("shows the object, its type and where it sits", async () => {
    mockApi(defaultRoutes());
    renderApp({ at });
    expect(await screen.findByRole("heading", { level: 1, name: "sw-a1" })).toBeInTheDocument();
    const facts = document.querySelector("dl.facts") as HTMLElement;
    expect(await within(facts).findByText("Device")).toBeInTheDocument();
    expect(await within(facts).findByRole("link", { name: "paris-1" })).toHaveAttribute("href", `/objects/${ID.site}`);
    expect(await within(facts).findByRole("link", { name: "r12" })).toHaveAttribute("href", `/objects/${ID.rack}`);
    expect(document.title).toBe("sw-a1 · trica");
  });
});

describe("object page attributes", () => {
  const at = `/objects/${ID.swA1}?tab=provenance`;
  it("shows the provenance of every attribute: source, confidence, first and last seen", async () => {
    mockApi(defaultRoutes());
    renderApp({ at });

    const table = await screen.findByRole("table", { name: /Attributes of sw-a1/ });
    const headers = within(table)
      .getAllByRole("columnheader")
      .map((h) => h.textContent);
    expect(headers).toEqual(["Attribute", "Value", "Source", "Confidence", "First seen", "Last seen"]);

    const row = within(table).getByRole("row", { name: /OS version/ });
    expect(row.querySelector('td[data-label="Source"]')).toHaveTextContent("arista:eos");
    expect(row.querySelector('td[data-label="Confidence"] .badge')).toHaveAttribute("data-value", "observed");
    expect(cellTime(row, "First seen")).toBe("2026-09-20T02:00:00Z");
    expect(cellTime(row, "Last seen")).toBe("2026-10-12T06:30:00Z");
  });

  it("gives each attribute its own source and confidence", async () => {
    mockApi(defaultRoutes());
    renderApp({ at });
    const ip = await attributeRow(/Management address/);
    expect(ip.querySelector('td[data-label="Source"]')).toHaveTextContent("snmp:core");
    expect(ip.querySelector('td[data-label="Confidence"] .badge')).toHaveAttribute("data-value", "deduced");
    const host = await attributeRow(/Hostname/);
    expect(host.querySelector('td[data-label="Source"]')).toHaveTextContent("arista:eos");
    expect(host.querySelector('td[data-label="Confidence"] .badge')).toHaveAttribute("data-value", "observed");
    expect(cellTime(host, "First seen")).toBe("2026-03-01T08:00:00Z");
  });

  it("shows the unit from the schema next to the value", async () => {
    mockApi(defaultRoutes());
    renderApp({ at });
    const row = await attributeRow(/Power draw/);
    expect(row.querySelector('td[data-label="Value"]')).toHaveTextContent("120 W");
  });

  it("lists the other claims of an attribute behind a disclosure", async () => {
    mockApi(defaultRoutes());
    renderApp({ at });
    const row = await attributeRow(/OS version/);
    const summary = within(row).getByText("1 other claim");
    const details = summary.closest("details") as HTMLDetailsElement;
    expect(details.open).toBe(false);
    await userEvent.setup().click(summary);
    expect(details.open).toBe(true);
    expect(within(details).getByText("declared:lab-file")).toBeInTheDocument();
    expect(within(details).getByText("4.27.0M")).toBeInTheDocument();
    expect(details.querySelector(".badge")).toHaveAttribute("data-value", "declared");
    expect(details.querySelector("time")).toHaveAttribute("datetime", "2026-03-01T08:00:00Z");
  });

  it("flags a value that has no provenance instead of passing it off as sourced", async () => {
    mockApi(defaultRoutes());
    renderApp({ at });
    const row = await attributeRow(/rack_unit/);
    expect(row).toHaveAttribute("data-has-provenance", "false");
    expect(within(row).getByText("No provenance recorded")).toBeInTheDocument();
    expect(within(row).getByText("U12")).toBeInTheDocument();
    expect(await attributeRow(/Hostname/)).toHaveAttribute("data-has-provenance", "true");
  });

  it("does not show a table of values when the provenance cannot be loaded", async () => {
    mockApi({ ...defaultRoutes(), [`GET /objects/${ID.swA1}/provenance`]: problem(500, "internal", "database is down") });
    renderApp({ at });
    expect(await screen.findByText("database is down")).toBeInTheDocument();
    expect(screen.queryByRole("table", { name: /Attributes/ })).not.toBeInTheDocument();
  });

  it("hides attributes whose name starts with an underscore (system keys)", async () => {
    mockApi({
      ...defaultRoutes(),
      [`GET /objects/${ID.swA1}/provenance`]: {
        ...provenanceSwA1,
        attributes: [...provenanceSwA1.attributes, { key: "_internal", value: "x", source: "engine", confidence: "observed", valid_from: "2026-03-01T08:00:00Z" }],
      },
    });
    renderApp({ at });
    await attributeRow(/Hostname/);
    expect(screen.queryByText("_internal")).not.toBeInTheDocument();
  });
});

describe("object page relations", () => {
  const at = `/objects/${ID.swA1}?tab=relations`;
  it("groups relations by name, seen from this object, with the other end as a link", async () => {
    mockApi(defaultRoutes());
    renderApp({ at });
    const out = (await screen.findByRole("heading", { level: 3, name: "mounted in" })).parentElement as HTMLElement;
    expect(out).toHaveAttribute("data-direction", "out");
    expect(within(out).getByRole("link", { name: "r12" })).toHaveAttribute("href", `/objects/${ID.rack}`);
    expect(within(out).getByText("declared:lab-file")).toBeInTheDocument();
    // An incoming relation reads with the inverse name.
    const incoming = screen.getByRole("heading", { level: 3, name: "connected from" }).parentElement as HTMLElement;
    expect(incoming).toHaveAttribute("data-direction", "in");
    expect(within(incoming).getByRole("link", { name: "srv-01" })).toHaveAttribute("href", `/objects/${ID.srv01}`);
    expect(within(incoming).getByText("lldp:core")).toBeInTheDocument();
  });

  it("says when there is no relation", async () => {
    mockApi({ ...defaultRoutes(), [`GET /objects/${ID.swA1}/relations`]: page([]) });
    renderApp({ at });
    expect(await screen.findByText("No relation.")).toBeInTheDocument();
  });
});

describe("object page timeline", () => {
  const at = `/objects/${ID.swA1}?tab=history`;
  it("shows the changes, newest first, in words", async () => {
    mockApi(defaultRoutes());
    renderApp({ at });
    const list = (await screen.findByText(/OS version changed from 4.27.0M to 4.28.3M/)).closest("ol") as HTMLElement;
    const items = within(list).getAllByRole("listitem");
    expect(items).toHaveLength(3);
    expect(items[0]).toHaveTextContent("OS version changed from 4.27.0M to 4.28.3M");
    expect(items[0]).toHaveTextContent("by arista:eos");
    expect(items[0]?.querySelector("time")).toHaveAttribute("datetime", "2026-09-20T02:00:00Z");
    await waitFor(() => expect(items[1]).toHaveTextContent("Relation connected to added with srv-01"));
    expect(items[2]).toHaveTextContent("Hostname set to sw-a1");
  });

  it("loads older changes on demand, asking for the next offset", async () => {
    const api = mockApi({
      ...defaultRoutes(),
      [`GET /objects/${ID.swA1}/timeline`]: (c) => {
        const offset = Number(c.query.get("offset") ?? 0);
        const older = { ...timelineSwA1.items[2]!, at: "2026-02-01T00:00:00Z", key: "vendor", after: "arista", changeset_id: 99 };
        return offset === 0 ? { ...timelineSwA1, total: 4 } : page([older], { total: 4, offset });
      },
    });
    renderApp({ at });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Load older changes" }));
    expect(await screen.findByText(/Vendor set to arista/)).toBeInTheDocument();
    const calls = api.calls.filter((c) => c.path === `/objects/${ID.swA1}/timeline`);
    expect(calls.map((c) => c.query.get("offset"))).toEqual(["0", "3"]);
    expect(screen.queryByRole("button", { name: "Load older changes" })).not.toBeInTheDocument();
  });

  it("says when there is nothing to show", async () => {
    mockApi({ ...defaultRoutes(), [`GET /objects/${ID.swA1}/timeline`]: page([]) });
    renderApp({ at });
    expect(await screen.findByText("No change recorded.")).toBeInTheDocument();
  });
});

describe("object page errors", () => {
  it("an unknown object is a not-found page, not an error box", async () => {
    mockApi({ ...defaultRoutes(), "GET /objects/nope": problem(404, "not-found", "object nope not found") });
    renderApp({ at: "/objects/nope" });
    expect(await screen.findByRole("heading", { level: 1, name: "Page not found" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("forbidden is said as forbidden, with the server's reason", async () => {
    mockApi({ ...defaultRoutes(), [`GET /objects/${ID.swA1}`]: problem(403, "forbidden", "site limited") });
    renderApp({ at });
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("site limited");
  });
});

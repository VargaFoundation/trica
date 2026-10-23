import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { instanceSwA1Admin } from "../test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp } from "../test/support";

describe("the collectors list", () => {
  it("is in the sidebar and on the overview", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/" });
    const nav = await screen.findByRole("navigation", { name: "Main" });
    expect(within(nav).getByRole("link", { name: "Collectors" })).toHaveAttribute("href", "/collectors");
    // The overview's sections list it too.
    expect(screen.getAllByRole("link", { name: "Collectors" })).toHaveLength(2);
  });

  it("lists every module the server describes, including one the UI has never heard of", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/collectors" });
    expect(await screen.findByRole("heading", { level: 1, name: "Collectors" })).toBeInTheDocument();
    expect(await screen.findByText("3 modules")).toBeInTheDocument();

    const unifi = (await screen.findByRole("link", { name: "Ubiquiti UniFi" })).closest("tr") as HTMLElement;
    expect(unifi).toHaveTextContent("UniFi gateways, switches and access points.");
    expect(unifi.querySelector('td[data-label="Instances"]')).toHaveTextContent("Available, not configured");
    expect(unifi.querySelector('td[data-label="Tested against"] .pill')).toHaveAttribute("data-value", "recorded");
    expect(within(unifi).getByRole("link", { name: "Ubiquiti UniFi" })).toHaveAttribute("href", "/collectors/unifi");

    const arista = screen.getByRole("link", { name: "Arista EOS" }).closest("tr") as HTMLElement;
    const counts = arista.querySelector('td[data-label="Instances"]') as HTMLElement;
    expect(counts).toHaveTextContent("1 Failing");
    expect(counts).toHaveTextContent("1 OK");
    expect(counts).toHaveTextContent("1 Pending");
    expect(counts).not.toHaveTextContent("Stale");
    expect(arista.querySelector('td[data-label="Tested against"]')).toHaveTextContent("Synthesised fixtures");
    expect(arista.querySelector('td[data-label="Deployment"]')).toHaveTextContent("Target in the targets file");

    const agent = screen.getByRole("link", { name: "Linux host agent" }).closest("tr") as HTMLElement;
    expect(agent.querySelector('td[data-label="Deployment"]')).toHaveTextContent("Installed on the host");
  });

  it("shows the summary in the reader's language", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/collectors", locale: "fr" });
    expect(await screen.findByText("Passerelles, commutateurs et points d'accès UniFi.")).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1, name: "Collecteurs" })).toBeInTheDocument();
    expect(screen.getByText("Disponible, non configuré")).toBeInTheDocument();
  });

  it("lists the sources no module claims, apart", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/collectors" });
    const heading = await screen.findByRole("heading", { level: 2, name: "Sources from other collectors" });
    const section = heading.closest("section") as HTMLElement;
    expect(within(section).getByText("snmp:core")).toBeInTheDocument();
  });

  it("has no such section when every source belongs to a module", async () => {
    mockApi({ ...defaultRoutes(), "GET /collectors": { modules: [], unregistered: [] } });
    renderApp({ at: "/collectors" });
    expect(await screen.findByText("0 modules")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Sources from other collectors" })).not.toBeInTheDocument();
  });

  it("shows a refusal like any other error", async () => {
    mockApi({ ...defaultRoutes(), "GET /collectors": problem(403, "forbidden", "no") });
    renderApp({ at: "/collectors" });
    expect(await screen.findByRole("alert")).toHaveTextContent("Your credentials do not allow this.");
  });
});

describe("a collector module", () => {
  it("says what it collects, how it is configured and what it was tested with", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/collectors/arista" });
    expect(await screen.findByRole("heading", { level: 1, name: "Arista EOS" })).toBeInTheDocument();
    expect(screen.getByText("Arista switches: interfaces, VLANs, transceivers.")).toBeInTheDocument();
    expect(screen.getByText("Sends show commands only, through a network-operator account.")).toBeInTheDocument();

    // The types it collects, with the schema's labels, each leading to its objects.
    const collects = (await screen.findByRole("heading", { name: "What it collects" })).closest("section") as HTMLElement;
    await waitFor(() => expect(within(collects).getByRole("link", { name: "Device" })).toHaveAttribute("href", "/objects?type=device"));
    expect(within(collects).getByRole("link", { name: "transceiver" })).toHaveAttribute("href", "/objects?type=transceiver");
    expect(within(collects).getByRole("link", { name: "Objects from this collector" })).toHaveAttribute("href", `/search?q=${encodeURIComponent("source:arista:*")}`);

    const config = screen.getByRole("heading", { name: "Configuration" }).closest("section") as HTMLElement;
    expect(config).toHaveTextContent("Account with the network-operator role");
    const row = within(config).getByText("max_edge_macs").closest("tr") as HTMLElement;
    expect(row.querySelector('td[data-label="Default"]')).toHaveTextContent("4");
    const role = within(config).getByText("role").closest("tr") as HTMLElement;
    expect(role.querySelector('td[data-label="Allowed values"]')).toHaveTextContent("switch, router");

    const tested = screen.getByRole("heading", { name: "Tested with" }).closest("section") as HTMLElement;
    expect(tested).toHaveTextContent("EOS 4.20.7M");
    expect(tested).toHaveTextContent("Synthesised fixtures");
    expect(within(tested).getByRole("link", { name: "Documentation of the module" })).toHaveAttribute(
      "href",
      "https://github.com/VargaFoundation/trica/blob/main/internal/collect/arista/README.md",
    );
  });

  it("marks an option that holds a reference to a secret", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/collectors/unifi" });
    const row = (await screen.findByText("key_file")).closest("tr") as HTMLElement;
    expect(row.querySelector('td[data-label="Type"]')).toHaveTextContent("reference (file: or env:)");
    const mapRow = (await screen.findByText("vids")).closest("tr") as HTMLElement;
    expect(mapRow.querySelector('td[data-label="Type"]')).toHaveTextContent("map (name to value) (integer)");
    expect(screen.getByText("Ubiquiti UniFi", { selector: "h1" })).toBeInTheDocument();
    expect(await screen.findByText("No instance is configured or has reported yet.")).toBeInTheDocument();
  });

  it("lists its instances failing first, each leading to its page, and filters them by status", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: "/collectors/arista" });
    const heading = await screen.findByRole("heading", { level: 2, name: "Instances" });
    const section = heading.closest("section") as HTMLElement;
    await within(section).findByText("3 instances");
    const rows = within(section).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.getAttribute("data-status"))).toEqual(["failing", "pending", "ok"]);
    expect(within(rows[0] as HTMLElement).getByRole("link", { name: "sw-a2" })).toHaveAttribute("href", "/collectors/arista/sw-a2");
    expect(rows[0]).toHaveTextContent("dial tcp 192.0.2.11:443: i/o timeout");
    expect(rows[1]).toHaveTextContent("never");

    const user = userEvent.setup();
    await user.selectOptions(within(section).getByRole("combobox", { name: "Status" }), "failing");
    await user.click(within(section).getByRole("button", { name: "Filter" }));
    await waitFor(() => expect(api.calls.some((c) => c.path === "/collectors/arista/instances" && c.query.get("status") === "failing")).toBe(true));
    await waitFor(() => expect(within(section).getAllByRole("row")).toHaveLength(2));
  });

  it("takes its filter from the address", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: "/collectors/arista?status=ok&offset=50" });
    await screen.findByRole("heading", { level: 2, name: "Instances" });
    await waitFor(() => expect(api.count("GET /collectors/arista/instances")).toBe(1));
    const q = api.calls.find((c) => c.path === "/collectors/arista/instances")?.query;
    expect(q?.get("status")).toBe("ok");
    expect(q?.get("offset")).toBe("50");
  });

  it("describes the host agent as installed on the host, with the hosts that report", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/collectors/agent" });
    expect(await screen.findByText(/has no target: it is configured where it runs/)).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Options" })).not.toBeInTheDocument();
    expect(await screen.findByRole("link", { name: "host-01" })).toHaveAttribute("href", "/collectors/agent/host-01");
  });

  it("answers 404 for a module that does not exist", async () => {
    mockApi({ ...defaultRoutes(), "GET /collectors/nope": problem(404, "not-found", "no collector module") });
    renderApp({ at: "/collectors/nope" });
    expect(await screen.findByRole("alert")).toHaveTextContent("Not found.");
  });
});

describe("a collector instance", () => {
  it("shows its configuration and tells a reader the references are for administrators", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/collectors/arista/sw-a1" });
    expect(await screen.findByRole("heading", { level: 1, name: "sw-a1" })).toBeInTheDocument();
    const config = screen.getByRole("heading", { name: "Configuration" }).closest("section") as HTMLElement;
    expect(config).toHaveTextContent("https://192.0.2.10");
    expect(config).toHaveTextContent("300 s");
    expect(config).toHaveTextContent("paris");
    expect(config).toHaveTextContent("/etc/trica/ca.pem");
    // An option that names a secret shows its reference, as the API gives it.
    expect(within(config).getByText("token_file").closest("div")).toHaveTextContent("file:/run/secrets/token");
    expect(config).toHaveTextContent("Credential references are shown to administrators.");
    expect(config).not.toHaveTextContent("arista-password");

    const run = screen.getByRole("heading", { name: "Last run" }).closest("section") as HTMLElement;
    expect(run).toHaveTextContent("840 ms");
    expect(within(run).getByRole("link", { name: "Objects this source reports" })).toHaveAttribute(
      "href",
      `/search?q=${encodeURIComponent('source:"arista:sw-a1"')}`,
    );
  });

  it("shows the credential references an administrator is given", async () => {
    mockApi({ ...defaultRoutes(), "GET /collectors/arista/instances/sw-a1": instanceSwA1Admin });
    renderApp({ at: "/collectors/arista/sw-a1" });
    expect(await screen.findByText("file:/run/secrets/arista-password")).toBeInTheDocument();
    expect(screen.getByText("trica", { selector: "dd" })).toBeInTheDocument();
    expect(screen.queryByText("Credential references are shown to administrators.")).not.toBeInTheDocument();
  });

  it("warns about a certificate that is not verified, and shows why a run failed", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/collectors/arista/sw-a2" });
    const warn = await screen.findByText("Certificate not verified");
    expect(warn.closest(".pill")).toHaveClass("pill-warn");
    expect(screen.getByText("dial tcp 192.0.2.11:443: i/o timeout")).toBeInTheDocument();
    expect(screen.getByText("no ifAlias on 3 interfaces")).toBeInTheDocument();
    expect(screen.getByText("Failing")).toHaveClass("pill-danger");
  });

  it("says it is configured where it runs when only its health is known", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/collectors/agent/host-01" });
    expect(await screen.findByText(/only its health is known here/)).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Configuration" })).not.toBeInTheDocument();
  });

  it("answers 404 for an instance that does not exist", async () => {
    mockApi({ ...defaultRoutes(), "GET /collectors/arista/instances/nope": problem(404, "not-found", "no instance") });
    renderApp({ at: "/collectors/arista/nope" });
    expect(await screen.findByRole("alert")).toHaveTextContent("Not found.");
  });

  it("has its own breadcrumb: collectors, the module, the instance", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/collectors/arista/sw-a1" });
    await screen.findByRole("heading", { level: 1, name: "sw-a1" });
    const trail = await screen.findByRole("navigation", { name: "Breadcrumb" });
    await waitFor(() => expect(within(trail).getByRole("link", { name: "Arista EOS" })).toHaveAttribute("href", "/collectors/arista"));
    expect(within(trail).getByRole("link", { name: "Collectors" })).toHaveAttribute("href", "/collectors");
  });
});

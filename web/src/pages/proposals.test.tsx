import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { APPROVER, page, PROPOSER, proposalApplied, proposalPending, proposalPendingDetail, READER } from "../test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp } from "../test/support";

const decision = { proposal_id: 12, status: "applied" };

describe("approve and reject buttons are shown only to a caller allowed to approve", () => {
  it.each([
    ["a reader", READER],
    ["a proposer", PROPOSER],
  ])("%s sees no decision column and no button in the list", async (_who, me) => {
    mockApi(defaultRoutes(me));
    renderApp({ at: "/proposals?status=all" });
    await screen.findByRole("link", { name: "#12" });
    expect(screen.queryByRole("columnheader", { name: "Decision" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Approve/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Reject/ })).not.toBeInTheDocument();
  });

  it.each([
    ["a reader", READER],
    ["a proposer", PROPOSER],
  ])("%s sees no button on the detail page either", async (_who, me) => {
    mockApi(defaultRoutes(me));
    renderApp({ at: "/proposals/12" });
    expect(await screen.findByRole("heading", { level: 1, name: "Proposal 12" })).toBeInTheDocument();
    expect(await screen.findByText("rack_unit")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Approve/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Reject/ })).not.toBeInTheDocument();
  });

  it("an approver sees them on pending proposals only", async () => {
    mockApi(defaultRoutes(APPROVER));
    renderApp({ at: "/proposals?status=all" });
    const pending = (await screen.findByRole("link", { name: "#12" })).closest("tr") as HTMLElement;
    const applied = screen.getByRole("link", { name: "#11" }).closest("tr") as HTMLElement;
    expect(screen.getByRole("columnheader", { name: "Decision" })).toBeInTheDocument();
    expect(within(pending).getByRole("button", { name: /Approve/ })).toBeInTheDocument();
    expect(within(pending).getByRole("button", { name: /Reject/ })).toBeInTheDocument();
    expect(within(applied).queryByRole("button")).not.toBeInTheDocument();
  });

  it("the buttons wait for /me: nothing is offered while the actions are unknown", async () => {
    let release: (() => void) | undefined;
    const gate = new Promise<void>((r) => (release = r));
    const api = mockApi(defaultRoutes(APPROVER));
    const fetchMock = api.spy.getMockImplementation();
    api.spy.mockImplementation(async (input, init) => {
      if (String(input).endsWith("/me")) await gate;
      return (fetchMock as typeof fetch)(input, init);
    });
    renderApp({ at: "/proposals" });
    // The app shows nothing but its loading state until /me answers.
    expect(screen.queryByRole("button", { name: /Approve/ })).not.toBeInTheDocument();
    release?.();
    expect(await screen.findByRole("button", { name: /Approve/ })).toBeInTheDocument();
  });
});

describe("deciding", () => {
  it("approving asks for confirmation, posts once, tells the result and refreshes the list", async () => {
    let status = "pending";
    const api = mockApi({
      ...defaultRoutes(APPROVER),
      "GET /proposals": () => page([{ ...proposalPending, status }]),
      "POST /proposals/12/approve": () => {
        status = "applied";
        return decision;
      },
    });
    renderApp({ at: "/proposals" });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Approve/ }));
    // Nothing has been sent yet.
    expect(api.count("POST /proposals/12/approve")).toBe(0);
    expect(screen.getByRole("group", { name: "Approve proposal 12?" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Confirm" })).toHaveFocus();

    await user.click(screen.getByRole("button", { name: "Confirm" }));
    expect(await screen.findByText("Proposal 12 approved and applied.")).toBeInTheDocument();
    expect(api.count("POST /proposals/12/approve")).toBe(1);
    expect(api.count("POST /proposals/12/reject")).toBe(0);
    // The list was fetched again and no longer offers the buttons.
    await waitFor(() => expect(screen.queryByRole("button", { name: /Approve/ })).not.toBeInTheDocument());
    expect(api.count("GET /proposals")).toBeGreaterThanOrEqual(2);
  });

  it("cancelling sends nothing", async () => {
    const api = mockApi(defaultRoutes(APPROVER));
    renderApp({ at: "/proposals" });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Reject/ }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(api.calls.filter((c) => c.method === "POST")).toHaveLength(0);
    expect(screen.getByRole("button", { name: /Approve/ })).toBeInTheDocument();
  });

  it("rejecting posts to reject, not approve", async () => {
    const api = mockApi({ ...defaultRoutes(APPROVER), "POST /proposals/12/reject": { proposal_id: 12, status: "rejected" } });
    renderApp({ at: "/proposals" });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Reject/ }));
    expect(screen.getByRole("group", { name: "Reject proposal 12?" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    expect(await screen.findByText("Proposal 12 rejected.")).toBeInTheDocument();
    expect(api.count("POST /proposals/12/reject")).toBe(1);
    expect(api.count("POST /proposals/12/approve")).toBe(0);
  });

  it("sends the caller's token with the decision", async () => {
    const api = mockApi({ ...defaultRoutes(APPROVER), "POST /proposals/12/approve": decision });
    renderApp({ at: "/proposals" });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Approve/ }));
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    await screen.findByText("Proposal 12 approved and applied.");
    expect(api.calls.find((c) => c.method === "POST")?.headers.get("Authorization")).toBe("Bearer trica_test_token");
  });

  it("when the server refuses (the API decides), the refusal is shown and nothing is claimed done", async () => {
    mockApi({ ...defaultRoutes(APPROVER), "POST /proposals/12/approve": problem(403, "forbidden", "approve is limited to site paris-1") });
    renderApp({ at: "/proposals" });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Approve/ }));
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The decision was not recorded");
    expect(alert).toHaveTextContent("approve is limited to site paris-1");
    expect(screen.queryByText(/approved and applied/)).not.toBeInTheDocument();
  });

  it("when someone decided first (409), the refusal is shown and the list is refreshed", async () => {
    const api = mockApi({ ...defaultRoutes(APPROVER), "POST /proposals/12/approve": problem(409, "conflict", "proposal 12 is already applied") });
    renderApp({ at: "/proposals" });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Approve/ }));
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("proposal 12 is already applied");
    await waitFor(() => expect(api.count("GET /proposals")).toBeGreaterThanOrEqual(2));
  });

  it("works from the detail page too, and refreshes the proposal", async () => {
    let status = "pending";
    const api = mockApi({
      ...defaultRoutes(APPROVER),
      "GET /proposals/12": () => ({ ...proposalPendingDetail, status }),
      "POST /proposals/12/approve": () => {
        status = "applied";
        return decision;
      },
    });
    renderApp({ at: "/proposals/12" });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Approve/ }));
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    expect(await screen.findByText("Proposal 12 approved and applied.")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("button", { name: /Approve/ })).not.toBeInTheDocument());
    expect(api.count("POST /proposals/12/approve")).toBe(1);
  });
});

describe("proposals list", () => {
  it("is a deep link: it asks for pending proposals by default and for the status in the address otherwise", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: "/proposals" });
    await screen.findByRole("link", { name: "#12" });
    expect(api.calls.find((c) => c.path === "/proposals")?.query.get("status")).toBe("pending");
  });

  it("asks for the status in the address", async () => {
    const api = mockApi({ ...defaultRoutes(), "GET /proposals": page([proposalApplied]) });
    renderApp({ at: "/proposals?status=applied" });
    await screen.findByRole("link", { name: "#11" });
    expect(api.calls.find((c) => c.path === "/proposals")?.query.get("status")).toBe("applied");
    expect(screen.getByText(/ops-1/)).toBeInTheDocument();
  });

  it("all means no status filter", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: "/proposals?status=all" });
    await screen.findByRole("link", { name: "#12" });
    expect(api.calls.find((c) => c.path === "/proposals")?.query.has("status")).toBe(false);
  });

  it("the filter form changes the address", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: "/proposals" });
    const user = userEvent.setup();
    await user.selectOptions(await screen.findByRole("combobox", { name: "Status" }), "applied");
    await user.click(screen.getByRole("button", { name: "Filter" }));
    await waitFor(() => expect(api.calls.some((c) => c.path === "/proposals" && c.query.get("status") === "applied")).toBe(true));
  });

  it("says when nothing matches", async () => {
    mockApi({ ...defaultRoutes(), "GET /proposals": page([]) });
    renderApp({ at: "/proposals" });
    expect(await screen.findByText("No proposal matches.")).toBeInTheDocument();
  });

  it("links each proposal to its page", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/proposals" });
    expect(await screen.findByRole("link", { name: "#12" })).toHaveAttribute("href", "/proposals/12");
  });
});

describe("proposal page", () => {
  it("shows who, why, and what it would change", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/proposals/12" });
    expect(await screen.findByRole("heading", { level: 1, name: "Proposal 12" })).toBeInTheDocument();
    expect(screen.getByText("mcp:assistant")).toBeInTheDocument();
    expect(screen.getByText("Record the rack unit of sw-a1")).toBeInTheDocument();
    const row = (await screen.findByText("rack_unit")).closest("tr") as HTMLElement;
    expect(within(row).getByText("set_attr")).toBeInTheDocument();
    expect(row).toHaveTextContent("U12");
    expect(await within(row).findByRole("link", { name: "sw-a1" })).toHaveAttribute("href", expect.stringContaining("/objects/"));
  });

  it("an applied proposal shows its change set and no buttons", async () => {
    mockApi(defaultRoutes(APPROVER));
    renderApp({ at: "/proposals/11" });
    expect(await screen.findByRole("heading", { level: 1, name: "Proposal 11" })).toBeInTheDocument();
    expect(screen.getByText("52")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Approve/ })).not.toBeInTheDocument();
  });

  it("says when there is no preview", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/proposals/11" });
    expect(await screen.findByText("No preview is available for this proposal.")).toBeInTheDocument();
  });

  it.each(["/proposals/abc", "/proposals/0"])("%s is not found without calling the API", async (at) => {
    const api = mockApi(defaultRoutes());
    renderApp({ at });
    expect(await screen.findByRole("heading", { level: 1, name: "Page not found" })).toBeInTheDocument();
    expect(api.calls.some((c) => c.path.startsWith("/proposals"))).toBe(false);
  });

  it("an unknown proposal is not found", async () => {
    mockApi({ ...defaultRoutes(), "GET /proposals/999": problem(404, "not-found", "proposal 999 not found") });
    renderApp({ at: "/proposals/999" });
    expect(await screen.findByRole("heading", { level: 1, name: "Page not found" })).toBeInTheDocument();
  });
});

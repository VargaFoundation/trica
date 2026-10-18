import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { expectNoA11yViolations } from "./test/axe";
import { APPROVER, ID, page, READER } from "./test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp, type Opts } from "./test/support";

/** Lets the secondary lookups (schema labels, linked object names) land before the audit. */
const settle = () => new Promise((r) => setTimeout(r, 60));

describe("accessibility audit (axe-core, WCAG 2.2 A and AA rules)", () => {
  it("the audit itself catches a violation (a control with no name)", async () => {
    document.body.innerHTML = "<main><button></button><img src='x.png'></main>";
    await expect(expectNoA11yViolations()).rejects.toThrow(/button-name|image-alt/);
    document.body.innerHTML = "";
  });

  const pages: [string, Opts, string | RegExp][] = [
    ["sign-in", { signedIn: false }, "Sign in"],
    ["home", {}, "Inventory"],
    ["search results, grouped by type", { at: "/search?q=sw" }, "sw-a1"],
    ["a type view", { at: "/search?q=sw&type=device" }, "sw-a1"],
    ["object page, overview", { at: `/objects/${ID.swA1}` }, "Management address"],
    ["object page, provenance", { at: `/objects/${ID.swA1}?tab=provenance` }, /Attributes of sw-a1/],
    ["object page, components", { at: `/objects/${ID.srv01}?tab=components` }, /Power supply of srv-01/],
    ["object page, relations", { at: `/objects/${ID.swA1}?tab=relations` }, "lldp:core"],
    ["object page, history", { at: `/objects/${ID.swA1}?tab=history` }, /OS version changed/],
    ["discrepancies", { at: "/discrepancies" }, "value_mismatch"],
    ["proposals", { at: "/proposals" }, "#12"],
    ["proposal", { at: "/proposals/12" }, "rack_unit"],
    ["health", { at: "/health" }, "2 sources, 1 failing"],
    ["not found", { at: "/nowhere" }, "Page not found"],
    ["French", { at: `/objects/${ID.swA1}?tab=provenance`, locale: "fr" }, /Attributs de sw-a1/],
    ["French overview", { at: "/", locale: "fr" }, "Équipements"],
    ["dark overview", { at: "/", theme: "dark" }, "Equipment"],
    ["dark theme", { at: "/proposals", theme: "dark" }, "#12"],
  ];

  it.each(pages)("%s has no violation", async (_name, opts, ready) => {
    mockApi(defaultRoutes(APPROVER));
    renderApp(opts);
    await screen.findAllByText(ready);
    await settle();
    await expectNoA11yViolations();
  });

  it("a reader's pages (no decision column) have no violation", async () => {
    mockApi(defaultRoutes(READER));
    renderApp({ at: "/proposals" });
    await screen.findByRole("link", { name: "#12" });
    await expectNoA11yViolations();
  });

  it("an error state has no violation", async () => {
    mockApi({ ...defaultRoutes(), "GET /discrepancies": problem(500, "internal", "database is down") });
    renderApp({ at: "/discrepancies" });
    await screen.findByRole("alert");
    await expectNoA11yViolations();
  });

  it("an empty list has no violation", async () => {
    mockApi({ ...defaultRoutes(), "GET /proposals": page([]) });
    renderApp({ at: "/proposals" });
    await screen.findByText("No proposal matches.");
    await expectNoA11yViolations();
  });

  it("the open confirmation, the decision notice and a refusal have no violation", async () => {
    mockApi({ ...defaultRoutes(APPROVER), "POST /proposals/12/approve": problem(409, "conflict", "already applied") });
    renderApp({ at: "/proposals" });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Approve/ }));
    await screen.findByRole("group", { name: "Approve proposal 12?" });
    await expectNoA11yViolations();
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    await screen.findByRole("alert");
    await expectNoA11yViolations();
  });

  it("an object page with its other claims open has no violation", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.swA1}?tab=provenance` });
    const table = await screen.findByRole("table", { name: /Attributes of sw-a1/ });
    await userEvent.setup().click(within(table).getByText("1 other claim"));
    await settle();
    await expectNoA11yViolations();
  });

  it("axe-core is the pinned version", () => {
    expect(axe.version).toBe("4.13.0");
  });
});

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { expectNoA11yViolations } from "./test/axe";
import { APPROVER, ID, page, READER } from "./test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp, type Call, type Opts } from "./test/support";
import { MAPS } from "./topology/map/testdata/maps";

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
    ["object page, trends", { at: `/objects/${ID.srv01}` }, "Inlet temperature"],
    ["object page, components", { at: `/objects/${ID.srv01}?tab=components` }, /Power supply of srv-01/],
    ["object page, relations", { at: `/objects/${ID.swA1}?tab=relations` }, "lldp:core"],
    ["object page, history", { at: `/objects/${ID.swA1}?tab=history` }, /OS version changed/],
    ["discrepancies", { at: "/discrepancies" }, "value_mismatch"],
    ["proposals", { at: "/proposals" }, "#12"],
    ["proposal", { at: "/proposals/12" }, "rack_unit"],
    ["health", { at: "/health" }, "2 sources, 1 failing"],
    ["collectors", { at: "/collectors" }, "Ubiquiti UniFi"],
    ["a collector module", { at: "/collectors/arista" }, "EOS 4.20.7M"],
    ["a collector instance", { at: "/collectors/arista/sw-a1" }, "Credential references are shown to administrators."],
    ["not found", { at: "/nowhere" }, "Page not found"],
    ["French", { at: `/objects/${ID.swA1}?tab=provenance`, locale: "fr" }, /Attributs de sw-a1/],
    ["French collector module", { at: "/collectors/arista", locale: "fr" }, "EOS 4.20.7M"],
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

  const maps: [string, Opts][] = [
    ["the world", { at: "/topology" }],
    ["a site", { at: "/topology?level=site&id=%3Cparis%3E" }],
    ["a room", { at: "/topology?level=room&id=%3Croom%3E", theme: "dark" }],
    ["a rack", { at: "/topology?level=rack&id=%3Cr1%3E&sel=%3Cswa%3E" }],
    ["the unplaced devices", { at: "/topology?level=unplaced", theme: "dark" }],
    ["a room, in French", { at: "/topology?level=room&id=%3Croom%3E", locale: "fr" }],
  ];
  it.each(maps)("the topology map of %s has no violation", async (_name, opts) => {
    mockApi({
      ...defaultRoutes(APPROVER),
      "GET /topology/map": (c: Call) => MAPS[(c.query.get("level") ?? "global") as keyof typeof MAPS],
    });
    renderApp(opts);
    await waitFor(() => expect(document.querySelector('.iso-svg[data-state="drawn"]')).not.toBeNull(), { timeout: 8000 });
    await settle();
    await expectNoA11yViolations();
  }, 20_000);

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

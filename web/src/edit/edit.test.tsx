import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Batch, SchemaDoc } from "../api/types";
import { expectNoA11yViolations } from "../test/axe";
import { page, PROPOSER, READER } from "../test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp, type Call, type Handler } from "../test/support";
import {
  EID,
  emptyResult,
  model1u,
  pdu,
  pduRelations,
  physicalSchema,
  previewNewDevice,
  rack,
  rack2,
  rackContents,
  room,
  site,
  srv,
  sw,
  swRelations,
  wholeUnitSchema,
} from "./fixtures";

const empty = page([]);

function routes(me: Handler = PROPOSER, extra: Record<string, Handler> = {}, schema: SchemaDoc = physicalSchema): Record<string, Handler> {
  const r: Record<string, Handler> = {
    ...defaultRoutes(me),
    "GET /schema": schema,
    "GET /objects": (c: Call) => {
      switch (c.query.get("type")) {
        case "rack":
          return page([rack, rack2]);
        case "room":
          return page([room]);
        case "site":
          return page([site]);
        case "device":
          return page([sw, srv].filter((d) => d.display.includes(c.query.get("q") ?? "")));
        default:
          return empty;
      }
    },
    [`GET /racks/${EID.rack}/contents`]: rackContents,
    [`GET /racks/${EID.rack2}/contents`]: empty,
    [`GET /objects/${EID.sw}/relations`]: swRelations,
    [`GET /objects/${EID.pdu}/relations`]: pduRelations,
    [`GET /objects/${EID.srv}/relations`]: empty,
    "POST /batches/preview": { source: "ui-tests", result: previewNewDevice },
    "POST /batches": { source: "ui-tests", status: "applied", result: { ...previewNewDevice, changeset_id: 9 } },
  };
  for (const o of [site, room, rack, rack2, sw, srv, pdu, model1u]) {
    r[`GET /objects/${o.id}`] = o;
    r[`GET /objects/${o.id}/provenance`] = { object_id: o.id, attributes: [] };
    r[`GET /objects/${o.id}/timeline`] = empty;
    r[`GET /objects/${o.id}/relations`] ??= empty;
  }
  return { ...r, ...extra };
}

const bodyOf = (api: ReturnType<typeof mockApi>, key: string): Batch => {
  const c = [...api.calls].reverse().find((x) => `${x.method} ${x.path}` === key);
  if (!c) throw new Error(`no ${key}`);
  return c.body as Batch;
};

const freeU = (face: "front" | "rear", u: number) => document.querySelector(`svg[data-face="${face}"] rect.rack-free[data-u="${u}"]`) as SVGRectElement;

async function openRackPlacement(api = mockApi(routes())) {
  renderApp({ at: `/racks/${EID.rack}` });
  await screen.findByRole("heading", { level: 1, name: "Rack r12" });
  await waitFor(() => expect(freeU("front", 5)).not.toBeNull());
  return api;
}

describe("who sees the declaring controls", () => {
  it.each([
    ["the rack page", `/racks/${EID.rack}`, "Place a device"],
    ["the rack page", `/racks/${EID.rack}`, "Edit this rack"],
    ["a room's floor plan", `/rooms/${EID.room}`, "Add a rack"],
    ["the rooms page", "/rooms", "Add a site"],
    ["a device page", `/objects/${EID.srv}`, "Place in a rack"],
    ["a rack's object page", `/objects/${EID.rack}`, "Edit"],
  ])("%s offers %s to a caller who may propose, not to a reader", async (_, at, button) => {
    const reader = mockApi(routes(READER));
    const { unmount } = renderApp({ at });
    await screen.findByRole("heading", { level: 1 });
    // What the controls depend on has been read: who the caller is.
    await waitFor(() => expect(reader.count("GET /me")).toBeGreaterThan(0));
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByRole("button", { name: button })).toBeNull();
    expect(document.querySelectorAll(".rack-free, .floor-free")).toHaveLength(0);
    unmount();

    mockApi(routes(PROPOSER));
    renderApp({ at });
    expect(await screen.findByRole("button", { name: button })).toBeInTheDocument();
  });
});

describe("placing a device from the rack page", () => {
  it("opens on the unit clicked, declares a PDU no collector sees, previews, then applies", async () => {
    const user = userEvent.setup();
    const api = await openRackPlacement();
    // U 1 rear holds pdu-a, U 10-11 front holds srv-01: those are not targets.
    expect(freeU("rear", 1)).toBeNull();
    expect(freeU("front", 10)).toBeNull();
    expect(freeU("front", 11)).toBeNull();
    fireEvent.click(freeU("front", 5));
    const panel = await screen.findByRole("region", { name: "Place a device in rack r12" });
    expect(within(panel).getByRole("heading", { name: "Place a device in rack r12" })).toHaveFocus();
    expect(await within(panel).findByLabelText("Position (lowest U)")).toHaveValue(5);
    expect(within(panel).getByLabelText("Face")).toHaveValue("front");

    await user.click(await within(panel).findByRole("radio", { name: /no collector sees/ }));
    await user.type(within(panel).getByLabelText(/^Name/), "pdu-b");
    // The roles are the schema's enum values.
    const role = within(panel).getByLabelText("Role");
    expect(within(role).getAllByRole("option").map((o) => o.textContent)).toContain("patch panel");
    await user.selectOptions(role, "pdu");
    await user.type(within(panel).getByLabelText(/^Height/), "2");
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));

    const preview = await within(panel).findByRole("region", { name: "What the inventory would change" });
    expect(preview).toHaveTextContent("New Device:");
    expect(preview).toHaveTextContent("pdu-b is mounted in");
    expect(preview).toHaveTextContent("at U5, front face");
    expect(preview).not.toHaveTextContent("_status");
    const sent = bodyOf(api, "POST /batches/preview");
    expect(sent.scope).toBe("partial");
    expect(sent.objects).toEqual([{ ref: "device", type: "device", identity: { name: "pdu-b" }, attributes: { role: "pdu", u_height: 2 } }]);
    expect(sent.relations).toEqual([{ type: "mounted_in", from: { ref: "device" }, to: { type: "rack", identity: { id: EID.rack } }, attributes: { position: 5, face: "front" } }]);
    expect(api.count("POST /batches")).toBe(0);

    const before = api.count(`GET /racks/${EID.rack}/contents`);
    await user.click(within(panel).getByRole("button", { name: "Submit" }));
    expect(await within(panel).findByText("Applied: the inventory now says this.")).toBeInTheDocument();
    expect(bodyOf(api, "POST /batches").relations).toEqual(sent.relations);
    expect(within(panel).getByRole("link", { name: "Open pdu-b" })).toHaveAttribute("href", `/objects/${previewNewDevice.objects?.device}`);
    // Applied: the elevation is read again.
    await waitFor(() => expect(api.count(`GET /racks/${EID.rack}/contents`)).toBeGreaterThan(before));
    await expectNoA11yViolations();
  });

  it.each([
    [202, { source: "ui-tests", status: "proposed", result: { ...emptyResult, proposal_id: 77 } }, "Sent for approval"],
    [403, problem(403, "forbidden", "the propose action is not granted on rack objects"), "You are not allowed to change one of these objects"],
    [409, problem(409, "conflict", "changed meanwhile"), "Someone changed this in the meantime"],
    [422, problem(422, "rejected", 'relation mounted_in: value "side" is not one of front, rear'), "The inventory refused the change"],
    [400, problem(400, "invalid-batch", "unknown object type"), "The server found the change malformed"],
  ])("answers %i in words", async (status, reply, text) => {
    const user = userEvent.setup();
    const api = mockApi(routes(PROPOSER, { "POST /batches": status === 202 ? { status: 202, body: reply } : reply }));
    await openRackPlacement(api);
    fireEvent.click(freeU("front", 5));
    const panel = await screen.findByRole("region", { name: "Place a device in rack r12" });
    await user.type(await within(panel).findByLabelText("Search by name"), "sw");
    await user.click(await within(panel).findByRole("radio", { name: "sw-a1" }));
    await within(panel).findByText(/Height from its model ex-1u/);
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    const reads = api.count(`GET /racks/${EID.rack}/contents`);
    await user.click(await within(panel).findByRole("button", { name: "Submit" }));
    expect(await within(panel).findByText(new RegExp(text))).toBeInTheDocument();
    // Nothing was applied: the elevation is not read again.
    await new Promise((r) => setTimeout(r, 50));
    expect(api.count(`GET /racks/${EID.rack}/contents`)).toBe(reads);
    if (status === 202) {
      expect(within(panel).getByRole("link", { name: "Proposal #77" })).toHaveAttribute("href", "/proposals/77");
    } else {
      const alert = within(panel).getByRole("alert");
      expect(alert).toHaveAttribute("data-status", String(status));
      // The server's own words are shown too.
      expect(alert).toHaveTextContent((reply as { body: { detail: string } }).body.detail);
    }
    await expectNoA11yViolations();
  });

  it("shows a refused preview and sends nothing", async () => {
    const user = userEvent.setup();
    const api = mockApi(routes(PROPOSER, { "POST /batches/preview": problem(403, "forbidden", "not on this site") }));
    await openRackPlacement(api);
    fireEvent.click(freeU("front", 5));
    const panel = await screen.findByRole("region", { name: "Place a device in rack r12" });
    await user.click(await within(panel).findByRole("radio", { name: /no collector sees/ }));
    await user.type(within(panel).getByLabelText(/^Name/), "pdu-b");
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    expect(await within(panel).findByText("The preview failed")).toBeInTheDocument();
    expect(within(panel).queryByRole("button", { name: "Submit" })).toBeNull();
    expect(api.count("POST /batches")).toBe(0);
  });

  it("refuses a position outside the rack before asking the server", async () => {
    const user = userEvent.setup();
    const api = await openRackPlacement();
    await user.click(screen.getByRole("button", { name: "Place a device" }));
    const panel = await screen.findByRole("region", { name: "Place a device in rack r12" });
    await user.click(await within(panel).findByRole("radio", { name: /no collector sees/ }));
    await user.type(within(panel).getByLabelText(/^Name/), "shelf-1");
    await user.type(within(panel).getByLabelText(/^Height/), "2");
    await user.type(within(panel).getByLabelText("Position (lowest U)"), "12");
    expect(within(panel).getByText("The device would stick out of the rack: its top would be U13, the rack has 12 U.")).toBeInTheDocument();
    expect(within(panel).getByRole("button", { name: "Preview the change" })).toBeDisabled();
    await user.clear(within(panel).getByLabelText("Position (lowest U)"));
    await user.type(within(panel).getByLabelText("Position (lowest U)"), "11");
    expect(within(panel).getByRole("button", { name: "Preview the change" })).toBeEnabled();
    expect(api.count("POST /batches/preview")).toBe(0);
  });

  it("warns about an overlap on the same face but lets the person decide", async () => {
    const user = userEvent.setup();
    await openRackPlacement();
    fireEvent.click(freeU("front", 9));
    const panel = await screen.findByRole("region", { name: "Place a device in rack r12" });
    await user.click(await within(panel).findByRole("radio", { name: /no collector sees/ }));
    await user.type(within(panel).getByLabelText(/^Name/), "shelf-1");
    await user.type(within(panel).getByLabelText(/^Height/), "2");
    expect(within(panel).getByText("Shares rack space with srv-01 (U10-U11).")).toBeInTheDocument();
    // The rear at U 9-10 is free.
    await user.selectOptions(within(panel).getByLabelText("Face"), "rear");
    expect(within(panel).queryByText(/Shares rack space/)).toBeNull();
    await user.selectOptions(within(panel).getByLabelText("Face"), "front");
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    // The page's warning is repeated with the preview, next to the engine's.
    const preview = await within(panel).findByRole("region", { name: "What the inventory would change" });
    expect(preview).toHaveTextContent("Shares rack space with srv-01");
    expect(within(panel).getByRole("button", { name: "Submit" })).toBeEnabled();
  });

  it("turns a device from the table: the form opens on the other face and previews the change at once", async () => {
    const api = await openRackPlacement();
    fireEvent.click(await screen.findByRole("button", { name: /^Turn ?\(pdu-a\)$/ }));
    const panel = await screen.findByRole("region", { name: "Move pdu-a in rack r12" });
    await waitFor(() => expect(api.count("POST /batches/preview")).toBe(1));
    const sent = bodyOf(api, "POST /batches/preview");
    expect(sent.relations).toEqual([{ type: "mounted_in", from: { type: "device", identity: { id: EID.pdu } }, to: { type: "rack", identity: { id: EID.rack } }, attributes: { position: 1, face: "front" } }]);
    expect(await within(panel).findByRole("region", { name: "What the inventory would change" })).toBeInTheDocument();
    expect(api.count("POST /batches")).toBe(0);
  });

  it("moves a device by dragging it to another unit: the form opens there, nothing is sent before Submit", async () => {
    const api = await openRackPlacement();
    const link = document.querySelector(`svg[data-face="front"] a[data-id="${EID.srv}"]`) as SVGElement;
    expect(link).not.toBeNull();
    // jsdom has no PointerEvent: React reads the type, the coordinates come from MouseEvent.
    fireEvent(link, new MouseEvent("pointerdown", { bubbles: true, clientY: 100, button: 0 }));
    fireEvent(window, new MouseEvent("pointermove", { clientY: 78 }));
    await waitFor(() => expect(document.querySelector("[data-drop-u]")?.getAttribute("data-drop-u")).toBe("11"));
    fireEvent(window, new MouseEvent("pointerup", { clientY: 78 }));
    const panel = await screen.findByRole("region", { name: "Move srv-01 in rack r12" });
    expect(await within(panel).findByLabelText("Position (lowest U)")).toHaveValue(11);
    await waitFor(() => expect(api.count("POST /batches/preview")).toBe(1));
    expect(bodyOf(api, "POST /batches/preview").relations?.[0]?.attributes).toEqual({ position: 11, face: "front" });
    expect(api.count("POST /batches")).toBe(0);
  });

  it("draws a shallow device of the other face as an outline that leaves the unit free", async () => {
    await openRackPlacement();
    const behind = document.querySelector(`svg[data-face="front"] [data-behind="${EID.pdu}"]`);
    expect(behind).not.toBeNull();
    expect(behind?.getAttribute("aria-hidden")).toBe("true");
    expect(freeU("front", 1)).not.toBeNull();
  });

  it("offers no placement when the schema has no mounted_in with a position", async () => {
    mockApi(routes(PROPOSER, {}, { ...physicalSchema, relations: [] }));
    renderApp({ at: `/racks/${EID.rack}` });
    expect(await screen.findByRole("button", { name: "Edit this rack" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Place a device" })).toBeNull();
    expect(document.querySelectorAll(".rack-free")).toHaveLength(0);
  });

  it("takes half units when the schema's position is a float, and not when an overlay makes it an int", async () => {
    const user = userEvent.setup();
    await openRackPlacement();
    fireEvent.click(freeU("front", 3));
    let panel = await screen.findByRole("region", { name: "Place a device in rack r12" });
    const pos = await within(panel).findByLabelText("Position (lowest U)");
    expect(pos).toHaveAttribute("step", "0.5");
    await user.clear(pos);
    await user.type(pos, "3.5");
    expect(within(panel).queryByText(/not on a unit the schema allows/)).toBeNull();

    document.body.innerHTML = "";
    mockApi(routes(PROPOSER, {}, wholeUnitSchema));
    renderApp({ at: `/racks/${EID.rack}` });
    await waitFor(() => expect(freeU("front", 3)).not.toBeNull());
    fireEvent.click(freeU("front", 3));
    panel = await screen.findByRole("region", { name: "Place a device in rack r12" });
    const pos2 = await within(panel).findByLabelText("Position (lowest U)");
    expect(pos2).toHaveAttribute("step", "1");
    await user.clear(pos2);
    await user.type(pos2, "3.5");
    expect(within(panel).getByText("This position is not on a unit the schema allows.")).toBeInTheDocument();
  });

  it("moves a device a collector placed elsewhere: says the other placement stays, withdraws only the caller's claim", async () => {
    const user = userEvent.setup();
    const api = await openRackPlacement();
    fireEvent.click(freeU("front", 1));
    const panel = await screen.findByRole("region", { name: "Place a device in rack r12" });
    await user.type(await within(panel).findByLabelText("Search by name"), "sw");
    await user.click(await within(panel).findByRole("radio", { name: "sw-a1" }));
    expect(api.calls.filter((c) => c.path === "/objects" && c.query.get("type") === "device").at(-1)?.query.get("q")).toBe("sw");
    // Its model is 1 U and full depth: no height to ask, and pdu-a on the rear at U 1 is in the way.
    expect(await within(panel).findByText("Height from its model ex-1u: 1 U.")).toBeInTheDocument();
    expect(within(panel).queryByLabelText(/^Height/)).toBeNull();
    expect(within(panel).getByText("Shares rack space with pdu-a (U1).")).toBeInTheDocument();
    expect(within(panel).getByText(/The placement in r13 was declared by arista:sw-a1/)).toBeInTheDocument();
    expect(within(panel).getByText(/your claim on this placement is withdrawn/)).toBeInTheDocument();
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    await within(panel).findByRole("region", { name: "What the inventory would change" });
    const sent = bodyOf(api, "POST /batches/preview");
    expect(sent.objects).toEqual([]);
    expect(sent.relations?.[0]).toMatchObject({ from: { type: "device", identity: { id: EID.sw } }, attributes: { position: 1, face: "front" } });
    expect(sent.unrelations).toEqual([{ type: "mounted_in", from: { type: "device", identity: { id: EID.sw } }, to: { type: "rack", identity: { id: EID.rack2 } } }]);
  });

  it("forgets a preview when the form changes after it", async () => {
    const user = userEvent.setup();
    await openRackPlacement();
    fireEvent.click(freeU("front", 5));
    const panel = await screen.findByRole("region", { name: "Place a device in rack r12" });
    await user.click(await within(panel).findByRole("radio", { name: /no collector sees/ }));
    await user.type(within(panel).getByLabelText(/^Name/), "pdu-b");
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    await within(panel).findByRole("region", { name: "What the inventory would change" });
    await user.clear(within(panel).getByLabelText("Position (lowest U)"));
    await user.type(within(panel).getByLabelText("Position (lowest U)"), "6");
    await waitFor(() => expect(within(panel).queryByRole("region", { name: "What the inventory would change" })).toBeNull());
    expect(within(panel).queryByRole("button", { name: "Submit" })).toBeNull();
  });

  it("warns, after the preview, when a 'new' device is one the inventory already has", async () => {
    const user = userEvent.setup();
    mockApi(routes(PROPOSER, { "POST /batches/preview": { source: "ui-tests", result: { ...emptyResult, relations_opened: 1, changes: [{ op: "relate", object_id: EID.sw, type: "", key: "mounted_in", other_id: EID.rack, after: { position: 5 } }] } } }));
    renderApp({ at: `/racks/${EID.rack}` });
    await waitFor(() => expect(freeU("front", 5)).not.toBeNull());
    fireEvent.click(freeU("front", 5));
    const panel = await screen.findByRole("region", { name: "Place a device in rack r12" });
    await user.click(await within(panel).findByRole("radio", { name: /no collector sees/ }));
    await user.type(within(panel).getByLabelText(/^Name/), "sw-a1");
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    expect(await within(panel).findByText(/A device named sw-a1 is already in the inventory/)).toBeInTheDocument();
  });
});

describe("the device page", () => {
  it("shows where the device is and takes it out of a rack through a preview", async () => {
    const user = userEvent.setup();
    const api = mockApi(routes(PROPOSER, { "POST /batches/preview": { source: "ui-tests", result: { ...emptyResult, relations_closed: 1, changes: [{ op: "unrelate", object_id: EID.pdu, type: "", key: "mounted_in", other_id: EID.rack }] } } }));
    renderApp({ at: `/objects/${EID.pdu}` });
    const section = await screen.findByRole("region", { name: "Rack placement" });
    expect(await within(section).findByText(/U1, Rear/)).toBeInTheDocument();
    await user.click(await within(section).findByRole("button", { name: "Remove from this rack r12" }));
    const panel = await screen.findByRole("region", { name: "Take pdu-a out of r12" });
    // pdu-a was placed by this caller (ui-tests): no other-source warning.
    expect(within(panel).queryByText(/was declared by/)).toBeNull();
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    expect(await within(panel).findByText(/Withdrawn \(is mounted in\):/)).toBeInTheDocument();
    expect(bodyOf(api, "POST /batches/preview")).toMatchObject({
      objects: [],
      unrelations: [{ type: "mounted_in", from: { type: "device", identity: { id: EID.pdu } }, to: { type: "rack", identity: { id: EID.rack } } }],
    });
    await user.click(within(panel).getByRole("button", { name: "Submit" }));
    expect(await within(panel).findByText("Applied: the inventory now says this.")).toBeInTheDocument();
    await expectNoA11yViolations();
  });

  it("places a device in a rack chosen from the list, asking its height when nobody says it", async () => {
    const user = userEvent.setup();
    const api = mockApi(routes());
    renderApp({ at: `/objects/${EID.srv}` });
    const section = await screen.findByRole("region", { name: "Rack placement" });
    await within(section).findByText("This device is not mounted in any rack.");
    await user.click(await within(section).findByRole("button", { name: "Place in a rack" }));
    const panel = await screen.findByRole("region", { name: "Place srv-01 in a rack" });
    await user.selectOptions(await within(panel).findByLabelText("Rack"), EID.rack2);
    expect(await within(panel).findByText("This rack has 42 U.")).toBeInTheDocument();
    expect(within(panel).getByText(/Nobody says the device's height/)).toBeInTheDocument();
    // An empty rack: the lowest free position is offered.
    await user.click(await within(panel).findByRole("button", { name: "Use U1, the lowest free position on this face" }));
    expect(within(panel).getByLabelText("Position (lowest U)")).toHaveValue(1);
    await user.clear(within(panel).getByLabelText("Position (lowest U)"));
    await user.type(within(panel).getByLabelText("Position (lowest U)"), "20");
    await user.type(within(panel).getByLabelText(/^Height/), "2");
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    await within(panel).findByRole("region", { name: "What the inventory would change" });
    expect(bodyOf(api, "POST /batches/preview")).toMatchObject({
      objects: [{ ref: "device", type: "device", identity: { id: EID.srv }, attributes: { u_height: 2 } }],
      relations: [{ type: "mounted_in", from: { ref: "device" }, to: { type: "rack", identity: { id: EID.rack2 } }, attributes: { position: 20, face: "front" } }],
    });
    await expectNoA11yViolations();
  });
});

describe("rooms and racks", () => {
  it("adds a rack on the floor tile clicked, in the room, and warns when the tile is taken", async () => {
    const user = userEvent.setup();
    const api = mockApi(routes());
    renderApp({ at: `/rooms/${EID.room}` });
    await screen.findByRole("heading", { level: 1, name: "Floor plan of salle-1" });
    const tile = await waitFor(() => {
      const el = document.querySelector('rect.floor-free[data-tile="0,0"]');
      expect(el).not.toBeNull();
      return el as SVGRectElement;
    });
    // r12 stands on (1, 1): not a target.
    expect(document.querySelector('rect.floor-free[data-tile="1,1"]')).toBeNull();
    fireEvent.click(tile);
    const panel = await screen.findByRole("region", { name: "Add a rack in salle-1" });
    expect(await within(panel).findByText("In Room salle-1.")).toBeInTheDocument();
    expect(within(panel).getByLabelText("Floor X")).toHaveValue(0);
    expect(within(panel).getByLabelText("Floor Y")).toHaveValue(0);
    // The name is required by the schema: nothing to preview without it.
    expect(within(panel).getByRole("button", { name: "Preview the change" })).toBeDisabled();
    await user.type(within(panel).getByLabelText(/^Name/), "r14");
    await user.type(within(panel).getByLabelText(/^Height/), "42");
    await user.clear(within(panel).getByLabelText("Floor X"));
    await user.type(within(panel).getByLabelText("Floor X"), "1");
    await user.clear(within(panel).getByLabelText("Floor Y"));
    await user.type(within(panel).getByLabelText("Floor Y"), "1");
    expect(within(panel).getByText("This tile already holds r12.")).toBeInTheDocument();
    await user.clear(within(panel).getByLabelText("Floor X"));
    await user.type(within(panel).getByLabelText("Floor X"), "7");
    expect(within(panel).getByText("This tile is outside the size recorded for the room.")).toBeInTheDocument();
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    await within(panel).findByRole("region", { name: "What the inventory would change" });
    expect(bodyOf(api, "POST /batches/preview").objects).toEqual([
      { ref: "parent", type: "room", identity: { id: EID.room } },
      { ref: "new", type: "rack", identity: { name: "r14" }, attributes: { u_height: 42, floor_x: 7, floor_y: 1 }, parent: "parent" },
    ]);
    await expectNoA11yViolations();
  });

  it("refuses half a tile and a height of zero", async () => {
    const user = userEvent.setup();
    mockApi(routes());
    renderApp({ at: `/rooms/${EID.room}` });
    await user.click(await screen.findByRole("button", { name: "Add a rack" }));
    const panel = await screen.findByRole("region", { name: "Add a rack in salle-1" });
    await user.type(await within(panel).findByLabelText(/^Name/), "r15");
    await user.type(within(panel).getByLabelText("Floor X"), "2");
    expect(within(panel).getByText("Give both the X and the Y of the floor tile, or neither.")).toBeInTheDocument();
    await user.type(within(panel).getByLabelText("Floor Y"), "1.5");
    expect(within(panel).getByText("A whole number is expected.")).toBeInTheDocument();
    await user.clear(within(panel).getByLabelText("Floor Y"));
    await user.type(within(panel).getByLabelText("Floor Y"), "0");
    await user.type(within(panel).getByLabelText(/^Height/), "0");
    expect(within(panel).getByText("Must be more than zero.")).toBeInTheDocument();
    expect(within(panel).getByRole("button", { name: "Preview the change" })).toBeDisabled();
  });

  it("adds a site with its identifiers, and a room in a site chosen from the schema's parents", async () => {
    const user = userEvent.setup();
    const api = mockApi(routes());
    renderApp({ at: "/rooms" });
    await user.click(await screen.findByRole("button", { name: "Add a site" }));
    let panel = await screen.findByRole("region", { name: "Add a site" });
    await user.type(await within(panel).findByLabelText(/^Name/), "lyon-2");
    await user.type(within(panel).getByLabelText("Code"), "LYO2");
    await user.type(within(panel).getByLabelText("Time zone"), "Europe/Paris");
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    await within(panel).findByRole("region", { name: "What the inventory would change" });
    expect(bodyOf(api, "POST /batches/preview").objects).toEqual([{ ref: "new", type: "site", identity: { name: "lyon-2", code: "LYO2" }, attributes: { timezone: "Europe/Paris" } }]);
    await user.click(within(panel).getByRole("button", { name: "Cancel" }));

    await user.click(await screen.findByRole("button", { name: "Add a room" }));
    panel = await screen.findByRole("region", { name: "Add a room" });
    const parent = await within(panel).findByLabelText("In");
    await waitFor(() => expect(within(parent).getByRole("option", { name: "paris-1" })).toBeInTheDocument());
    await user.selectOptions(parent, `site:${EID.site}`);
    await user.type(within(panel).getByLabelText(/^Name/), "salle-2");
    await user.type(within(panel).getByLabelText(/^Floor tile/), "600");
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    await within(panel).findByRole("region", { name: "What the inventory would change" });
    expect(bodyOf(api, "POST /batches/preview").objects).toEqual([
      { ref: "parent", type: "site", identity: { id: EID.site } },
      { ref: "new", type: "room", identity: { name: "salle-2" }, attributes: { tile_mm: 600 }, parent: "parent" },
    ]);
    await expectNoA11yViolations();
  });

  it("renames a rack from its object page and withdraws a value the person empties", async () => {
    const user = userEvent.setup();
    const api = mockApi(routes());
    renderApp({ at: `/objects/${EID.rack}` });
    await user.click(await screen.findByRole("button", { name: "Edit" }));
    const panel = await screen.findByRole("region", { name: "Edit r12" });
    const nameField = await within(panel).findByLabelText(/^Name/);
    expect(nameField).toHaveValue("r12");
    expect(within(panel).getByText("Nothing has been changed yet.")).toBeInTheDocument();
    expect(within(panel).getByRole("button", { name: "Preview the change" })).toBeDisabled();
    await user.clear(nameField);
    await user.type(nameField, "r12b");
    await user.selectOptions(within(panel).getByLabelText("Front faces"), "");
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    await within(panel).findByRole("region", { name: "What the inventory would change" });
    expect(bodyOf(api, "POST /batches/preview").objects).toEqual([{ ref: "obj", type: "rack", identity: { id: EID.rack }, attributes: { name: "r12b" }, withdraw: ["facing"] }]);
  });

  it("speaks French", async () => {
    mockApi(routes());
    renderApp({ at: `/racks/${EID.rack}`, locale: "fr" });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Placer un équipement" }));
    const panel = await screen.findByRole("region", { name: "Placer un équipement dans la baie r12" });
    expect(await within(panel).findByLabelText("Position (U la plus basse)")).toBeInTheDocument();
    expect(within(panel).getByRole("radio", { name: /aucun collecteur/ })).toBeInTheDocument();
    await expectNoA11yViolations();
  });
});

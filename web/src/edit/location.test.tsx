import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Batch, RelationView } from "../api/types";
import { expectNoA11yViolations } from "../test/axe";
import { discrepancyLocation, discrepancyLocationRack, page, PROPOSER, READER } from "../test/fixtures";
import { defaultRoutes, mockApi, renderApp, type Call, type Handler } from "../test/support";
import { EID, emptyResult, obj, physicalSchema, rack2, room, site, sw, swRelations } from "./fixtures";
import { placeGroups } from "./PlaceInForm";

const empty = page([]);

const ID = { building: "0e000000-0000-4000-8000-000000000005", roomB: "0e000000-0000-4000-8000-000000000011", ap: "0e000000-0000-4000-8000-0000000000e1" };
const building = obj(ID.building, "building", "batiment-1", { name: "batiment-1" }, { parent_id: EID.site, site_id: EID.site });
const roomB = obj(ID.roomB, "room", "salle-b", { name: "salle-b" }, { parent_id: ID.building, site_id: EID.site });
const ap = obj(ID.ap, "device", "ap-1", { name: "ap-1", role: "switch" }, { observed: true });

const placedIn = (id: number, place: { id: string; type: string; display: string }, source: string): RelationView => ({
  id,
  type: "located_in",
  from_id: ID.ap,
  to_id: place.id,
  source,
  confidence: "declared",
  valid_from: "2026-03-01T08:00:00Z",
  direction: "out",
  attributes: {},
  other: place,
});

const preview = { ...emptyResult, relations_opened: 1, changes: [{ op: "relate", object_id: ID.ap, type: "device", key: "located_in", other_id: ID.roomB, after: {} }] };

function routes(me: Handler = PROPOSER, extra: Record<string, Handler> = {}): Record<string, Handler> {
  const r: Record<string, Handler> = {
    ...defaultRoutes(me),
    "GET /schema": physicalSchema,
    "GET /objects": (c: Call) => {
      switch (c.query.get("type")) {
        case "site":
          return page([site]);
        case "building":
          return page([building]);
        case "room":
          return page([room, roomB]);
        default:
          return empty;
      }
    },
    [`GET /objects/${ID.ap}/relations`]: empty,
    [`GET /objects/${EID.sw}/relations`]: page([...swRelations.items, placedIn(9, { id: ID.roomB, type: "room", display: "salle-b" }, "user:old")]),
    [`GET /objects/${ID.roomB}/relations`]: page<RelationView>([
      { ...placedIn(10, { id: ID.ap, type: "device", display: "ap-1" }, "user:carol"), direction: "in", from_id: ID.ap, to_id: ID.roomB },
    ]),
    "POST /batches/preview": { source: "ui-tests", result: preview },
    "POST /batches": { source: "ui-tests", status: "applied", result: { ...preview, changeset_id: 9 } },
  };
  for (const o of [site, building, room, roomB, rack2, sw, ap]) {
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

async function openForm(locale: "en" | "fr" = "en") {
  const user = userEvent.setup();
  const api = mockApi(routes());
  renderApp({ at: `/objects/${ID.ap}`, locale });
  await screen.findByRole("heading", { level: 1, name: "ap-1" });
  await user.click(await screen.findByRole("button", { name: locale === "en" ? "Place in a site, building or room" : "Placer dans un site, un bâtiment ou une salle" }));
  return { user, api };
}

describe("placing a device that is in no rack", () => {
  it("offers the place to a caller who may propose, not to a reader", async () => {
    mockApi(routes(READER));
    const { unmount } = renderApp({ at: `/objects/${ID.ap}` });
    expect(await screen.findByRole("heading", { level: 2, name: "Location" })).toBeInTheDocument();
    expect(screen.getByText("This device is not placed in any site, building or room.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Place in a site/ })).toBeNull();
    unmount();

    mockApi(routes(PROPOSER));
    renderApp({ at: `/objects/${ID.ap}` });
    expect(await screen.findByRole("button", { name: "Place in a site, building or room" })).toBeInTheDocument();
  });

  it("lists the sites, buildings and rooms by site, then declares the chosen one by its id", async () => {
    const { user, api } = await openForm();
    const panel = await screen.findByRole("region", { name: "Place ap-1 in a site, building or room" });
    expect(within(panel).getByRole("heading", { name: "Place ap-1 in a site, building or room" })).toHaveFocus();
    const select = await within(panel).findByLabelText("Where the device stands");
    const group = within(select).getByRole("group", { name: "paris-1" });
    expect(within(group).getAllByRole("option").map((o) => o.textContent)).toEqual([
      "paris-1",
      "paris-1 › batiment-1",
      "paris-1 › batiment-1 › salle-b",
      "paris-1 › salle-1",
    ]);
    await user.selectOptions(select, "paris-1 › batiment-1 › salle-b");
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    await within(panel).findByRole("region", { name: "What the inventory would change" });
    expect(bodyOf(api, "POST /batches/preview").relations).toEqual([
      { type: "located_in", from: { type: "device", identity: { id: ID.ap } }, to: { type: "room", identity: { id: ID.roomB } }, attributes: {} },
    ]);
    expect(bodyOf(api, "POST /batches/preview").unrelations).toBeUndefined();
    await user.click(within(panel).getByRole("button", { name: "Submit" }));
    expect(await within(panel).findByText("Applied: the inventory now says this.")).toBeInTheDocument();
    expect(bodyOf(api, "POST /batches").relations).toEqual(bodyOf(api, "POST /batches/preview").relations);
  });

  it.each([["en"], ["fr"]] as const)("has no accessibility violation with the form open (%s)", async (locale) => {
    await openForm(locale);
    await screen.findByLabelText(locale === "en" ? "Where the device stands" : "Où se trouve l'équipement");
    await expectNoA11yViolations();
  });

  it("says a place another source declared stays, before anything is sent", async () => {
    const user = userEvent.setup();
    const api = mockApi(routes(PROPOSER, { [`GET /objects/${ID.ap}/relations`]: page([placedIn(5, { id: EID.room, type: "room", display: "salle-1" }, "user:old")]) }));
    renderApp({ at: `/objects/${ID.ap}` });
    await user.click(await screen.findByRole("button", { name: "Move to another place" }));
    const panel = await screen.findByRole("region", { name: "Place ap-1 in a site, building or room" });
    await user.selectOptions(await within(panel).findByLabelText("Where the device stands"), "paris-1 › batiment-1 › salle-b");
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    expect(await within(panel).findByText(/declared by user:old/)).toBeInTheDocument();
    expect(api.count("POST /batches")).toBe(0);
  });
});

describe("a device in a rack", () => {
  it("shows its places as ignored, offers no new one, and still lets one be withdrawn", async () => {
    const user = userEvent.setup();
    const api = mockApi(routes());
    renderApp({ at: `/objects/${EID.sw}` });
    const section = (await screen.findByRole("heading", { level: 2, name: "Location" })).closest("section") as HTMLElement;
    expect(within(section).getByText(/the rack decides its site/)).toBeInTheDocument();
    expect(within(section).getByText("ignored while in a rack")).toBeInTheDocument();
    expect(within(section).queryByRole("button", { name: /Place in a site|Move to another place/ })).toBeNull();
    await user.click(within(section).getByRole("button", { name: /Remove from this place/ }));
    const panel = await screen.findByRole("region", { name: "Take sw-a1 out of salle-b" });
    await user.click(within(panel).getByRole("button", { name: "Preview the change" }));
    await waitFor(() => expect(api.count("POST /batches/preview")).toBe(1));
    expect(bodyOf(api, "POST /batches/preview").unrelations).toEqual([
      { type: "located_in", from: { type: "device", identity: { id: EID.sw } }, to: { type: "room", identity: { id: ID.roomB } } },
    ]);
  });

  it("has no Location section when nothing is placed: the rack says it all", async () => {
    mockApi(routes(PROPOSER, { [`GET /objects/${EID.sw}/relations`]: swRelations }));
    renderApp({ at: `/objects/${EID.sw}` });
    await screen.findByRole("heading", { level: 1, name: "sw-a1" });
    await screen.findByRole("heading", { level: 2, name: "Rack placement" });
    expect(screen.queryByRole("heading", { level: 2, name: "Location" })).toBeNull();
  });
});

describe("a device whose places disagree on the site", () => {
  const places = page([placedIn(5, { id: ID.roomB, type: "room", display: "salle-b" }, "user:carol")]);
  const sites = { ...discrepancyLocation, object_id: ID.ap };
  const rack = { ...discrepancyLocationRack, object_id: ID.ap };

  it("says so in its Location section, and links to the conflicts", async () => {
    const api = mockApi(routes(PROPOSER, { [`GET /objects/${ID.ap}/relations`]: places, "GET /discrepancies": page([sites]) }));
    renderApp({ at: `/objects/${ID.ap}` });
    const section = (await screen.findByRole("heading", { level: 2, name: "Location" })).closest("section") as HTMLElement;
    const note = await within(section).findByText(/in different sites: it has no site until they agree/);
    expect(within(note.closest("p") as HTMLElement).getByRole("link", { name: "See the location conflicts" })).toHaveAttribute("href", "/discrepancies?kind=location_conflict");
    const q = api.calls.find((c) => c.path === "/discrepancies")?.query;
    expect(q?.get("kind")).toBe("location_conflict");
    expect(q?.get("object")).toBe(ID.ap);
    expect(q?.get("status")).toBe("open");
    await expectNoA11yViolations();
  });

  it("says it in French", async () => {
    mockApi(routes(PROPOSER, { [`GET /objects/${ID.ap}/relations`]: places, "GET /discrepancies": page([sites]) }));
    renderApp({ at: `/objects/${ID.ap}`, locale: "fr" });
    expect(await screen.findByText(/dans des sites différents : il n'a pas de site/)).toBeInTheDocument();
  });

  it("says the rack decides when a place is in another site than the rack", async () => {
    mockApi(routes(PROPOSER, { [`GET /objects/${ID.ap}/relations`]: places, "GET /discrepancies": page([rack]) }));
    renderApp({ at: `/objects/${ID.ap}` });
    expect(await screen.findByText(/in another site than its rack: the rack decides/)).toBeInTheDocument();
  });

  it("says nothing when no conflict is open", async () => {
    mockApi(routes(PROPOSER, { [`GET /objects/${ID.ap}/relations`]: places, "GET /discrepancies": page([]) }));
    renderApp({ at: `/objects/${ID.ap}` });
    await screen.findByRole("heading", { level: 2, name: "Location" });
    await waitFor(() => expect(screen.queryByText(/it has no site until they agree/)).toBeNull());
    expect(screen.queryByRole("link", { name: "See the location conflicts" })).toBeNull();
  });
});

describe("where a placed device shows", () => {
  it("is the location fact of a device in no rack", async () => {
    mockApi(routes(PROPOSER, { [`GET /objects/${ID.ap}/relations`]: page([placedIn(5, { id: ID.roomB, type: "room", display: "salle-b" }, "user:carol")]) }));
    renderApp({ at: `/objects/${ID.ap}` });
    const fact = await waitFor(() => {
      const f = document.querySelector('[data-fact="location"]');
      expect(f).not.toBeNull();
      return f as HTMLElement;
    });
    expect(fact).toHaveTextContent("salle-b");
  });

  it("is listed on the page of the room, building or site", async () => {
    mockApi(routes());
    renderApp({ at: `/objects/${ID.roomB}` });
    const here = (await screen.findByRole("heading", { level: 2, name: "Devices here" })).closest("section") as HTMLElement;
    expect(within(here).getByRole("link", { name: "ap-1" })).toBeInTheDocument();
  });
});

describe("the choice of places", () => {
  it("groups by site, sites unknown last, paths from the parents", () => {
    const groups = placeGroups([roomB, site, building, room, obj("x", "room", "orphan", {})], "Site unknown");
    expect(groups.map((g) => g.title)).toEqual(["paris-1", "Site unknown"]);
    expect(groups[0]?.options.map((o) => o.path)).toEqual(["paris-1", "paris-1 › batiment-1", "paris-1 › batiment-1 › salle-b", "paris-1 › salle-1"]);
    expect(groups[1]?.options.map((o) => o.path)).toEqual(["orphan"]);
  });
});

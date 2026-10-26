// Renaming a site, a room or a rack (ADR 0067): one claim on the name, the object designated by its id.
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Batch, Provenance } from "../api/types";
import { expectNoA11yViolations } from "../test/axe";
import { page, PROPOSER, READER } from "../test/fixtures";
import {
  defaultRoutes,
  mockApi,
  problem,
  renderApp,
  type Handler,
} from "../test/support";
import {
  EID,
  emptyResult,
  physicalSchema,
  rack,
  rack2,
  room,
  site,
} from "./fixtures";

const empty = page([]);

/** The name of an object as the server resolves it: the winner, and the claims of the other sources. */
const nameProvenance = (
  id: string,
  value: string,
  winner: string,
  others: string[] = [],
): Provenance => ({
  object_id: id,
  attributes: [
    {
      key: "name",
      value,
      source: winner,
      confidence: "declared",
      valid_from: "2026-03-01T08:00:00Z",
      other_claims: others.map((source) => ({
        key: "name",
        source,
        value,
        confidence: "declared",
        first_seen: "2026-03-01T08:00:00Z",
        last_seen: "2026-03-01T08:00:00Z",
      })),
    },
  ],
});

function routes(
  me: Handler = PROPOSER,
  extra: Record<string, Handler> = {},
): Record<string, Handler> {
  const r: Record<string, Handler> = {
    ...defaultRoutes(me),
    "GET /schema": physicalSchema,
    "GET /objects": page([]),
    [`GET /racks/${EID.rack}/contents`]: empty,
    [`GET /rooms/${EID.room}/contents`]: { racks: [], aisles: [] },
    [`GET /racks/${EID.rack}/removed`]: empty,
    "POST /batches/preview": {
      source: "user:carol",
      result: {
        ...emptyResult,
        values_changed: 1,
        changes: [
          {
            op: "set",
            object_id: EID.rack,
            type: "rack",
            key: "name",
            before: "r12",
            after: "r12b",
          },
        ],
      },
    },
    "POST /batches": {
      source: "user:carol",
      status: "applied",
      result: { ...emptyResult, changeset_id: 9 },
    },
  };
  for (const o of [site, room, rack, rack2]) {
    r[`GET /objects/${o.id}`] = o;
    r[`GET /objects/${o.id}/provenance`] = nameProvenance(
      o.id,
      o.display,
      "user:carol",
    );
    r[`GET /objects/${o.id}/timeline`] = empty;
    r[`GET /objects/${o.id}/relations`] = empty;
  }
  return { ...r, ...extra };
}

const lastBody = (api: ReturnType<typeof mockApi>, key: string): Batch => {
  const c = [...api.calls]
    .reverse()
    .find((x) => `${x.method} ${x.path}` === key);
  if (!c) throw new Error(`no ${key}`);
  return c.body as Batch;
};

describe("renaming a place", () => {
  it("renames a rack from its page: the object by id, one claim on the name, nothing else", async () => {
    const user = userEvent.setup();
    const api = mockApi(routes());
    renderApp({ at: `/racks/${EID.rack}` });
    await user.click(await screen.findByRole("button", { name: "Rename" }));
    const panel = await screen.findByRole("region", { name: "Rename r12" });
    const field = within(panel).getByLabelText("New name");
    expect(field).toHaveValue("r12");
    expect(
      within(panel).getByRole("button", { name: "Preview the change" }),
    ).toBeDisabled();
    await user.clear(field);
    await user.type(field, " r12b ");
    await user.click(
      within(panel).getByRole("button", { name: "Preview the change" }),
    );
    await within(panel).findByRole("region", {
      name: "What the inventory would change",
    });
    expect(lastBody(api, "POST /batches/preview").objects).toEqual([
      {
        ref: "obj",
        type: "rack",
        identity: { id: EID.rack },
        attributes: { name: "r12b" },
      },
    ]);
    await user.click(within(panel).getByRole("button", { name: "Submit" }));
    expect(
      await within(panel).findByText(/applied/i, undefined, { timeout: 2000 }),
    ).toBeInTheDocument();
    expect(lastBody(api, "POST /batches").objects).toHaveLength(1);
    await expectNoA11yViolations();
  });

  it("says which sources still give the name, and nothing when only people do", async () => {
    const user = userEvent.setup();
    mockApi(
      routes(PROPOSER, {
        [`GET /objects/${EID.rack}/provenance`]: nameProvenance(
          EID.rack,
          "r12",
          "user:carol",
          ["import:pereire"],
        ),
        [`GET /objects/${EID.rack2}/provenance`]: nameProvenance(
          EID.rack2,
          "r13",
          "user:carol",
        ),
      }),
    );
    const view = renderApp({ at: `/racks/${EID.rack}` });
    await user.click(await screen.findByRole("button", { name: "Rename" }));
    const panel = await screen.findByRole("region", { name: "Rename r12" });
    const note = await within(panel).findByRole("note");
    expect(note).toHaveTextContent("import:pereire");
    expect(note).toHaveTextContent("takes precedence over");
    expect(note).toHaveTextContent('"r12"');
    await expectNoA11yViolations();
    view.unmount();

    renderApp({ at: `/racks/${EID.rack2}` });
    await userEvent.click(await screen.findByRole("button", { name: "Rename" }));
    const other = await screen.findByRole("region", { name: "Rename r13" });
    await waitFor(() =>
      expect(within(other).queryByRole("note")).not.toBeInTheDocument(),
    );
  });

  it("refuses an empty name and a name that did not change", async () => {
    const user = userEvent.setup();
    mockApi(routes());
    renderApp({ at: `/racks/${EID.rack}` });
    await user.click(await screen.findByRole("button", { name: "Rename" }));
    const panel = await screen.findByRole("region", { name: "Rename r12" });
    const field = within(panel).getByLabelText("New name");
    await user.clear(field);
    expect(await within(panel).findByRole("alert")).toBeInTheDocument();
    expect(
      within(panel).getByRole("button", { name: "Preview the change" }),
    ).toBeDisabled();
    await user.type(field, "r12");
    expect(
      within(panel).getByText("Nothing has been changed yet."),
    ).toBeInTheDocument();
  });

  it("shows that the name is taken, in words, when the server answers 409 name-taken", async () => {
    const user = userEvent.setup();
    mockApi(
      routes(PROPOSER, {
        "POST /batches/preview": problem(
          409,
          "name-taken",
          'conflict: name taken: another rack here already carries the name "r13" (r13, claimed by import:pereire)',
        ),
      }),
    );
    renderApp({ at: `/racks/${EID.rack}` });
    await user.click(await screen.findByRole("button", { name: "Rename" }));
    const panel = await screen.findByRole("region", { name: "Rename r12" });
    const field = within(panel).getByLabelText("New name");
    await user.clear(field);
    await user.type(field, "r13");
    await user.click(
      within(panel).getByRole("button", { name: "Preview the change" }),
    );
    const alert = await within(panel).findByRole("alert");
    expect(alert).toHaveTextContent("already has this name here");
    expect(alert).toHaveTextContent("claimed by import:pereire");
    expect(alert).not.toHaveTextContent("Someone changed this");
    await expectNoA11yViolations();
  });

  it("renames a room from its floor plan and a site from its page", async () => {
    const user = userEvent.setup();
    const api = mockApi(routes());
    const view = renderApp({ at: `/rooms/${EID.room}` });
    await user.click(await screen.findByRole("button", { name: "Rename" }));
    const panel = await screen.findByRole("region", {
      name: `Rename ${room.display}`,
    });
    const field = within(panel).getByLabelText("New name");
    await user.clear(field);
    await user.type(field, "salle serveurs");
    await user.click(
      within(panel).getByRole("button", { name: "Preview the change" }),
    );
    await within(panel).findByRole("region", {
      name: "What the inventory would change",
    });
    expect(lastBody(api, "POST /batches/preview").objects?.[0]).toMatchObject({
      type: "room",
      identity: { id: EID.room },
      attributes: { name: "salle serveurs" },
    });
    view.unmount();

    renderApp({ at: `/objects/${EID.site}` });
    await userEvent.click(await screen.findByRole("button", { name: "Rename" }));
    expect(
      await screen.findByRole("region", { name: `Rename ${site.display}` }),
    ).toBeInTheDocument();
  });

  it("is not offered to someone who cannot declare", async () => {
    mockApi(routes(READER));
    renderApp({ at: `/racks/${EID.rack}` });
    await screen.findByRole("heading", { level: 1, name: "Rack r12" });
    expect(screen.queryByRole("button", { name: "Rename" })).toBeNull();
  });

  it("speaks French", async () => {
    const user = userEvent.setup();
    mockApi(routes());
    renderApp({ at: `/racks/${EID.rack}`, locale: "fr" });
    await user.click(await screen.findByRole("button", { name: "Renommer" }));
    const panel = await screen.findByRole("region", { name: "Renommer r12" });
    expect(within(panel).getByLabelText("Nouveau nom")).toBeInTheDocument();
    await expectNoA11yViolations();
  });
});

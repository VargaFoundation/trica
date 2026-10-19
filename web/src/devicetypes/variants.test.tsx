import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { DeviceTypeCandidate, DeviceTypeChoice } from "../api/types";
import { chooseModel } from "../edit/batch";
import { clearFaceImages } from "../racks/faceImages";
import { expectNoA11yViolations } from "../test/axe";
import { ID, meWith, page, PROPOSER, READER, srv01 } from "../test/fixtures";
import { defaultRoutes, mockApi, renderApp, type Call, type Handler } from "../test/support";

const BASE = "0a000000-0000-4000-8000-0000000000e0";
const SFF = "0a000000-0000-4000-8000-0000000000e1";
const cand = (slug: string, model: string, u: number, extra: Partial<DeviceTypeCandidate> = {}): DeviceTypeCandidate => ({
  slug,
  model,
  vendor: "Dell",
  u_height: u,
  full_depth: true,
  how: "name",
  current: false,
  library_match: false,
  ...extra,
});

const candidates = [
  cand("dell-poweredge-r330", "PowerEdge R330", 1, { current: true, library_match: true }),
  cand("dell-poweredge-r330-4lff-cabled", "PowerEdge R330 4 x 3.5in cabled", 1, { front_image: "/api/v1/device-types/dell-poweredge-r330-4lff-cabled/front" }),
  cand("dell-poweredge-r330-4lff-hot-plug", "PowerEdge R330 4 x 3.5in hot-plug", 1, { front_image: "/api/v1/device-types/dell-poweredge-r330-4lff-hot-plug/front" }),
  cand("dell-poweredge-r330-8sff", "PowerEdge R330 8 x 2.5in hot-plug", 1, {
    front_image: "/api/v1/device-types/dell-poweredge-r330-8sff/front",
    rear_image: "/api/v1/device-types/dell-poweredge-r330-8sff/rear",
  }),
];

const uncertain: DeviceTypeChoice = {
  device: { id: ID.srv01, type: "device", display: "srv-01" },
  vendor: "Dell",
  model: "PowerEdge R330",
  current: { model: { id: BASE, type: "device_type", display: "PowerEdge R330" }, slug: "dell-poweredge-r330", model_name: "PowerEdge R330", source: "library:devicetypes" },
  library_match: { slug: "dell-poweredge-r330", model: "PowerEdge R330", how: "model" },
  certain: false,
  reason: "base",
  declarations: [],
  candidates,
  can_choose: true,
};

const declared = (by: string): DeviceTypeChoice => ({
  ...uncertain,
  current: { model: { id: SFF, type: "device_type", display: "PowerEdge R330 8 x 2.5in hot-plug" }, slug: "dell-poweredge-r330-8sff", source: by },
  certain: true,
  reason: "declared",
  declared_by: by,
  declarations: [{ model: { id: SFF, type: "device_type", display: "PowerEdge R330 8 x 2.5in hot-plug" }, slug: "dell-poweredge-r330-8sff", source: by, last_seen: "2026-10-19T08:00:00Z" }],
  candidates: candidates.map((c) => ({ ...c, current: c.slug === "dell-poweredge-r330-8sff" })),
});

const KEY = `GET /objects/${ID.srv01}/device-type-candidates`;
const srvR330 = { ...srv01, attributes: { ...srv01.attributes, vendor: "Dell", model: "PowerEdge R330" } };

function routes(choice: Handler, me: Handler = PROPOSER, extra: Record<string, Handler> = {}) {
  return { ...defaultRoutes(me), [`GET /objects/${ID.srv01}`]: srvR330, [KEY]: choice, ...extra };
}

async function section() {
  return (await screen.findByRole("heading", { name: "Device model", level: 2 })).closest("section") as HTMLElement;
}

describe("choosing the chassis of a device", () => {
  beforeEach(() => clearFaceImages());
  afterEach(() => vi.restoreAllMocks());

  it("says the variant is not certain and offers the candidates side by side", async () => {
    mockApi(routes(uncertain));
    renderApp({ at: `/objects/${ID.srv01}` });
    const s = await section();
    expect(within(s).getByRole("note")).toHaveTextContent("Variant not certain — choose the chassis");
    expect(within(s).getByRole("note")).toHaveTextContent("The device reports “PowerEdge R330”: the library has several chassis under that name");
    expect(within(s).getByRole("note")).toHaveTextContent("For now it is drawn as PowerEdge R330.");
    // The header says it too, and points at the section.
    expect(screen.getByText("Variant not certain").closest("a")).toHaveAttribute("href", "#device-model");
    const group = within(s).getByRole("group", { name: "Choose the chassis" });
    const radios = within(group).getAllByRole("radio");
    expect(radios).toHaveLength(4);
    expect(within(group).getByRole("radio", { name: /PowerEdge R330 8 x 2\.5in hot-plug/ })).not.toBeChecked();
    expect(within(group).getByRole("radio", { name: /^PowerEdge R330\s*1 U · full depth\s*current\s*name match$/ })).toBeInTheDocument();
    // No image decoder in the test browser: the thumbnails are placeholders, never an <img> with a blob: or data: URL.
    expect(s.querySelectorAll("img")).toHaveLength(0);
    expect(s.querySelector('[data-slug="dell-poweredge-r330"] .variant-thumb')).toHaveTextContent("No picture");
    // Nothing to preview before a choice.
    expect(within(s).queryByRole("button", { name: "Preview the change" })).toBeNull();
    await expectNoA11yViolations();
  });

  it("previews then submits the declared instance_of, and shows the model once applied", async () => {
    let now: DeviceTypeChoice = uncertain;
    const sent: Call[] = [];
    const api = mockApi(
      routes(() => now, PROPOSER, {
        "POST /batches/preview": (c: Call) => {
          sent.push(c);
          return { source: "ui-tests", result: { created: 0, values_changed: 0, relations_opened: 1, relations_closed: 0, objects: { model: SFF }, changes: [{ op: "relate", object_id: ID.srv01, type: "device", key: "instance_of", other_id: SFF }] } };
        },
        "POST /batches": (c: Call) => {
          sent.push(c);
          now = declared("ui-tests");
          return { source: "ui-tests", status: "applied", result: { created: 0, values_changed: 0, relations_opened: 1, relations_closed: 0, objects: { model: SFF }, changes: [] } };
        },
      }),
    );
    renderApp({ at: `/objects/${ID.srv01}` });
    const s = await section();
    await userEvent.click(within(s).getByRole("radio", { name: /8 x 2\.5in/ }));
    expect(within(s).getByRole("region", { name: "PowerEdge R330 8 x 2.5in hot-plug" })).toHaveTextContent(/Front.*Rear/);
    expect(s).toHaveTextContent("Submitting declares that srv-01 is a PowerEdge R330 8 x 2.5in hot-plug.");
    await userEvent.click(within(s).getByRole("button", { name: "Preview the change" }));
    await userEvent.click(await within(s).findByRole("button", { name: "Submit" }));
    await waitFor(() => expect(within(s).getByRole("status")).toHaveTextContent("Applied"));
    const want = chooseModel({ deviceId: ID.srv01, slug: "dell-poweredge-r330-8sff" });
    for (const c of sent) {
      const b = c.body as typeof want;
      expect(b.objects).toEqual(want.objects);
      expect(b.relations).toEqual(want.relations);
      expect(b.scope).toBe("partial");
    }
    expect(sent).toHaveLength(2);
    // Refreshed: the model is declared, the chooser is gone, the header pill too.
    await waitFor(() => expect(s.querySelector(".variant-decided")).toHaveTextContent("PowerEdge R330 8 x 2.5in hot-plug. Declared by ui-tests."));
    expect(within(s).queryByRole("radio")).toBeNull();
    expect(screen.queryByText("Variant not certain")).toBeNull();
    expect(api.count(KEY)).toBeGreaterThan(1);
  });

  it("does not offer the choice to a caller who may not propose", async () => {
    mockApi(routes({ ...uncertain, can_choose: false }, READER));
    renderApp({ at: `/objects/${ID.srv01}` });
    const s = await section();
    expect(within(s).getByRole("note")).toHaveTextContent("Someone who may propose changes can choose it.");
    expect(within(s).queryByRole("radio")).toBeNull();
  });

  it("says which rule decided a certain model, with no change offered to a proposer", async () => {
    mockApi(routes({ ...uncertain, certain: true, reason: "sku", candidates: [candidates[3] as DeviceTypeCandidate, candidates[2] as DeviceTypeCandidate] }));
    renderApp({ at: `/objects/${ID.srv01}` });
    const s = await section();
    expect(s.querySelector(".variant-decided")).toHaveTextContent("Decided by the part number the device reports");
    expect(within(s).queryByRole("button", { name: "Change the model" })).toBeNull();
    expect(screen.queryByText("Variant not certain")).toBeNull();
  });

  it("lets an administrator change a certain model", async () => {
    mockApi(routes({ ...uncertain, certain: true, reason: "model" }, meWith(["read", "propose", "admin"])));
    renderApp({ at: `/objects/${ID.srv01}` });
    const s = await section();
    expect(s.querySelector(".variant-decided")).toHaveTextContent("Decided by the model name the device reports");
    await userEvent.click(within(s).getByRole("button", { name: "Change the model" }));
    expect(within(s).getByRole("group", { name: "Choose the chassis" })).toBeInTheDocument();
    await userEvent.click(within(s).getByRole("button", { name: "Cancel" }));
    expect(within(s).queryByRole("radio")).toBeNull();
  });

  it("lets the person who declared change their mind, withdrawing their own declaration only", async () => {
    const sent: Call[] = [];
    mockApi(
      routes(declared("ui-tests"), PROPOSER, {
        "POST /batches/preview": (c: Call) => {
          sent.push(c);
          return { source: "ui-tests", result: { created: 0, values_changed: 0, relations_opened: 1, relations_closed: 1, objects: {}, changes: [{ op: "relate", object_id: ID.srv01, type: "device", key: "instance_of", other_id: BASE }] } };
        },
      }),
    );
    renderApp({ at: `/objects/${ID.srv01}` });
    const s = await section();
    expect(s.querySelector(".variant-decided")).toHaveTextContent("Declared by ui-tests.");
    await userEvent.click(within(s).getByRole("button", { name: "Change the model" }));
    // The current model is not a change: nothing to preview while it is the one selected.
    await userEvent.click(within(s).getByRole("radio", { name: /4 x 3\.5in cabled/ }));
    await userEvent.click(within(s).getByRole("button", { name: "Preview the change" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect((sent[0]?.body as { unrelations?: unknown }).unrelations).toEqual(
      chooseModel({ deviceId: ID.srv01, slug: "dell-poweredge-r330-4lff-cabled", withdraw: [SFF] }).unrelations,
    );
  });

  it("does not change a colleague's declaration for a proposer, and says when a part number disagrees", async () => {
    mockApi(routes({ ...declared("user:alice"), sku_conflict: { slug: "dell-poweredge-r330-4lff-cabled", model: "PowerEdge R330 4 x 3.5in cabled", how: "sku" } }));
    renderApp({ at: `/objects/${ID.srv01}` });
    const s = await section();
    expect(within(s).queryByRole("button", { name: "Change the model" })).toBeNull();
    expect(s).toHaveTextContent("The part number the device reports names another chassis, PowerEdge R330 4 x 3.5in cabled.");
    await userEvent.click(within(s).getByRole("button", { name: "Choose PowerEdge R330 4 x 3.5in cabled" }));
    expect(within(s).getByRole("radio", { name: /4 x 3\.5in cabled/ })).toBeChecked();
    await expectNoA11yViolations();
  });

  it("is not shown for a device the library knows nothing of", async () => {
    mockApi(routes({ ...uncertain, current: undefined, candidates: [], reason: "none" }));
    renderApp({ at: `/objects/${ID.srv01}` });
    await screen.findByRole("heading", { name: "srv-01", level: 1 });
    await waitFor(() => expect(screen.queryByRole("heading", { name: "Device model" })).toBeNull());
  });

  it("is in French too", async () => {
    mockApi(routes(uncertain));
    renderApp({ at: `/objects/${ID.srv01}`, locale: "fr" });
    const s = (await screen.findByRole("heading", { name: "Modèle de l'équipement", level: 2 })).closest("section") as HTMLElement;
    expect(within(s).getByRole("note")).toHaveTextContent("Variante incertaine — choisissez le châssis");
    expect(within(s).getByRole("group", { name: "Choisissez le châssis" })).toBeInTheDocument();
  });
});

describe("a rack holding a device of uncertain variant", () => {
  afterEach(() => vi.restoreAllMocks());

  it("says so in the tooltip and links to the choice", async () => {
    mockApi({
      ...defaultRoutes(),
      [`GET /objects/${ID.rack}`]: { id: ID.rack, type: "rack", display: "r12", status: "active", attributes: { u_height: 12 }, created_at: "2026-03-01T08:00:00Z", observed: false },
      [`GET /racks/${ID.rack}/contents`]: page([
        { device: { id: ID.srv01, type: "device", display: "srv-01" }, position: 3, face: "front", u_height: 1, full_depth: true, model_name: "PowerEdge R330", variant_uncertain: true, variants: 4 },
      ]),
    });
    renderApp({ at: `/racks/${ID.rack}` });
    const slot = await waitFor(() => {
      const el = document.querySelector('svg[data-face="front"] a.rack-slot');
      if (!el) throw new Error("no slot yet");
      return el;
    });
    expect(slot).toHaveAttribute("data-variant-uncertain", "true");
    expect(slot.querySelector("title")).toHaveTextContent("variant not certain");
    const link = await screen.findByRole("link", { name: /^Choose the chassis\s*\(srv-01\)$/ });
    expect(link).toHaveAttribute("href", `/objects/${ID.srv01}#device-model`);
  });
});

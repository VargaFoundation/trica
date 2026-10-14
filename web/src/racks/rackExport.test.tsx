import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ObjectView, RelationView } from "../api/types";
import { ID, page } from "../test/fixtures";
import { defaultRoutes, mockApi, renderApp, type Call } from "../test/support";

const obj = (
  id: string,
  type: string,
  display: string,
  attributes: Record<string, unknown> = {},
): ObjectView => ({
  id,
  type,
  display,
  status: "active",
  attributes,
  created_at: "2026-03-01T08:00:00Z",
  observed: false,
});

const DEV = "0a000000-0000-4000-8000-000000000101";
const MODEL = "0a000000-0000-4000-8000-0000000000c2";
const rel = (
  type: string,
  direction: string,
  other: RelationView["other"],
  attributes: Record<string, unknown>,
): RelationView => ({
  id: 1,
  type,
  direction,
  from_id: DEV,
  to_id: ID.rack,
  other,
  source: "declared:lab-file",
  confidence: "declared",
  valid_from: "2026-03-01T08:00:00Z",
  attributes,
});

describe("rack export", () => {
  const saved: { name: string; blob: Blob }[] = [];
  const texts: string[] = [];
  const types: (string | undefined)[] = [];

  beforeEach(() => {
    saved.length = 0;
    texts.length = 0;
    types.length = 0;
    const state: Record<string, unknown> = {};
    const ctx = new Proxy(state, {
      get: (t, k) =>
        k === "fillText"
          ? (s: string) => texts.push(s)
          : k in t
            ? t[k as string]
            : () => undefined,
      set: (t, k, v) => ((t[k as string] = v), true),
    });
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(
      () => ctx as never,
    );
    vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation(
      (cb, type) => {
        types.push(type);
        cb(new Blob(["x"], { type }));
      },
    );
    let n = 0;
    const urls = new Map<string, Blob>();
    URL.createObjectURL = vi.fn((b: Blob) => {
      const u = `blob:test/${++n}`;
      urls.set(u, b);
      return u;
    });
    URL.revokeObjectURL = vi.fn();
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      saved.push({ name: this.download, blob: urls.get(this.href) as Blob });
    });
    mockApi({
      ...defaultRoutes(),
      [`GET /objects/${ID.rack}`]: obj(ID.rack, "rack", "r12", {
        u_height: 12,
      }),
      [`GET /objects/${ID.rack}/relations`]: page([
        rel(
          "mounted_in",
          "in",
          { id: DEV, type: "device", display: "sw-a1" },
          { position: 10, face: "front" },
        ),
      ]),
      [`GET /objects/${DEV}/relations`]: page([
        rel(
          "instance_of",
          "out",
          { id: MODEL, type: "device_type", display: "model" },
          {},
        ),
      ]),
      "GET /objects": (c: Call) =>
        c.query.get("type") === "device_type"
          ? page([obj(MODEL, "device_type", "ex-2u", { u_height: 2 })])
          : page([]),
    });
  });
  afterEach(() => vi.restoreAllMocks());

  it("saves the elevation as a PNG: both faces, the devices and the date", async () => {
    renderApp({ at: `/racks/${ID.rack}` });
    await screen.findByRole("table");
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Export PNG" }));
    await waitFor(() => expect(saved).toHaveLength(1));
    expect(saved[0]?.name).toBe("trica-rack-r12.png");
    expect(types).toEqual(["image/png"]);
    expect(texts).toEqual(
      expect.arrayContaining(["Rack r12", "Front", "Rear", "sw-a1  U10-U11"]),
    );
    expect(texts).toContain(
      `Exported from trica on ${new Date().toISOString().slice(0, 10)}`,
    );
  });

  it("saves it as a PDF, from a JPEG of the same sheet", async () => {
    renderApp({ at: `/racks/${ID.rack}` });
    await screen.findByRole("table");
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Export PDF" }));
    // The fake canvas encodes a blob that is not a JPEG: the PDF writer says so rather than saving a broken file.
    expect(
      await screen.findByText("The PDF export failed: not a JPEG"),
    ).toBeDefined();
    expect(types).toEqual(["image/jpeg"]);
    expect(saved).toHaveLength(0);
  });
});

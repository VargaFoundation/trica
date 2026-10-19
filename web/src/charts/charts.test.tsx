import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { SeriesPoint } from "../api/types";
import { ID, schema, seriesOf, srv01 } from "../test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp, type Call } from "../test/support";
import { makeLookup } from "../hooks";
import { bandPath, linePath, nearest, runs, scale, valueRange } from "./geometry";
import { readRange, seriesKeys } from "./Measurements";

const HOUR = 3_600_000;
const pt = (h: number, avg: number, min = avg, max = avg): SeriesPoint => ({ t: new Date(Date.UTC(2026, 9, 12, h)).toISOString(), avg, min, max });

describe("the geometry of a curve", () => {
  const pts = [pt(0, 10, 5, 15), pt(1, 20), pt(2, 30), pt(5, 40, 35, 50)];

  it("cuts the line where buckets are missing, and only there", () => {
    expect(runs(pts, 3600).map((r) => r.length)).toEqual([3, 1]);
    // Half a step late is still the next bucket.
    expect(runs([pt(0, 1), { ...pt(1, 2), t: new Date(Date.UTC(2026, 9, 12, 1, 29)).toISOString() }], 3600)).toHaveLength(1);
    expect(runs([], 3600)).toEqual([]);
  });

  it("draws a value range a tenth wider than the readings, never flat", () => {
    expect(valueRange(pts)).toEqual([0.5, 54.5]);
    expect(valueRange([pt(0, 20)])).toEqual([18, 22]);
    expect(valueRange([pt(0, 0)])).toEqual([-1, 1]);
    expect(valueRange([])).toEqual([0, 1]);
  });

  it("places time left to right and values bottom to top", () => {
    const from = Date.UTC(2026, 9, 12);
    const s = scale({ width: 100, height: 50, left: 0, right: 0, top: 0, bottom: 0 }, from, from + 10 * HOUR, [0, 100]);
    expect(s.x(from)).toBe(0);
    expect(s.x(from + 5 * HOUR)).toBe(50);
    expect(s.y(0)).toBe(50);
    expect(s.y(100)).toBe(0);
  });

  it("draws one stretch of line and one closed band per run, at the middle of each bucket", () => {
    const from = Date.UTC(2026, 9, 12);
    const s = scale({ width: 600, height: 100, left: 0, right: 0, top: 0, bottom: 0 }, from, from + 6 * HOUR, [0, 100]);
    const line = linePath(runs(pts, 3600), 3600, s);
    expect(line.match(/M/g)).toHaveLength(2);
    expect(line.startsWith("M50,90L150,80L250,70M550,60")).toBe(true);
    const band = bandPath(runs(pts, 3600), 3600, s);
    expect(band.match(/Z/g)).toHaveLength(2);
    expect(band).toContain("M50,85");
  });

  it("finds the bucket nearest to a time", () => {
    const t0 = Date.UTC(2026, 9, 12);
    expect(nearest(pts, 3600, t0)).toBe(0);
    expect(nearest(pts, 3600, t0 + 2.4 * HOUR)).toBe(2);
    expect(nearest(pts, 3600, t0 + 4.9 * HOUR)).toBe(3);
    expect(nearest([], 3600, t0)).toBe(-1);
  });
});

describe("which measurements get a curve", () => {
  const lookup = makeLookup(schema, (_l, f) => f);

  it("keeps those the type declares as a series and the object has as a number", () => {
    expect(seriesKeys(srv01, lookup)).toEqual(["power_w", "inlet_temp_c", "outlet_temp_c"]);
    expect(seriesKeys({ ...srv01, attributes: { ...srv01.attributes, inlet_temp_c: "n/a", power_w: undefined } }, lookup)).toEqual(["outlet_temp_c"]);
    expect(seriesKeys({ ...srv01, type: "rack" }, lookup)).toEqual([]);
  });

  it("reads the period from the address, a day unless told otherwise", () => {
    expect(readRange(null)).toBe("24h");
    expect(readRange("7d")).toBe("7d");
    expect(readRange("30d")).toBe("30d");
    expect(readRange("1y")).toBe("24h");
  });
});

const seriesCalls = (calls: Call[], key?: string) => calls.filter((c) => c.path === `/objects/${ID.srv01}/series` && (!key || c.query.get("key") === key));
const span = (c: Call) => Date.parse(c.query.get("to") ?? "") - Date.parse(c.query.get("from") ?? "");

async function trends() {
  return (await screen.findByRole("heading", { name: "Trends" })).closest("section") as HTMLElement;
}

async function curve(name: string) {
  const plot = await screen.findByRole("group", { name: `${name} over time. Use the arrow keys to read each point.` });
  return { plot, card: plot.closest("section.series-item") as HTMLElement };
}

describe("the trends of an object", () => {
  it("draws one curve per measurement over the last day, asked to the minute", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.srv01}` });
    const section = await trends();
    for (const name of ["Power draw", "Inlet temperature", "Outlet temperature"]) await curve(name);
    expect(within(section).getAllByRole("heading", { level: 3 }).map((h) => h.textContent)).toEqual(["Power draw", "Inlet temperature", "Outlet temperature"]);
    const power = seriesCalls(api.calls, "power_w");
    expect(power).toHaveLength(1);
    const c = power[0] as Call;
    expect(c.query.get("points")).toBe("120");
    expect(span(c)).toBe(24 * HOUR);
    expect(Date.parse(c.query.get("to") ?? "") % 60_000).toBe(0);
    // Only the series: uptime is a number but not a measurement over time.
    expect(seriesCalls(api.calls).map((x) => x.query.get("key")).sort()).toEqual(["inlet_temp_c", "outlet_temp_c", "power_w"]);
    expect(within(section).getByRole("button", { name: "24 h" })).toHaveAttribute("aria-pressed", "true");
  });

  it("says the last, lowest and highest readings in words", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.srv01}` });
    const { card } = await curve("Power draw");
    expect(within(card).getByText("Last 405 W · lowest 390 W · highest 430 W")).toBeInTheDocument();
    expect(card.querySelector("path.series-line")?.getAttribute("d")?.match(/M/g)).toHaveLength(2);
  });

  it("changes the period for every curve", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.srv01}` });
    const section = await trends();
    await curve("Power draw");
    await userEvent.click(within(section).getByRole("button", { name: "7 days" }));
    await waitFor(() => expect(seriesCalls(api.calls, "power_w").map(span)).toEqual([24 * HOUR, 7 * 24 * HOUR]));
    expect(seriesCalls(api.calls, "inlet_temp_c").map(span)).toEqual([24 * HOUR, 7 * 24 * HOUR]);
    expect(within(section).getByRole("button", { name: "7 days" })).toHaveAttribute("aria-pressed", "true");
    expect(within(section).getByRole("button", { name: "24 h" })).toHaveAttribute("aria-pressed", "false");
  });

  it("opens on the period the address names", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.srv01}?range=30d` });
    await curve("Power draw");
    expect(seriesCalls(api.calls, "power_w").map(span)).toEqual([30 * 24 * HOUR]);
    expect(screen.getByRole("button", { name: "30 days" })).toHaveAttribute("aria-pressed", "true");
  });

  it("reads each point aloud from the keyboard", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.srv01}` });
    const { plot, card } = await curve("Power draw");
    const readout = card.querySelector(".series-readout") as HTMLElement;
    expect(readout).toHaveAttribute("aria-live", "polite");
    expect(readout).toBeEmptyDOMElement();
    plot.focus();
    await userEvent.keyboard("{ArrowLeft}");
    expect(readout).toHaveTextContent(/: 405 W \(from 395 W to 415 W\)$/);
    await userEvent.keyboard("{Home}");
    expect(readout).toHaveTextContent(/: 400 W \(from 390 W to 410 W\)$/);
    await userEvent.keyboard("{ArrowRight}");
    expect(readout).toHaveTextContent(/: 410 W/);
    expect(card.querySelector(".series-cursor circle")).not.toBeNull();
    await userEvent.keyboard("{Escape}");
    expect(readout).toBeEmptyDOMElement();
    expect(card.querySelector(".series-cursor")).toBeNull();
  });

  it("follows the pointer to the nearest bucket", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.srv01}` });
    const { card } = await curve("Power draw");
    const svg = card.querySelector("svg") as SVGSVGElement;
    svg.getBoundingClientRect = () => ({ left: 0, top: 0, width: 1200, height: 144, right: 1200, bottom: 144, x: 0, y: 0, toJSON: () => ({}) });
    // 120 buckets on 1200 px: bucket 5 (the one after the gap) is around x = 55.
    fireEvent.pointerMove(svg, { clientX: 55 });
    expect(card.querySelector(".series-readout")).toHaveTextContent(/: 405 W/);
    fireEvent.pointerMove(svg, { clientX: 1 });
    expect(card.querySelector(".series-readout")).toHaveTextContent(/: 400 W/);
    fireEvent.pointerLeave(svg);
    expect(card.querySelector(".series-readout")).toBeEmptyDOMElement();
  });

  it("says so when a period has no reading, or more than one answer reads", async () => {
    mockApi({
      ...defaultRoutes(),
      [`GET /objects/${ID.srv01}/series`]: (c: Call) => {
        const s = seriesOf(ID.srv01, c.query);
        if (s.key === "inlet_temp_c") return { ...s, points: [] };
        if (s.key === "power_w") return { ...s, truncated: true };
        return s;
      },
    });
    renderApp({ at: `/objects/${ID.srv01}` });
    const inlet = (await screen.findByRole("heading", { name: "Inlet temperature", level: 3 })).closest("section") as HTMLElement;
    expect(await within(inlet).findByText("No reading in this period.")).toBeInTheDocument();
    const { card } = await curve("Power draw");
    expect(within(card).getByText(/Too many readings in this period/)).toBeInTheDocument();
  });

  it("shows a curve's error in its own card", async () => {
    mockApi({
      ...defaultRoutes(),
      [`GET /objects/${ID.srv01}/series`]: (c: Call) => (c.query.get("key") === "power_w" ? problem(400, "not-a-series", "device has no measurement") : seriesOf(ID.srv01, c.query)),
    });
    renderApp({ at: `/objects/${ID.srv01}` });
    const power = (await screen.findByRole("heading", { name: "Power draw", level: 3 })).closest("section") as HTMLElement;
    expect(await within(power).findByRole("alert")).toHaveTextContent("device has no measurement");
    await curve("Inlet temperature");
  });

  it("has no trends when the object has no measurement", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.rack}` });
    await screen.findByRole("heading", { name: "r12", level: 1 });
    expect(screen.queryByRole("heading", { name: "Trends" })).toBeNull();
    expect(api.calls.some((c) => c.path.endsWith("/series"))).toBe(false);
  });

  it("speaks French", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.srv01}`, locale: "fr" });
    expect(await screen.findByRole("heading", { name: "Tendances" })).toBeInTheDocument();
    expect(await screen.findByText("Dernier 405 W · plus bas 390 W · plus haut 430 W")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "7 jours" })).toBeInTheDocument();
  });
});

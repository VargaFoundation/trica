import { screen, within } from "@testing-library/react";
import type { ObjectView } from "../api/types";
import { ID, searchSw, srv01 } from "../test/fixtures";
import { expectNoA11yViolations } from "../test/axe";
import { defaultRoutes, mockApi, renderApp } from "../test/support";
import { ageDays, compareVersions, fleetNewest, isNameVersionList, isNewer, peerRecords, versionRecords, type VersionRecord } from "./versions";

describe("version records", () => {
  it("are read from the value: a non-empty list of objects with a name and a version", () => {
    expect(versionRecords([{ name: "BIOS", version: "2.10.5" }])).toHaveLength(1);
    expect(versionRecords([])).toBeUndefined();
    expect(versionRecords([{ name: "BIOS" }])).toBeUndefined();
    expect(versionRecords(["BIOS=2.10.5"])).toBeUndefined();
    expect(versionRecords({ name: "BIOS", version: "1" })).toBeUndefined();
    expect(isNameVersionList(["BIOS=2.10.5", "iLO 5=2.72 Aug 24 2022"])).toBe(true);
    expect(isNameVersionList(["web", "db"])).toBe(false);
    expect(isNameVersionList([])).toBe(false);
  });

  it("compare versions of one family, leaving the build date aside", () => {
    expect(compareVersions("2.10.5", "2.9.12")).toBe(1);
    expect(compareVersions("2.78 Dec 08 2020", "2.80 Jan 10 2022")).toBe(-1);
    expect(compareVersions("U30 v2.72 (07/09/2021)", "U30 v2.76 (02/09/2023)")).toBe(-1);
    expect(compareVersions("U30 v2.72 (07/09/2021)", "U30 v2.72 (07/09/2021)")).toBe(0);
    expect(compareVersions("2.10", "2.10.0")).toBe(0);
    expect(compareVersions("2.10", "2.10.1")).toBe(-1);
    // Two ROM lines are not two releases of one.
    expect(compareVersions("P89 v2.60 (05/21/2018)", "U30 v2.72 (07/09/2021)")).toBeUndefined();
    expect(compareVersions("HPD4", "HPD5")).toBe(-1);
    expect(compareVersions("HPD4", "HPG1")).toBeUndefined();
    expect(compareVersions("", "1.0")).toBeUndefined();
  });

  it("fall back to the release date when the versions cannot be compared", () => {
    const a = { name: "Disk", version: "HPD4", released: "2019-01-01" };
    expect(isNewer({ name: "Disk", version: "HPG1", released: "2021-01-01" }, a)).toBe(true);
    expect(isNewer({ name: "Disk", version: "HPG1" }, a)).toBe(false);
  });

  it("count the age in days from the release date", () => {
    const now = Date.UTC(2026, 9, 19, 12);
    expect(ageDays("2026-10-18", now)).toBe(1);
    expect(ageDays("2024-10-19", now)).toBe(730);
    expect(ageDays(undefined, now)).toBeUndefined();
    expect(ageDays("not a date", now)).toBeUndefined();
  });

  it("find the newest release of a component on the other devices of the model", () => {
    const bios = (version: string): VersionRecord => ({ name: "BIOS", version });
    const mine = bios("2.10.5");
    expect(fleetNewest(mine, [[bios("2.12.1")], [bios("2.12.1")], [bios("2.11.0")], [bios("2.9.0")]])).toEqual({ version: "2.12.1", released: undefined, count: 2 });
    expect(fleetNewest(mine, [[bios("2.10.5")], [bios("2.9.0")]])).toBeUndefined();
    expect(fleetNewest(mine, [[{ name: "iDRAC", version: "9.0" }]])).toBeUndefined();
  });

  it("compare only with devices of the same model", () => {
    const fw = [{ name: "BIOS", version: "1" }];
    const dev = (id: string, model: string | undefined): ObjectView => ({ ...srv01, id, attributes: { model, firmware: fw } });
    const me = dev("me", "R330");
    expect(peerRecords(me, [me, dev("a", "R330"), dev("b", "R640"), { ...dev("c", "R330"), attributes: { model: "R330" } }], "firmware")).toEqual([fw]);
    expect(peerRecords(dev("me", undefined), [dev("a", undefined)], "firmware")).toEqual([]);
  });
});

const YEAR = 365 * 86_400_000;
const isoDay = (ms: number) => new Date(ms).toISOString().slice(0, 10);
const firmware = [
  { name: "System ROM", version: "U30 v2.72 (07/09/2021)", released: isoDay(Date.now() - 3 * YEAR), location: "System Board", updateable: true },
  { name: "iLO 5", version: "2.80", released: isoDay(Date.now() - 100 * 86_400_000), location: "System Board" },
  { name: "Smart Array P408i-a SR Gen10", version: "2.62", location: "Slot 0" },
];
const server: ObjectView = {
  ...srv01,
  attributes: {
    ...srv01.attributes,
    model: "ProLiant DL380 Gen10",
    firmware,
    firmware_inventory: ["Backup System ROM=U30 v2.70 (01/01/2021)", "Smart Array P408i-a SR Gen10=2.62", "System ROM=U30 v2.72 (07/09/2021)", "iLO 5=2.80"],
  },
};
const peer = (id: string, fw: VersionRecord[], model = "ProLiant DL380 Gen10"): ObjectView => ({ ...srv01, id, display: id, attributes: { model, firmware: fw } });
const fleet = [
  server,
  peer("p1", [{ name: "System ROM", version: "U30 v2.76 (02/09/2023)" }, { name: "iLO 5", version: "2.80" }]),
  peer("p2", [{ name: "System ROM", version: "U30 v2.76 (02/09/2023)" }]),
  // Another model's newer ROM says nothing about this server.
  peer("p3", [{ name: "iLO 5", version: "3.10" }], "ProLiant DL360 Gen10"),
];

function routes(devices: ObjectView[] = fleet) {
  return { ...defaultRoutes(), [`GET /objects/${ID.srv01}`]: server, "GET /objects": { ...searchSw, items: devices, total: devices.length, limit: 1000 } };
}

async function table() {
  const card = (await screen.findByRole("heading", { name: "Firmware", level: 2 })).closest("section") as HTMLElement;
  return { card, rows: within(card).getAllByRole("row").slice(1) };
}

describe("the firmware of a device", () => {
  it("lists each component with its version, release date, age and location", async () => {
    mockApi(routes());
    renderApp({ at: `/objects/${ID.srv01}` });
    const { rows } = await table();
    expect(rows.map((r) => within(r).getByRole("rowheader").textContent)).toEqual(["iLO 5System Board", "Smart Array P408i-a SR Gen10Slot 0", "System ROMSystem Board"]);
    const rom = rows[2] as HTMLElement;
    expect(within(rom).getByText("U30 v2.72 (07/09/2021)")).toBeInTheDocument();
    expect(rom.querySelector("time")?.getAttribute("dateTime")).toBe(firmware[0]?.released);
    // Three years: flagged, in words for a screen reader.
    expect(rom).toHaveAttribute("data-stale");
    expect(within(rom).getByText(/3 years/)).toBeInTheDocument();
    expect(within(rom).getByText("(more than two years old)")).toBeInTheDocument();
    expect(within(rows[0] as HTMLElement).getByText("3 months")).toBeInTheDocument();
    expect(rows[0]).not.toHaveAttribute("data-stale");
    // No date: no age; no peer reports it: nothing to compare.
    expect(within(rows[1] as HTMLElement).getAllByText("None")).toHaveLength(3);
  });

  it("says which components another device of the same model runs newer", async () => {
    mockApi(routes());
    renderApp({ at: `/objects/${ID.srv01}` });
    const { card, rows } = await table();
    expect(await within(rows[2] as HTMLElement).findByText("U30 v2.76 (02/09/2023) on 2 devices")).toBeInTheDocument();
    expect(rows[2]).toHaveAttribute("data-behind");
    expect(within(rows[0] as HTMLElement).getByText("Latest")).toBeInTheDocument();
    expect(rows[0]).not.toHaveAttribute("data-behind");
    expect(within(card).getByText("3 components · 1 over two years old · 1 behind the fleet. Compared with 2 other devices of model ProLiant DL380 Gen10.")).toBeInTheDocument();
  });

  it("replaces the raw lists in the attribute cards", async () => {
    mockApi(routes());
    renderApp({ at: `/objects/${ID.srv01}` });
    await table();
    expect(document.querySelector('[data-attribute="firmware"]')).toBeNull();
    expect(document.querySelector('[data-attribute="firmware_inventory"]')).toBeNull();
    expect(screen.queryByText(/Backup System ROM/)).toBeNull();
    // The other attributes are still there.
    expect(document.querySelector('[data-attribute="model"]')).not.toBeNull();
  });

  it("shows no fleet comparison when the device is alone of its model", async () => {
    mockApi(routes([server]));
    renderApp({ at: `/objects/${ID.srv01}` });
    const { card, rows } = await table();
    expect(await within(card).findByText("3 components · 1 over two years old · 0 behind the fleet.")).toBeInTheDocument();
    expect(within(rows[0] as HTMLElement).queryByText("Latest")).toBeNull();
  });

  it("is not drawn for a device without firmware records, and keeps its lists", async () => {
    const api = mockApi({ ...defaultRoutes(), [`GET /objects/${ID.srv01}`]: { ...srv01, attributes: { ...srv01.attributes, tags: ["a=b"] } } });
    renderApp({ at: `/objects/${ID.srv01}` });
    await screen.findByRole("heading", { name: "srv-01", level: 1 });
    expect(screen.queryByRole("heading", { name: "Firmware" })).toBeNull();
    expect(document.querySelector('[data-attribute="tags"]')).not.toBeNull();
    expect(api.calls.some((c) => c.path === "/objects" && c.query.get("type") === "device")).toBe(false);
  });

  it.each(["light", "dark"] as const)("has no accessibility violation, in the %s theme", async (theme) => {
    mockApi(routes());
    renderApp({ at: `/objects/${ID.srv01}`, theme });
    const { rows } = await table();
    await within(rows[2] as HTMLElement).findByText(/on 2 devices/);
    await expectNoA11yViolations();
  }, 20_000); // axe takes about 1.5 s alone and several times that with the whole suite running in parallel

  it("speaks French", async () => {
    mockApi(routes());
    renderApp({ at: `/objects/${ID.srv01}`, locale: "fr" });
    const card = (await screen.findByRole("heading", { name: "Firmwares", level: 2 })).closest("section") as HTMLElement;
    expect(await within(card).findByText("U30 v2.76 (02/09/2023) sur 2 équipements")).toBeInTheDocument();
    expect(within(card).getByRole("columnheader", { name: "Publié le" })).toBeInTheDocument();
  });
});

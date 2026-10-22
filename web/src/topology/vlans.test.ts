import { describe, expect, it } from "vitest";
import { vlanLabels } from "./vlans";

const d = (...e: [string, string][]) => new Map(e);

describe("the label of a VLAN in the filter", () => {
  it("is its display when nothing more is known", () => {
    expect(vlanLabels(d(["a", "201"], ["b", "202"]))).toEqual(d(["a", "201"], ["b", "202"]));
  });

  it("adds the name when it says more than the VID", () => {
    const info = new Map([
      ["a", { name: "MGMT" }],
      ["b", { name: "202" }],
      ["c", { name: " " }],
    ]);
    expect(vlanLabels(d(["a", "201"], ["b", "202"], ["c", "203"]), info)).toEqual(d(["a", "201 MGMT"], ["b", "202"], ["c", "203"]));
  });

  it("names the domain only when two entries would read alike", () => {
    const info = new Map([
      ["a", { name: "MGMT", domain: "default" }],
      ["b", { name: "MGMT", domain: "lab" }],
      ["c", { name: "ADMIN", domain: "default" }],
    ]);
    expect(vlanLabels(d(["a", "201"], ["b", "201"], ["c", "202"]), info)).toEqual(
      d(["a", "201 MGMT (default)"], ["b", "201 MGMT (lab)"], ["c", "202 ADMIN"]),
    );
  });

  it("never gives two VLANs the same label, even without a domain to tell them apart", () => {
    const labels = [...vlanLabels(d(["aaaaaaaa-1", "20"], ["bbbbbbbb-2", "20"])).values()];
    expect(new Set(labels).size).toBe(2);
    const same = [...vlanLabels(d(["aaaaaaaa-1", "20"], ["bbbbbbbb-2", "20"]), new Map([["aaaaaaaa-1", { domain: "x" }], ["bbbbbbbb-2", { domain: "x" }]])).values()];
    expect(new Set(same).size).toBe(2);
  });
});

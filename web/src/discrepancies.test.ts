import { DISCREPANCY_KINDS, isKnownKind, kindLabel, locationConflictOf } from "./discrepancies";
import { discrepancyLocation, discrepancyLocationRack } from "./test/fixtures";

describe("locationConflictOf", () => {
  it("reads what the engine writes, for both reasons", () => {
    expect(locationConflictOf(discrepancyLocation.details)?.reason).toBe("sites");
    const rack = locationConflictOf(discrepancyLocationRack.details);
    expect(rack?.reason).toBe("rack");
    expect(rack?.rack_id).toBeDefined();
    expect(rack?.places[0]?.sources).toEqual(["user:dan"]);
  });

  it.each([
    ["nothing", undefined],
    ["a string", "sites"],
    ["an unknown reason", { reason: "other", sites: [], places: [] }],
    ["sites that are not strings", { reason: "sites", sites: [1], places: [] }],
    ["places that are not a list", { reason: "sites", sites: [], places: {} }],
    ["a place with no sources", { reason: "sites", sites: [], places: [{ place_id: "a", place_type: "room" }] }],
    ["a place that is not an object", { reason: "sites", sites: [], places: ["a"] }],
    ["a site id that is not a string", { reason: "sites", sites: [], places: [{ place_id: "a", place_type: "room", site_id: 3, sources: [] }] }],
    ["a rack id that is not a string", { reason: "rack", sites: [], places: [], rack_id: 3 }],
  ])("rejects %s", (_, details) => {
    expect(locationConflictOf(details)).toBeNull();
  });
});

describe("kinds", () => {
  it("knows the kinds the engine and the deducer raise", () => {
    expect(DISCREPANCY_KINDS).toContain("location_conflict");
    expect(isKnownKind("cable_conflict")).toBe(true);
    expect(isKnownKind("future_kind")).toBe(false);
  });

  it("shows a kind it does not know as the API names it", () => {
    const t = (k: string) => `T(${k})`;
    expect(kindLabel(t as never, "future_kind")).toBe("future_kind");
    expect(kindLabel(t as never, "value_mismatch")).toBe("T(discrepancies.kinds.value_mismatch)");
  });
});

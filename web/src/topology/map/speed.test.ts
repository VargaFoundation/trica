import { describe, expect, it } from "vitest";
import { BAND_WIDTH, bandWidth, SPEED_BANDS, speedBand } from "./speed";

describe("speed classes", () => {
  it.each([
    [undefined, "unknown"],
    [0, "unknown"],
    [-5, "unknown"],
    [10, "slow"],
    [100, "slow"],
    [999, "slow"],
    [1000, "1g"],
    [2000, "1g"],
    [2500, "5g"],
    [5000, "5g"],
    [10_000, "10g"],
    [24_999, "10g"],
    [25_000, "40g"],
    [40_000, "40g"],
    [100_000, "100g"],
    [400_000, "100g"],
  ])("%s Mb/s is %s", (mbps, band) => expect(speedBand(mbps)).toBe(band));

  it("lists the classes from the fastest, each wider than the next, the unknown one the thinnest", () => {
    const w = SPEED_BANDS.map((b) => BAND_WIDTH[b]);
    expect(SPEED_BANDS).toHaveLength(7);
    for (let i = 1; i < w.length; i++) expect(w[i - 1]).toBeGreaterThanOrEqual(w[i] as number);
    expect(bandWidth(100_000)).toBe(7);
    expect(bandWidth(undefined)).toBe(BAND_WIDTH.unknown);
  });
});

// The speed classes of a line: one colour token and one width each, from the
// capacity in Mb/s. The classes are the ones a network engineer reads a
// switch's ports by.

export type SpeedBand = "100g" | "40g" | "10g" | "5g" | "1g" | "slow" | "unknown";

/** From the fastest down; the legend lists them in this order. */
export const SPEED_BANDS: readonly SpeedBand[] = ["100g", "40g", "10g", "5g", "1g", "slow", "unknown"];

/** Line width of each class, px: it grows with the speed. */
export const BAND_WIDTH: Readonly<Record<SpeedBand, number>> = { "100g": 7, "40g": 5.6, "10g": 4.4, "5g": 3.4, "1g": 2.6, slow: 1.8, unknown: 1.5 };

/**
 * The class of a capacity: 100G and above, 25-40G, 10G, 2.5-5G, 1G, under 1G, and
 * unknown (no capacity declared). A value between two classes takes the lower one.
 */
export function speedBand(mbps: number | undefined): SpeedBand {
  if (mbps === undefined || !(mbps > 0)) return "unknown";
  if (mbps >= 100_000) return "100g";
  if (mbps >= 25_000) return "40g";
  if (mbps >= 10_000) return "10g";
  if (mbps >= 2_500) return "5g";
  if (mbps >= 1_000) return "1g";
  return "slow";
}

export const bandWidth = (mbps: number | undefined): number => BAND_WIDTH[speedBand(mbps)];

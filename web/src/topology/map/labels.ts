// Label de-collision: a label takes the first position that does not run into
// one already placed. Pure rectangles, tested without a DOM.

import type { Bounds } from "./iso";

/** Whether two rectangles touch or come closer than `pad` px. */
export function overlaps(a: Bounds, b: Bounds, pad = 2): boolean {
  return a.x0 - pad < b.x1 && b.x0 < a.x1 + pad && a.y0 - pad < b.y1 && b.y0 < a.y1 + pad;
}

/** The index of the first candidate that is clear of every taken rectangle, or -1. */
export function firstFree(taken: readonly Bounds[], candidates: readonly Bounds[], pad = 2): number {
  return candidates.findIndex((c) => !taken.some((t) => overlaps(c, t, pad)));
}

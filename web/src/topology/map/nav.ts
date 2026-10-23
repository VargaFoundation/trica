// Arrow-key navigation between the items of a scene: the arrow goes to the
// nearest item inside the 90-degree cone it points at, on screen.

import type { Pt } from "./iso";

export type Arrow = "ArrowUp" | "ArrowDown" | "ArrowLeft" | "ArrowRight";
export interface Spot {
  id: string;
  at: Pt;
}

export function nextSpot(spots: readonly Spot[], from: string, key: Arrow): string | undefined {
  const cur = spots.find((s) => s.id === from);
  if (!cur) return spots[0]?.id;
  let best: { id: string; dist: number } | undefined;
  for (const s of spots) {
    if (s.id === from) continue;
    const dx = s.at.x - cur.at.x;
    const dy = s.at.y - cur.at.y;
    const along = key === "ArrowRight" ? dx : key === "ArrowLeft" ? -dx : key === "ArrowDown" ? dy : -dy;
    const across = key === "ArrowRight" || key === "ArrowLeft" ? dy : dx;
    if (along <= 0 || Math.abs(across) > along) continue;
    const dist = Math.hypot(dx, dy);
    if (!best || dist < best.dist || (dist === best.dist && s.id < best.id)) best = { id: s.id, dist };
  }
  return best?.id;
}

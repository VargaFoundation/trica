// The recorded answers of GET /topology/map (testdata/map-api.json, regenerated
// by `TRICA_UPDATE_TOPOLOGY=1 go test ./internal/api -run RecordedForTheUI`),
// typed, with a helper to take a level with its live block changed.
import type { TopologyMap } from "../../../api/types";
import recorded from "./map-api.json";

export const MAPS = recorded as unknown as Record<"global" | "site" | "room" | "rack" | "unplaced", TopologyMap>;

/** A copy of a recorded level with the live overlay switched on or off. */
export function withLive(m: TopologyMap, enabled: boolean): TopologyMap {
  return { ...m, live: { ...m.live, enabled } };
}

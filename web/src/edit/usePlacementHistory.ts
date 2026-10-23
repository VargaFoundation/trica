import { useQuery } from "@tanstack/react-query";
import { api } from "../api/endpoints";
import type { PlacementRecord } from "../api/types";

/** The placement a device had before it was taken out (ADR 0053): where "Put back" starts from. */
export interface LastPlacement {
  rack: { id: string; display: string; absent: boolean };
  position?: number;
  face?: "front" | "rear";
  until: string;
  source: string;
}

/** The most recently closed placement, when the device has no current one. */
export function lastPlacement(items: readonly PlacementRecord[]): LastPlacement | undefined {
  if (items.some((r) => !r.until)) return undefined;
  const r = items[0];
  if (!r?.until) return undefined;
  return {
    rack: { id: r.rack.id, display: r.rack.display, absent: r.rack.status === "absent" },
    position: typeof r.position === "number" ? r.position : undefined,
    face: r.face === "front" || r.face === "rear" ? r.face : undefined,
    until: r.until,
    source: r.source,
  };
}

/** GET /objects/{id}/placement-history, reduced to what the device page needs. */
export function usePlacementHistory(deviceId: string, enabled: boolean) {
  return useQuery({
    queryKey: ["placement-history", deviceId],
    queryFn: async ({ signal }) => lastPlacement((await api.placementHistory(deviceId, { limit: 5 }, signal)).items),
    enabled,
    staleTime: 15_000,
  });
}

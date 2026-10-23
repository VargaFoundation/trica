import { useQuery } from "@tanstack/react-query";
import { api } from "../api/endpoints";

/** What the tray under the elevations says of a device taken out of the rack. */
export interface Removed {
  id: string;
  name: string;
  position?: number;
  face?: "front" | "rear";
  heightU?: number;
  absent: boolean;
  removedAt: string;
  removedBy: string;
}

/** The newest few devices taken out of a rack (GET /racks/{id}/removed). */
export const REMOVED_LIMIT = 20;

export function useRemoved(rackId: string) {
  return useQuery({
    queryKey: ["rack-removed", rackId],
    queryFn: async ({ signal }): Promise<Removed[]> => {
      const p = await api.rackRemoved(rackId, { limit: REMOVED_LIMIT }, signal);
      return p.items.map((it) => ({
        id: it.device.id,
        name: it.device.display,
        position: typeof it.position === "number" ? it.position : undefined,
        face: it.face === "front" || it.face === "rear" ? it.face : undefined,
        heightU: typeof it.u_height === "number" ? it.u_height : undefined,
        absent: it.status === "absent",
        removedAt: it.removed_at,
        removedBy: it.removed_by,
      }));
    },
    enabled: rackId !== "",
    staleTime: 30_000,
  });
}

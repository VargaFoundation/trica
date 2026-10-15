import { useQuery } from "@tanstack/react-query";
import { api } from "../api/endpoints";
import { FACINGS, type Facing, type FloorRack } from "./floor";
import type { Mounted } from "./layout";

const num = (v: unknown): number | undefined => (typeof v === "number" && Number.isFinite(v) ? v : undefined);
export const facingOf = (v: unknown): Facing | undefined => FACINGS.find((f) => f === v);

export interface RackContents {
  items: Mounted[];
  /** More devices are mounted than one page of the API returned. */
  truncated: boolean;
}

/** Everything the elevation needs, in one call (GET /racks/{id}/contents). */
export async function loadRackContents(rackId: string, signal?: AbortSignal): Promise<RackContents> {
  const p = await api.rackContents(rackId, { limit: 1000 }, signal);
  return {
    items: p.items.map(
      (it): Mounted => ({
        id: it.device.id,
        name: it.device.display,
        position: num(it.position),
        face: it.face === "rear" || it.face === "front" ? it.face : undefined,
        heightU: num(it.u_height),
        fullDepth: it.full_depth === true,
        typeName: it.model?.display,
      }),
    ),
    truncated: p.total > p.items.length,
  };
}

export function useRackContents(rackId: string) {
  return useQuery({ queryKey: ["rack-contents", rackId], queryFn: ({ signal }) => loadRackContents(rackId, signal), staleTime: 30_000 });
}

export interface RoomContents {
  racks: (FloorRack & { uHeight?: number })[];
  truncated: boolean;
}

/** Racks standing in a room, directly or through one of its rows. */
export async function loadRoomContents(roomId: string, signal?: AbortSignal): Promise<RoomContents> {
  const [racks, rows] = await Promise.all([
    api.objects({ type: "rack", limit: 1000 }, signal),
    api.objects({ type: "row", limit: 1000 }, signal),
  ]);
  const rowsOfRoom = new Set(rows.items.filter((r) => r.parent_id === roomId).map((r) => r.id));
  const mine = racks.items.filter((r) => r.parent_id === roomId || (r.parent_id != null && rowsOfRoom.has(r.parent_id)));
  return {
    racks: mine.map((r) => ({
      id: r.id,
      name: r.display,
      x: num(r.attributes?.floor_x),
      y: num(r.attributes?.floor_y),
      facing: facingOf(r.attributes?.facing),
      uHeight: num(r.attributes?.u_height),
    })),
    truncated: racks.total > racks.items.length || rows.total > rows.items.length,
  };
}

export function useRoomContents(roomId: string) {
  return useQuery({ queryKey: ["room-contents", roomId], queryFn: ({ signal }) => loadRoomContents(roomId, signal), staleTime: 30_000 });
}

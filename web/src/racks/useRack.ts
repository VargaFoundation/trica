import { useQuery } from "@tanstack/react-query";
import { api } from "../api/endpoints";
import type { ObjectView } from "../api/types";
import { FACINGS, type Facing, type FloorRack } from "./floor";
import type { Mounted } from "./layout";

const num = (v: unknown): number | undefined => (typeof v === "number" && Number.isFinite(v) ? v : undefined);
export const facingOf = (v: unknown): Facing | undefined => FACINGS.find((f) => f === v);

export interface RackContents {
  items: Mounted[];
  /** More devices are mounted than one page of the API returned. */
  truncated: boolean;
}

/**
 * Everything the elevation needs, from the public API only: the devices
 * mounted in the rack (relations of the rack), the model of each (relation
 * instance_of of the device) and the height of each model (the device_type
 * list). There is no endpoint that returns this in one call, hence one request
 * per device; see the finding linked from docs/reference/ui.md.
 */
export async function loadRackContents(rackId: string, signal?: AbortSignal): Promise<RackContents> {
  const [mounts, types] = await Promise.all([
    api.relations(rackId, { limit: 1000 }, signal),
    api.objects({ type: "device_type", limit: 1000 }, signal),
  ]);
  const byType = new Map<string, ObjectView>(types.items.map((t) => [t.id, t]));
  const holds = mounts.items.filter((r) => r.type === "mounted_in" && r.direction === "in");
  const items = await Promise.all(
    holds.map(async (r): Promise<Mounted> => {
      const model = await modelOf(r.other.id, signal);
      const t = model ? byType.get(model) : undefined;
      const face = r.attributes?.face;
      return {
        id: r.other.id,
        name: r.other.display,
        position: num(r.attributes?.position),
        face: face === "rear" || face === "front" ? face : undefined,
        heightU: num(t?.attributes?.u_height),
        fullDepth: t?.attributes?.full_depth === true,
        typeName: t?.display,
      };
    }),
  );
  return { items, truncated: mounts.total > mounts.items.length };
}

async function modelOf(deviceId: string, signal?: AbortSignal): Promise<string | undefined> {
  const rels = await api.relations(deviceId, { limit: 200 }, signal);
  return rels.items.find((r) => r.type === "instance_of" && r.direction === "out")?.other.id;
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

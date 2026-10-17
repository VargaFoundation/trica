import { useQuery } from "@tanstack/react-query";
import { api } from "../api/endpoints";
import type { ObjectView } from "../api/types";
import { layoutFloor, tileOf } from "../racks/floor";
import { useRoomContents } from "../racks/useRack";
import type { FloorContext } from "./ObjectForm";

/** The room a rack stands in: its parent, or the room of its row. */
export function useRoomOf(rack: ObjectView | undefined): string | undefined {
  const parent = useQuery({
    queryKey: ["object", rack?.parent_id],
    queryFn: ({ signal }) => api.object(rack?.parent_id as string, signal),
    enabled: !!rack?.parent_id,
    staleTime: 5 * 60_000,
  });
  const p = parent.data;
  if (!p) return undefined;
  if (p.type === "room") return p.id;
  if (p.type === "row") return p.parent_id ?? undefined;
  return undefined;
}

/** Where racks stand in a room and how large it is, for the tile check of a rack form. */
export function useFloorContext(roomId: string | undefined): FloorContext | undefined {
  const room = useQuery({
    queryKey: ["object", roomId],
    queryFn: ({ signal }) => api.object(roomId as string, signal),
    enabled: !!roomId,
    staleTime: 5 * 60_000,
  });
  const contents = useRoomContents(roomId);
  if (!room.data || !contents.data) return undefined;
  const w = room.data.attributes.width_mm;
  const d = room.data.attributes.depth_mm;
  const plan = layoutFloor(typeof w === "number" ? w : undefined, typeof d === "number" ? d : undefined, contents.data.racks, tileOf(room.data.attributes.tile_mm));
  return { racks: contents.data.racks, size: plan.sizeFromRacks ? undefined : { cols: plan.cols, rows: plan.rows } };
}

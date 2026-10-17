import { useQuery } from "@tanstack/react-query";
import { api } from "../api/endpoints";
import type { RelationView } from "../api/types";

const num = (v: unknown): number | undefined => (typeof v === "number" && Number.isFinite(v) ? v : undefined);

/** One rack a device is mounted in now, with who said so. */
export interface CurrentMount {
  rackId: string;
  rackName: string;
  position?: number;
  face?: string;
  /** The source whose claim is shown (the winner of the priority rules). */
  source: string;
}

export interface DevicePlacement {
  mounts: CurrentMount[];
  /** Height from the device's model, when it has one that says. */
  modelHeight?: number;
  fullDepth: boolean;
  modelName?: string;
  /** The device's own u_height (declared for a device without a known model). */
  ownHeight?: number;
}

/** Where a device is mounted and what its model says, from its relations and its model. */
export async function loadPlacement(deviceId: string, signal?: AbortSignal): Promise<DevicePlacement> {
  const [rels, device] = await Promise.all([api.relations(deviceId, { limit: 200 }, signal), api.object(deviceId, signal)]);
  const out = (r: RelationView, type: string) => r.type === type && r.direction === "out";
  const mounts = rels.items
    .filter((r) => out(r, "mounted_in"))
    .map(
      (r): CurrentMount => ({
        rackId: r.other.id,
        rackName: r.other.display,
        position: num(r.attributes?.position),
        face: typeof r.attributes?.face === "string" ? r.attributes.face : undefined,
        source: r.source,
      }),
    );
  const modelRel = rels.items.find((r) => out(r, "instance_of"));
  let modelHeight: number | undefined;
  let fullDepth = false;
  if (modelRel) {
    try {
      const model = await api.object(modelRel.other.id, signal);
      const h = num(model.attributes.u_height);
      modelHeight = h !== undefined && h > 0 ? h : undefined;
      fullDepth = model.attributes.full_depth === true;
    } catch {
      /* the model cannot be read: its height stays unknown and the person may say it */
    }
  }
  const own = num(device.attributes.u_height);
  return { mounts, modelHeight, fullDepth, modelName: modelRel?.other.display, ownHeight: own !== undefined && own > 0 ? own : undefined };
}

export function usePlacement(deviceId: string | undefined) {
  return useQuery({
    queryKey: ["placement", deviceId],
    queryFn: ({ signal }) => loadPlacement(deviceId as string, signal),
    enabled: !!deviceId,
    staleTime: 15_000,
  });
}

import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api } from "../../api/endpoints";
import { LIVE_REFRESH_MS } from "../useLiveTopology";
import type { Level } from "./url";

/**
 * One level of the map. It is asked again every 30 s only while the server
 * says its live overlay is on; with it off the map is the inventory alone and
 * nothing polls. The previous level stays on screen while the next one loads.
 */
export function useTopologyMap(level: Level, id: string | undefined) {
  return useQuery({
    queryKey: ["topology-map", level, id ?? null],
    queryFn: ({ signal }) => api.topologyMap({ level, id }, signal),
    placeholderData: keepPreviousData,
    refetchInterval: (q) => (q.state.data?.live.enabled ? LIVE_REFRESH_MS : false),
    refetchOnWindowFocus: (q) => q.state.data?.live.enabled === true,
    retry: false,
    staleTime: LIVE_REFRESH_MS / 2,
  });
}

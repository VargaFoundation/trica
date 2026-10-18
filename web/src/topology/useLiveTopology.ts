import { useQuery } from "@tanstack/react-query";
import { api } from "../api/endpoints";
import type { LiveTopology } from "../api/types";

/** How often the page asks again; the server serves one cached set per period (30 s by default). */
export const LIVE_REFRESH_MS = 30_000;

/** What the page uses when the server has no live overlay, or cannot say. */
export const LIVE_OFF: LiveTopology = { enabled: false, stale: false, refresh_seconds: 0, links: {}, sources: [] };

/** The polling period: 30 s while the server says the overlay is on, never otherwise. */
export function liveInterval(data: LiveTopology | undefined): number | false {
  return data?.enabled ? LIVE_REFRESH_MS : false;
}

/**
 * The live overlay, apart from the inventory's topology (useTopology): its own
 * query, asked again every 30 s while the server says it is enabled. When it
 * is not (no live block, or an older server without the endpoint), the page
 * draws the inventory alone and asks nothing more.
 */
export function useLiveTopology() {
  return useQuery({
    queryKey: ["topology-live"],
    queryFn: async ({ signal }) => {
      try {
        return await api.liveTopology(signal);
      } catch (e) {
        // An older server answers 404; the drawing does not depend on the overlay.
        if (signal?.aborted) throw e;
        return LIVE_OFF;
      }
    },
    refetchInterval: (q) => liveInterval(q.state.data),
    refetchOnWindowFocus: (q) => q.state.data?.enabled === true,
    retry: false,
    staleTime: LIVE_REFRESH_MS / 2,
  });
}

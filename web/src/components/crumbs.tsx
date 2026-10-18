import { createContext, useContext, useEffect } from "react";
import { useLocation } from "react-router";
import type { Crumb } from "./ui/Breadcrumbs";

type Setter = (path: string, trail: Crumb[] | null) => void;

export const CrumbsContext = createContext<Setter>(() => {});

/**
 * Sets the breadcrumb trail of the current page, shown in the top bar. A page
 * that sets none gets its section's. The trail is keyed by the address, so a
 * trail never outlives its page.
 */
export function useCrumbs(trail: Crumb[] | undefined): void {
  const set = useContext(CrumbsContext);
  const { pathname } = useLocation();
  const key = trail ? JSON.stringify(trail) : "";
  useEffect(() => {
    if (!key) return;
    set(pathname, JSON.parse(key) as Crumb[]);
    return () => set(pathname, null);
  }, [key, pathname, set]);
}

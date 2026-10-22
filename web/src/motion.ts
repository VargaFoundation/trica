/**
 * True when the visitor asked the system for less motion. Kept apart from the
 * graph module (which pulls in cytoscape) so that the map can use it without
 * loading the graph engine.
 */
export function prefersReducedMotion(win: Window | undefined = typeof window === "undefined" ? undefined : window): boolean {
  return win?.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
}

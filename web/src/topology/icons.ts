// The device icons of the topology canvas. Cytoscape draws a node's picture
// from a URL, and the page's policy (img-src 'self') refuses data: URLs, so
// the application's own icons (components/ui/icons.tsx) are also served as
// files: public/topology-icons/<theme>/<icon>.svg, one set per colour scheme,
// stroked with the graph's ink (--trica-graph-ink). icons.test.ts writes them
// from the same paths and fails when a file drifts.
import type { IconName } from "../components/ui/icons";

export type IconTheme = "dark" | "light";

/** The icons a device can have on the canvas: one per device role, and the generic one. */
export const TOPOLOGY_ICONS = [
  "server",
  "switch",
  "router",
  "firewall",
  "storage",
  "nas",
  "pdu",
  "ups",
  "patch_panel",
  "sensor",
  "kvm",
  "device",
] as const satisfies readonly IconName[];

export function iconUrl(theme: IconTheme, icon: string): string {
  return `${import.meta.env.BASE_URL}topology-icons/${theme}/${icon}.svg`;
}

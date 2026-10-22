// The address of the topology page: which view, and for the map which level,
// which object it is focused on, which item is selected and how the scene is
// turned. Pure: the page reads and writes it, the tests exercise it alone.

export const LEVELS = ["global", "site", "room", "rack", "unplaced"] as const;
export type Level = (typeof LEVELS)[number];
export type View = "map" | "network";

export interface MapAddress {
  view: View;
  level: Level;
  /** The site, room or rack the level is focused on; absent for global and unplaced. */
  id?: string;
  /** The selected node or edge id. */
  sel?: string;
  /** Quarter turns of the scene, 0 to 3. */
  rot: 0 | 1 | 2 | 3;
  /** The address held something the map could not use: it fell back to the global level. */
  invalid: boolean;
}

/** Parameters of the network view that predate the tabs: they keep opening it. */
export const LEGACY_NETWORK_PARAMS = ["site", "rack", "vlan", "classes"] as const;

const needsId = (l: Level) => l === "site" || l === "room" || l === "rack";
const ID = /^[^\s&=#?/]{1,200}$/;

export function parseAddress(p: URLSearchParams): MapAddress {
  const view = p.get("view");
  const legacy = LEGACY_NETWORK_PARAMS.some((k) => p.has(k));
  const v: View = view === "network" || (view === null && legacy) ? "network" : "map";
  const lv = p.get("level");
  const id = p.get("id") ?? undefined;
  const sel = p.get("sel") ?? undefined;
  const r = Number(p.get("rot"));
  const rot = (Number.isInteger(r) && r >= 0 && r <= 3 ? r : 0) as MapAddress["rot"];
  let level: Level = "global";
  let invalid = false;
  if (lv !== null) {
    if ((LEVELS as readonly string[]).includes(lv)) level = lv as Level;
    else invalid = true;
  }
  if (!invalid && needsId(level) && (id === undefined || !ID.test(id))) invalid = true;
  if (invalid) return { view: v, level: "global", sel: undefined, rot, invalid: true };
  return {
    view: v,
    level,
    id: needsId(level) ? id : undefined,
    sel: sel !== undefined && ID.test(sel) ? sel : undefined,
    rot,
    invalid: false,
  };
}

/** The query string (without "?") of an address; defaults are left out. */
export function mapSearch(a: Pick<MapAddress, "level" | "id" | "sel" | "rot">): string {
  const p = new URLSearchParams();
  if (a.level !== "global") p.set("level", a.level);
  if (a.id && needsId(a.level)) p.set("id", a.id);
  if (a.sel) p.set("sel", a.sel);
  if (a.rot) p.set("rot", String(a.rot));
  return p.toString();
}

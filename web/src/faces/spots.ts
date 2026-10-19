// The places of a device's parts on its faces (GET /objects/{id}/faces, ADR
// 0047): which parts sit in each spot, and the state a spot is drawn in. The
// server decides where a part goes; this module only groups and colours. Kept
// apart from React so that it is tested on values.
import type { DeviceFace, FacePart, FaceSpot, ObjectView } from "../api/types";
import { healthOf, SEVERITY, type Health } from "../inventory/health";

/**
 * What a spot shows: the worst health of its parts, else, for a port, whether
 * its link is up; empty when no part the reader may see is reported there.
 */
export type SpotState = Exclude<Health, "absent"> | "up" | "down" | "empty";

export const SPOT_STATES: readonly SpotState[] = ["critical", "warning", "ok", "up", "down", "unknown", "empty"];

export interface PlacedSpot {
  /** Unique on the page: face, kind and name. */
  key: string;
  face: string;
  spot: FaceSpot;
  parts: ObjectView[];
  state: SpotState;
}

const spotKey = (face: string, kind: string, name: string) => `${face}\u0000${kind}\u0000${name}`;

/** The state of one part: its health when it reports one, else its link state. */
export function partState(part: ObjectView): SpotState {
  const h = healthOf(part);
  if (h !== "unknown" && h !== "absent") return h;
  const oper = part.attributes.oper_status;
  if (oper === "up") return "up";
  if (typeof oper === "string" && oper !== "") return "down";
  return "unknown";
}

// Worst first, as the health pills order: a failed part in a spot shared with
// a healthy one (a breakout port) colours the spot.
const RANK: Record<SpotState, number> = { critical: SEVERITY.critical, warning: SEVERITY.warning, down: 2, unknown: 3, up: 4, ok: 4, empty: 5 };

// A part that says nothing of its state (the OS's name of a disk the BMC
// reports healthy) does not grey out a spot another part speaks for.
export function worst(states: SpotState[]): SpotState {
  const known = states.filter((s) => s !== "unknown");
  if (known.length === 0) return states.length > 0 ? "unknown" : "empty";
  return known.reduce<SpotState>((w, s) => (RANK[s] < RANK[w] ? s : w), "empty");
}

/**
 * Every spot of a face with the parts placed in it, in reading order for the
 * keyboard: kind by kind, then by name as a person counts (Ethernet2 before
 * Ethernet10, bay 1:8 before 2:1).
 */
export function placeSpots(face: DeviceFace, parts: FacePart[], locale: string): PlacedSpot[] {
  const by = new Map<string, ObjectView[]>();
  for (const p of parts) {
    if (p.face !== face.face || !p.spot) continue;
    const k = spotKey(face.face, p.kind, p.spot);
    by.set(k, [...(by.get(k) ?? []), p.part]);
  }
  const order = ["bay", "psu", "fan", "port", "slot"];
  const collator = new Intl.Collator(locale, { numeric: true });
  return face.spots
    .map((spot) => {
      const key = spotKey(face.face, spot.kind, spot.name);
      const placed = by.get(key) ?? [];
      return { key, face: face.face, spot, parts: placed, state: worst(placed.map(partState)) };
    })
    .sort((a, b) => order.indexOf(a.spot.kind) - order.indexOf(b.spot.kind) || collator.compare(a.spot.name, b.spot.name));
}

/** The parts the server could not place: their names do not say where they sit. */
export const unplaced = (parts: FacePart[]): FacePart[] => parts.filter((p) => !p.spot);

/** Why a part is not placed, as the server says it (FacePart.unplaced). */
export type UnplacedWhy = "os" | "no_spot" | "unread";
const WHY_ORDER: readonly UnplacedWhy[] = ["os", "no_spot", "unread"];
const TYPE_ORDER = ["disk", "psu", "fan", "interface"];

export interface UnplacedGroup {
  why: UnplacedWhy;
  parts: FacePart[];
}

/**
 * The unplaced parts grouped by why they are not placed (the system's names
 * first: the disks only the OS knows), each group disks first, then supplies,
 * fans and ports, by name as a person counts. A server older than the reason
 * says nothing: its parts are "unread".
 */
export function groupUnplaced(parts: FacePart[], locale: string): UnplacedGroup[] {
  const collator = new Intl.Collator(locale, { numeric: true });
  const rank = (t: string) => {
    const i = TYPE_ORDER.indexOf(t);
    return i < 0 ? TYPE_ORDER.length : i;
  };
  const why = (p: FacePart): UnplacedWhy => (WHY_ORDER.includes(p.unplaced as UnplacedWhy) ? (p.unplaced as UnplacedWhy) : "unread");
  return WHY_ORDER.map((w) => ({
    why: w,
    parts: unplaced(parts)
      .filter((p) => why(p) === w)
      .sort((a, b) => rank(a.part.type) - rank(b.part.type) || collator.compare(a.part.display, b.part.display)),
  })).filter((g) => g.parts.length > 0);
}

/** The attributes worth reading on a part, by type, in the order shown. */
export const PART_FACTS: Record<string, readonly string[]> = {
  disk: ["model", "media", "capacity_bytes", "wear_pct", "temperature_c", "serial"],
  psu: ["model", "capacity_w", "input_w", "feed", "serial"],
  fan: ["speed_rpm", "speed_pct", "airflow"],
  interface: ["description", "speed_mbps", "oper_status", "admin_status", "mac"],
};

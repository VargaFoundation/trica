// The kinds of discrepancy the inventory raises (docs/reference/discrepancies.md),
// and the shape of the details a location_conflict carries (ADR 0061).
import type { I18n, MessageKey } from "./i18n";

export const DISCREPANCY_KINDS = [
  "value_mismatch",
  "unresolved_neighbor",
  "unresolved_reference",
  "cable_not_observed",
  "cable_not_declared",
  "cable_conflict",
  "location_conflict",
] as const;

export type DiscrepancyKind = (typeof DISCREPANCY_KINDS)[number];

export function isKnownKind(k: string): k is DiscrepancyKind {
  return (DISCREPANCY_KINDS as readonly string[]).includes(k);
}

/** The translated name of a kind; a kind this build does not know is shown as the API names it. */
export function kindLabel(t: I18n["t"], kind: string): string {
  if (!isKnownKind(kind)) return kind;
  const key: MessageKey = `discrepancies.kinds.${kind}`;
  return t(key);
}

/** One place of a location_conflict: ids and sources only, never a name. */
export interface ConflictPlace {
  place_id: string;
  place_type: string;
  site_id?: string;
  sources: string[];
}

/** One mounting of a "racks" conflict: ids and sources only. */
export interface ConflictMount {
  rack_id: string;
  site_id?: string;
  sources: string[];
}

export interface LocationConflictDetails {
  reason: "sites" | "rack" | "racks";
  sites: string[];
  places: ConflictPlace[];
  rack_id?: string;
  rack_site_id?: string;
  racks?: ConflictMount[];
}

const isStrings = (v: unknown): v is string[] => Array.isArray(v) && v.every((x) => typeof x === "string");

/** The details of a location_conflict, or null when they are not the shape the engine writes. */
export function locationConflictOf(details: unknown): LocationConflictDetails | null {
  if (typeof details !== "object" || details === null) return null;
  const d = details as Record<string, unknown>;
  if (d.reason !== "sites" && d.reason !== "rack" && d.reason !== "racks") return null;
  if (!isStrings(d.sites) || !Array.isArray(d.places)) return null;
  const places: ConflictPlace[] = [];
  for (const p of d.places) {
    if (typeof p !== "object" || p === null) return null;
    const q = p as Record<string, unknown>;
    if (typeof q.place_id !== "string" || typeof q.place_type !== "string" || !isStrings(q.sources)) return null;
    if (q.site_id !== undefined && typeof q.site_id !== "string") return null;
    places.push({ place_id: q.place_id, place_type: q.place_type, site_id: q.site_id, sources: q.sources });
  }
  if (d.rack_id !== undefined && typeof d.rack_id !== "string") return null;
  if (d.rack_site_id !== undefined && typeof d.rack_site_id !== "string") return null;
  let racks: ConflictMount[] | undefined;
  if (d.racks !== undefined || d.reason === "racks") {
    if (!Array.isArray(d.racks)) return null;
    racks = [];
    for (const m of d.racks) {
      if (typeof m !== "object" || m === null) return null;
      const q = m as Record<string, unknown>;
      if (typeof q.rack_id !== "string" || !isStrings(q.sources)) return null;
      if (q.site_id !== undefined && typeof q.site_id !== "string") return null;
      racks.push({ rack_id: q.rack_id, site_id: q.site_id, sources: q.sources });
    }
  }
  return { reason: d.reason, sites: d.sites, places, rack_id: d.rack_id, rack_site_id: d.rack_site_id, racks };
}

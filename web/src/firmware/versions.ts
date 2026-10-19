// Version records: a json attribute holding a list of {name, version,
// released?, location?, software_id?, updateable?} — the firmware a BMC
// reports (ADR 0046). The shape is read from the value, not from the
// attribute's name, so that any collector giving the same records gets the
// same table. Kept apart from React so that it is tested on values.
import type { ObjectView } from "../api/types";

export interface VersionRecord {
  name: string;
  version: string;
  /** YYYY-MM-DD, when the source or the version string gives it. */
  released?: string;
  location?: string;
  software_id?: string;
  updateable?: boolean;
}

const isRecord = (v: unknown): v is VersionRecord =>
  typeof v === "object" && v !== null && typeof (v as VersionRecord).name === "string" && typeof (v as VersionRecord).version === "string";

/** The value as version records, or undefined when it is not a non-empty list of them. */
export function versionRecords(value: unknown): VersionRecord[] | undefined {
  return Array.isArray(value) && value.length > 0 && value.every(isRecord) ? value : undefined;
}

/** A list of "name=version" strings: the searchable form of a version table, redundant beside it. */
export const isNameVersionList = (value: unknown): boolean =>
  Array.isArray(value) && value.length > 0 && value.every((v) => typeof v === "string" && /^[^=]+=.+$/.test(v));

// HPE writes the build date into the version; it is not part of the number.
const DATES = /\(\d{2}\/\d{2}\/\d{4}\)|\b(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) \d{1,2} \d{4}\b/g;

/**
 * Compares two version strings: negative when a is older, positive when newer,
 * 0 when equal, undefined when they are not of the same family ("P89 v2.60"
 * and "U30 v2.72" are two ROM lines, not two releases of one).
 */
export function compareVersions(a: string, b: string): number | undefined {
  const tokens = (v: string) => v.replace(DATES, " ").toLowerCase().match(/\d+|[a-z]+/g) ?? [];
  const ta = tokens(a);
  const tb = tokens(b);
  if (ta.length === 0 || tb.length === 0) return undefined;
  for (let i = 0; i < Math.max(ta.length, tb.length); i++) {
    const x = ta[i];
    const y = tb[i];
    if (x === undefined || y === undefined) {
      // 2.10 and 2.10.0 are one version; 2.10 and 2.10.1 are not.
      const rest = (x === undefined ? tb : ta).slice(i);
      if (rest.every((r) => r === "0" || /^0+$/.test(r))) return 0;
      return rest.every((r) => /^\d+$/.test(r)) ? (x === undefined ? -1 : 1) : undefined;
    }
    const nx = /^\d+$/.test(x);
    const ny = /^\d+$/.test(y);
    if (nx && ny) {
      const d = Number(x) - Number(y);
      if (d !== 0) return Math.sign(d);
    } else if (nx !== ny || x !== y) return undefined;
  }
  return 0;
}

/** Whether b is a later release of a's component: by version, else by release date. */
export function isNewer(b: VersionRecord, a: VersionRecord): boolean {
  const c = compareVersions(b.version, a.version);
  if (c !== undefined) return c > 0;
  return !!a.released && !!b.released && b.released > a.released;
}

/** Days from a YYYY-MM-DD date to now, or undefined without a date. */
export function ageDays(released: string | undefined, now: number): number | undefined {
  if (!released) return undefined;
  const t = Date.parse(`${released}T00:00:00Z`);
  return Number.isNaN(t) ? undefined : Math.max(0, Math.floor((now - t) / 86_400_000));
}

export interface FleetNewest {
  version: string;
  released?: string;
  /** How many other devices of the model run it. */
  count: number;
}

/**
 * The newest release of a component among the other devices of the same
 * model, when it is newer than the one given; undefined when no peer runs a
 * newer one (or no peer reports the component).
 */
export function fleetNewest(current: VersionRecord, peers: VersionRecord[][]): FleetNewest | undefined {
  let best: VersionRecord | undefined;
  for (const records of peers) {
    for (const r of records) {
      if (r.name === current.name && isNewer(r, current) && (!best || isNewer(r, best))) best = r;
    }
  }
  if (!best) return undefined;
  const top = best;
  const count = peers.filter((records) => records.some((r) => r.name === top.name && r.version === top.version)).length;
  return { version: top.version, released: top.released, count };
}

/** Whether any peer reports the component at all. */
export const fleetHas = (name: string, peers: VersionRecord[][]): boolean => peers.some((records) => records.some((r) => r.name === name));

/** The version records of the other devices of obj's model, attribute by attribute. */
export function peerRecords(obj: ObjectView, devices: ObjectView[], attribute: string): VersionRecord[][] {
  const model = obj.attributes.model;
  if (typeof model !== "string" || model === "") return [];
  return devices
    .filter((d) => d.id !== obj.id && d.attributes.model === model)
    .map((d) => versionRecords(d.attributes[attribute]))
    .filter((r): r is VersionRecord[] => r !== undefined);
}

import { useQuery } from "@tanstack/react-query";
import { useId, useMemo, useState } from "react";
import { Link } from "react-router";
import { api } from "../api/endpoints";
import type { ObjectView, RelationLink } from "../api/types";
import { Async } from "../components/Async";
import { Alert, EmptyState, HealthPill, KindIcon, Pill, Stat, Stats, useKindLabel } from "../components/ui";
import { useI18n, type MessageKey } from "../i18n";
import { useDurationWords } from "../components/Quantity";
import { formatQuantity } from "./format";
import { HEALTH_TONE, HEALTHS, healthOf, num, SEVERITY, type Health } from "./health";
import type { Kind } from "./kind";

/** The most devices one page draws; the API's own page limit. */
export const FLEET_LIMIT = 1000;

export interface Mount {
  rackId: string;
  rack: string;
  position?: number;
  face?: string;
}

/** A site, building or room a person placed a device in (located_in, ADR 0058). */
export interface Place {
  id: string;
  name: string;
}

export interface Tile {
  obj: ObjectView;
  kind: Kind;
  kindLabel: string;
  health: Health;
  mount?: Mount;
  /** Where the device stands when it is in no rack. */
  place?: Place;
}

export interface Group {
  key: string;
  title: string;
  rackId?: string;
  placeId?: string;
  tiles: Tile[];
}

/** Where each device not in a rack stands, from the located_in relations (the first place said, when sources disagree). */
export function placesOf(links: RelationLink[]): Map<string, Place> {
  const out = new Map<string, Place>();
  for (const l of links) {
    if (l.type !== "located_in" || out.has(l.from_id)) continue;
    out.set(l.from_id, { id: l.to_id, name: l.to.display });
  }
  return out;
}

/** Where each device is mounted, from the mounted_in relations. */
export function mountsOf(links: RelationLink[]): Map<string, Mount> {
  const out = new Map<string, Mount>();
  for (const l of links) {
    if (l.type !== "mounted_in") continue;
    const a = l.attributes ?? {};
    out.set(l.from_id, { rackId: l.to_id, rack: l.to.display, position: num(a.position), face: typeof a.face === "string" ? a.face : undefined });
  }
  return out;
}

/** Health counts, worst first. */
export function countHealth(tiles: Tile[]): Record<Health, number> {
  const c = { ok: 0, warning: 0, critical: 0, unknown: 0, absent: 0 };
  for (const t of tiles) c[t.health]++;
  return c;
}

const byTile = (a: Tile, b: Tile) =>
  SEVERITY[a.health] - SEVERITY[b.health] || a.obj.display.localeCompare(b.obj.display);

/**
 * Tiles grouped by rack (top of the rack first, as one stands in front of it),
 * then by the place of the devices in no rack, then the devices that are
 * nowhere; or by kind (the groups holding the worst health first).
 */
export function groupTiles(tiles: Tile[], by: "rack" | "kind", notRacked: string): Group[] {
  const groups = new Map<string, Group>();
  for (const tile of tiles) {
    // A rack decides where a device is: a place counts only for a device in no rack.
    const place = tile.mount ? undefined : tile.place;
    const key = by === "rack" ? (tile.mount ? tile.mount.rackId : place ? `place:${place.id}` : "") : tile.kind.key;
    let g = groups.get(key);
    if (!g) {
      g =
        by === "rack"
          ? { key, title: tile.mount?.rack ?? place?.name ?? notRacked, rackId: tile.mount?.rackId, placeId: place?.id, tiles: [] }
          : { key, title: tile.kindLabel, tiles: [] };
      groups.set(key, g);
    }
    g.tiles.push(tile);
  }
  const list = [...groups.values()];
  if (by === "rack") {
    for (const g of list)
      g.tiles.sort((a, b) => (b.mount?.position ?? -1) - (a.mount?.position ?? -1) || a.obj.display.localeCompare(b.obj.display));
    const rank = (g: Group) => (g.rackId ? 0 : g.placeId ? 1 : 2);
    return list.sort((a, b) => rank(a) - rank(b) || a.title.localeCompare(b.title, undefined, { numeric: true }));
  }
  for (const g of list) g.tiles.sort(byTile);
  const worst = (g: Group) => Math.min(...g.tiles.map((t) => SEVERITY[t.health]));
  return list.sort((a, b) => worst(a) - worst(b) || a.title.localeCompare(b.title));
}

function HealthCounts({ counts }: { counts: Record<Health, number> }) {
  const { t, number } = useI18n();
  return (
    <span className="object-head-pills" role="group" aria-label={t("fleet.counts")}>
      {HEALTHS.filter((h) => counts[h] > 0).map((h) => (
        <Pill key={h} tone={HEALTH_TONE[h]} value={h}>
          {number(counts[h])} {t(`healthState.${h}` as MessageKey).toLowerCase()}
        </Pill>
      ))}
    </span>
  );
}

function DeviceTile({ tile, showPosition }: { tile: Tile; showPosition: boolean }) {
  const { t, tp, locale } = useI18n();
  const w = useDurationWords();
  const a = tile.obj.attributes;
  const power = formatQuantity(a.power_w, "W", locale, w);
  const inlet = formatQuantity(a.inlet_temp_c, "°C", locale, w);
  const outlet = formatQuantity(a.outlet_temp_c, "°C", locale, w);
  const critical = num(a.log_critical_count) ?? 0;
  const where = showPosition && tile.mount?.position !== undefined ? ` · U${tile.mount.position}` : "";
  return (
    <li className="tile" data-health={tile.health} data-kind={tile.kind.key}>
      <div className="tile-head">
        <KindIcon kind={tile.kind} />
        <div className="tile-name">
          <Link to={`/objects/${encodeURIComponent(tile.obj.id)}`}>{tile.obj.display}</Link>
          <span className="tile-kind">
            {tile.kindLabel}
            {where}
          </span>
        </div>
        <HealthPill value={tile.health} />
      </div>
      {power || inlet || outlet || critical > 0 ? (
        <dl className="tile-metrics">
          {power ? (
            <div>
              <dt>{t("fleet.power")}</dt>
              <dd>{power}</dd>
            </div>
          ) : null}
          {inlet ? (
            <div>
              <dt>{t("fleet.inlet")}</dt>
              <dd>{inlet}</dd>
            </div>
          ) : null}
          {outlet ? (
            <div>
              <dt>{t("fleet.outlet")}</dt>
              <dd>{outlet}</dd>
            </div>
          ) : null}
          {critical > 0 ? (
            <div>
              <dt className="sr-only">{t("healthState.critical")}</dt>
              <dd className="bad">{tp("fleet.critical", critical)}</dd>
            </div>
          ) : null}
        </dl>
      ) : null}
    </li>
  );
}

/** A group shows this many tiles before it folds the healthy ones. */
export const FOLD_AFTER = 12;

/**
 * What a folded group shows: every device that is not ok, then ok ones up to
 * FOLD_AFTER, in the group's order. Nothing that needs attention is ever hidden.
 */
export function foldTiles(tiles: Tile[]): Tile[] {
  if (tiles.length <= FOLD_AFTER) return tiles;
  let room = Math.max(0, FOLD_AFTER - tiles.filter((x) => x.health !== "ok").length);
  return tiles.filter((x) => x.health !== "ok" || room-- > 0);
}

/** Devices that draw no power of their own: not counted as unmeasured. */
const UNPOWERED_ROLES = new Set(["patch_panel"]);

export type PowerTotal = { watts: number; measured: number; of: number; outputWatts: number };

/**
 * The power total of the fleet and what it covers: the devices the inventory
 * still sees and that draw power, how many of them report a reading, and how
 * much of the sum is what supplies deliver rather than what devices draw
 * (power_basis "output", a switch that reports no input power). A total that
 * leaves devices out or mixes outputs with draws says so.
 */
export function powerTotal(tiles: Tile[]): PowerTotal {
  const t: PowerTotal = { watts: 0, measured: 0, of: 0, outputWatts: 0 };
  for (const x of tiles) {
    if (x.health === "absent" || UNPOWERED_ROLES.has(String(x.obj.attributes.role))) continue;
    t.of++;
    const w = num(x.obj.attributes.power_w);
    if (w === undefined) continue;
    t.measured++;
    t.watts += w;
    if (x.obj.attributes.power_basis === "output") t.outputWatts += w;
  }
  return t;
}

function GroupTiles({ tiles, showPosition }: { tiles: Tile[]; showPosition: boolean }) {
  const { t, number } = useI18n();
  const [open, setOpen] = useState(false);
  const shown = open ? tiles : foldTiles(tiles);
  return (
    <>
      <ul className="tiles">
        {shown.map((tile) => (
          <DeviceTile key={tile.obj.id} tile={tile} showPosition={showPosition} />
        ))}
      </ul>
      {shown.length < tiles.length || open ? (
        <p className="actions">
          <button type="button" className="ghost" aria-expanded={open} onClick={() => setOpen(!open)}>
            {open ? t("fleet.fold") : t("fleet.showAll", { count: number(tiles.length), hidden: number(tiles.length - shown.length) })}
          </button>
        </p>
      ) : null}
    </>
  );
}

/**
 * The physical estate at a glance: one tile per device with its kind, health,
 * power draw, temperatures and critical events; counts on top. Everything is
 * read from the schema's attributes (role, health, power_w, inlet_temp_c,
 * outlet_temp_c, log_critical_count) and the mounted_in relations.
 */
export function Fleet({ defaultGroup = "rack", headingLevel = 2 }: { defaultGroup?: "rack" | "kind"; headingLevel?: 2 | 3 }) {
  const { t, number, locale } = useI18n();
  const w = useDurationWords();
  const kindLabel = useKindLabel();
  const id = useId();
  const [by, setBy] = useState<"rack" | "kind">(defaultGroup);
  const [onlyProblems, setOnlyProblems] = useState(false);
  const devices = useQuery({
    queryKey: ["fleet", "devices"],
    queryFn: ({ signal }) => api.objects({ type: "device", limit: FLEET_LIMIT }, signal),
    refetchInterval: 60_000,
  });
  const mounts = useQuery({
    queryKey: ["fleet", "mounts"],
    queryFn: ({ signal }) => api.relationLinks({ type: "mounted_in,located_in", limit: FLEET_LIMIT }, signal),
  });
  const mountMap = useMemo(() => mountsOf(mounts.data?.items ?? []), [mounts.data]);
  const placeMap = useMemo(() => placesOf(mounts.data?.items ?? []), [mounts.data]);
  const H = headingLevel === 2 ? "h2" : "h3";
  const G = headingLevel === 2 ? "h3" : "h4";

  return (
    <section aria-labelledby={`${id}-h`} className="fleet">
      <H id={`${id}-h`}>{t("fleet.title")}</H>
      <Async query={devices}>
        {(page) => {
          if (page.items.length === 0) return <EmptyState icon="server">{t("fleet.empty")}</EmptyState>;
          const tiles: Tile[] = page.items.map((obj) => {
            const k = kindLabel(obj);
            return { obj, kind: k.kind, kindLabel: k.label, health: healthOf(obj), mount: mountMap.get(obj.id), place: placeMap.get(obj.id) };
          });
          const counts = countHealth(tiles);
          const power = powerTotal(tiles);
          const grouping = mounts.isError ? "kind" : by;
          const shown = onlyProblems ? tiles.filter((x) => x.health === "critical" || x.health === "warning") : tiles;
          const groups = groupTiles(shown, grouping, t("fleet.notRacked"));
          return (
            <>
              <Stats label={t("fleet.stats")}>
                <Stat id="total" value={number(tiles.length)} label={t("fleet.total")} />
                <Stat id="ok" value={number(counts.ok)} label={t("healthState.ok")} tone="ok" />
                <Stat id="warning" value={number(counts.warning)} label={t("healthState.warning")} tone={counts.warning ? "warn" : undefined} />
                <Stat id="critical" value={number(counts.critical)} label={t("healthState.critical")} tone={counts.critical ? "danger" : undefined} />
                <Stat id="unknown" value={number(counts.unknown + counts.absent)} label={t("healthState.unknown")} />
                <Stat
                  id="power"
                  value={formatQuantity(power.watts, "W", locale, w)}
                  label={
                    <>
                      {t("fleet.power")}
                      {power.measured < power.of ? (
                        <span className="stat-note">{t("fleet.powerMeasured", { measured: number(power.measured), of: number(power.of) })}</span>
                      ) : null}
                      {power.outputWatts > 0 ? (
                        <span className="stat-note">{t("fleet.powerOutput", { watts: formatQuantity(power.outputWatts, "W", locale, w) ?? "" })}</span>
                      ) : null}
                    </>
                  }
                />
              </Stats>
              {page.total > page.items.length ? (
                <Alert role="note" tone="info">
                  <p>{t("fleet.truncated", { count: number(page.items.length) })}</p>
                </Alert>
              ) : null}
              {mounts.isError ? (
                <Alert role="note" tone="info">
                  <p>{t("fleet.locationUnknown")}</p>
                </Alert>
              ) : null}
              <div className="health-toolbar">
                <label>
                  {t("fleet.groupBy")}
                  <select value={grouping} onChange={(e) => setBy(e.target.value as "rack" | "kind")} disabled={mounts.isError}>
                    <option value="rack">{t("fleet.byRack")}</option>
                    <option value="kind">{t("fleet.byKind")}</option>
                  </select>
                </label>
                <label>
                  <input type="checkbox" checked={onlyProblems} onChange={(e) => setOnlyProblems(e.target.checked)} />
                  {t("fleet.onlyProblems")}
                </label>
              </div>
              {groups.length === 0 ? (
                <EmptyState icon="health">{counts.critical + counts.warning === 0 ? t("fleet.allClear") : t("fleet.noMatch")}</EmptyState>
              ) : (
                groups.map((g) => (
                  <div key={g.key || "none"} className="health-group" data-group={g.key}>
                    <div className="health-group-head">
                      <G>
                        {g.rackId ? (
                          <Link to={`/racks/${encodeURIComponent(g.rackId)}`} aria-label={t("fleet.openRack", { rack: g.title })}>
                            {g.title}
                          </Link>
                        ) : g.placeId ? (
                          <Link to={`/objects/${encodeURIComponent(g.placeId)}`}>{g.title}</Link>
                        ) : (
                          g.title
                        )}
                      </G>
                      <HealthCounts counts={countHealth(g.tiles)} />
                    </div>
                    <GroupTiles tiles={g.tiles} showPosition={grouping === "rack"} />
                  </div>
                ))
              )}
            </>
          );
        }}
      </Async>
    </section>
  );
}

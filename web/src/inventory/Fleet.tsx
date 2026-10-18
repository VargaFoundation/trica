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

export interface Tile {
  obj: ObjectView;
  kind: Kind;
  kindLabel: string;
  health: Health;
  mount?: Mount;
}

export interface Group {
  key: string;
  title: string;
  rackId?: string;
  tiles: Tile[];
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
 * Tiles grouped by rack (top of the rack first, as one stands in front of it;
 * devices in no rack last) or by kind (the groups holding the worst health
 * first).
 */
export function groupTiles(tiles: Tile[], by: "rack" | "kind", notRacked: string): Group[] {
  const groups = new Map<string, Group>();
  for (const tile of tiles) {
    const key = by === "rack" ? (tile.mount?.rackId ?? "") : tile.kind.key;
    let g = groups.get(key);
    if (!g) {
      g = by === "rack" ? { key, title: tile.mount?.rack ?? notRacked, rackId: tile.mount?.rackId, tiles: [] } : { key, title: tile.kindLabel, tiles: [] };
      groups.set(key, g);
    }
    g.tiles.push(tile);
  }
  const list = [...groups.values()];
  if (by === "rack") {
    for (const g of list)
      g.tiles.sort((a, b) => (b.mount?.position ?? -1) - (a.mount?.position ?? -1) || a.obj.display.localeCompare(b.obj.display));
    return list.sort((a, b) => Number(!a.rackId) - Number(!b.rackId) || a.title.localeCompare(b.title, undefined, { numeric: true }));
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
    queryFn: ({ signal }) => api.relationLinks({ type: "mounted_in", limit: FLEET_LIMIT }, signal),
  });
  const mountMap = useMemo(() => mountsOf(mounts.data?.items ?? []), [mounts.data]);
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
            return { obj, kind: k.kind, kindLabel: k.label, health: healthOf(obj), mount: mountMap.get(obj.id) };
          });
          const counts = countHealth(tiles);
          const watts = tiles.reduce((s, x) => s + (num(x.obj.attributes.power_w) ?? 0), 0);
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
                <Stat id="power" value={formatQuantity(watts, "W", locale, w)} label={t("fleet.power")} />
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

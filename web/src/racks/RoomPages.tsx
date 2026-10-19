import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link, useParams } from "react-router";
import { ApiError } from "../api/client";
import { api } from "../api/endpoints";
import type { ObjectView } from "../api/types";
import { Async } from "../components/Async";
import { ObjectLink } from "../components/ObjectLink";
import { useI18n } from "../i18n";
import { NotFoundPage } from "../pages/NotFoundPage";
import { useCrumbs } from "../components/crumbs";
import { usePageTitle } from "../usePageTitle";
import { layoutFloor, tileOf, type FloorPlan, type PlacedRack } from "./floor";
import { useRoomContents, type RoomContents } from "./useRack";
import { EditPanel, useCanDeclare } from "../edit/EditPanel";
import { ObjectForm } from "../edit/ObjectForm";
import { aisleTiles, conflictingRacks, type Aisle, type AisleTile } from "../aisles/aisles";
import "../aisles/aisles.css";

/** /rooms — every room, each linking to its floor plan. */
export function RoomsPage() {
  const { t, number } = useI18n();
  usePageTitle(t("rooms.title"));
  const rooms = useQuery({
    queryKey: ["rooms"],
    queryFn: ({ signal }) => api.objects({ type: "room", sort: "name", limit: 1000 }, signal),
    staleTime: 30_000,
  });
  const can = useCanDeclare();
  const [adding, setAdding] = useState<"site" | "room" | null>(null);
  return (
    <>
      <h1>{t("rooms.title")}</h1>
      <p>{t("rooms.intro")}</p>
      {can && adding === null ? (
        <p className="actions">
          <button type="button" onClick={() => setAdding("site")}>
            {t("edit.site.add")}
          </button>{" "}
          <button type="button" onClick={() => setAdding("room")}>
            {t("edit.room.add")}
          </button>
        </p>
      ) : null}
      {can && adding ? (
        <EditPanel title={t(adding === "site" ? "edit.site.add" : "edit.room.add")}>
          <ObjectForm key={adding} mode="create" type={adding} onClose={() => setAdding(null)} />
        </EditPanel>
      ) : null}
      <Async query={rooms}>
        {(page) =>
          page.items.length === 0 ? (
            <p>{t("rooms.empty")}</p>
          ) : (
            <table>
              <caption className="sr-only">{t("rooms.title")}</caption>
              <thead>
                <tr>
                  <th scope="col">{t("rooms.col.name")}</th>
                  <th scope="col">{t("rooms.col.site")}</th>
                  <th scope="col">{t("rooms.col.size")}</th>
                </tr>
              </thead>
              <tbody>
                {page.items.map((r) => (
                  <tr key={r.id}>
                    <th scope="row" data-label={t("rooms.col.name")}>
                      <Link to={`/rooms/${encodeURIComponent(r.id)}`}>{r.display}</Link>
                    </th>
                    <td data-label={t("rooms.col.site")}>{r.site_id ? <ObjectLink id={r.site_id} /> : "—"}</td>
                    <td data-label={t("rooms.col.size")}>
                      {typeof r.attributes?.width_mm === "number" && typeof r.attributes?.depth_mm === "number"
                        ? `${number(r.attributes.width_mm)} × ${number(r.attributes.depth_mm)} mm`
                        : "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )
        }
      </Async>
    </>
  );
}

/** /rooms/:id — the floor plan of one room and the list of its racks. */
export function RoomPage() {
  const { id = "" } = useParams();
  const room = useQuery({ queryKey: ["object", id], queryFn: ({ signal }) => api.object(id, signal), staleTime: 5 * 60_000 });
  if (room.isError && room.error instanceof ApiError && room.error.status === 404) return <NotFoundPage />;
  return <Async query={room}>{(r) => (r.type === "room" ? <Room room={r} /> : <NotFoundPage />)}</Async>;
}

type RoomPanel = { kind: "rack"; x?: number; y?: number } | { kind: "edit" } | null;

function Room({ room }: { room: ObjectView }) {
  const { t } = useI18n();
  const contents = useRoomContents(room.id);
  const can = useCanDeclare();
  const [panel, setPanel] = useState<RoomPanel>(null);
  usePageTitle(t("room.pageTitle", { room: room.display }));
  useCrumbs([{ label: t("nav.rooms"), to: "/rooms" }, { label: room.display }]);
  const w = room.attributes?.width_mm;
  const d = room.attributes?.depth_mm;
  const widthMm = typeof w === "number" ? w : undefined;
  const depthMm = typeof d === "number" ? d : undefined;
  const tileMm = tileOf(room.attributes?.tile_mm);
  return (
    <>
      <h1>{t("room.pageTitle", { room: room.display })}</h1>
      <p>
        <ObjectLink id={room.id} display={t("room.openObject")} />
      </p>
      {can && panel === null ? (
        <p className="actions">
          <button type="button" className="primary" onClick={() => setPanel({ kind: "rack" })}>
            {t("edit.rack.add")}
          </button>{" "}
          <button type="button" onClick={() => setPanel({ kind: "edit" })}>
            {t("edit.room.button")}
          </button>
        </p>
      ) : null}
      {can && panel?.kind === "rack" && contents.data ? (
        <EditPanel title={t("edit.rack.addTitle", { room: room.display })}>
          <ObjectForm
            key={`${panel.x ?? ""}-${panel.y ?? ""}`}
            mode="create"
            type="rack"
            parent={{ type: "room", id: room.id, display: room.display }}
            initial={{ floor_x: panel.x === undefined ? "" : String(panel.x), floor_y: panel.y === undefined ? "" : String(panel.y) }}
            floor={floorContext(widthMm, depthMm, tileMm, contents.data)}
            onClose={() => setPanel(null)}
          />
        </EditPanel>
      ) : null}
      {can && panel?.kind === "edit" ? (
        <EditPanel title={t("edit.room.title", { room: room.display })}>
          <ObjectForm mode="edit" object={room} onClose={() => setPanel(null)} />
        </EditPanel>
      ) : null}
      <Async query={contents}>
        {(c) => (
          <Plan
            roomName={room.display}
            widthMm={widthMm}
            depthMm={depthMm}
            tileMm={tileMm}
            contents={c}
            onPick={can ? (x, y) => setPanel({ kind: "rack", x, y }) : undefined}
          />
        )}
      </Async>
    </>
  );
}

function floorContext(widthMm: number | undefined, depthMm: number | undefined, tileMm: number, c: RoomContents) {
  const plan = layoutFloor(widthMm, depthMm, c.racks, tileMm);
  return { racks: c.racks, size: plan.sizeFromRacks ? undefined : { cols: plan.cols, rows: plan.rows } };
}

const TILE_PX = 64;
const PAD = 8;

function Plan({
  roomName,
  widthMm,
  depthMm,
  tileMm,
  contents,
  onPick,
}: {
  roomName: string;
  widthMm?: number;
  depthMm?: number;
  tileMm: number;
  contents: RoomContents;
  onPick?: (x: number, y: number) => void;
}) {
  const { t, number } = useI18n();
  const plan = useMemo(() => layoutFloor(widthMm, depthMm, contents.racks, tileMm), [widthMm, depthMm, contents.racks, tileMm]);
  const uHeights = new Map(contents.racks.map((r) => [r.id, r.uHeight]));
  const aisleOfRack = new Map<string, Aisle | undefined>(contents.racks.map((r) => [r.id, r.frontAisle]));
  const aisles = useMemo(() => {
    const front = new Map(contents.racks.map((r) => [r.id, r.frontAisle]));
    return aisleTiles(plan.placed.map((r) => ({ ...r, frontAisle: front.get(r.id) })));
  }, [plan.placed, contents.racks]);
  const conflicts = conflictingRacks(aisles);
  return (
    <>
      {plan.sizeFromRacks ? (
        <p role="note" className="notice">
          {t("room.sizeFromRacks")}
        </p>
      ) : null}
      {plan.outside.length > 0 ? (
        <p role="note" className="notice">
          {t("room.outside")}
        </p>
      ) : null}
      {contents.truncated ? (
        <p role="note" className="notice">
          {t("room.truncated")}
        </p>
      ) : null}
      {contents.racks.length === 0 ? <p>{t("room.empty")}</p> : null}
      {plan.placed.length > 0 || (onPick && !plan.sizeFromRacks) ? (
        <figure className="floor-plan">
          <figcaption>{t("room.tile", { mm: number(tileMm) })}</figcaption>
          <FloorSvg roomName={roomName} plan={plan} aisles={aisles} names={new Map(contents.racks.map((r) => [r.id, r.name]))} onPick={onPick} />
          {aisles.length > 0 ? <p className="hint">{t("aisle.floorLegend")}</p> : null}
          {onPick ? <p className="hint">{t("edit.rack.clickHint")}</p> : null}
        </figure>
      ) : null}
      {contents.racks.length > 0 ? (
        <section aria-labelledby="room-racks">
          <h2 id="room-racks">{t("room.racks")}</h2>
          <table>
            <caption className="sr-only">{t("room.racks")}</caption>
            <thead>
              <tr>
                <th scope="col">{t("room.col.rack")}</th>
                <th scope="col">{t("room.col.tile")}</th>
                <th scope="col">{t("room.col.facing")}</th>
                <th scope="col">{t("room.col.aisle")}</th>
                <th scope="col">{t("room.col.height")}</th>
                <th scope="col">{t("room.col.notes")}</th>
              </tr>
            </thead>
            <tbody>
              {[...plan.placed, ...plan.unplaced].map((r) => {
                const placed = "clash" in r ? (r as PlacedRack) : undefined;
                const u = uHeights.get(r.id);
                return (
                  <tr key={r.id}>
                    <th scope="row" data-label={t("room.col.rack")}>
                      <Link to={`/racks/${encodeURIComponent(r.id)}`}>{r.name}</Link>
                    </th>
                    <td data-label={t("room.col.tile")}>{placed ? `${placed.x}, ${placed.y}` : "—"}</td>
                    <td data-label={t("room.col.facing")}>{r.facing ? t(`facing.${r.facing}`) : "—"}</td>
                    <td data-label={t("room.col.aisle")} data-front-aisle={aisleOfRack.get(r.id) ?? "unset"}>
                      {aisleOfRack.get(r.id) ? (
                        <span className={`aisle-chip aisle-${aisleOfRack.get(r.id)}`}>{t(aisleOfRack.get(r.id) === "cold" ? "aisle.cold" : "aisle.hot")}</span>
                      ) : (
                        "—"
                      )}
                    </td>
                    <td data-label={t("room.col.height")}>{u === undefined ? "—" : t("rack.units", { units: number(u) })}</td>
                    <td data-label={t("room.col.notes")}>
                      {[
                        placed?.clash ? t("room.clash") : !placed ? t("room.unplaced") : plan.outside.includes(r.id) ? t("room.outsideRack") : "",
                        conflicts.has(r.id) ? t("aisle.conflict") : "",
                      ]
                        .filter(Boolean)
                        .join(" · ") || "—"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </section>
      ) : null}
    </>
  );
}

const FACING_EDGE = {
  north: (x: number, y: number) => ({ x1: x + 4, y1: y + 2, x2: x + TILE_PX - 4, y2: y + 2 }),
  south: (x: number, y: number) => ({ x1: x + 4, y1: y + TILE_PX - 2, x2: x + TILE_PX - 4, y2: y + TILE_PX - 2 }),
  west: (x: number, y: number) => ({ x1: x + 2, y1: y + 4, x2: x + 2, y2: y + TILE_PX - 4 }),
  east: (x: number, y: number) => ({ x1: x + TILE_PX - 2, y1: y + 4, x2: x + TILE_PX - 2, y2: y + TILE_PX - 4 }),
} as const;

/**
 * With onPick (a caller who may propose), each free tile of the room is a
 * pointer shortcut to "add a rack here", kept out of the tab order and of the
 * accessibility tree: the page's "Add a rack" button is the keyboard path.
 */
function FloorSvg({
  roomName,
  plan,
  aisles,
  names,
  onPick,
}: {
  roomName: string;
  plan: FloorPlan;
  aisles: readonly AisleTile[];
  names: Map<string, string>;
  onPick?: (x: number, y: number) => void;
}) {
  const { t } = useI18n();
  const cols = Math.max(plan.cols, ...plan.placed.map((r) => r.x + 1));
  const rows = Math.max(plan.rows, ...plan.placed.map((r) => r.y + 1));
  const width = cols * TILE_PX + 2 * PAD;
  const height = rows * TILE_PX + 2 * PAD;
  return (
    <svg className="floor-svg" viewBox={`0 0 ${width} ${height}`} width={width} height={height} role="group" aria-label={t("room.figureLabel", { room: roomName })}>
      <rect className="floor-room" x={PAD} y={PAD} width={plan.cols * TILE_PX} height={plan.rows * TILE_PX} />
      {Array.from({ length: cols + 1 }, (_, i) => (
        <line key={`c${i}`} className="rack-grid" x1={PAD + i * TILE_PX} x2={PAD + i * TILE_PX} y1={PAD} y2={PAD + rows * TILE_PX} />
      ))}
      {Array.from({ length: rows + 1 }, (_, i) => (
        <line key={`r${i}`} className="rack-grid" y1={PAD + i * TILE_PX} y2={PAD + i * TILE_PX} x1={PAD} x2={PAD + cols * TILE_PX} />
      ))}
      {aisles.map((a) => (
        <AisleTileView key={`aisle-${a.x}-${a.y}`} tile={a} names={names} />
      ))}
      {onPick
        ? Array.from({ length: plan.cols * plan.rows }, (_, i) => ({ x: i % plan.cols, y: Math.floor(i / plan.cols) }))
            .filter((tile) => !plan.placed.some((r) => r.x === tile.x && r.y === tile.y))
            .map((tile) => (
              <rect
                key={`free-${tile.x}-${tile.y}`}
                className="floor-free"
                data-tile={`${tile.x},${tile.y}`}
                aria-hidden="true"
                x={PAD + tile.x * TILE_PX + 2}
                y={PAD + tile.y * TILE_PX + 2}
                width={TILE_PX - 4}
                height={TILE_PX - 4}
                onClick={() => onPick(tile.x, tile.y)}
              >
                <title>{t("edit.rack.here", { x: tile.x, y: tile.y })}</title>
              </rect>
            ))
        : null}
      {plan.placed.map((r) => {
        const x = PAD + r.x * TILE_PX;
        const y = PAD + r.y * TILE_PX;
        const bad = r.clash || plan.outside.includes(r.id);
        const maxChars = 8;
        const label = r.name.length > maxChars ? `${r.name.slice(0, maxChars - 1)}…` : r.name;
        return (
          <Link
            key={r.id}
            to={`/racks/${encodeURIComponent(r.id)}`}
            className={bad ? "floor-rack rack-problem" : "floor-rack"}
            aria-label={r.facing ? `${r.name}, ${t(`facing.${r.facing}`)}` : r.name}
            data-id={r.id}
          >
            <title>{r.name}</title>
            <rect x={x + 3} y={y + 3} width={TILE_PX - 6} height={TILE_PX - 6} rx={3} />
            <text x={x + TILE_PX / 2} y={y + TILE_PX / 2 + 4} textAnchor="middle">
              {label}
            </text>
            {r.facing ? <line className="floor-front" {...FACING_EDGE[r.facing](x, y)} /> : null}
          </Link>
        );
      })}
    </svg>
  );
}

/** An aisle tile of the floor plan: its kind in a letter and in its tooltip, its colour repeats it. */
function AisleTileView({ tile: a, names }: { tile: AisleTile; names: Map<string, string> }) {
  const { t } = useI18n();
  const racks = a.racks.map((id) => names.get(id) ?? id).join(", ");
  const text = t(a.kind === "cold" ? "aisle.floorTile.cold" : a.kind === "hot" ? "aisle.floorTile.hot" : "aisle.floorTile.conflict", { racks });
  const x = PAD + a.x * TILE_PX;
  const y = PAD + a.y * TILE_PX;
  return (
    <g className="floor-aisle-tile" data-aisle={a.kind} data-tile={`${a.x},${a.y}`}>
      <title>{text}</title>
      <rect className={`floor-aisle floor-aisle-${a.kind}`} x={x + 1} y={y + 1} width={TILE_PX - 2} height={TILE_PX - 2} />
      <text className={`floor-aisle-label floor-aisle-label-${a.kind}`} x={x + TILE_PX / 2} y={y + TILE_PX / 2 + 4} textAnchor="middle">
        {a.kind === "cold" ? t("aisle.short.cold") : a.kind === "hot" ? t("aisle.short.hot") : "!"}
      </text>
    </g>
  );
}

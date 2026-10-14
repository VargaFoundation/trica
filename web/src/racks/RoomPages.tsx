import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { Link, useParams } from "react-router";
import { ApiError } from "../api/client";
import { api } from "../api/endpoints";
import type { ObjectView } from "../api/types";
import { Async } from "../components/Async";
import { ObjectLink } from "../components/ObjectLink";
import { useI18n } from "../i18n";
import { NotFoundPage } from "../pages/NotFoundPage";
import { usePageTitle } from "../usePageTitle";
import { layoutFloor, TILE_MM, type FloorPlan, type PlacedRack } from "./floor";
import { useRoomContents, type RoomContents } from "./useRack";

/** /rooms — every room, each linking to its floor plan. */
export function RoomsPage() {
  const { t, number } = useI18n();
  usePageTitle(t("rooms.title"));
  const rooms = useQuery({
    queryKey: ["rooms"],
    queryFn: ({ signal }) => api.objects({ type: "room", sort: "name", limit: 1000 }, signal),
    staleTime: 30_000,
  });
  return (
    <>
      <h1>{t("rooms.title")}</h1>
      <p>{t("rooms.intro")}</p>
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

function Room({ room }: { room: ObjectView }) {
  const { t } = useI18n();
  const contents = useRoomContents(room.id);
  usePageTitle(t("room.pageTitle", { room: room.display }));
  return (
    <>
      <h1>{t("room.pageTitle", { room: room.display })}</h1>
      <p>
        <ObjectLink id={room.id} display={t("room.openObject")} />
      </p>
      <Async query={contents}>
        {(c) => {
          const w = room.attributes?.width_mm;
          const d = room.attributes?.depth_mm;
          return <Plan roomName={room.display} widthMm={typeof w === "number" ? w : undefined} depthMm={typeof d === "number" ? d : undefined} contents={c} />;
        }}
      </Async>
    </>
  );
}

const TILE_PX = 64;
const PAD = 8;

function Plan({ roomName, widthMm, depthMm, contents }: { roomName: string; widthMm?: number; depthMm?: number; contents: RoomContents }) {
  const { t, number } = useI18n();
  const plan = useMemo(() => layoutFloor(widthMm, depthMm, contents.racks), [widthMm, depthMm, contents.racks]);
  const uHeights = new Map(contents.racks.map((r) => [r.id, r.uHeight]));
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
      {plan.placed.length > 0 ? (
        <figure className="floor-plan">
          <figcaption>{t("room.tile", { mm: number(TILE_MM) })}</figcaption>
          <FloorSvg roomName={roomName} plan={plan} />
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
                    <td data-label={t("room.col.height")}>{u === undefined ? "—" : t("rack.units", { units: number(u) })}</td>
                    <td data-label={t("room.col.notes")}>
                      {placed?.clash ? t("room.clash") : !placed ? t("room.unplaced") : plan.outside.includes(r.id) ? t("room.outsideRack") : "—"}
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

function FloorSvg({ roomName, plan }: { roomName: string; plan: FloorPlan }) {
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

import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useParams } from "react-router";
import { ApiError } from "../api/client";
import { api } from "../api/endpoints";
import type { ObjectView } from "../api/types";
import { Async } from "../components/Async";
import { fileSlug } from "../export/download";
import { ExportButtons } from "../export/ExportButtons";
import { rackRaster } from "../export/rackCanvas";
import { ObjectLink } from "../components/ObjectLink";
import { useI18n, type MessageKey } from "../i18n";
import { NotFoundPage } from "../pages/NotFoundPage";
import { usePageTitle } from "../usePageTitle";
import { freeUnits, formatRange, layoutRack, type Issue, type Slot } from "./layout";
import { RackElevation } from "./RackElevation";
import { useRackContents, type RackContents } from "./useRack";

export const DEFAULT_RACK_U = 42;

const ISSUE_TEXT: Record<Issue, MessageKey> = {
  overlap: "rack.issue.overlap",
  outOfRange: "rack.issue.outOfRange",
  assumedHeight: "rack.issue.assumedHeight",
  assumedFace: "rack.issue.assumedFace",
};

/** /racks/:id — front and rear elevations of one rack. */
export function RackPage() {
  const { id = "" } = useParams();
  const rack = useQuery({ queryKey: ["object", id], queryFn: ({ signal }) => api.object(id, signal), staleTime: 5 * 60_000 });
  if (rack.isError && rack.error instanceof ApiError && rack.error.status === 404) return <NotFoundPage />;
  return <Async query={rack}>{(r) => (r.type === "rack" ? <Rack rack={r} /> : <NotFoundPage />)}</Async>;
}

function Rack({ rack }: { rack: ObjectView }) {
  const { t, number } = useI18n();
  const contents = useRackContents(rack.id);
  usePageTitle(t("rack.pageTitle", { rack: rack.display }));
  const declared = rack.attributes?.u_height;
  const uHeight = typeof declared === "number" && declared > 0 ? Math.floor(declared) : DEFAULT_RACK_U;
  return (
    <>
      <h1>{t("rack.pageTitle", { rack: rack.display })}</h1>
      <p>
        <ObjectLink id={rack.id} display={t("rack.openObject")} />
      </p>
      {uHeight !== declared ? (
        <p role="note" className="notice">
          {t("rack.assumedHeight", { units: number(uHeight) })}
        </p>
      ) : null}
      <Async query={contents}>{(c) => <Elevations rack={rack} uHeight={uHeight} contents={c} />}</Async>
    </>
  );
}

function Elevations({ rack, uHeight, contents }: { rack: ObjectView; uHeight: number; contents: RackContents }) {
  const { t, tp, number } = useI18n();
  const layout = useMemo(() => layoutRack(uHeight, contents.items), [uHeight, contents.items]);
  const rows = useMemo(
    () => [...layout.front, ...layout.rear].filter((s) => !s.shadow).sort((a, b) => b.bottom - a.bottom || a.item.name.localeCompare(b.item.name)),
    [layout],
  );
  return (
    <>
      <dl className="facts">
        <div>
          <dt>{t("rack.height")}</dt>
          <dd>{t("rack.units", { units: number(uHeight) })}</dd>
        </div>
        <div>
          <dt>{t("rack.devices")}</dt>
          <dd>{number(contents.items.length)}</dd>
        </div>
        <div>
          <dt>{t("rack.freeFront")}</dt>
          <dd>{number(freeUnits(uHeight, layout.front))}</dd>
        </div>
        <div>
          <dt>{t("rack.freeRear")}</dt>
          <dd>{number(freeUnits(uHeight, layout.rear))}</dd>
        </div>
      </dl>
      {contents.truncated ? (
        <p role="note" className="notice">
          {t("rack.truncated", { count: number(contents.items.length) })}
        </p>
      ) : null}
      {contents.items.length === 0 ? <p>{t("rack.empty")}</p> : null}
      <div className="rack-faces">
        {(["front", "rear"] as const).map((face) => (
          <figure key={face} className="rack-face">
            <figcaption>{t(face === "front" ? "rack.front" : "rack.rear")}</figcaption>
            <RackElevation rack={rack.display} uHeight={uHeight} face={face} slots={layout[face]} />
          </figure>
        ))}
      </div>
      <p className="hint">{t("rack.legend")}</p>
      <ExportButtons
        basename={`trica-rack-${fileSlug(rack.display)}`}
        title={t("rack.pageTitle", { rack: rack.display })}
        render={(format) =>
          rackRaster(
            {
              title: t("rack.pageTitle", { rack: rack.display }),
              uHeight,
              faces: [
                { caption: t("rack.front"), slots: layout.front },
                { caption: t("rack.rear"), slots: layout.rear },
              ],
              footer: t("export.footer", { date: new Date().toISOString().slice(0, 10) }),
            },
            format,
          )
        }
      />

      {layout.unplaced.length > 0 ? (
        <section aria-labelledby="unplaced">
          <h2 id="unplaced">{tp("rack.unplaced", layout.unplaced.length)}</h2>
          <ul>
            {layout.unplaced.map((m) => (
              <li key={m.id}>
                <ObjectLink id={m.id} display={m.name} />
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      {rows.length > 0 ? (
        <section aria-labelledby="mounted">
          <h2 id="mounted">{t("rack.mounted")}</h2>
          <table>
            <caption className="sr-only">{t("rack.mounted")}</caption>
            <thead>
              <tr>
                <th scope="col">{t("rack.col.device")}</th>
                <th scope="col">{t("rack.col.model")}</th>
                <th scope="col">{t("rack.col.position")}</th>
                <th scope="col">{t("rack.col.face")}</th>
                <th scope="col">{t("rack.col.height")}</th>
                <th scope="col">{t("rack.col.notes")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((s) => (
                <Row key={`${s.item.id}-${s.face}`} slot={s} />
              ))}
            </tbody>
          </table>
        </section>
      ) : null}
    </>
  );
}

function Row({ slot: s }: { slot: Slot }) {
  const { t, number } = useI18n();
  return (
    <tr>
      <th scope="row" data-label={t("rack.col.device")}>
        <ObjectLink id={s.item.id} display={s.item.name} />
      </th>
      <td data-label={t("rack.col.model")}>{s.item.typeName ?? "—"}</td>
      <td data-label={t("rack.col.position")}>{formatRange(s.bottom, s.height)}</td>
      <td data-label={t("rack.col.face")}>
        {t(s.face === "front" ? "rack.front" : "rack.rear")}
        {s.item.fullDepth ? ` (${t("rack.fullDepth")})` : ""}
      </td>
      <td data-label={t("rack.col.height")}>{t("rack.units", { units: number(s.height) })}</td>
      <td data-label={t("rack.col.notes")}>
        {s.issues.length === 0 ? "—" : s.issues.map((i) => t(ISSUE_TEXT[i])).join(" · ")}
      </td>
    </tr>
  );
}

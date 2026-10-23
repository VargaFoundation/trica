import { useQuery } from "@tanstack/react-query";
import { useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useParams } from "react-router";
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
import { useCrumbs } from "../components/crumbs";
import { usePageTitle } from "../usePageTitle";
import {
  DEFAULT_RACK_U,
  freeUnits,
  formatRange,
  layoutRack,
  otherFace,
  overallHealth,
  slotImage,
  type Issue,
  type Slot,
} from "./layout";
import { HealthPill } from "../components/ui";
import { RackElevation } from "./RackElevation";
import { loadFaceImages } from "./faceImages";
import { useRackContents, type RackContents } from "./useRack";
import { RemovedTray } from "./RemovedTray";
import { useRemoved } from "./useRemoved";
import { EditPanel, useCanDeclare, useCanMount } from "../edit/EditPanel";
import { ObjectForm } from "../edit/ObjectForm";
import { PlaceDeviceForm } from "../edit/PlaceDeviceForm";
import { QuickPlaceDialog, type PlaceMode } from "../edit/QuickPlaceDialog";
import type { PickedDevice } from "../edit/DevicePicker";
import { useFloorContext, useRoomOf } from "../edit/useFloor";
import type { Face } from "./layout";
import { AisleSection } from "../aisles/AisleSection";
import { AirflowPill } from "../aisles/Airflow";
import { aisleOf, faceAisle } from "../aisles/aisles";

/**
 * What is open. Placing, moving and turning a device are a dialog (ADR 0052);
 * the full form (warn-only on overlaps, a device no collector sees) and the
 * rack's own fields stay a panel under the actions.
 */
type Panel =
  | {
      kind: "quick";
      mode: PlaceMode;
      u?: number;
      face?: Face;
      device?: PickedDevice;
    }
  | {
      kind: "full";
      u?: number;
      face?: Face;
      device?: PickedDevice;
      fixed: boolean;
      height?: number;
      declare?: boolean;
    }
  | { kind: "edit" }
  | null;

const ISSUE_TEXT: Record<Issue, MessageKey> = {
  overlap: "rack.issue.overlap",
  outOfRange: "rack.issue.outOfRange",
  assumedHeight: "rack.issue.assumedHeight",
  assumedFace: "rack.issue.assumedFace",
};

/** /racks/:id — front and rear elevations of one rack. */
export function RackPage() {
  const { id = "" } = useParams();
  const rack = useQuery({
    queryKey: ["object", id],
    queryFn: ({ signal }) => api.object(id, signal),
    staleTime: 5 * 60_000,
  });
  if (
    rack.isError &&
    rack.error instanceof ApiError &&
    rack.error.status === 404
  )
    return <NotFoundPage />;
  return (
    <Async query={rack}>
      {(r) => (r.type === "rack" ? <Rack rack={r} /> : <NotFoundPage />)}
    </Async>
  );
}

function Rack({ rack }: { rack: ObjectView }) {
  const { t, number } = useI18n();
  const contents = useRackContents(rack.id);
  usePageTitle(t("rack.pageTitle", { rack: rack.display }));
  useCrumbs([
    { label: t("nav.rooms"), to: "/rooms" },
    { label: rack.display, to: `/objects/${encodeURIComponent(rack.id)}` },
    { label: t("rack.elevation") },
  ]);
  const declared = rack.attributes?.u_height;
  const uHeight =
    typeof declared === "number" && declared > 0
      ? Math.floor(declared)
      : DEFAULT_RACK_U;
  const can = useCanDeclare();
  const canMount = useCanMount();
  const [panel, setPanel] = useState<Panel>(null);
  const [done, setDone] = useState("");
  const placeButton = useRef<HTMLButtonElement>(null);
  const removed = useRemoved(rack.id);
  // Devices whose put back was sent for approval from this page: the tray says so.
  const [sent, setSent] = useState<ReadonlySet<string>>(new Set());
  const [incoming, setIncoming] = useState(false);
  const open = (p: Panel) => {
    setDone("");
    setPanel(p);
  };
  const form = panel?.kind === "full" ? panel : undefined;
  return (
    <>
      <h1>{t("rack.pageTitle", { rack: rack.display })}</h1>
      <p>
        <ObjectLink id={rack.id} display={t("rack.openObject")} />
      </p>
      {can && (panel === null || panel.kind === "quick") ? (
        <p className="actions">
          {canMount ? (
            <>
              <button
                ref={placeButton}
                type="button"
                className="primary"
                onClick={() => open({ kind: "quick", mode: "add" })}
              >
                {t("edit.place.button")}
              </button>{" "}
            </>
          ) : null}
          <button type="button" onClick={() => open({ kind: "edit" })}>
            {t("edit.rack.button")}
          </button>
        </p>
      ) : null}
      {done ? (
        <p role="status" className="decision-notice" data-outcome="applied">
          {done}
        </p>
      ) : null}
      {canMount && panel?.kind === "quick" ? (
        <QuickPlaceDialog
          key={`${panel.mode}-${panel.device?.id ?? ""}-${panel.u ?? ""}-${panel.face ?? ""}`}
          mode={panel.mode}
          rack={{ id: rack.id, display: rack.display }}
          device={panel.device}
          initialPosition={panel.u}
          initialFace={panel.face}
          returnFocus={() => placeButton.current}
          onClose={() => setPanel(null)}
          onDone={(message) => {
            setDone(message);
            setPanel(null);
          }}
          onProposed={() => {
            const id = panel.mode === "putBack" ? panel.device?.id : undefined;
            if (id) setSent((s) => new Set(s).add(id));
          }}
          onFullForm={(carry) =>
            setPanel({
              kind: "full",
              u: carry.position,
              face: carry.face as Face | undefined,
              device: carry.device,
              fixed: panel.mode !== "add",
              height: carry.heightU,
              declare: carry.declare,
            })
          }
        />
      ) : null}
      {canMount && form ? (
        <EditPanel
          title={
            form.device && form.fixed
              ? t("edit.place.titleMove", {
                  device: form.device.display,
                  rack: rack.display,
                })
              : t("edit.place.titleRack", { rack: rack.display })
          }
        >
          <PlaceDeviceForm
            key={`${form.device?.id ?? ""}-${form.u ?? ""}-${form.face ?? ""}-${form.declare ? "new" : ""}`}
            rack={{ id: rack.id, display: rack.display }}
            device={form.fixed ? form.device : undefined}
            initialDevice={form.fixed ? undefined : form.device}
            initialPosition={form.u}
            initialFace={form.face}
            initialHeight={form.height}
            initialDeclare={form.declare}
            onClose={() => setPanel(null)}
          />
        </EditPanel>
      ) : null}
      {can && panel?.kind === "edit" ? (
        <EditRack rack={rack} onClose={() => setPanel(null)} />
      ) : null}
      {uHeight !== declared ? (
        <p role="note" className="notice">
          {t("rack.assumedHeight", { units: number(uHeight) })}
        </p>
      ) : null}
      <AisleSection rack={rack} items={contents.data?.items} />
      <Async query={contents}>
        {(c) => (
          <Elevations
            rack={rack}
            uHeight={uHeight}
            contents={c}
            incoming={incoming}
            tray={
              <RemovedTray
                items={removed.data ?? []}
                canPutBack={canMount}
                sent={sent}
                onDragging={setIncoming}
                onPutBack={(it) =>
                  open({
                    kind: "quick",
                    mode: "putBack",
                    u: it.position,
                    face: it.face,
                    device: { id: it.id, display: it.name },
                  })
                }
                onDrop={(it, u, face) =>
                  open({
                    kind: "quick",
                    mode: "putBack",
                    u,
                    face,
                    device: { id: it.id, display: it.name },
                  })
                }
              />
            }
            onPick={
              canMount
                ? (u, face) => open({ kind: "quick", mode: "add", u, face })
                : undefined
            }
            onPlace={
              canMount
                ? (s, u, face) =>
                    open({
                      kind: "quick",
                      mode: face !== s.face && u === s.bottom ? "turn" : "move",
                      u,
                      face,
                      device: { id: s.item.id, display: s.item.name },
                    })
                : undefined
            }
          />
        )}
      </Async>
    </>
  );
}

function EditRack({
  rack,
  onClose,
}: {
  rack: ObjectView;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const floor = useFloorContext(useRoomOf(rack));
  return (
    <EditPanel title={t("edit.rack.title", { rack: rack.display })}>
      <ObjectForm mode="edit" object={rack} floor={floor} onClose={onClose} />
    </EditPanel>
  );
}

function Elevations({
  rack,
  uHeight,
  contents,
  incoming,
  tray,
  onPick,
  onPlace,
}: {
  rack: ObjectView;
  uHeight: number;
  contents: RackContents;
  /** A device is being dragged in from the tray: the free units say they are targets. */
  incoming?: boolean;
  /** The "taken out" tray, under the elevations. */
  tray?: ReactNode;
  onPick?: (u: number, face: Face) => void;
  /** Move or turn a device of this rack: the placement form, filled in with the new unit and face. */
  onPlace?: (slot: Slot, u: number, face: Face) => void;
}) {
  const { t, tp, number } = useI18n();
  const layout = useMemo(
    () => layoutRack(uHeight, contents.items),
    [uHeight, contents.items],
  );
  const frontAisle = aisleOf(rack.attributes?.front_aisle);
  const rows = useMemo(
    () =>
      [...layout.front, ...layout.rear]
        .filter((s) => !s.shadow)
        .sort(
          (a, b) =>
            b.bottom - a.bottom || a.item.name.localeCompare(b.item.name),
        ),
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
      <div className="rack-faces" data-incoming={incoming ? "true" : undefined}>
        {(["front", "rear"] as const).map((face) => (
          <figure key={face} className="rack-face">
            <figcaption>
              {t(face === "front" ? "rack.front" : "rack.rear")}
              <FaceAisle aisle={faceAisle(frontAisle, face)} />
            </figcaption>
            <RackElevation
              rack={rack.display}
              uHeight={uHeight}
              face={face}
              slots={layout[face]}
              onPick={onPick}
              onMove={
                onPlace ? (s, bottom) => onPlace(s, bottom, s.face) : undefined
              }
            />
          </figure>
        ))}
      </div>
      <p className="hint">{t("rack.legend")}</p>
      <p className="hint">{t("rack.facesLegend")}</p>
      {frontAisle ? <p className="hint">{t("aisle.legend")}</p> : null}
      {onPick ? <p className="hint">{t("edit.place.clickHint")}</p> : null}
      {onPlace ? <p className="hint">{t("rack.moveHint")}</p> : null}
      {tray}
      <ExportButtons
        basename={`trica-rack-${fileSlug(rack.display)}`}
        title={t("rack.pageTitle", { rack: rack.display })}
        render={async (format) =>
          rackRaster(
            {
              title: t("rack.pageTitle", { rack: rack.display }),
              uHeight,
              faces: [
                { caption: t("rack.front"), slots: layout.front },
                { caption: t("rack.rear"), slots: layout.rear },
              ],
              footer: t("export.footer", {
                date: new Date().toISOString().slice(0, 10),
              }),
              // The same images as on screen, fetched from this origin with the session's token.
              images: await loadFaceImages(
                [...layout.front, ...layout.rear]
                  .map(slotImage)
                  .filter((u): u is string => !!u),
              ),
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
                <th scope="col">{t("health.label")}</th>
                <th scope="col">{t("airflow.label")}</th>
                <th scope="col">{t("rack.col.notes")}</th>
                {onPlace ? <th scope="col">{t("rack.col.actions")}</th> : null}
              </tr>
            </thead>
            <tbody>
              {rows.map((s) => (
                <Row
                  key={`${s.item.id}-${s.face}`}
                  slot={s}
                  onPlace={onPlace}
                />
              ))}
            </tbody>
          </table>
        </section>
      ) : null}
    </>
  );
}

function Row({
  slot: s,
  onPlace,
}: {
  slot: Slot;
  onPlace?: (slot: Slot, u: number, face: Face) => void;
}) {
  const { t, number } = useI18n();
  const turnTo = otherFace(s.face);
  return (
    <tr>
      <th scope="row" data-label={t("rack.col.device")}>
        <ObjectLink id={s.item.id} display={s.item.name} />
      </th>
      <td data-label={t("rack.col.model")}>{s.item.typeName ?? "—"}</td>
      <td data-label={t("rack.col.position")}>
        {formatRange(s.bottom, s.height)}
      </td>
      <td data-label={t("rack.col.face")}>
        {t(s.face === "front" ? "rack.front" : "rack.rear")}
        {s.item.fullDepth ? ` (${t("rack.fullDepth")})` : ""}
      </td>
      <td data-label={t("rack.col.height")}>
        {t("rack.units", { units: number(s.height) })}
      </td>
      <td data-label={t("health.label")}>
        <HealthPill value={overallHealth(s.item)} />
      </td>
      <td
        data-label={t("airflow.label")}
        data-airflow={s.item.airflow?.status ?? "unknown"}
      >
        <AirflowPill check={s.item.airflow} />
      </td>
      <td data-label={t("rack.col.notes")}>
        {s.issues.length === 0 && !s.item.variantUncertain
          ? "—"
          : s.issues.map((i) => t(ISSUE_TEXT[i])).join(" · ")}
        {s.item.variantUncertain ? (
          <>
            {s.issues.length > 0 ? " · " : ""}
            <Link
              to={`/objects/${encodeURIComponent(s.item.id)}#device-model`}
              data-variant-link=""
            >
              {t("variant.rackLink")}
              <span className="sr-only"> ({s.item.name})</span>
            </Link>
          </>
        ) : null}
      </td>
      {onPlace ? (
        <td data-label={t("rack.col.actions")} className="row-actions">
          <button
            type="button"
            onClick={() => onPlace(s, s.bottom, s.face)}
            data-move={s.item.id}
          >
            {t("rack.move")}
            <span className="sr-only"> ({s.item.name})</span>
          </button>{" "}
          <button
            type="button"
            onClick={() => onPlace(s, s.bottom, turnTo)}
            data-turn={s.item.id}
            title={t("rack.turnTitle", {
              face: t(
                turnTo === "front" ? "rack.front" : "rack.rear",
              ).toLowerCase(),
            })}
          >
            {t("rack.turn")}
            <span className="sr-only"> ({s.item.name})</span>
          </button>
        </td>
      ) : null}
    </tr>
  );
}

/** The aisle a face of the rack is on, as a chip in the elevation's caption. */
function FaceAisle({ aisle }: { aisle?: "cold" | "hot" }) {
  const { t } = useI18n();
  if (!aisle) return null;
  return (
    <span className={`aisle-chip aisle-${aisle}`} data-aisle={aisle}>
      {t(aisle === "cold" ? "aisle.cold" : "aisle.hot")}
    </span>
  );
}

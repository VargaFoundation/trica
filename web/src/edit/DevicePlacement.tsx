// The device page's "Rack placement" section: where the device is mounted,
// and, for a caller who may propose, place it, move it, or take it out.
import { useRef, useState } from "react";
import type { ObjectView } from "../api/types";
import { Async } from "../components/Async";
import { ObjectLink } from "../components/ObjectLink";
import { useMe } from "../hooks";
import { useI18n } from "../i18n";
import { enumText } from "./AttributeFields";
import { BatchReview } from "./BatchReview";
import { callerSource, unplaceDevice } from "./batch";
import { EditPanel, useCanDeclare, useCanMount } from "./EditPanel";
import { PlaceDeviceForm } from "./PlaceDeviceForm";
import { QuickPlaceDialog } from "./QuickPlaceDialog";
import { usePlacement, type CurrentMount } from "./usePlacement";
import { usePlacementHistory } from "./usePlacementHistory";

type Open =
  | { kind: "place"; rackId?: string; position?: number; face?: string; height?: number }
  | { kind: "putBack" }
  | { kind: "remove"; mount: CurrentMount }
  | null;

export function DevicePlacement({ device }: { device: ObjectView }) {
  const { t, number, dateTime } = useI18n();
  const can = useCanDeclare();
  const canMount = useCanMount();
  const source = callerSource(useMe().data);
  const placement = usePlacement(device.id);
  const [open, setOpen] = useState<Open>(null);
  const [done, setDone] = useState("");
  const unmounted = placement.data !== undefined && placement.data.mounts.length === 0;
  const last = usePlacementHistory(device.id, canMount && unmounted).data;
  const putBackButton = useRef<HTMLButtonElement>(null);
  return (
    <section aria-labelledby="placement-h" className="placement">
      <h2 id="placement-h">{t("edit.placement.title")}</h2>
      <Async query={placement}>
        {(p) => (
          <>
            {p.mounts.length === 0 ? (
              <p>{t("edit.placement.none")}</p>
            ) : (
              <ul>
                {p.mounts.map((m) => (
                  <li key={m.rackId} data-rack={m.rackId}>
                    <ObjectLink id={m.rackId} display={m.rackName} />
                    {m.position !== undefined
                      ? `, U${number(m.position)}`
                      : `, ${t("edit.placement.noPosition")}`}
                    {m.face
                      ? `, ${m.face === "front" ? t("rack.front") : m.face === "rear" ? t("rack.rear") : enumText(m.face)}`
                      : ""}{" "}
                    <span className="muted">({m.source})</span>
                    {can ? (
                      <>
                        {" "}
                        <button
                          type="button"
                          onClick={() => setOpen({ kind: "remove", mount: m })}
                        >
                          {t("edit.placement.remove")}{" "}
                          <span className="sr-only">{m.rackName}</span>
                        </button>
                      </>
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
            {canMount && unmounted && last ? (
              <div className="put-back">
                <p>
                  {last.position !== undefined
                    ? t("edit.placement.last", {
                        rack: last.rack.display,
                        where: `U${number(last.position)}${last.face ? `, ${last.face === "front" ? t("rack.front") : t("rack.rear")}`.toLowerCase() : ""}`,
                        date: dateTime(last.until),
                        source: last.source,
                      })
                    : t("edit.placement.lastNoPos", {
                        rack: last.rack.display,
                        date: dateTime(last.until),
                        source: last.source,
                      })}
                </p>
                {last.rack.absent ? (
                  <p className="notice" role="note">
                    {t("edit.placement.rackAbsent", { rack: last.rack.display })}
                  </p>
                ) : null}
              </div>
            ) : null}
            {canMount && open === null ? (
              <p className="actions">
                {unmounted && last ? (
                  <button
                    ref={putBackButton}
                    type="button"
                    className="primary"
                    data-put-back=""
                    onClick={() => {
                      setDone("");
                      setOpen({ kind: "putBack" });
                    }}
                  >
                    {t("edit.placement.putBack", { rack: last.rack.display })}
                  </button>
                ) : null}
                <button
                  type="button"
                  className={unmounted && last ? undefined : "primary"}
                  onClick={() => {
                    setDone("");
                    // Moving a mounted device starts from where it is now.
                    const here = p.mounts[0];
                    setOpen({
                      kind: "place",
                      rackId: here?.rackId,
                      position: here?.position,
                      face: here?.face,
                    });
                  }}
                >
                  {p.mounts.length > 0
                    ? t("edit.placement.move")
                    : last
                      ? t("edit.placement.putBackElsewhere")
                      : t("edit.placement.place")}
                </button>
              </p>
            ) : null}
          </>
        )}
      </Async>
      {done ? (
        <p role="status" className="decision-notice" data-outcome="applied">
          {done}
        </p>
      ) : null}
      {canMount && open?.kind === "putBack" && last ? (
        <QuickPlaceDialog
          mode="putBack"
          rack={{ id: last.rack.id, display: last.rack.display }}
          device={{ id: device.id, display: device.display }}
          initialPosition={last.position}
          initialFace={last.face}
          returnFocus={() => putBackButton.current}
          onClose={() => setOpen(null)}
          onDone={(message) => {
            setDone(message);
            setOpen(null);
          }}
          onFullForm={(carry) =>
            // The choices carry over to the form, on the same rack.
            setOpen({
              kind: "place",
              rackId: last.rack.id,
              position: carry.position,
              face: carry.face,
              height: carry.heightU,
            })
          }
        />
      ) : null}
      {canMount && open?.kind === "place" ? (
        <EditPanel
          title={t("edit.place.titleDevice", { device: device.display })}
        >
          <PlaceDeviceForm
            device={{ id: device.id, display: device.display }}
            initialRack={open.rackId}
            initialPosition={open.position}
            initialFace={open.face}
            initialHeight={open.height}
            onClose={() => setOpen(null)}
          />
        </EditPanel>
      ) : null}
      {can && open?.kind === "remove" ? (
        <EditPanel
          title={t("edit.placement.removeTitle", {
            device: device.display,
            rack: open.mount.rackName,
          })}
        >
          <p>{t("edit.placement.removeHint")}</p>
          <BatchReview
            batch={unplaceDevice(device.id, open.mount.rackId)}
            warnings={
              source && open.mount.source !== source
                ? [
                    t("edit.place.otherSource", {
                      rack: open.mount.rackName,
                      source: open.mount.source,
                    }),
                  ]
                : []
            }
            onCancel={() => setOpen(null)}
          />
        </EditPanel>
      ) : null}
    </section>
  );
}

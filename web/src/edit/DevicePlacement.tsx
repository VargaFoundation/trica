// The device page's "Rack placement" section: where the device is mounted,
// and, for a caller who may propose, place it, move it, or take it out.
import { useState } from "react";
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
import { usePlacement, type CurrentMount } from "./usePlacement";

type Open = { kind: "place" } | { kind: "remove"; mount: CurrentMount } | null;

export function DevicePlacement({ device }: { device: ObjectView }) {
  const { t, number } = useI18n();
  const can = useCanDeclare();
  const canMount = useCanMount();
  const source = callerSource(useMe().data);
  const placement = usePlacement(device.id);
  const [open, setOpen] = useState<Open>(null);
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
            {canMount && open === null ? (
              <button
                type="button"
                className="primary"
                onClick={() => setOpen({ kind: "place" })}
              >
                {p.mounts.length > 0
                  ? t("edit.placement.move")
                  : t("edit.placement.place")}
              </button>
            ) : null}
          </>
        )}
      </Async>
      {canMount && open?.kind === "place" ? (
        <EditPanel
          title={t("edit.place.titleDevice", { device: device.display })}
        >
          <PlaceDeviceForm
            device={{ id: device.id, display: device.display }}
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

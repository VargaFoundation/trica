// Where a device that is in no rack stands (ADR 0058): the device page's
// "Location" section, and the "Devices here" list of a site, building or room.
// A rack decides a device's site whatever a place says: a device that is mounted
// shows its old places as ignored, and they can still be withdrawn.
import { useState } from "react";
import type { ObjectView } from "../api/types";
import { Async } from "../components/Async";
import { ObjectLink } from "../components/ObjectLink";
import { useMe } from "../hooks";
import { useI18n } from "../i18n";
import { BatchReview } from "./BatchReview";
import { callerSource, unlocateDevice } from "./batch";
import { EditPanel, useCanDeclare } from "./EditPanel";
import { PlaceInForm } from "./PlaceInForm";
import { currentPlaces, devicesHere, isMounted, useCanLocate, useObjectRelations, type CurrentPlace } from "./useLocation";

type Open = { kind: "place" } | { kind: "remove"; place: CurrentPlace } | null;

export function DeviceLocation({ device }: { device: ObjectView }) {
  const { t } = useI18n();
  const can = useCanDeclare();
  const canLocate = useCanLocate();
  const source = callerSource(useMe().data);
  const relations = useObjectRelations(device.id);
  const [open, setOpen] = useState<Open>(null);
  return (
    <Async query={relations}>
      {(page) => {
        const places = currentPlaces(page.items);
        const mounted = isMounted(page.items);
        // A mounted device with nothing placed has nothing to say here: its rack does.
        if (mounted && places.length === 0) return null;
        return (
          <section aria-labelledby="location-h" className="placement">
            <h2 id="location-h">{t("edit.location.title")}</h2>
            {mounted ? <p role="note">{t("edit.location.overriddenByRack")}</p> : null}
            {places.length === 0 ? (
              <p>{t("edit.location.none")}</p>
            ) : (
              <ul>
                {places.map((p) => (
                  <li key={`${p.id}/${p.source}`} data-place={p.id}>
                    <ObjectLink id={p.id} display={p.display} /> <span className="muted">({p.source})</span>
                    {mounted ? <span className="muted"> {t("edit.location.ignored")}</span> : null}
                    {can ? (
                      <>
                        {" "}
                        <button type="button" onClick={() => setOpen({ kind: "remove", place: p })}>
                          {t("edit.location.remove")} <span className="sr-only">{p.display}</span>
                        </button>
                      </>
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
            {canLocate && !mounted && open === null ? (
              <button type="button" className="primary" onClick={() => setOpen({ kind: "place" })}>
                {places.length > 0 ? t("edit.location.move") : t("edit.location.place")}
              </button>
            ) : null}
            {canLocate && !mounted && open?.kind === "place" ? (
              <EditPanel title={t("edit.location.formTitle", { device: device.display })}>
                <PlaceInForm device={{ id: device.id, display: device.display }} current={places} onClose={() => setOpen(null)} />
              </EditPanel>
            ) : null}
            {can && open?.kind === "remove" ? (
              <EditPanel title={t("edit.location.removeTitle", { device: device.display, place: open.place.display })}>
                <p>{t("edit.location.removeHint")}</p>
                <BatchReview
                  batch={unlocateDevice(device.id, { type: open.place.type, id: open.place.id })}
                  warnings={source && open.place.source !== source ? [t("edit.location.otherSource", { place: open.place.display, source: open.place.source })] : []}
                  onCancel={() => setOpen(null)}
                />
              </EditPanel>
            ) : null}
          </section>
        );
      }}
    </Async>
  );
}

/** The devices placed in a site, building or room, from its incoming located_in relations. */
export function LocatedHere({ obj }: { obj: ObjectView }) {
  const { t } = useI18n();
  const relations = useObjectRelations(obj.id);
  return (
    <Async query={relations}>
      {(page) => {
        const here = devicesHere(page.items);
        if (here.length === 0) return null;
        return (
          <section aria-labelledby="here-h">
            <h2 id="here-h">{t("edit.location.here")}</h2>
            <ul>
              {here.map((r) => (
                <li key={`${r.other.id}/${r.source}`} data-device={r.other.id}>
                  <ObjectLink id={r.other.id} display={r.other.display} /> <span className="muted">({r.source})</span>
                </li>
              ))}
            </ul>
          </section>
        );
      }}
    </Async>
  );
}

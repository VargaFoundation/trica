// Says where a device that is in no rack stands: a site, a building or a room.
// The choice lists every place the inventory has, grouped by site ("site ›
// building › room"). The batch is one declared located_in; moving replaces the
// caller's previous place, and a place another source declared stays until that
// source withdraws it (the page says so before anything is sent).
import { useQuery } from "@tanstack/react-query";
import { useId, useState } from "react";
import { api } from "../api/endpoints";
import type { ObjectView } from "../api/types";
import { Async } from "../components/Async";
import { useMe, useSchema } from "../hooks";
import { useI18n } from "../i18n";
import { BatchReview } from "./BatchReview";
import { callerSource, locateDevice, type PlaceType } from "./batch";
import { editRules } from "./rules";
import type { CurrentPlace } from "./useLocation";

interface Option {
  id: string;
  type: PlaceType;
  /** "site › building › room". */
  path: string;
}

interface Group {
  key: string;
  title: string;
  options: Option[];
}

/** The places of the inventory, grouped by site. Places whose site is unknown go in a last group. */
export function placeGroups(places: ObjectView[], noSite: string): Group[] {
  const byId = new Map(places.map((p) => [p.id, p]));
  const pathOf = (p: ObjectView): string => {
    const chain = [p.display];
    for (let at = p.parent_id ? byId.get(p.parent_id) : undefined, n = 0; at && n < 8; at = at.parent_id ? byId.get(at.parent_id) : undefined, n++) chain.unshift(at.display);
    return chain.join(" › ");
  };
  const groups = new Map<string, Group>();
  for (const p of places) {
    const site = p.type === "site" ? p.id : (p.site_id ?? "");
    let g = groups.get(site);
    if (!g) {
      g = { key: site, title: site ? (byId.get(site)?.display ?? site) : noSite, options: [] };
      groups.set(site, g);
    }
    g.options.push({ id: p.id, type: p.type as PlaceType, path: pathOf(p) });
  }
  const list = [...groups.values()];
  for (const g of list) g.options.sort((a, b) => a.path.localeCompare(b.path, undefined, { numeric: true }));
  return list.sort((a, b) => Number(!a.key) - Number(!b.key) || a.title.localeCompare(b.title, undefined, { numeric: true }));
}

export function PlaceInForm({ device, current, onClose }: { device: { id: string; display: string }; current: CurrentPlace[]; onClose: () => void }) {
  const schema = useSchema();
  return <Async query={schema}>{(doc) => <PlaceIn device={device} current={current} types={editRules(doc).locationTypes} onClose={onClose} />}</Async>;
}

function PlaceIn({ device, current, types, onClose }: { device: { id: string; display: string }; current: CurrentPlace[]; types: string[]; onClose: () => void }) {
  const { t } = useI18n();
  const uid = useId();
  const source = callerSource(useMe().data);
  const [choice, setChoice] = useState("");
  const places = useQuery({
    queryKey: ["places", types],
    queryFn: async ({ signal }) => {
      const pages = await Promise.all(types.map((type) => api.objects({ type, sort: "display", limit: 1000 }, signal)));
      return pages.flatMap((p) => p.items);
    },
    staleTime: 30_000,
  });
  return (
    <Async query={places}>
      {(items) => {
        const groups = placeGroups(items, t("edit.location.noSite"));
        const picked = items.find((p) => p.id === choice);
        const others = source ? current.filter((c) => c.source !== source) : current;
        return (
          <>
            {items.length === 0 ? (
              <p role="note">{t("edit.location.noPlaces")}</p>
            ) : (
              <div className="field">
                <label htmlFor={`${uid}-place`}>{t("edit.location.pick")}</label>
                <select id={`${uid}-place`} value={choice} onChange={(e) => setChoice(e.target.value)} aria-describedby={`${uid}-hint`}>
                  <option value="">{t("edit.location.choose")}</option>
                  {groups.map((g) => (
                    <optgroup key={g.key || "none"} label={g.title}>
                      {g.options.map((o) => (
                        <option key={o.id} value={o.id}>
                          {o.path}
                        </option>
                      ))}
                    </optgroup>
                  ))}
                </select>
                <p id={`${uid}-hint`} className="muted">
                  {t("edit.location.pickHint")}
                </p>
              </div>
            )}
            <BatchReview
              batch={picked ? locateDevice(device.id, { type: picked.type as PlaceType, id: picked.id }) : null}
              warnings={others.map((c) => t("edit.location.otherSource", { place: c.display, source: c.source }))}
              onCancel={onClose}
            />
          </>
        );
      }}
    </Async>
  );
}

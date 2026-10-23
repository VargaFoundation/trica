import type { TopologyMap } from "../../api/types";
import { Table } from "../../components/ui";
import { useI18n, type MessageKey } from "../../i18n";
import { formatBps, formatSpeed, formatUtil } from "../live";

/** Rows listed at most: a floor of 200 devices has hundreds of links. */
export const LIST_MAX = 50;

/** The lines of the scene as a table: the way to them without a pointer, and a reading of the figures. */
export function MapLinksTable({ map, selected, onSelect }: { map: TopologyMap; selected?: string; onSelect: (id: string) => void }) {
  const { t, number, locale } = useI18n();
  const name = (id: string) => map.nodes.find((n) => n.id === id)?.display ?? id;
  const ids = new Set(map.nodes.map((n) => n.id));
  const all = map.edges.filter((e) => ids.has(e.a) && ids.has(e.b));
  const shown = all.slice(0, LIST_MAX);
  return (
    <section className="iso-links" aria-labelledby="iso-links-title">
      <h2 id="iso-links-title">{t("topology.map.links.title")}</h2>
      {shown.length === 0 ? (
        <p className="hint">{t("topology.map.links.none")}</p>
      ) : (
        <Table>
          <thead>
            <tr>
              <th scope="col">{t("topology.map.links.from")}</th>
              <th scope="col">{t("topology.map.links.to")}</th>
              <th scope="col">{t("topology.map.panel.medium")}</th>
              <th scope="col">{t("topology.map.links.count")}</th>
              <th scope="col">{t("topology.map.panel.capacity")}</th>
              <th scope="col">{t("topology.map.panel.traffic")}</th>
              <th scope="col">{t("topology.map.panel.util")}</th>
              <th scope="col">{t("topology.map.panel.state")}</th>
              <th scope="col">
                <span className="sr-only">{t("topology.map.links.open")}</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {shown.map((e) => (
              <tr key={e.id} data-id={e.id} aria-selected={selected === e.id}>
                <td>{name(e.a)}</td>
                <td>{name(e.b)}</td>
                <td>{t(`topology.map.medium.${e.medium}` as MessageKey)}</td>
                <td>{number(e.links)}</td>
                <td>{e.capacity_mbps !== undefined ? formatSpeed(e.capacity_mbps, locale) : "-"}</td>
                <td>{e.measured ? `${formatBps(e.a_to_b_bps ?? 0, locale)} / ${formatBps(e.b_to_a_bps ?? 0, locale)}` : "-"}</td>
                <td>{e.util !== undefined ? formatUtil(e.util, locale) : "-"}</td>
                <td>{t(`topology.map.oper.${e.oper}` as MessageKey)}</td>
                <td>
                  <button type="button" className="button" onClick={() => onSelect(e.id)} aria-label={t("topology.map.links.openFor", { a: name(e.a), b: name(e.b) })}>
                    {t("topology.map.links.open")}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      {all.length > shown.length ? <p className="hint">{t("topology.map.links.more", { shown: number(shown.length), total: number(all.length) })}</p> : null}
    </section>
  );
}

import { useEffect, useRef } from "react";
import { Link } from "react-router";
import type { MapEdge, MapNode, TopologyMap } from "../../api/types";
import { ObjectLink } from "../../components/ObjectLink";
import { HealthPill } from "../../components/ui";
import { useI18n, type MessageKey } from "../../i18n";
import { formatBps, formatSpeed, formatUtil } from "../live";
import { edgeUtilClass } from "./flow";

/** Ids that are not objects: the map's own places. */
const isObjectId = (id: string) => !/^(internet|unplaced|area:|portal:)/.test(id);

/** The side panel of the map: what a place holds, or what a line carries. */
export function MapPanel({ map, selected, onClose, onDrill }: { map: TopologyMap; selected: string; onClose: () => void; onDrill: (n: MapNode) => void }) {
  const { t } = useI18n();
  const heading = useRef<HTMLHeadingElement>(null);
  // Whatever opened it (a click, a key, a button of the table), the reader is taken to it.
  useEffect(() => heading.current?.focus(), [selected]);
  const node = map.nodes.find((n) => n.id === selected);
  const edge = map.edges.find((e) => e.id === selected);
  if (!node && !edge) return null;
  return (
    <aside className="iso-panel card" aria-labelledby="iso-panel-title">
      <div className="card-head">
        <h2 id="iso-panel-title" ref={heading} tabIndex={-1}>
          {t("topology.map.panel")}
        </h2>
        <button type="button" className="button" onClick={onClose}>
          {t("topology.map.panel.close")}
        </button>
      </div>
      {node ? <NodeDetails node={node} onDrill={onDrill} /> : edge ? <EdgeDetails edge={edge} map={map} /> : null}
    </aside>
  );
}

function NodeDetails({ node, onDrill }: { node: MapNode; onDrill: (n: MapNode) => void }) {
  const { t, number } = useI18n();
  const c = node.counts;
  const m = node.mount;
  return (
    <>
      <h3>{node.display}</h3>
      <p>
        {t(`topology.map.kind.${node.kind}` as MessageKey)} <HealthPill value={node.health.worst} />
      </p>
      <dl className="facts">
        {node.kind === "site" ? <Fact label={t("topology.map.panel.rooms")} value={number(c.rooms)} /> : null}
        {node.kind === "site" || node.kind === "room" || node.kind === "area" ? <Fact label={t("topology.map.panel.racks")} value={number(c.racks)} /> : null}
        {node.kind !== "device" ? <Fact label={t("topology.map.panel.devices")} value={number(c.devices)} /> : null}
        {node.kind !== "device" ? <Fact label={t("topology.map.panel.internalLinks")} value={number(c.internal_links)} /> : null}
        {node.role ? <Fact label={t("topology.map.panel.role")} value={node.role} /> : null}
        {m ? (
          <Fact
            label={t("topology.map.kind.device")}
            value={t("topology.map.panel.mount", {
              position: m.position,
              height: m.u_height ?? 1,
              face: t(m.face === "rear" ? "topology.map.panel.faceRear" : "topology.map.panel.faceFront"),
            })}
          />
        ) : null}
        {node.power_w !== undefined ? <Fact label={t("topology.map.panel.power")} value={`${number(node.power_w)} W`} /> : null}
      </dl>
      <p className="iso-panel-actions">
        {node.drill ? (
          <button type="button" className="button primary" onClick={() => onDrill(node)}>
            {t("topology.map.open", { name: node.display })}
          </button>
        ) : null}
        {isObjectId(node.id) ? (
          <Link className="button" to={`/objects/${encodeURIComponent(node.id)}`}>
            {t("topology.map.panel.page")}
          </Link>
        ) : null}
      </p>
    </>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}

function EdgeDetails({ edge: e, map }: { edge: MapEdge; map: TopologyMap }) {
  const { t, number, locale } = useI18n();
  const name = (id: string) => map.nodes.find((n) => n.id === id)?.display ?? id;
  const a = name(e.a);
  const b = name(e.b);
  const util = edgeUtilClass(e);
  const cap = e.capacity_mbps !== undefined ? formatSpeed(e.capacity_mbps, locale) : undefined;
  return (
    <>
      <h3>{t("topology.panel.link", { from: a, to: b })}</h3>
      <dl className="facts" data-util={util}>
        <Fact label={t("topology.map.panel.medium")} value={t(`topology.map.medium.${e.medium}` as MessageKey)} />
        <Fact label={t("topology.map.panel.state")} value={t(`topology.map.oper.${e.oper}` as MessageKey)} />
        <Fact label={t("topology.map.panel.confidence")} value={e.confidence} />
        <Fact label={t("topology.map.links.count")} value={number(e.links)} />
        {cap ? <Fact label={t("topology.map.panel.capacity")} value={e.capacity_partial ? t("topology.map.panel.capacityPartial", { value: cap }) : cap} /> : null}
        {e.measured ? (
          <>
            <Fact label={t("topology.map.panel.aToB", { a, b })} value={formatBps(e.a_to_b_bps ?? 0, locale)} />
            <Fact label={t("topology.map.panel.aToB", { a: b, b: a })} value={formatBps(e.b_to_a_bps ?? 0, locale)} />
            <Fact label={t("topology.map.panel.util")} value={e.util === undefined ? t("topology.map.panel.notMeasured") : formatUtil(e.util, locale)} />
          </>
        ) : (
          <Fact label={t("topology.map.panel.traffic")} value={t("topology.map.panel.notMeasured")} />
        )}
      </dl>
      {e.tunnel ? (
        <p>
          {t("topology.map.panel.tunnel", { kind: e.tunnel.kind, status: e.tunnel.status })}
          {e.tunnel.peer ? ` · ${t("topology.map.panel.tunnelPeer", { peer: e.tunnel.peer })}` : ""}
          {!e.tunnel.resolved ? ` · ${t("topology.map.panel.tunnelUnresolved")}` : ""}
        </p>
      ) : null}
      {e.uplink ? (
        <p>
          {t("topology.map.panel.uplink", { status: e.uplink.status })}
          {e.uplink.provider ? ` · ${t("topology.map.panel.provider", { provider: e.uplink.provider })}` : ""}
          {e.uplink.latency_ms !== undefined ? ` · ${t("topology.map.panel.latency", { value: t("unit.ms", { value: number(e.uplink.latency_ms) }) })}` : ""}
          {e.uplink.loss_pct !== undefined ? ` · ${t("topology.map.panel.loss", { value: `${number(e.uplink.loss_pct)} %` })}` : ""}
        </p>
      ) : null}
      {e.members.length > 0 ? (
        <>
          <h4>{t("topology.map.panel.members")}</h4>
          <ul className="iso-members">
            {e.members.map((m, i) => (
              <li key={`${m.a_port.id}-${m.b_port.id}-${i}`}>
                <ObjectLink id={m.a_device.id} display={m.a_device.display} />:<ObjectLink id={m.a_port.id} display={m.a_port.display} /> ↔ <ObjectLink id={m.b_device.id} display={m.b_device.display} />:
                <ObjectLink id={m.b_port.id} display={m.b_port.display} />
              </li>
            ))}
          </ul>
          {e.members_truncated ? <p className="hint">{t("topology.map.panel.membersMore")}</p> : null}
        </>
      ) : null}
    </>
  );
}

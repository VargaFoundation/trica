// The firmware of a device as a table: each component with its version, the
// day it was released and its age, and whether another device of the same
// model already runs a newer release. Drawn for every json attribute holding
// version records (see versions.ts); the generic attribute cards leave those
// out, and the "name=version" list beside them too.
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/endpoints";
import type { ObjectView } from "../api/types";
import { Card, Pill, Table } from "../components/ui";
import { Empty } from "../components/Time";
import type { SchemaLookup } from "../hooks";
import { useI18n } from "../i18n";
import { FLEET_LIMIT } from "../inventory/Fleet";
import { ageDays, fleetHas, fleetNewest, peerRecords, versionRecords, type VersionRecord } from "./versions";

/** Past this age a firmware is flagged: two years without an update. */
export const STALE_DAYS = 730;

/** The attributes of obj drawn as a version table, in schema order. */
export function versionKeys(obj: ObjectView, lookup: SchemaLookup): string[] {
  const order = new Map((lookup.typeDoc(obj.type)?.attributes ?? []).map((a, i) => [a.name, i]));
  return Object.keys(obj.attributes)
    .filter((k) => versionRecords(obj.attributes[k]) !== undefined)
    .sort((a, b) => (order.get(a) ?? 1e6) - (order.get(b) ?? 1e6) || a.localeCompare(b));
}

export function FirmwareSection({ obj, lookup }: { obj: ObjectView; lookup: SchemaLookup }) {
  const keys = versionKeys(obj, lookup);
  // The same query as the fleet view: one request for both, refreshed together.
  const devices = useQuery({
    queryKey: ["fleet", "devices"],
    queryFn: ({ signal }) => api.objects({ type: "device", limit: FLEET_LIMIT }, signal),
    enabled: keys.length > 0 && typeof obj.attributes.model === "string",
  });
  if (keys.length === 0) return null;
  return (
    <>
      {keys.map((k) => (
        <VersionTable key={k} obj={obj} name={k} lookup={lookup} devices={devices.data?.items} />
      ))}
    </>
  );
}

function VersionTable({ obj, name, lookup, devices }: { obj: ObjectView; name: string; lookup: SchemaLookup; devices: ObjectView[] | undefined }) {
  const { t, tp, locale, number } = useI18n();
  const records = [...(versionRecords(obj.attributes[name]) ?? [])].sort((a, b) => a.name.localeCompare(b.name, locale, { numeric: true }));
  const label = lookup.attributeLabel(obj.type, name);
  const peers = devices ? peerRecords(obj, devices, name) : [];
  // An age in days: the time the page opened is precise enough.
  const [now] = useState(() => Date.now());
  const day = new Intl.DateTimeFormat(locale, { year: "numeric", month: "short", day: "numeric", timeZone: "UTC" });
  const rows = records.map((r) => ({ r, age: ageDays(r.released, now), newer: fleetNewest(r, peers), known: fleetHas(r.name, peers) }));
  const stale = rows.filter((x) => x.age !== undefined && x.age > STALE_DAYS).length;
  const behind = rows.filter((x) => x.newer).length;
  const headId = `versions-${name}`;
  return (
    <Card title={label} titleId={headId} className="versions-card">
      <p className="summary">
        {tp("firmware.summary", records.length, { stale: number(stale), behind: number(behind) })}
        {peers.length > 0 ? ` ${tp("firmware.peers", peers.length, { model: String(obj.attributes.model) })}` : ""}
      </p>
      <Table className="versions" aria-labelledby={headId}>
        <thead>
          <tr>
            <th scope="col">{t("firmware.component")}</th>
            <th scope="col">{t("firmware.version")}</th>
            <th scope="col">{t("firmware.released")}</th>
            <th scope="col">{t("firmware.age")}</th>
            <th scope="col">{t("firmware.fleet")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(({ r, age, newer, known }) => (
            <tr key={`${r.name}=${r.version}`} data-stale={age !== undefined && age > STALE_DAYS ? "" : undefined} data-behind={newer ? "" : undefined}>
              <th scope="row" data-label={t("firmware.component")}>
                {r.name}
                {r.location ? <span className="key">{r.location}</span> : null}
              </th>
              <td data-label={t("firmware.version")}>
                <code>{r.version}</code>
              </td>
              <td data-label={t("firmware.released")}>{r.released ? <time dateTime={r.released}>{day.format(new Date(`${r.released}T00:00:00Z`))}</time> : <Empty />}</td>
              <td data-label={t("firmware.age")}>
                <Age days={age} />
              </td>
              <td data-label={t("firmware.fleet")}>
                <FleetCell record={r} newer={newer} known={known} />
              </td>
            </tr>
          ))}
        </tbody>
      </Table>
    </Card>
  );
}

function Age({ days }: { days: number | undefined }) {
  const { t, locale } = useI18n();
  if (days === undefined) return <Empty />;
  const unit = (u: "year" | "month" | "day", n: number, digits: number) =>
    new Intl.NumberFormat(locale, { style: "unit", unit: u, unitDisplay: "long", maximumFractionDigits: digits }).format(n);
  const text = days >= 365 ? unit("year", days / 365.25, 1) : days >= 31 ? unit("month", Math.floor(days / 30.44), 0) : unit("day", days, 0);
  if (days <= STALE_DAYS) return <span className="num">{text}</span>;
  return (
    <Pill tone="warn" value="stale">
      {text}
      <span className="sr-only"> ({t("firmware.stale")})</span>
    </Pill>
  );
}

function FleetCell({ record, newer, known }: { record: VersionRecord; newer: ReturnType<typeof fleetNewest>; known: boolean }) {
  const { t, tp } = useI18n();
  if (newer)
    return (
      <span className="versions-newer">
        <Pill tone="warn" value="behind">
          {t("firmware.behind")}
        </Pill>{" "}
        {tp("firmware.newer", newer.count, { version: newer.version })}
      </span>
    );
  if (!known) return <Empty />;
  return (
    <Pill tone="ok" value="latest">
      {t("firmware.latest")}
      <span className="sr-only"> ({record.version})</span>
    </Pill>
  );
}

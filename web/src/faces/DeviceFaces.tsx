// A device drawn from its model's faces, with its parts where they really
// sit: each bay, power supply, fan, port and slot the library has a layout
// for is a spot over the image, coloured by the state of the part in it. The
// spots of a face are one stop in the tab order; the arrow keys move between
// them, a pointer hovers them, and the panel beside the drawing gives the part's
// details. The parts whose place their name does not tell are listed under it.
import { useQuery } from "@tanstack/react-query";
import { useRef, useState, type KeyboardEvent } from "react";
import { Link } from "react-router";
import { api } from "../api/endpoints";
import type { DeviceFace, FacePart, ObjectView } from "../api/types";
import { ErrorBox } from "../components/Async";
import { ObjectLink } from "../components/ObjectLink";
import { Quantity } from "../components/Quantity";
import { Alert, Card, HealthPill, Pill } from "../components/ui";
import type { SchemaLookup } from "../hooks";
import { useI18n, type MessageKey } from "../i18n";
import { healthOf } from "../inventory/health";
import { useFaceImage } from "../racks/faceImages";
import { FaceCanvas } from "../racks/RackElevation";
import { groupUnplaced, PART_FACTS, placeSpots, SPOT_STATES, unplaced, type PlacedSpot, type SpotState, type UnplacedWhy } from "./spots";
import "./faces.css";

const KIND_TEXT: Record<string, MessageKey> = {
  bay: "faces.kind.bay",
  psu: "faces.kind.psu",
  fan: "faces.kind.fan",
  port: "faces.kind.port",
  slot: "faces.kind.slot",
};

const WHY_TEXT: Record<UnplacedWhy, { title: MessageKey; hint: MessageKey }> = {
  os: { title: "faces.why.os", hint: "faces.why.osHint" },
  no_spot: { title: "faces.why.noSpot", hint: "faces.why.noSpotHint" },
  unread: { title: "faces.why.unread", hint: "faces.unplacedHint" },
};

const STATE_TEXT: Record<SpotState, MessageKey> = {
  ok: "healthState.ok",
  warning: "healthState.warning",
  critical: "healthState.critical",
  unknown: "healthState.unknown",
  up: "faces.state.up",
  down: "faces.state.down",
  empty: "faces.state.empty",
};

export function DeviceFacesSection({ obj, lookup }: { obj: ObjectView; lookup: SchemaLookup }) {
  const { t } = useI18n();
  const q = useQuery({
    queryKey: ["faces", obj.id],
    queryFn: ({ signal }) => api.objectFaces(obj.id, signal),
    enabled: obj.type === "device",
  });
  if (obj.type !== "device") return null;
  if (q.isError)
    return (
      <Card title={t("faces.title")} titleId="faces-title">
        <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      </Card>
    );
  if (!q.data || q.data.faces.length === 0) return null;
  return <Faces obj={obj} faces={q.data.faces} parts={q.data.parts} truncated={q.data.truncated} model={q.data.model} modelName={q.data.model_name} lookup={lookup} />;
}

function Faces({
  obj,
  faces,
  parts,
  truncated,
  model,
  modelName,
  lookup,
}: {
  obj: ObjectView;
  faces: DeviceFace[];
  parts: FacePart[];
  truncated: boolean;
  model?: { id: string; display: string };
  modelName?: string;
  lookup: SchemaLookup;
}) {
  const { t, tp, locale, number } = useI18n();
  const placed = faces.map((f) => ({ face: f, spots: placeSpots(f, parts, locale) }));
  const all = placed.flatMap((p) => p.spots);
  const [active, setActive] = useState<string | null>(null);
  const current = all.find((s) => s.key === active);
  const loose = unplaced(parts);
  const filled = all.filter((s) => s.parts.length > 0);
  const attention = filled.filter((s) => s.state === "critical" || s.state === "warning").length;
  const shown = new Set(all.map((s) => s.state));
  const detailsId = `faces-details-${obj.id}`;
  return (
    <Card title={t("faces.title")} titleId="faces-title" className="faces-card">
      <p className="faces-summary">
        {model ? (
          <>
            {t("faces.model")} <ObjectLink id={model.id} display={modelName || model.display} />.{" "}
          </>
        ) : null}
        {all.length > 0 ? tp("faces.summary", all.length, { filled: number(filled.length), attention: number(attention) }) : t("faces.noLayout")}
      </p>
      <div className="faces-layout">
        <div className="faces-drawings">
          {placed.map(({ face, spots }) => (
            <FaceDrawing key={face.face} face={face} spots={spots} active={active} detailsId={detailsId} onActive={setActive} />
          ))}
          {all.length > 0 ? (
            <ul className="faces-legend" aria-label={t("faces.legend")}>
              {SPOT_STATES.filter((s) => shown.has(s)).map((s) => (
                <li key={s}>
                  <svg viewBox="0 0 14 10" width="14" height="10" aria-hidden="true">
                    <rect className={`face-spot face-spot-${s}`} x="1" y="1" width="12" height="8" rx="1.5" />
                  </svg>
                  {t(STATE_TEXT[s])}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
        {all.length > 0 ? (
          <div className="faces-details" id={detailsId}>
            {current ? <SpotDetails spot={current} lookup={lookup} /> : <p className="hint">{t("faces.hint")}</p>}
          </div>
        ) : null}
      </div>
      {loose.length > 0 ? (
        <div className="faces-unplaced">
          <h3>{tp("faces.unplaced", loose.length)}</h3>
          {groupUnplaced(parts, locale).map((g) => (
            <section key={g.why} className="faces-unplaced-group" aria-labelledby={`faces-unplaced-${g.why}-${obj.id}`}>
              <h4 id={`faces-unplaced-${g.why}-${obj.id}`}>{t(WHY_TEXT[g.why].title)}</h4>
              <p className="hint">{t(WHY_TEXT[g.why].hint)}</p>
              <ul>
                {g.parts.map((p) => (
                  <UnplacedPart key={p.part.id} p={p} lookup={lookup} />
                ))}
              </ul>
            </section>
          ))}
        </div>
      ) : null}
      {truncated ? (
        <Alert role="note">
          <p>{t("faces.truncated")}</p>
        </Alert>
      ) : null}
    </Card>
  );
}

function FaceDrawing({
  face,
  spots,
  active,
  detailsId,
  onActive,
}: {
  face: DeviceFace;
  spots: PlacedSpot[];
  active: string | null;
  detailsId: string;
  onActive: (key: string) => void;
}) {
  const { t } = useI18n();
  const bitmap = useFaceImage(face.image);
  const links = useRef(new Map<string, SVGAElement>());
  const targets = spots.filter((s) => s.parts.length > 0);
  // The spot that takes the tab: the active one when it is on this face, else the first.
  const [stop, setStop] = useState<string | undefined>(undefined);
  const tabbable = targets.some((s) => s.key === stop) ? stop : targets[0]?.key;
  // A face without a layout is drawn at its image's size; without the image there is nothing to draw.
  const width = face.width || bitmap?.width || 0;
  const height = face.height || bitmap?.height || 0;
  if (!width || !height) return null;
  const faceName = t(face.face === "rear" ? "faces.rear" : "faces.front");

  const move = (e: KeyboardEvent) => {
    const i = targets.findIndex((s) => s.key === tabbable);
    const last = targets.length - 1;
    const next = { ArrowRight: i + 1, ArrowDown: i + 1, ArrowLeft: i - 1, ArrowUp: i - 1, Home: 0, End: last }[e.key];
    if (next === undefined || i < 0) return;
    e.preventDefault();
    const to = targets[Math.min(Math.max(next, 0), last)];
    if (!to) return;
    setStop(to.key);
    onActive(to.key);
    links.current.get(to.key)?.focus();
  };

  return (
    <figure className="device-face" data-face={face.face}>
      <figcaption>{faceName}</figcaption>
      <svg
        className="device-face-svg"
        viewBox={`0 0 ${width} ${height}`}
        role="group"
        aria-label={t("faces.drawing", { face: faceName })}
        onKeyDown={move}
        data-drawn={bitmap ? "image" : "plate"}
      >
        {bitmap ? (
          <FaceCanvas bitmap={bitmap} x={0} y={0} width={width} height={height} scale={face.width ? 3 : 1} />
        ) : (
          <rect className="device-face-plate" x={0} y={0} width={width} height={height} />
        )}
        {spots.map((s) => {
          const label = spotLabel(t, s);
          const r = <rect x={s.spot.x} y={s.spot.y} width={s.spot.w} height={s.spot.h} rx={Math.min(s.spot.w, s.spot.h) * 0.08} />;
          const first = s.parts[0];
          if (!first)
            return (
              <g key={s.key} className={`face-spot face-spot-empty${s.key === active ? " face-spot-active" : ""}`} data-spot={s.spot.name} data-kind={s.spot.kind} aria-hidden="true" onMouseEnter={() => onActive(s.key)}>
                {r}
              </g>
            );
          return (
            <Link
              key={s.key}
              to={`/objects/${encodeURIComponent(first.id)}`}
              ref={(el) => {
                if (el) links.current.set(s.key, el as unknown as SVGAElement);
                else links.current.delete(s.key);
              }}
              tabIndex={s.key === tabbable ? 0 : -1}
              className={`face-spot face-spot-${s.state}${s.key === active ? " face-spot-active" : ""}`}
              data-spot={s.spot.name}
              data-kind={s.spot.kind}
              aria-label={label}
              aria-describedby={s.key === active ? detailsId : undefined}
              onMouseEnter={() => onActive(s.key)}
              onFocus={() => {
                setStop(s.key);
                onActive(s.key);
              }}
            >
              {r}
            </Link>
          );
        })}
      </svg>
    </figure>
  );
}

type T = ReturnType<typeof useI18n>["t"];

/** "Bay 1:3: Disk 3, Critical", for the accessible name of a spot. */
function spotLabel(t: T, s: PlacedSpot): string {
  const where = t(KIND_TEXT[s.spot.kind] ?? "faces.kind.port", { name: s.spot.name });
  const what = s.parts.map((p) => p.display).join(", ");
  return t("faces.spotLabel", { where, what, state: t(STATE_TEXT[s.state]) });
}

function SpotDetails({ spot: s, lookup }: { spot: PlacedSpot; lookup: SchemaLookup }) {
  const { t } = useI18n();
  const where = t(KIND_TEXT[s.spot.kind] ?? "faces.kind.port", { name: s.spot.name });
  const face = t(s.face === "rear" ? "faces.rear" : "faces.front");
  return (
    <>
      <h3>
        {where} <span className="key">{face}</span>
      </h3>
      {s.parts.length === 0 ? <p className="hint">{t("faces.emptySpot")}</p> : null}
      {s.parts.map((p) => (
        <div key={p.id} className="faces-part">
          <p>
            <Link to={`/objects/${encodeURIComponent(p.id)}`}>{p.display}</Link> <PartState part={p} />
          </p>
          <dl className="facts">
            {(PART_FACTS[p.type] ?? [])
              .filter((k) => p.attributes[k] !== undefined && p.attributes[k] !== null && p.attributes[k] !== "")
              .map((k) => (
                <div key={k}>
                  <dt>{lookup.attributeLabel(p.type, k)}</dt>
                  <dd>
                    <Quantity value={p.attributes[k]} unit={lookup.attribute(p.type, k)?.unit} />
                  </dd>
                </div>
              ))}
          </dl>
        </div>
      ))}
    </>
  );
}

/**
 * One part not placed: its link, its type, what tells a system disk from a
 * data disk (model and capacity), its state, and the other names the same
 * part has (the BMC's number of an OS port).
 */
function UnplacedPart({ p, lookup }: { p: FacePart; lookup: SchemaLookup }) {
  const { t } = useI18n();
  const a = p.part.attributes;
  const facts = (["model", "capacity_bytes"] as const).filter((k) => a[k] !== undefined && a[k] !== null && a[k] !== "");
  return (
    <li>
      <ObjectLink id={p.part.id} display={p.part.display} /> <span className="key">{lookup.typeLabel(p.part.type)}</span>
      {facts.map((k) => (
        <span key={k} className="faces-unplaced-fact">
          {" "}
          <Quantity value={a[k]} unit={lookup.attribute(p.part.type, k)?.unit} />
        </span>
      ))}{" "}
      <PartState part={p.part} />
      {p.also && p.also.length > 0 ? (
        <span className="faces-unplaced-also">
          {" "}
          {t("faces.also")}{" "}
          {p.also.map((o, i) => (
            <span key={o.id}>
              {i > 0 ? ", " : null}
              <ObjectLink id={o.id} display={o.display} />
            </span>
          ))}
        </span>
      ) : null}
    </li>
  );
}

/** The health pill of a part, or its link state for a port that has no health. */
function PartState({ part }: { part: ObjectView }) {
  const { t } = useI18n();
  const h = healthOf(part);
  if (h !== "unknown") return <HealthPill value={h} />;
  const oper = part.attributes.oper_status;
  if (typeof oper !== "string" || oper === "") return null;
  return <Pill tone={oper === "up" ? "ok" : "neutral"} value={oper}>{oper === "up" ? t("faces.state.up") : t("faces.state.down")}</Pill>;
}

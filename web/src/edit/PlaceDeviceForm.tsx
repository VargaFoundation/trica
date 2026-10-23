// Places a device in a rack: which rack, which U (1 at the bottom), which face,
// and its height when its model does not say. The device is one the inventory
// has (searched by name) or one declared here because no collector sees it (a
// PDU, a UPS, a patch panel, a shelf). Moving a device withdraws the caller's
// own claim that it is in the other rack; a claim another source made stays
// and the page says so before anything is sent.
import { useQuery } from "@tanstack/react-query";
import { useId, useState } from "react";
import { api } from "../api/endpoints";
import type { ApplyResult, AttributeDoc, Batch } from "../api/types";
import { Async } from "../components/Async";
import { ObjectLink } from "../components/ObjectLink";
import { useMe, useSchema } from "../hooks";
import { useI18n, type MessageKey } from "../i18n";
import {
  DEFAULT_RACK_U,
  formatRange,
  type Face,
  type Mounted,
} from "../racks/layout";
import { useRackContents } from "../racks/useRack";
import { DevicePicker } from "./DevicePicker";
import {
  AttributeField,
  EDITABLE_KINDS,
  enumText,
  parseField,
  type FieldError,
} from "./AttributeFields";
import { BatchReview } from "./BatchReview";
import {
  callerSource,
  placeDevice,
  type Attrs,
  type DeviceChoice,
} from "./batch";
import { editRules, NEW_DEVICE_FIELDS, type EditRules } from "./rules";
import { usePlacement } from "./usePlacement";
import {
  checkPlacement,
  firstFit,
  parseNumber,
  type PlacementError,
} from "./validate";

export interface RackRef {
  id: string;
  display: string;
}

const PLACEMENT_TEXT: Record<PlacementError, MessageKey> = {
  positionMissing: "edit.place.error.positionMissing",
  positionStep: "edit.place.error.positionStep",
  belowRack: "edit.place.error.belowRack",
  aboveRack: "edit.place.error.aboveRack",
  heightInvalid: "edit.place.error.heightInvalid",
  heightStep: "edit.place.error.heightStep",
};

export interface PlaceDeviceProps {
  /** The rack, when the form is opened from a rack page. */
  rack?: RackRef;
  /** The device, when the form is opened from a device page. */
  device?: { id: string; display: string };
  /** The rack the select starts on (a device page: where the device is now or was last). */
  initialRack?: string;
  initialPosition?: number;
  initialFace?: string;
  /** A device chosen already (the quick dialog's, carried over); the person may pick another. */
  initialDevice?: { id: string; display: string };
  /** The height the quick dialog was given, carried over. */
  initialHeight?: number;
  /** Start on "a device no collector sees". */
  initialDeclare?: boolean;
  /** Preview the change as soon as the form is valid (a move or a turn: everything is already said). */
  autoPreview?: boolean;
  onClose: () => void;
}

/** The form needs the schema (faces, units, roles): it waits for it. */
export function PlaceDeviceForm(props: PlaceDeviceProps) {
  const schema = useSchema();
  return (
    <Async query={schema}>
      {(doc) => <PlaceDevice {...props} rules={editRules(doc)} />}
    </Async>
  );
}

function PlaceDevice({
  rack: fixedRack,
  device: fixedDevice,
  initialRack,
  initialPosition,
  initialFace,
  initialDevice,
  initialHeight,
  initialDeclare,
  autoPreview,
  onClose,
  rules,
}: PlaceDeviceProps & { rules: EditRules }) {
  const { t, number, label } = useI18n();
  const uid = useId();
  const source = callerSource(useMe().data);

  /* ---- which rack ---- */
  const [rackChoice, setRackChoice] = useState(initialRack ?? "");
  const racks = useQuery({
    queryKey: ["objects", { type: "rack", sort: "name", limit: 1000 }],
    queryFn: ({ signal }) =>
      api.objects({ type: "rack", sort: "name", limit: 1000 }, signal),
    enabled: !fixedRack,
    staleTime: 30_000,
  });
  const rackId = fixedRack?.id ?? rackChoice;
  const rackObj = useQuery({
    queryKey: ["object", rackId],
    queryFn: ({ signal }) => api.object(rackId, signal),
    enabled: !!rackId,
    staleTime: 5 * 60_000,
  });
  const declaredU = rackObj.data?.attributes.u_height;
  const rackU =
    typeof declaredU === "number" && declaredU > 0
      ? Math.floor(declaredU)
      : DEFAULT_RACK_U;
  const contents = useRackContents(rackId);

  /* ---- which device ---- */
  const [mode, setMode] = useState<"existing" | "new">(
    initialDeclare ? "new" : "existing",
  );
  const [picked, setPicked] = useState<
    { id: string; display: string } | undefined
  >(fixedDevice ?? initialDevice);
  const deviceId = mode === "existing" ? picked?.id : undefined;
  const placement = usePlacement(deviceId);

  const deviceType = rules.type("device");
  const newFields = NEW_DEVICE_FIELDS.map((k) =>
    deviceType?.attributes.find((a) => a.name === k),
  ).filter((a): a is AttributeDoc => !!a && EDITABLE_KINDS.has(a.kind));
  const [newValues, setNewValues] = useState<Record<string, string>>({});

  /* ---- where ---- */
  const [positionRaw, setPositionRaw] = useState(
    initialPosition === undefined ? "" : String(initialPosition),
  );
  const faces = rules.faces;
  const [face, setFace] = useState(
    initialFace && faces.includes(initialFace) ? initialFace : (faces[0] ?? ""),
  );
  const knownModelHeight =
    mode === "existing" ? placement.data?.modelHeight : undefined;
  // undefined until the person types: the field then starts from the device's own height.
  const [heightRaw, setHeightRaw] = useState<string | undefined>(
    initialHeight === undefined ? undefined : String(initialHeight),
  );
  const ownHeight = mode === "existing" ? placement.data?.ownHeight : undefined;
  const heightShown = knownModelHeight === undefined && rules.deviceHeight;
  const heightText =
    heightRaw ?? (ownHeight !== undefined ? String(ownHeight) : "");
  const typedHeight = parseNumber(heightText);
  const heightU = knownModelHeight ?? typedHeight ?? 1;
  const position = parseNumber(positionRaw);
  const fullDepth =
    mode === "existing" ? (placement.data?.fullDepth ?? false) : false;

  const others: Mounted[] = (contents.data?.items ?? []).filter(
    (m) => m.id !== deviceId,
  );
  const fit =
    rackId && contents.data && positionRaw.trim() === ""
      ? firstFit(
          rackU,
          heightU,
          (face || "front") as Face,
          fullDepth,
          others,
          rules.halfUnits,
        )
      : undefined;
  const check = checkPlacement({
    rackU,
    position,
    heightU,
    face: (face || "front") as Face,
    fullDepth,
    halfUnits: rules.halfUnits,
    others,
  });

  /* ---- the new device's fields ---- */
  const newParsed = newFields.map((a) => {
    const raw = newValues[a.name] ?? "";
    const p = parseField(a, raw);
    return { a, raw, value: p.value, error: p.error as FieldError | undefined };
  });
  const newIdentity: Record<string, string> = {};
  const newAttrs: Attrs = {};
  for (const p of newParsed) {
    if (p.value === undefined) continue;
    if (rules.isIdentity("device", p.a.name) && p.a.kind === "string")
      newIdentity[p.a.name] = String(p.value);
    else newAttrs[p.a.name] = p.value;
  }

  const formErrors: string[] = [];
  if (!rackId) formErrors.push(t("edit.place.error.rack"));
  if (mode === "existing" && !deviceId)
    formErrors.push(t("edit.place.error.device"));
  if (mode === "new" && Object.keys(newIdentity).length === 0)
    formErrors.push(t("edit.place.error.identity"));
  if (heightShown && heightText.trim() !== "" && typedHeight === undefined)
    formErrors.push(t("edit.error.number"));
  const placementErrors = check.errors.map((e) =>
    t(PLACEMENT_TEXT[e], {
      units: number(rackU),
      top: number((position ?? 1) + heightU - 1),
    }),
  );
  const fieldErrors = newParsed.some((p) => p.error && mode === "new");
  const blocked =
    formErrors.length > 0 ||
    placementErrors.length > 0 ||
    fieldErrors ||
    (mode === "existing" && placement.isPending && !!deviceId);

  /* ---- warnings said before anything is sent ---- */
  const warnings: string[] = [];
  for (const o of check.overlaps) {
    warnings.push(
      t("edit.place.overlap", {
        device: o.name,
        range: formatRange(o.position ?? 1, o.heightU ?? 1),
      }),
    );
  }
  const mounts = placement.data?.mounts ?? [];
  const leaving = mounts.filter((m) => m.rackId !== rackId);
  for (const m of mounts) {
    if (source && m.source !== source)
      warnings.push(
        t("edit.place.otherSource", { rack: m.rackName, source: m.source }),
      );
  }
  if (knownModelHeight === undefined && typedHeight === undefined)
    warnings.push(t("edit.place.heightAssumed"));

  let batch: Batch | null = null;
  if (!blocked && rackId && position !== undefined) {
    const device: DeviceChoice =
      mode === "new"
        ? { create: { identity: newIdentity, attributes: newAttrs } }
        : { id: deviceId as string };
    const ownSaid =
      heightShown && typedHeight !== undefined && typedHeight !== ownHeight
        ? typedHeight
        : undefined;
    const createdHeight =
      mode === "new" && heightShown ? typedHeight : undefined;
    batch = placeDevice({
      device,
      placement: { rackId, position, face: face || undefined },
      heightU: mode === "new" ? createdHeight : ownSaid,
      leave: leaving.map((m) => m.rackId),
    });
  }
  const newName =
    newIdentity.name ?? newIdentity.serial ?? newIdentity.asset_tag ?? "";
  const resultWarnings = (r: ApplyResult): string[] =>
    mode === "new" &&
    !(r.changes ?? []).some((c) => c.op === "create" && c.type === "device")
      ? [t("edit.place.matchesExisting", { name: newName })]
      : [];

  return (
    <form className="edit-form" noValidate onSubmit={(e) => e.preventDefault()}>
      {fixedRack ? (
        <p>
          {t("edit.place.inRack", {
            rack: fixedRack.display,
            units: number(rackU),
          })}
        </p>
      ) : (
        <div className="field">
          <label htmlFor={`${uid}-rack`}>
            {label(rules.type("rack")?.label, "rack")}
          </label>
          <select
            id={`${uid}-rack`}
            value={rackChoice}
            onChange={(e) => setRackChoice(e.target.value)}
            aria-describedby={`${uid}-rack-hint`}
          >
            <option value="">{t("edit.place.chooseRack")}</option>
            {(racks.data?.items ?? []).map((r) => (
              <option key={r.id} value={r.id}>
                {r.display}
              </option>
            ))}
          </select>
          <span id={`${uid}-rack-hint`} className="hint">
            {rackId
              ? t("edit.place.rackHeight", { units: number(rackU) })
              : t("edit.place.rackHint")}
          </span>
        </div>
      )}

      {fixedDevice ? (
        <p>{t("edit.place.device", { device: fixedDevice.display })}</p>
      ) : (
        <fieldset>
          <legend>{t("edit.place.which")}</legend>
          <label className="check">
            <input
              type="radio"
              name={`${uid}-mode`}
              checked={mode === "existing"}
              onChange={() => setMode("existing")}
            />{" "}
            {t("edit.place.existing")}
          </label>
          <label className="check">
            <input
              type="radio"
              name={`${uid}-mode`}
              checked={mode === "new"}
              onChange={() => setMode("new")}
            />{" "}
            {t("edit.place.new")}
          </label>
          {mode === "existing" ? (
            <DevicePicker
              picked={picked?.id}
              onPick={setPicked}
              exclude={new Set((contents.data?.items ?? []).map((m) => m.id))}
            />
          ) : (
            <>
              <p className="hint">{t("edit.place.newHint")}</p>
              <div className="field-grid">
                {newParsed.map((p) => (
                  <AttributeField
                    key={p.a.name}
                    attr={p.a}
                    idPrefix={`${uid}-new`}
                    value={p.raw}
                    error={p.error}
                    onChange={(v) =>
                      setNewValues((s) => ({ ...s, [p.a.name]: v }))
                    }
                  />
                ))}
              </div>
            </>
          )}
        </fieldset>
      )}

      {mounts.length > 0 ? (
        <div className="notice" role="note">
          <p>{t("edit.place.currently")}</p>
          <ul>
            {mounts.map((m) => (
              <li key={m.rackId}>
                <ObjectLink id={m.rackId} display={m.rackName} />
                {m.position !== undefined ? `, U${number(m.position)}` : ""}
                {m.face ? `, ${enumText(m.face)}` : ""} ({m.source})
                {m.rackId !== rackId && rackId
                  ? ` — ${t("edit.place.willLeave")}`
                  : ""}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      <div className="field-grid">
        <div className="field">
          <label htmlFor={`${uid}-position`}>{t("edit.place.position")}</label>
          <input
            id={`${uid}-position`}
            type="number"
            inputMode="decimal"
            min={1}
            max={rackU}
            step={rules.halfUnits ? 0.5 : 1}
            value={positionRaw}
            onChange={(e) => setPositionRaw(e.target.value)}
            aria-describedby={`${uid}-position-hint`}
            aria-invalid={placementErrors.length > 0 ? true : undefined}
          />
          <span id={`${uid}-position-hint`} className="hint">
            {rules.halfUnits
              ? t("edit.place.positionHintHalf")
              : t("edit.place.positionHint")}
          </span>
          {fit !== undefined ? (
            <button type="button" onClick={() => setPositionRaw(String(fit))}>
              {t("edit.place.firstFit", { u: number(fit) })}
            </button>
          ) : null}
        </div>
        {faces.length > 0 ? (
          <div className="field">
            <label htmlFor={`${uid}-face`}>{t("rack.col.face")}</label>
            <select
              id={`${uid}-face`}
              value={face}
              onChange={(e) => setFace(e.target.value)}
            >
              {faces.map((f) => (
                <option key={f} value={f}>
                  {f === "front"
                    ? t("rack.front")
                    : f === "rear"
                      ? t("rack.rear")
                      : enumText(f)}
                </option>
              ))}
            </select>
          </div>
        ) : null}
        {heightShown ? (
          <div className="field">
            <label htmlFor={`${uid}-height`}>
              {t("edit.place.height")} <span className="unit">(U)</span>
            </label>
            <input
              id={`${uid}-height`}
              type="number"
              inputMode="decimal"
              min={rules.halfUnits ? 0.5 : 1}
              step={rules.halfUnits ? 0.5 : 1}
              value={heightText}
              onChange={(e) => setHeightRaw(e.target.value)}
              aria-describedby={`${uid}-height-hint`}
            />
            <span id={`${uid}-height-hint`} className="hint">
              {t("edit.place.heightHint")}
            </span>
          </div>
        ) : knownModelHeight !== undefined ? (
          <p className="hint">
            {t("edit.place.heightFromModel", {
              units: number(knownModelHeight),
              model: placement.data?.modelName ?? "",
            })}
          </p>
        ) : null}
      </div>

      {formErrors.length + placementErrors.length > 0 ? (
        <ul className="field-error" aria-live="polite" data-errors="placement">
          {[...formErrors, ...placementErrors].map((e) => (
            <li key={e}>{e}</li>
          ))}
        </ul>
      ) : null}
      {warnings.length > 0 ? (
        <ul className="notice" aria-live="polite" data-warnings="placement">
          {warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      ) : null}
      <BatchReview
        batch={batch}
        names={mode === "new" ? { device: newName } : {}}
        warnings={warnings}
        resultWarnings={resultWarnings}
        autoPreview={autoPreview}
        onCancel={onClose}
      />
    </form>
  );
}

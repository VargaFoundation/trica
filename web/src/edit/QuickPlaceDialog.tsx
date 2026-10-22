// Places, moves or turns one device from a small dialog (ADR 0052). Everything
// is said on one screen: which device (only those in no rack), where (U and
// face), whether it fits, and what the engine would do, which is previewed as
// the fields change. A quick dialog does not let two devices share rack space:
// it offers the nearest free position instead. The full form ("Open full form")
// keeps the warn-only behaviour of ADR 0039 for the cases where the floor
// disagrees with the model.
//
// `mode` is what the person is doing: add (a free unit, "Place a device"),
// move (a drag, the Move button), turn (the Turn button) or putBack (a device
// whose placement was withdrawn, from its page). Only the title, the button
// and the defaults differ.
import { useQuery } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState } from "react";
import { Link } from "react-router";
import { api } from "../api/endpoints";
import type { BatchOutcome } from "../api/types";
import { Async } from "../components/Async";
import { Dialog } from "../components/ui";
import { useMe, useSchema } from "../hooks";
import { useI18n } from "../i18n";
import {
  DEFAULT_RACK_U,
  formatRange,
  type Face,
  type Mounted,
} from "../racks/layout";
import { useRackContents } from "../racks/useRack";
import { Failure, ResultChanges } from "./BatchReview";
import { callerSource, placeDevice } from "./batch";
import { DevicePicker, type PickedDevice } from "./DevicePicker";
import { editRules, type EditRules } from "./rules";
import type { RackRef } from "./PlaceDeviceForm";
import { usePlacement } from "./usePlacement";
import { useQuickReview } from "./useQuickReview";
import { checkPlacement, firstFit, nearestFit, parseNumber } from "./validate";

export type PlaceMode = "add" | "move" | "turn" | "putBack";

/** What the full form starts from when the person leaves the dialog for it. */
export interface FullFormCarry {
  device?: PickedDevice;
  position?: number;
  face?: string;
  heightU?: number;
  /** Open the form on "a device no collector sees". */
  declare?: boolean;
}

export interface QuickPlaceProps {
  mode: PlaceMode;
  rack: RackRef;
  /** The device, for move, turn and putBack; add starts with the picker. */
  device?: PickedDevice;
  initialPosition?: number;
  initialFace?: string;
  onClose: () => void;
  /** The change was applied (200): the caller closes the dialog and may say what happened. */
  onDone?: (message: string) => void;
  /** The person asked for the full form. */
  onFullForm?: (carry: FullFormCarry) => void;
  /** Where the focus goes back to when the element that opened the dialog is gone. */
  returnFocus?: () => HTMLElement | null;
}

export function QuickPlaceDialog(props: QuickPlaceProps) {
  const schema = useSchema();
  return (
    <Async query={schema}>
      {(doc) => <Body {...props} rules={editRules(doc)} />}
    </Async>
  );
}

const faceText = (t: ReturnType<typeof useI18n>["t"], f: string) =>
  f === "front" ? t("rack.front") : f === "rear" ? t("rack.rear") : f;

function Body({
  mode,
  rack,
  device: fixed,
  initialPosition,
  initialFace,
  onClose,
  onDone,
  onFullForm,
  returnFocus,
  rules,
}: QuickPlaceProps & { rules: EditRules }) {
  const { t, number } = useI18n();
  const uid = useId();
  const source = callerSource(useMe().data);

  const rackObj = useQuery({
    queryKey: ["object", rack.id],
    queryFn: ({ signal }) => api.object(rack.id, signal),
    staleTime: 5 * 60_000,
  });
  const declaredU = rackObj.data?.attributes.u_height;
  const rackU =
    typeof declaredU === "number" && declaredU > 0
      ? Math.floor(declaredU)
      : DEFAULT_RACK_U;
  const contents = useRackContents(rack.id);

  const [picked, setPicked] = useState<PickedDevice | undefined>(fixed);
  const deviceId = picked?.id;
  const positionInput = useRef<HTMLInputElement>(null);
  // Once a device is chosen the picker is gone: the next thing to say is where.
  useEffect(() => {
    if (deviceId && !fixed) positionInput.current?.focus();
  }, [deviceId, fixed]);
  const placement = usePlacement(deviceId);
  const faces = rules.faces;
  const [face, setFace] = useState(
    initialFace && faces.includes(initialFace) ? initialFace : (faces[0] ?? ""),
  );
  const [positionRaw, setPositionRaw] = useState(
    initialPosition === undefined ? "" : String(initialPosition),
  );
  const [heightRaw, setHeightRaw] = useState<string | undefined>(undefined);

  const mountedSelf = (contents.data?.items ?? []).find(
    (m) => m.id === deviceId,
  );
  const knownModelHeight = placement.data?.modelHeight;
  const ownHeight = placement.data?.ownHeight;
  // Asking for the height only when nobody knows it and the person is adding a device.
  const heightShown =
    !!deviceId &&
    knownModelHeight === undefined &&
    rules.deviceHeight &&
    mode !== "move" &&
    mode !== "turn" &&
    mountedSelf?.heightU === undefined;
  const heightText =
    heightRaw ?? (ownHeight !== undefined ? String(ownHeight) : "");
  const typedHeight = parseNumber(heightText);
  const heightU =
    knownModelHeight ?? typedHeight ?? ownHeight ?? mountedSelf?.heightU ?? 1;
  const fullDepth =
    placement.data?.fullDepth ?? mountedSelf?.fullDepth ?? false;
  const faceKey = (face || "front") as Face;

  const others: Mounted[] = (contents.data?.items ?? []).filter(
    (m) => m.id !== deviceId,
  );
  const ready = !!deviceId && !!contents.data && !placement.isPending;
  const fit = ready
    ? firstFit(rackU, heightU, faceKey, fullDepth, others, rules.halfUnits)
    : undefined;
  const position =
    parseNumber(positionRaw) ?? (positionRaw.trim() === "" ? fit : undefined);
  const check = checkPlacement({
    rackU,
    position,
    heightU,
    face: faceKey,
    fullDepth,
    halfUnits: rules.halfUnits,
    others,
  });
  const taken = check.overlaps[0];
  const nearest =
    ready &&
    position !== undefined &&
    (taken || check.errors.includes("aboveRack"))
      ? nearestFit(
          rackU,
          heightU,
          faceKey,
          fullDepth,
          others,
          rules.halfUnits,
          position,
        )
      : undefined;
  const otherFace = faces.find((f) => f !== face);
  const otherFaceFree =
    ready && taken && !fullDepth && otherFace && position !== undefined
      ? checkPlacement({
          rackU,
          position,
          heightU,
          face: otherFace as Face,
          fullDepth,
          halfUnits: rules.halfUnits,
          others,
        }).overlaps.length === 0 &&
        checkPlacement({
          rackU,
          position,
          heightU,
          face: otherFace as Face,
          fullDepth,
          halfUnits: rules.halfUnits,
          others,
        }).errors.length === 0
      : false;

  const mounts = placement.data?.mounts ?? [];
  const leaving = mounts.filter((m) => m.rackId !== rack.id);
  const heightBad =
    heightShown && heightText.trim() !== "" && typedHeight === undefined;
  const blocked =
    !ready ||
    position === undefined ||
    check.errors.length > 0 ||
    check.overlaps.length > 0 ||
    heightBad;
  const ownSaid =
    heightShown && typedHeight !== undefined && typedHeight !== ownHeight
      ? typedHeight
      : undefined;
  const batch =
    !blocked && deviceId && position !== undefined
      ? placeDevice({
          device: { id: deviceId },
          placement: { rackId: rack.id, position, face: face || undefined },
          heightU: ownSaid,
          leave: leaving.map((m) => m.rackId),
        })
      : null;
  const review = useQuickReview(batch);
  const [applied, setApplied] = useState(false);

  const range = formatRange(position ?? 1, heightU);
  const placedAt = () => {
    const where = {
      device: picked?.display ?? "",
      u: number(position ?? 0),
      rack: rack.display,
      face: faceText(t, face),
    };
    return t(
      mode === "add"
        ? "quick.done.add"
        : mode === "move"
          ? "quick.done.move"
          : mode === "turn"
            ? "quick.done.turn"
            : "quick.done.putBack",
      where,
    );
  };
  const confirm = () =>
    review.confirm((o: BatchOutcome) => {
      if (o.status === "applied") {
        setApplied(true);
        onDone?.(placedAt());
      }
    });
  const proposed =
    review.outcome?.status === "proposed" ? review.outcome : undefined;
  const carry = (): FullFormCarry => ({
    device: picked,
    position,
    face,
    heightU: ownSaid ?? typedHeight,
  });
  const unknownOtherSource = source
    ? mounts.filter((m) => m.source !== source)
    : [];

  const title =
    mode === "add"
      ? initialPosition === undefined
        ? t("quick.titleAddAny", { rack: rack.display })
        : t("quick.titleAdd", {
            u: number(initialPosition),
            rack: rack.display,
          })
      : t(
          mode === "move"
            ? "quick.titleMove"
            : mode === "turn"
              ? "quick.titleTurn"
              : "quick.titlePutBack",
          { device: fixed?.display ?? "", rack: rack.display },
        );
  const actionKey =
    mode === "add"
      ? "quick.place"
      : mode === "move"
        ? "quick.move"
        : mode === "turn"
          ? "quick.turn"
          : "quick.putBack";
  const primary = (
    <button
      type="button"
      className="primary"
      disabled={!review.canConfirm || applied}
      onClick={confirm}
      data-autofocus={fixed && mode !== "add" ? "" : undefined}
    >
      {t(actionKey)}
    </button>
  );

  const footer = (
    <>
      {onFullForm ? (
        <button
          type="button"
          className="ghost"
          onClick={() => onFullForm(carry())}
          title={t("quick.fullFormHint")}
        >
          {t("quick.fullForm")}
        </button>
      ) : null}
      <span className="spacer" />
      {proposed ? (
        <>
          <span className="sent" role="status" data-outcome="proposed">
            {t("quick.sent")}
            {proposed.result.proposal_id ? (
              <>
                {" · "}
                <Link to={`/proposals/${proposed.result.proposal_id}`}>
                  {t("quick.sentProposal", { id: proposed.result.proposal_id })}
                </Link>
              </>
            ) : null}
          </span>
          <button type="button" className="primary" onClick={onClose}>
            {t("edit.close")}
          </button>
        </>
      ) : (
        <>
          <button type="button" onClick={onClose}>
            {t("edit.cancel")}
          </button>
          {primary}
        </>
      )}
    </>
  );

  return (
    <Dialog
      title={title}
      onClose={onClose}
      compact={mode === "move" || mode === "turn"}
      returnFocus={returnFocus}
      footer={footer}
    >
      {mode === "add" || mode === "putBack" ? (
        picked && !fixed ? (
          <div className="picker-chosen">
            <span>{t("quick.picker.chosen", { device: picked.display })}</span>
            <button
              type="button"
              className="ghost"
              onClick={() => setPicked(undefined)}
            >
              {t("quick.picker.change")}
            </button>
          </div>
        ) : picked ? (
          <p>{t("quick.picker.chosen", { device: picked.display })}</p>
        ) : (
          <>
            <DevicePicker
              exclude={new Set((contents.data?.items ?? []).map((m) => m.id))}
              onPick={setPicked}
              autoFocus
            />
            {onFullForm ? (
              <p>
                <button
                  type="button"
                  className="ghost"
                  onClick={() => onFullForm({ declare: true, position, face })}
                >
                  {t("quick.declare")}
                </button>
              </p>
            ) : null}
          </>
        )
      ) : null}

      {mounts.length > 0 && mode !== "move" && mode !== "turn" ? (
        <div className="notice" role="note">
          <ul>
            {mounts.map((m) => (
              <li key={m.rackId}>
                {m.rackName}
                {m.position !== undefined ? `, U${number(m.position)}` : ""}
                {m.rackId !== rack.id ? ` — ${t("edit.place.willLeave")}` : ""}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {unknownOtherSource.length > 0 ? (
        <p className="notice" role="note">
          {t("edit.place.otherSource", {
            rack: unknownOtherSource[0]?.rackName ?? "",
            source: unknownOtherSource[0]?.source ?? "",
          })}
        </p>
      ) : null}

      {deviceId ? (
        <>
          <div className="quick-grid">
            <div className="field">
              <label htmlFor={`${uid}-position`}>{t("quick.position")}</label>
              <input
                id={`${uid}-position`}
                ref={positionInput}
                data-autofocus=""
                type="number"
                inputMode="decimal"
                min={1}
                max={rackU}
                step={rules.halfUnits ? 0.5 : 1}
                value={positionRaw}
                placeholder={fit === undefined ? "" : String(fit)}
                onChange={(e) => setPositionRaw(e.target.value)}
                aria-invalid={
                  check.errors.length > 0 || check.overlaps.length > 0
                    ? true
                    : undefined
                }
              />
            </div>
            {faces.length > 0 ? (
              <div className="field">
                <span className="label" id={`${uid}-face`}>
                  {t("quick.face")}
                </span>
                <div
                  className="seg"
                  role="radiogroup"
                  aria-labelledby={`${uid}-face`}
                >
                  {faces.map((f) => (
                    <label key={f}>
                      <input
                        type="radio"
                        name={`${uid}-face`}
                        checked={face === f}
                        onChange={() => setFace(f)}
                      />{" "}
                      {faceText(t, f)}
                    </label>
                  ))}
                </div>
              </div>
            ) : null}
            {heightShown ? (
              <div className="field">
                <label htmlFor={`${uid}-height`}>{t("quick.height")}</label>
                <input
                  id={`${uid}-height`}
                  type="number"
                  inputMode="decimal"
                  min={rules.halfUnits ? 0.5 : 1}
                  step={rules.halfUnits ? 0.5 : 1}
                  value={heightText}
                  onChange={(e) => setHeightRaw(e.target.value)}
                />
              </div>
            ) : null}
          </div>
          {knownModelHeight !== undefined ? (
            <p className="hint">
              {t("edit.place.heightFromModel", {
                units: number(knownModelHeight),
                model: placement.data?.modelName ?? "",
              })}
            </p>
          ) : knownModelHeight === undefined &&
            typedHeight === undefined &&
            ownHeight === undefined &&
            mountedSelf?.heightU === undefined ? (
            <p className="hint">{t("edit.place.heightAssumed")}</p>
          ) : null}

          <div aria-live="polite" data-errors="placement">
            {check.errors.length > 0 ? (
              <ul className="field-error">
                {check.errors.map((e) => (
                  <li key={e}>
                    {t(`edit.place.error.${e}`, {
                      units: number(rackU),
                      top: number((position ?? 1) + heightU - 1),
                    })}
                  </li>
                ))}
              </ul>
            ) : taken ? (
              <p className="fit-line fit-taken">
                {t("quick.fit.taken", {
                  device: taken.name,
                  range: formatRange(taken.position ?? 1, taken.heightU ?? 1),
                })}
              </p>
            ) : position !== undefined && ready ? (
              <p className="fit-line fit-free">
                {t("quick.fit.free", { range, face: faceText(t, face) })}
              </p>
            ) : null}
            {(taken || check.errors.includes("aboveRack")) && ready ? (
              <p className="fit-line">
                {nearest !== undefined ? (
                  <button
                    type="button"
                    onClick={() => setPositionRaw(String(nearest))}
                  >
                    {t("quick.fit.nearest", { u: number(nearest) })}
                  </button>
                ) : (
                  <span>{t("quick.fit.none", { units: number(heightU) })}</span>
                )}
                {otherFaceFree && otherFace ? (
                  <button type="button" onClick={() => setFace(otherFace)}>
                    {t("quick.fit.otherFace", { face: faceText(t, otherFace) })}
                  </button>
                ) : null}
              </p>
            ) : null}
          </div>

          <section
            className="dialog-preview"
            aria-live="polite"
            aria-label={t("quick.will")}
          >
            {review.error ? (
              <Failure error={review.error} title={t("quick.previewFailed")} />
            ) : review.result ? (
              <>
                <h3>{t("quick.will")}</h3>
                <ResultChanges result={review.result} names={{}} />
                {(review.result.warnings ?? []).map((w) => (
                  <p key={w} className="hint">
                    {w}
                  </p>
                ))}
              </>
            ) : review.pending ? (
              <p className="hint" role="status">
                {t("quick.previewing")}
              </p>
            ) : null}
          </section>
        </>
      ) : null}
      {review.submitError ? (
        <Failure error={review.submitError} title={t("edit.submit.failed")} />
      ) : null}
    </Dialog>
  );
}

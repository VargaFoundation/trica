// "Recently taken out of this rack" (ADR 0053): the devices whose placement
// here ended and that are mounted nowhere since, each one click from being put
// back where it was. The handle is a pointer shortcut like the elevation's
// free units: drop it on a free unit and the dialog opens at that unit and
// that face. The button is the keyboard path, and nothing is sent before the
// dialog's own confirmation.
import { useEffect, useState, type PointerEvent as ReactPointerEvent } from "react";
import { ObjectLink } from "../components/ObjectLink";
import { useI18n } from "../i18n";
import { formatRange, type Face } from "./layout";
import type { Removed } from "./useRemoved";

/** The free unit and the face under a point of the page, when there is one. */
export function freeUnitAt(x: number, y: number): { u: number; face: Face } | undefined {
  const el = document.elementFromPoint(x, y);
  const free = el?.closest?.("rect.rack-free");
  const svg = free?.closest("svg[data-face]");
  const u = Number(free?.getAttribute("data-u"));
  const face = svg?.getAttribute("data-face");
  if (!free || !svg || !Number.isFinite(u) || (face !== "front" && face !== "rear")) return undefined;
  return { u, face };
}

export function RemovedTray({
  items,
  canPutBack,
  sent,
  onPutBack,
  onDrop,
  onDragging,
}: {
  items: readonly Removed[];
  canPutBack: boolean;
  /** Devices whose put back was sent for approval from this page. */
  sent: ReadonlySet<string>;
  onPutBack: (item: Removed) => void;
  onDrop: (item: Removed, u: number, face: Face) => void;
  /** A handle is being dragged: the page highlights its free units. */
  onDragging: (dragging: boolean) => void;
}) {
  const { t, number, dateTime } = useI18n();
  const [drag, setDrag] = useState<{ item: Removed; x: number; y: number; moved: boolean } | null>(null);
  const start = (item: Removed, e: ReactPointerEvent) => {
    if (e.button !== 0) return;
    setDrag({ item, x: e.clientX, y: e.clientY, moved: false });
  };
  useEffect(() => {
    if (!drag) return;
    const move = (e: PointerEvent) => {
      if (!drag.moved && Math.hypot(e.clientX - drag.x, e.clientY - drag.y) > 4) {
        setDrag({ ...drag, moved: true });
        onDragging(true);
      }
    };
    const end = () => {
      onDragging(false);
      setDrag(null);
    };
    const up = (e: PointerEvent) => {
      const at = drag.moved ? freeUnitAt(e.clientX, e.clientY) : undefined;
      end();
      if (at) onDrop(drag.item, at.u, at.face);
    };
    const cancel = (e: KeyboardEvent) => {
      if (e.key === "Escape") end();
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("keydown", cancel);
    return () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("keydown", cancel);
    };
  }, [drag, onDragging, onDrop]);

  if (items.length === 0) return null;
  return (
    <section aria-labelledby="removed-h" className="removed-tray" data-dragging={drag?.moved ? "true" : undefined}>
      <h2 id="removed-h">
        {t("rack.removed.title")} ({number(items.length)})
      </h2>
      <ul>
        {items.map((it) => (
          <li key={it.id} data-removed={it.id}>
            <span className="removed-main">
              <ObjectLink id={it.id} display={it.name} />
              <span className="muted">
                {it.position !== undefined
                  ? t("rack.removed.was", {
                      where: `${formatRange(it.position, it.heightU ?? 1)}${it.face ? `, ${t(it.face === "front" ? "rack.front" : "rack.rear").toLowerCase()}` : ""}`,
                    })
                  : t("rack.removed.wasNoPos")}
                {it.absent ? ` · ${t("rack.removed.absent")}` : ""}
                {` · ${dateTime(it.removedAt)} (${it.removedBy})`}
              </span>
            </span>
            {sent.has(it.id) ? (
              <span className="sent" role="status">
                {t("rack.removed.sent")}
              </span>
            ) : canPutBack ? (
              <>
                <button type="button" data-put-back={it.id} onClick={() => onPutBack(it)}>
                  {t("rack.removed.putBack")}
                  <span className="sr-only"> ({it.name})</span>
                </button>
                <span
                  className="drag-handle"
                  aria-hidden="true"
                  title={t("rack.removed.handle")}
                  data-handle={it.id}
                  onPointerDown={(e) => start(it, e)}
                >
                  ⠿
                </span>
              </>
            ) : null}
          </li>
        ))}
      </ul>
      {canPutBack ? <p className="hint">{t("rack.removed.hint")}</p> : null}
    </section>
  );
}

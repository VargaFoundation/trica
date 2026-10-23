import {
  useEffect,
  useId,
  useRef,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { useI18n } from "../../i18n";
import { Icon } from "./icons";

export interface DialogProps {
  /** Its accessible name, shown as the heading. */
  title: string;
  /** Called when the person closes it (the close button, Esc, a click on the backdrop). The parent unmounts it. */
  onClose: () => void;
  /** A narrower card, for a question that takes one line. */
  compact?: boolean;
  /** CSS selector of what takes the focus first; by default the first `[data-autofocus]`, else the browser's choice. */
  initialFocus?: string;
  /** Where the focus returns when the element that opened it is gone (a click on a drawing, which is not focusable). */
  returnFocus?: () => HTMLElement | null;
  /** The buttons, in a bar under the body. */
  footer?: ReactNode;
  children: ReactNode;
}

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex]:not([tabindex="-1"])';

/** The focusable elements of a dialog, those inside a folded <details> left out (but not its summary). */
function tabStops(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => {
    const folded = el.closest("details:not([open])");
    return !folded || el.tagName === "SUMMARY";
  });
}

/**
 * A modal dialog on the native <dialog> element (ADR 0052): the browser makes
 * the page behind inert, draws the backdrop and closes on Esc. This wraps
 * what it does not do: the heading as the accessible name, the focus on
 * opening and its return on closing, Tab wrapping inside, and a click on the
 * backdrop. Mount it to open it, unmount it to close it.
 */
export function Dialog({
  title,
  onClose,
  compact,
  initialFocus,
  returnFocus,
  footer,
  children,
}: DialogProps) {
  const { t } = useI18n();
  const id = useId();
  const ref = useRef<HTMLDialogElement>(null);
  // The latest callbacks, without re-running the opening effect when a parent re-renders.
  const back = useRef(returnFocus);
  const focusFirst = useRef(initialFocus);
  useEffect(() => {
    back.current = returnFocus;
    focusFirst.current = initialFocus;
  });

  useEffect(() => {
    const dlg = ref.current;
    if (!dlg) return;
    const opener =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    if (!dlg.open) dlg.showModal();
    const target =
      (focusFirst.current
        ? dlg.querySelector<HTMLElement>(focusFirst.current)
        : null) ?? dlg.querySelector<HTMLElement>("[data-autofocus]");
    target?.focus();
    return () => {
      if (dlg.open) dlg.close();
      const home =
        opener &&
        opener !== document.body &&
        opener.getAttribute("tabindex") !== "-1" && // a landmark the app focused, not a control
        opener.isConnected
          ? opener
          : back.current?.();
      home?.focus();
    };
  }, []);

  const onKeyDown = (e: KeyboardEvent<HTMLDialogElement>) => {
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
      return;
    }
    if (e.key !== "Tab" || !ref.current) return;
    const stops = tabStops(ref.current);
    const first = stops[0];
    const last = stops[stops.length - 1];
    if (!first || !last) return;
    const active = document.activeElement;
    if (e.shiftKey && (active === first || active === ref.current)) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && active === last) {
      e.preventDefault();
      first.focus();
    }
  };

  return (
    <dialog
      ref={ref}
      className={compact ? "dialog dialog-compact" : "dialog"}
      aria-labelledby={id}
      onKeyDown={onKeyDown}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      // The dialog has no padding: a click that lands on it, not on its card, is a click on the backdrop.
      onClick={(e) => {
        if (e.target === ref.current) onClose();
      }}
    >
      <div className="dialog-card">
        <header className="dialog-head">
          <h2 id={id}>{title}</h2>
          <button
            type="button"
            className="ghost dialog-close"
            onClick={onClose}
            aria-label={t("dialog.close")}
          >
            <Icon name="close" />
          </button>
        </header>
        <div className="dialog-body">{children}</div>
        {footer ? <footer className="dialog-foot">{footer}</footer> : null}
      </div>
    </dialog>
  );
}

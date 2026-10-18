import { useRef, type KeyboardEvent, type ReactNode } from "react";
import { useSearchParams } from "react-router";
import { useI18n } from "../../i18n";

export interface TabDef {
  id: string;
  label: string;
  count?: number;
}

/**
 * The ARIA tabs pattern: one tab stop, the arrows move between tabs, Home and
 * End go to the ends, and moving selects (automatic activation).
 */
export function Tabs({ tabs, active, onSelect, label, idPrefix }: { tabs: TabDef[]; active: string; onSelect: (id: string) => void; label: string; idPrefix: string }) {
  const { number } = useI18n();
  const refs = useRef<Record<string, HTMLButtonElement | null>>({});
  const onKey = (e: KeyboardEvent) => {
    const i = tabs.findIndex((t) => t.id === active);
    let next = -1;
    if (e.key === "ArrowRight") next = (i + 1) % tabs.length;
    else if (e.key === "ArrowLeft") next = (i - 1 + tabs.length) % tabs.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = tabs.length - 1;
    if (next < 0) return;
    e.preventDefault();
    const id = (tabs[next] as TabDef).id;
    onSelect(id);
    refs.current[id]?.focus();
  };
  return (
    <div role="tablist" aria-label={label} className="tabs" onKeyDown={onKey}>
      {tabs.map((tab) => (
        <button
          key={tab.id}
          ref={(el) => {
            refs.current[tab.id] = el;
          }}
          type="button"
          role="tab"
          className="tab"
          id={`${idPrefix}-tab-${tab.id}`}
          aria-controls={`${idPrefix}-panel-${tab.id}`}
          aria-selected={tab.id === active}
          tabIndex={tab.id === active ? 0 : -1}
          onClick={() => onSelect(tab.id)}
        >
          {tab.label}
          {tab.count !== undefined ? <span className="count">{number(tab.count)}</span> : null}
        </button>
      ))}
    </div>
  );
}

export function TabPanel({ idPrefix, id, children }: { idPrefix: string; id: string; children: ReactNode }) {
  return (
    <div role="tabpanel" className="tab-panel" id={`${idPrefix}-panel-${id}`} aria-labelledby={`${idPrefix}-tab-${id}`} tabIndex={0}>
      {children}
    </div>
  );
}

/** The selected tab, kept in the address (?tab=), so a tab can be linked to and Back returns to it. */
export function useTabParam(ids: readonly string[], fallback: string): [string, (id: string) => void] {
  const [params, setParams] = useSearchParams();
  const asked = params.get("tab") ?? "";
  const active = ids.includes(asked) ? asked : fallback;
  const select = (id: string) => {
    const next = new URLSearchParams(params);
    if (id === fallback) next.delete("tab");
    else next.set("tab", id);
    setParams(next, { replace: true });
  };
  return [active, select];
}

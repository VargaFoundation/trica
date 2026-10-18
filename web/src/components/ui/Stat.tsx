import type { ReactNode } from "react";

/** A number put forward: in monospace (a number is compared), its label below. */
export function Stat({ value, label, tone, id }: { value: ReactNode; label: ReactNode; tone?: "ok" | "warn" | "danger"; id?: string }) {
  return (
    <li className={tone ? `stat stat-${tone}` : "stat"} data-stat={id}>
      <span className="stat-value">{value}</span>
      <span className="stat-label">{label}</span>
    </li>
  );
}

/** A row of stats; a list, so a screen reader announces how many. */
export function Stats({ children, label }: { children: ReactNode; label: string }) {
  return (
    <ul className="stats" aria-label={label}>
      {children}
    </ul>
  );
}

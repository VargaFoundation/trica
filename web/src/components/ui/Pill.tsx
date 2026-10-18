import type { ReactNode } from "react";
import { useI18n, type MessageKey } from "../../i18n";
import { HEALTH_TONE, HEALTHS, type Health, type Tone } from "../../inventory/health";

/**
 * A status pill: a tinted ground, a line of its hue, a dot, and its word. The
 * state is read in the word, never in the colour alone (WCAG 1.4.1).
 */
export function Pill({ tone = "neutral", children, value, className }: { tone?: Tone; children: ReactNode; value?: string; className?: string }) {
  return (
    <span className={`pill pill-${tone}${className ? ` ${className}` : ""}`} data-value={value}>
      {children}
    </span>
  );
}

/** The health of an object: ok, warning, critical, unknown or absent. */
export function HealthPill({ value }: { value: Health | string }) {
  const { t } = useI18n();
  const known = (HEALTHS as readonly string[]).includes(value);
  const h = (known ? value : "unknown") as Health;
  return (
    <Pill tone={HEALTH_TONE[h]} value={value} className="health-pill">
      <span className="sr-only">{t("health.label")}: </span>
      {known ? t(`healthState.${h}` as MessageKey) : value}
    </Pill>
  );
}

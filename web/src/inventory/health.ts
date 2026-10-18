// The health of an object as the UI shows it: what the schema's `health`
// attribute says (ok, warning, critical, unknown, absent), and absent for an
// object the inventory no longer sees. Nothing is inferred from a name; a value
// the schema does not list (a newer server) is shown as unknown.

export const HEALTHS = ["ok", "warning", "critical", "unknown", "absent"] as const;
export type Health = (typeof HEALTHS)[number];

export function healthOf(obj: { status?: string; attributes?: Record<string, unknown> }): Health {
  if (obj.status === "absent") return "absent";
  const h = obj.attributes?.health;
  return typeof h === "string" && (HEALTHS as readonly string[]).includes(h) ? (h as Health) : "unknown";
}

/** Worst first: what needs attention leads. */
export const SEVERITY: Record<Health, number> = { critical: 0, warning: 1, unknown: 2, absent: 3, ok: 4 };

export type Tone = "ok" | "warn" | "danger" | "info" | "declared" | "neutral" | "absent";

export const HEALTH_TONE: Record<Health, Tone> = {
  ok: "ok",
  warning: "warn",
  critical: "danger",
  unknown: "neutral",
  absent: "absent",
};

/** A number from an attribute, when it is one. */
export function num(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

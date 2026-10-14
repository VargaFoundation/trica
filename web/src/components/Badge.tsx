import { useI18n, type MessageKey } from "../i18n";

const CONFIDENCES = new Set(["observed", "deduced", "declared"]);
const STATUSES = new Set(["active", "absent", "pending", "applied", "rejected", "superseded", "open", "resolved", "accepted"]);

/**
 * A short label whose meaning is in its text; the colour only reinforces it.
 * A value the UI does not know (a newer server) is shown as it is.
 */
function Badge({ known, kind, value, prefix }: { known: Set<string>; kind: string; value: string; prefix: string }) {
  const { t } = useI18n();
  const text = known.has(value) ? t(`${prefix}.${value}` as MessageKey) : value;
  return (
    <span className={`badge badge-${known.has(value) ? kind : "unknown"}`} data-value={value}>
      {text}
    </span>
  );
}

export function Confidence({ value }: { value: string }) {
  return <Badge known={CONFIDENCES} kind={value} value={value} prefix="confidence" />;
}

export function Status({ value }: { value: string }) {
  return <Badge known={STATUSES} kind={`status-${value}`} value={value} prefix="status" />;
}

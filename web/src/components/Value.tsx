import { useI18n } from "../i18n";
import { Empty } from "./Time";

/** Text for an attribute value of any JSON type. */
export function formatValue(v: unknown, yes: string, no: string): string {
  if (v === null || v === undefined) return "";
  if (typeof v === "string") return v;
  if (typeof v === "boolean") return v ? yes : no;
  if (typeof v === "number") return String(v);
  if (Array.isArray(v)) return v.map((x) => (typeof x === "object" && x !== null ? JSON.stringify(x) : formatValue(x, yes, no))).join(", ");
  return JSON.stringify(v);
}

export function Value({ value, unit }: { value: unknown; unit?: string }) {
  const { t } = useI18n();
  const text = formatValue(value, t("app.yes"), t("app.no"));
  if (text === "") return <Empty />;
  return (
    <span className="value">
      {text}
      {unit ? <span className="unit"> {unit}</span> : null}
    </span>
  );
}

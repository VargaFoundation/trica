// One form field per schema attribute: the label, the kind, the unit and the
// enum values are the registry's (GET /schema), so a custom attribute or a new
// enum value needs no change here.
import type { AttributeDoc } from "../api/types";
import { useI18n, type MessageKey } from "../i18n";
import { parseNumber } from "./validate";

export type FieldError = "required" | "number" | "whole" | "positive";

const ERROR_TEXT: Record<FieldError, MessageKey> = {
  required: "edit.error.required",
  number: "edit.error.number",
  whole: "edit.error.whole",
  positive: "edit.error.positive",
};

/** The kinds a person types in a form here; the others (ip, time, json, list…) are left to collectors. */
export const EDITABLE_KINDS = new Set(["string", "text", "int", "float", "enum", "bool"]);

/** An enum value as the schema writes it, underscores read as spaces (the registry has no labels for values). */
export const enumText = (v: string): string => v.replace(/_/g, " ");

/** The raw text of a field from a stored value. */
export function rawOf(v: unknown): string {
  if (v === undefined || v === null) return "";
  if (typeof v === "boolean") return v ? "true" : "false";
  if (typeof v === "string") return v;
  if (typeof v === "number") return String(v);
  return JSON.stringify(v);
}

/** The value a field holds, typed by its kind; undefined when empty. */
export function parseField(a: AttributeDoc, raw: string): { value?: unknown; error?: FieldError } {
  const s = raw.trim();
  if (s === "") return a.required ? { error: "required" } : {};
  switch (a.kind) {
    case "int": {
      const n = parseNumber(s);
      if (n === undefined) return { error: "number" };
      if (!Number.isInteger(n)) return { error: "whole" };
      return { value: n };
    }
    case "float": {
      const n = parseNumber(s);
      return n === undefined ? { error: "number" } : { value: n };
    }
    case "bool":
      return { value: s === "true" };
    default:
      return { value: s };
  }
}

export function AttributeField({
  attr,
  idPrefix,
  value,
  onChange,
  error,
  required,
  min,
}: {
  attr: AttributeDoc;
  idPrefix: string;
  value: string;
  onChange: (v: string) => void;
  error?: FieldError;
  required?: boolean;
  min?: number;
}) {
  const { t, label } = useI18n();
  const id = `${idPrefix}-${attr.name}`;
  const errId = `${id}-error`;
  const text = label(attr.label, attr.name);
  const describedBy = error ? errId : undefined;
  let input;
  if (attr.kind === "enum") {
    input = (
      <select id={id} value={value} onChange={(e) => onChange(e.target.value)} aria-invalid={error ? true : undefined} aria-describedby={describedBy}>
        <option value="">{t("edit.notSaid")}</option>
        {(attr.values ?? []).map((v) => (
          <option key={v} value={v}>
            {enumText(v)}
          </option>
        ))}
      </select>
    );
  } else if (attr.kind === "bool") {
    input = (
      <select id={id} value={value} onChange={(e) => onChange(e.target.value)} aria-describedby={describedBy}>
        <option value="">{t("edit.notSaid")}</option>
        <option value="true">{t("app.yes")}</option>
        <option value="false">{t("app.no")}</option>
      </select>
    );
  } else {
    const numeric = attr.kind === "int" || attr.kind === "float";
    input = (
      <input
        id={id}
        type={numeric ? "number" : "text"}
        inputMode={numeric ? (attr.kind === "int" ? "numeric" : "decimal") : undefined}
        step={attr.kind === "int" ? 1 : numeric ? "any" : undefined}
        min={min}
        value={value}
        required={required}
        aria-required={required ? true : undefined}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }
  return (
    <div className="field" data-attribute={attr.name}>
      <label htmlFor={id}>
        {text}
        {attr.unit ? <span className="unit"> ({attr.unit})</span> : null}
        {required ? <span aria-hidden="true"> *</span> : null}
      </label>
      {input}
      {error ? (
        <span id={errId} className="field-error">
          {t(ERROR_TEXT[error])}
        </span>
      ) : null}
    </div>
  );
}

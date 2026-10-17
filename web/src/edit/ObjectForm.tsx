// Declares a physical container (site, building, room, row, rack) or changes
// one: its name, a rack's height and its floor tile, a room's size. The fields
// are the registry's attributes of the type; identifiers (a name, a code, a
// serial) go into the batch identity, the rest into its attributes.
import { useQueries } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/endpoints";
import type { AttributeDoc, Batch, ObjectView } from "../api/types";
import { Async } from "../components/Async";
import { useSchema } from "../hooks";
import { useI18n } from "../i18n";
import { AttributeField, EDITABLE_KINDS, parseField, rawOf, type FieldError } from "./AttributeFields";
import { BatchReview } from "./BatchReview";
import { createObject, updateObject, type Attrs } from "./batch";
import { CONTAINER_FIELDS, editRules, type EditRules } from "./rules";
import { checkTile } from "./validate";

/** Where racks already stand in the room, for the tile check. */
export interface FloorContext {
  racks: { id: string; name: string; x?: number; y?: number }[];
  size?: { cols: number; rows: number };
}

export interface ParentRef {
  type: string;
  id: string;
  display: string;
}

type Props = {
  floor?: FloorContext;
  onClose: () => void;
} & ({ mode: "create"; type: string; parent?: ParentRef; initial?: Record<string, string> } | { mode: "edit"; object: ObjectView });

/** Must be more than zero: a height or a size of zero is a typing slip, not a fact. */
const POSITIVE = new Set(["u_height", "width_mm", "depth_mm", "tile_mm"]);

/** The form is the schema's attributes of the type: it waits for the schema. */
export function ObjectForm(props: Props) {
  const schema = useSchema();
  return <Async query={schema}>{(doc) => <ObjectFormBody {...props} rules={editRules(doc)} />}</Async>;
}

function ObjectFormBody(props: Props & { rules: EditRules }) {
  const { t, label } = useI18n();
  const rules = props.rules;
  const type = props.mode === "create" ? props.type : props.object.type;
  const typeDoc = rules.type(type);
  const fields = (CONTAINER_FIELDS[type] ?? [])
    .map((k) => typeDoc?.attributes.find((a) => a.name === k))
    .filter((a): a is AttributeDoc => !!a && EDITABLE_KINDS.has(a.kind));
  const current = props.mode === "edit" ? props.object.attributes : {};
  // Only what the person typed; a field not touched shows the stored value (the schema may load after the first render).
  const [edits, setEdits] = useState<Record<string, string>>({});
  const initialRaw = (k: string) => (props.mode === "create" ? (props.initial?.[k] ?? "") : rawOf(current[k]));
  const rawValue = (k: string) => edits[k] ?? initialRaw(k);

  // A new container goes into a parent the schema allows; a fixed parent (the room of the page) is not asked.
  const fixedParent = props.mode === "create" ? props.parent : undefined;
  const parentTypes = props.mode === "create" && !fixedParent ? (typeDoc?.parents ?? []) : [];
  const parentLists = useQueries({
    queries: parentTypes.map((pt) => ({
      queryKey: ["objects", { type: pt, sort: "name", limit: 1000 }],
      queryFn: ({ signal }: { signal: AbortSignal }) => api.objects({ type: pt, sort: "name", limit: 1000 }, signal),
      staleTime: 30_000,
    })),
  });
  const [parentChoice, setParentChoice] = useState("");

  const idPrefix = `form-${type}`;
  const parsed = fields.map((a) => {
    const raw = rawValue(a.name);
    const p = parseField({ ...a, required: props.mode === "create" && a.required }, raw);
    let error: FieldError | undefined = p.error;
    if (!error && POSITIVE.has(a.name) && typeof p.value === "number" && p.value <= 0) error = "positive";
    return { a, raw, value: p.value, error };
  });
  const num = (k: string) => {
    const v = parsed.find((p) => p.a.name === k)?.value;
    return typeof v === "number" ? v : undefined;
  };

  const tile = type === "rack" && props.floor ? checkTile(num("floor_x"), num("floor_y"), props.floor.racks, props.floor.size, props.mode === "edit" ? props.object.id : undefined) : undefined;
  const tileErrorText = tile?.errors[0] ? t(tile.errors[0] === "tileMissing" ? "edit.tile.missing" : "edit.tile.whole") : "";
  const warnings: string[] = [];
  if (tile?.taken.length) warnings.push(t("edit.tile.taken", { racks: tile.taken.join(", ") }));
  if (tile?.outside) warnings.push(t("edit.tile.outside"));

  let batch: Batch | null = null;
  let nothing = false;
  const hasErrors = parsed.some((p) => p.error) || !!tileErrorText;
  if (!hasErrors) {
    if (props.mode === "create") {
      const identity: Record<string, string> = {};
      const attributes: Attrs = {};
      for (const p of parsed) {
        if (p.value === undefined) continue;
        if (rules.isIdentity(type, p.a.name) && p.a.kind === "string") identity[p.a.name] = String(p.value);
        else attributes[p.a.name] = p.value;
      }
      let parent = fixedParent ? { type: fixedParent.type, id: fixedParent.id } : undefined;
      if (!parent && parentChoice) {
        const [pt, pid] = parentChoice.split(":");
        if (pt && pid) parent = { type: pt, id: pid };
      }
      if (Object.keys(identity).length > 0) batch = createObject({ type, identity, attributes, parent });
    } else {
      const set: Attrs = {};
      const withdraw: string[] = [];
      for (const p of parsed) {
        if (p.raw.trim() === rawOf(current[p.a.name]).trim()) continue;
        if (p.value === undefined) withdraw.push(p.a.name);
        else set[p.a.name] = p.value;
      }
      if (Object.keys(set).length === 0 && withdraw.length === 0) nothing = true;
      else batch = updateObject(type, props.object.id, set, withdraw);
    }
  }
  const nameValue = rawValue("name").trim() || (props.mode === "edit" ? props.object.display : "");

  return (
    <form className="edit-form" noValidate onSubmit={(e) => e.preventDefault()}>
      {fixedParent ? (
        <p>
          {t("edit.parentFixed", { type: label(rules.type(fixedParent.type)?.label, fixedParent.type), name: fixedParent.display })}
        </p>
      ) : null}
      {parentTypes.length > 0 ? (
        <div className="field">
          <label htmlFor={`${idPrefix}-parent`}>{t("edit.parent")}</label>
          <select id={`${idPrefix}-parent`} value={parentChoice} onChange={(e) => setParentChoice(e.target.value)}>
            <option value="">{t("edit.noParent")}</option>
            {parentTypes.map((pt, i) => (
              <optgroup key={pt} label={label(rules.type(pt)?.label, pt)}>
                {(parentLists[i]?.data?.items ?? []).map((o) => (
                  <option key={o.id} value={`${pt}:${o.id}`}>
                    {o.display}
                  </option>
                ))}
              </optgroup>
            ))}
          </select>
        </div>
      ) : null}
      <div className="field-grid">
        {parsed.map((p) => (
          <AttributeField
            key={p.a.name}
            attr={p.a}
            idPrefix={idPrefix}
            value={p.raw}
            required={props.mode === "create" && p.a.required}
            min={p.a.name === "floor_x" || p.a.name === "floor_y" ? 0 : POSITIVE.has(p.a.name) ? 1 : undefined}
            error={p.error}
            onChange={(v) => setEdits((s) => ({ ...s, [p.a.name]: v }))}
          />
        ))}
      </div>
      {tileErrorText ? (
        <p className="field-error" role="alert">
          {tileErrorText}
        </p>
      ) : null}
      {warnings.length > 0 ? (
        <ul className="notice" aria-live="polite">
          {warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      ) : null}
      {props.mode === "edit" ? <p className="hint">{t("edit.withdrawHint")}</p> : null}
      {nothing ? <p className="hint">{t("edit.nothingChanged")}</p> : null}
      <BatchReview batch={batch} names={{ new: nameValue }} warnings={warnings} onCancel={props.onClose} />
    </form>
  );
}

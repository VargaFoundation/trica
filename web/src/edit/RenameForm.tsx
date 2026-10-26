// Renames a site, a building, a room, a row, an aisle or a rack. The object is
// designated by its id and the new name is one more claim from the person
// (ADR 0067): same object, same history, same children and relations. The
// form says which other sources still give the old name, because the new one
// outranks them but does not change what they say.
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/endpoints";
import type { ObjectView, Provenance } from "../api/types";
import { useI18n } from "../i18n";
import { BatchReview } from "./BatchReview";
import { updateObject } from "./batch";
import { EditPanel } from "./EditPanel";

/** The sources, other than people, that currently say something about the name of the object. */
export function nameSources(prov: Provenance | undefined): string[] {
  const entry = prov?.attributes.find((a) => a.key === "name");
  if (!entry) return [];
  const all = [entry.source, ...(entry.other_claims ?? []).map((c) => c.source)];
  return [...new Set(all)].filter((s) => !s.startsWith("user:"));
}

export function RenameForm({
  object,
  onClose,
}: {
  object: ObjectView;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const [name, setName] = useState(object.display);
  const prov = useQuery({
    queryKey: ["provenance", object.id],
    queryFn: ({ signal }) => api.provenance(object.id, signal),
    staleTime: 60_000,
  });
  const sources = nameSources(prov.data);
  const trimmed = name.trim();
  const empty = trimmed === "";
  const same = trimmed === object.display;
  const batch = empty || same ? null : updateObject(object.type, object.id, { name: trimmed });
  const id = `rename-${object.id}`;
  return (
    <EditPanel title={t("edit.rename.title", { name: object.display })}>
      <form className="edit-form" noValidate onSubmit={(e) => e.preventDefault()}>
        <div className="field">
          <label htmlFor={`${id}-name`}>{t("edit.rename.label")}</label>
          <input
            id={`${id}-name`}
            type="text"
            value={name}
            aria-invalid={empty}
            aria-describedby={`${id}-hint`}
            onChange={(e) => setName(e.target.value)}
          />
          {empty ? (
            <p className="field-error" role="alert">
              {t("edit.error.required")}
            </p>
          ) : null}
        </div>
        <p className="hint" id={`${id}-hint`}>
          {t("edit.rename.keeps")}
        </p>
        {sources.length > 0 ? (
          <p className="notice" role="note">
            {t("edit.rename.sources", { sources: sources.join(", "), old: object.display })}
          </p>
        ) : null}
        {same && !empty ? <p className="hint">{t("edit.nothingChanged")}</p> : null}
        <BatchReview batch={batch} onCancel={onClose} />
      </form>
    </EditPanel>
  );
}

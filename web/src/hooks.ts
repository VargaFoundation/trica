import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { api } from "./api/endpoints";
import type { AttributeDoc, Me, RelationDoc, SchemaDoc, TypeDoc } from "./api/types";
import { useI18n } from "./i18n";

export type Action = "read" | "propose" | "approve" | "admin";

export function useMe() {
  return useQuery({ queryKey: ["me"], queryFn: ({ signal }) => api.me(signal), staleTime: 60_000 });
}

/**
 * Whether the signed-in caller holds an action, as GET /me reports it. This only
 * decides what the UI shows: the API checks every request again, and a refusal
 * (403) is displayed like any other error.
 */
export function useCan(action: Action): boolean {
  const me = useMe().data as Me | undefined;
  return !!me?.actions.includes(action);
}

export function useSchema() {
  // The schema changes only when an administrator adds a custom type.
  return useQuery({ queryKey: ["schema"], queryFn: ({ signal }) => api.schema(signal), staleTime: Infinity });
}

export interface SchemaLookup {
  typeLabel: (type: string) => string;
  typeDoc: (type: string) => TypeDoc | undefined;
  attribute: (type: string, key: string) => AttributeDoc | undefined;
  attributeLabel: (type: string, key: string) => string;
  /** Name of a relation as seen from the object: the label when it leaves it, the inverse when it arrives. */
  relationLabel: (name: string, direction: string) => string;
}

export function makeLookup(doc: SchemaDoc | undefined, label: (l: { en: string; fr: string } | undefined, f: string) => string): SchemaLookup {
  const types = new Map<string, TypeDoc>((doc?.types ?? []).map((t) => [t.name, t]));
  const relations = new Map<string, RelationDoc>((doc?.relations ?? []).map((r) => [r.name, r]));
  const attribute = (type: string, key: string) => types.get(type)?.attributes.find((a) => a.name === key);
  return {
    typeLabel: (type) => label(types.get(type)?.label, type),
    typeDoc: (type) => types.get(type),
    attribute,
    attributeLabel: (type, key) => label(attribute(type, key)?.label, key),
    relationLabel: (name, direction) => {
      const r = relations.get(name);
      if (!r) return name;
      return label(direction === "in" ? r.inverse : r.label, name);
    },
  };
}

export function useSchemaLookup(): SchemaLookup {
  const { label } = useI18n();
  const doc = useSchema().data;
  return useMemo(() => makeLookup(doc, label), [doc, label]);
}

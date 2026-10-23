// What the pages know about where a device stands outside a rack (ADR 0058):
// its open located_in relations, whether a rack overrides them, and whether the
// caller may place devices at all.
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/endpoints";
import type { RelationView } from "../api/types";
import { useSchema } from "../hooks";
import { useCanDeclare } from "./EditPanel";
import type { PlaceType } from "./batch";
import { editRules } from "./rules";

/** One place a device is declared to stand in, with who said so. */
export interface CurrentPlace {
  id: string;
  type: PlaceType;
  display: string;
  source: string;
}

/** The relations of an object: the same query as the object page's, so a submitted batch refreshes both. */
export function useObjectRelations(id: string) {
  return useQuery({
    queryKey: ["relations", id],
    queryFn: ({ signal }) => api.relations(id, { limit: 200 }, signal),
    staleTime: 15_000,
  });
}

/** The places a device is located in now (one per source that said so). */
export function currentPlaces(rels: RelationView[]): CurrentPlace[] {
  return rels
    .filter((r) => r.type === "located_in" && r.direction === "out")
    .map((r) => ({ id: r.other.id, type: r.other.type as PlaceType, display: r.other.display, source: r.source }));
}

/** Whether the device is mounted in a rack: the rack then decides its site, whatever a place says. */
export function isMounted(rels: RelationView[]): boolean {
  return rels.some((r) => r.type === "mounted_in" && r.direction === "out");
}

/** The devices placed in a site, building or room. */
export function devicesHere(rels: RelationView[]): RelationView[] {
  return rels.filter((r) => r.type === "located_in" && r.direction === "in");
}

/** Whether to offer placing devices: the caller may propose and the schema has located_in from device. */
export function useCanLocate(): boolean {
  const can = useCanDeclare();
  const schema = useSchema();
  return can && editRules(schema.data).locationTypes.length > 0;
}

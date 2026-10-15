// One function per API operation the UI uses. Parameters are typed from the
// OpenAPI document (QueryOf reads them out of the generated `paths`), so a
// parameter the server renames fails the type check once openapi.json is
// regenerated. The UI only reads, except for the two decisions on a proposal:
// those are API calls, and the server applies them through the reconcile engine.
import { request } from "./client";
import type {
  Change,
  DecisionOutcome,
  Discrepancy,
  Me,
  ObjectView,
  Page,
  Proposal,
  Provenance,
  QueryOf,
  RackItem,
  RelationLink,
  RelationView,
  SchemaDoc,
  SearchPage,
  SourceHealth,
} from "./types";

type Paging = { limit?: number; offset?: number };

export const api = {
  me: (signal?: AbortSignal) => request<Me>("/me", { signal }),

  search: (q: QueryOf<"/search">, signal?: AbortSignal) => request<SearchPage>("/search", { query: q, signal }),
  // type is free text here: the URL carries it, the server validates it against the registry.
  objects: (q: Omit<QueryOf<"/objects">, "type"> & { type?: string }, signal?: AbortSignal) =>
    request<SearchPage>("/objects", { query: q, signal }),

  object: (id: string, signal?: AbortSignal) => request<ObjectView>(`/objects/${encodeURIComponent(id)}`, { signal }),
  provenance: (id: string, signal?: AbortSignal) =>
    request<Provenance>(`/objects/${encodeURIComponent(id)}/provenance`, { signal }),
  relations: (id: string, q: Paging = {}, signal?: AbortSignal) =>
    request<Page<RelationView>>(`/objects/${encodeURIComponent(id)}/relations`, { query: q, signal }),
  /** The current relations of some types across the inventory, both ends named (comma-separated types). */
  relationLinks: (q: QueryOf<"/relations">, signal?: AbortSignal) =>
    request<Page<RelationLink>>("/relations", { query: q, signal }),
  /** The devices mounted in a rack, with position, face, model, height and depth. */
  rackContents: (id: string, q: Paging = {}, signal?: AbortSignal) =>
    request<Page<RackItem>>(`/racks/${encodeURIComponent(id)}/contents`, { query: q, signal }),
  timeline: (id: string, q: Paging = {}, signal?: AbortSignal) =>
    request<Page<Change>>(`/objects/${encodeURIComponent(id)}/timeline`, { query: q, signal }),

  proposals: (q: QueryOf<"/proposals">, signal?: AbortSignal) =>
    request<Page<Proposal>>("/proposals", { query: q, signal }),
  proposal: (id: number, signal?: AbortSignal) => request<Proposal>(`/proposals/${id}`, { signal }),
  approve: (id: number) => request<DecisionOutcome>(`/proposals/${id}/approve`, { method: "POST" }),
  reject: (id: number) => request<DecisionOutcome>(`/proposals/${id}/reject`, { method: "POST" }),

  discrepancies: (q: QueryOf<"/discrepancies">, signal?: AbortSignal) =>
    request<Page<Discrepancy>>("/discrepancies", { query: q, signal }),
  health: (signal?: AbortSignal) => request<Page<SourceHealth>>("/health/collection", { query: { limit: 1000 }, signal }),
  schema: (signal?: AbortSignal) => request<SchemaDoc>("/schema", { signal }),
};

// One function per API operation the UI uses. Parameters are typed from the
// OpenAPI document (QueryOf reads them out of the generated `paths`), so a
// parameter the server renames fails the type check once openapi.json is
// regenerated. The UI reads, decides proposals, and declares what collectors
// cannot see (racks, placements, PDUs) as batches: every write is a call the
// server hands to the reconcile engine, which applies it or queues it as a
// proposal (ADR 0039).
import { request } from "./client";
import type {
  Batch,
  DeviceAirflow,
  BatchOutcome,
  Change,
  CollectorCatalogue,
  CollectorInstance,
  CollectorModule,
  DecisionOutcome,
  DeviceFaces,
  DeviceTypeChoice,
  Discrepancy,
  Me,
  ObjectView,
  Page,
  PreviewOutcome,
  Proposal,
  Provenance,
  QueryOf,
  TopologyMap,
  PlacementRecord,
  RackItem,
  RemovedItem,
  RelationLink,
  LiveTopology,
  RelationView,
  SchemaDoc,
  SearchPage,
  Series,
  SeriesKeys,
  SourceHealth,
  StoredTheme,
  ThemeSelection,
  ThemeState,
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
  /** The live overlay of the topology: throughput and link state per interface id (ADR 0041). */
  liveTopology: (signal?: AbortSignal) => request<LiveTopology>("/topology/live", { signal }),
  /** One level of the topology map: places as nodes, aggregated links as edges, live rates (ADR 0051). */
  topologyMap: (q: QueryOf<"/topology/map">, signal?: AbortSignal) =>
    request<TopologyMap>("/topology/map", { query: q, signal }),
  /** The faces of a device's model with its parts in place (ADR 0047). */
  objectFaces: (id: string, signal?: AbortSignal) =>
    request<DeviceFaces>(`/objects/${encodeURIComponent(id)}/faces`, { signal }),
  /** How sure the inventory is of a device's model, and the models (chassis variants) it may be (ADR 0048). */
  deviceTypeCandidates: (id: string, signal?: AbortSignal) =>
    request<DeviceTypeChoice>(`/objects/${encodeURIComponent(id)}/device-type-candidates`, { signal }),
  /** The airflow check of a device against its rack's aisles (ADR 0049). */
  objectAirflow: (id: string, signal?: AbortSignal) =>
    request<DeviceAirflow>(`/objects/${encodeURIComponent(id)}/airflow`, { signal }),
  /** The devices mounted in a rack, with position, face, model, height and depth. */
  rackContents: (id: string, q: Paging = {}, signal?: AbortSignal) =>
    request<Page<RackItem>>(`/racks/${encodeURIComponent(id)}/contents`, { query: q, signal }),
  /** Where a device was mounted: the current placement first, then the closed ones, newest first (ADR 0053). */
  placementHistory: (id: string, q: Paging = {}, signal?: AbortSignal) =>
    request<Page<PlacementRecord>>(`/objects/${encodeURIComponent(id)}/placement-history`, { query: q, signal }),
  /** The devices taken out of a rack and not mounted since, newest first (ADR 0053). */
  rackRemoved: (id: string, q: Paging & { since?: string } = {}, signal?: AbortSignal) =>
    request<Page<RemovedItem>>(`/racks/${encodeURIComponent(id)}/removed`, { query: q, signal }),
  /** A measurement of the object over a range, in buckets (average, lowest, highest). */
  series: (id: string, q: QueryOf<"/objects/{id}/series">, signal?: AbortSignal) =>
    request<Series>(`/objects/${encodeURIComponent(id)}/series`, { query: q, signal }),
  /** The measurements of the object with a reading in a range, current or withdrawn (#167). */
  seriesKeys: (id: string, q: QueryOf<"/objects/{id}/series/keys">, signal?: AbortSignal) =>
    request<SeriesKeys>(`/objects/${encodeURIComponent(id)}/series/keys`, { query: q, signal }),
  timeline: (id: string, q: Paging = {}, signal?: AbortSignal) =>
    request<Page<Change>>(`/objects/${encodeURIComponent(id)}/timeline`, { query: q, signal }),

  proposals: (q: QueryOf<"/proposals">, signal?: AbortSignal) =>
    request<Page<Proposal>>("/proposals", { query: q, signal }),
  proposal: (id: number, signal?: AbortSignal) => request<Proposal>(`/proposals/${id}`, { signal }),
  approve: (id: number) => request<DecisionOutcome>(`/proposals/${id}/approve`, { method: "POST" }),
  reject: (id: number) => request<DecisionOutcome>(`/proposals/${id}/reject`, { method: "POST" }),

  /** What a batch would change, nothing written. */
  previewBatch: (batch: Batch) => request<PreviewOutcome>("/batches/preview", { method: "POST", body: batch }),
  /** Submits a batch: status "applied" (200) or "proposed" (202, queued for approval). */
  submitBatch: (batch: Batch, reason?: string) => request<BatchOutcome>("/batches", { method: "POST", body: batch, query: { reason } }),

  discrepancies: (q: QueryOf<"/discrepancies">, signal?: AbortSignal) =>
    request<Page<Discrepancy>>("/discrepancies", { query: q, signal }),
  health: (signal?: AbortSignal) => request<Page<SourceHealth>>("/health/collection", { query: { limit: 1000 }, signal }),
  schema: (signal?: AbortSignal) => request<SchemaDoc>("/schema", { signal }),

  /** The collector modules this server knows, and the sources no module claims (ADR 0055). */
  collectors: (signal?: AbortSignal) => request<CollectorCatalogue>("/collectors", { signal }),
  collector: (kind: string, signal?: AbortSignal) => request<CollectorModule>(`/collectors/${encodeURIComponent(kind)}`, { signal }),
  collectorInstances: (kind: string, q: QueryOf<"/collectors/{kind}/instances">, signal?: AbortSignal) =>
    request<Page<CollectorInstance>>(`/collectors/${encodeURIComponent(kind)}/instances`, { query: q, signal }),
  collectorInstance: (kind: string, name: string, signal?: AbortSignal) =>
    request<CollectorInstance>(`/collectors/${encodeURIComponent(kind)}/instances/${encodeURIComponent(name)}`, { signal }),
  /** The console's theme: in force, built in, stored (ADR 0054). Administrators only. */
  themes: (signal?: AbortSignal) => request<ThemeState>("/ui/themes", { signal }),
  /** Keeps a theme archive (the zip is the body); it is validated, not selected. */
  uploadTheme: (file: Blob) => request<StoredTheme>("/ui/themes", { method: "POST", raw: { body: file, type: "application/zip" } }),
  activateTheme: (sel: ThemeSelection) => request<ThemeState>("/ui/themes/active", { method: "PUT", body: sel }),
  resetTheme: () => request<undefined>("/ui/themes/active", { method: "DELETE" }),
  deleteTheme: (sha256: string) => request<undefined>(`/ui/themes/${encodeURIComponent(sha256)}`, { method: "DELETE" }),
};

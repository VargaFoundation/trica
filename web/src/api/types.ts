// Types of the API as the UI uses them. Everything is an alias of what
// openapi-typescript generated from openapi.json (the snapshot of the document
// the server publishes at /api/v1/openapi.json): nothing here restates a
// response shape by hand. The one exception is the `Object` union, see below.
import type { components, paths } from "./schema.gen";

type S = components["schemas"];

/**
 * An object, with `type` as a plain string.
 *
 * The document describes an object as a union discriminated by `type`, one
 * member per registry type. A custom type added at run time appears in the
 * document but not in the generated union, which is fixed at build time, so
 * the UI types objects with the common part, which carries every field the
 * server sends.
 */
export type ObjectView = S["ObjectBase"];
export type SearchPage = Omit<S["SearchPage"], "items"> & { items: ObjectView[] };

export type Me = S["Me"];
export type Problem = S["Problem"];
export type Provenance = S["Provenance"];
export type ProvenanceEntry = S["ProvenanceEntry"];
export type Claim = S["Claim"];
export type RelationView = S["RelationView"];
export type RelationLink = S["RelationLink"];
export type RelationEnd = S["RelationEnd"];
export type RackItem = S["RackItem"];
export type PlacementRecord = S["PlacementRecord"];
export type RemovedItem = S["RemovedItem"];
export type LiveTopology = S["LiveTopology"];
export type TopologyMap = S["TopologyMap"];
export type MapNode = S["MapNode"];
export type MapEdge = S["MapEdge"];
export type MapPlace = S["MapPlace"];
export type MapGroup = S["MapGroup"];
export type MapDrill = S["MapDrill"];
export type MapFloor = S["MapFloor"];
export type MapSize = S["MapSize"];
export type MapMount = S["MapMount"];
export type MapCounts = S["MapCounts"];
export type MapHealth = S["MapHealth"];
export type MapKinds = S["MapKinds"];
export type MapTunnel = S["MapTunnel"];
export type MapUplink = S["MapUplink"];
export type MapMember = S["MapMember"];
export type MapLive = S["MapLive"];
export type MapNotes = S["MapNotes"];
export type Series = S["Series"];
export type SeriesKeys = S["SeriesKeys"];
export type SeriesPoint = S["SeriesPoint"];
export type LiveLink = S["LiveLink"];
export type LiveSourceStatus = S["LiveSourceStatus"];
export type Change = S["Change"];
export type Proposal = S["Proposal"];
export type PlannedChange = S["PlannedChange"];
export type ApplyResult = S["ApplyResult"];
export type Batch = S["Batch"];
export type BatchObject = S["BatchObject"];
export type BatchRelation = S["BatchRelation"];
export type BatchEndpoint = S["BatchEndpoint"];
export type BatchOutcome = S["BatchOutcome"];
export type PreviewOutcome = S["PreviewOutcome"];
export type DecisionOutcome = S["DecisionOutcome"];
export type Discrepancy = S["Discrepancy"];
export type SourceHealth = S["SourceHealth"];
export type CollectorCatalogue = S["CollectorCatalogue"];
export type CollectorModule = S["CollectorModule"];
export type CollectorOption = S["CollectorOption"];
export type CollectorCounts = S["CollectorCounts"];
export type CollectorInstance = S["CollectorInstance"];
export type TargetConfig = S["TargetConfig"];
export type TargetCredentials = S["TargetCredentials"];
export type SchemaDoc = S["SchemaDoc"];
export type TypeDoc = S["TypeDoc"];
export type AttributeDoc = S["AttributeDoc"];
export type RelationDoc = S["RelationDoc"];
export type Label = S["Label"];
export type FacePart = Omit<S["FacePart"], "part"> & { part: ObjectView };
export type DeviceFaces = Omit<S["DeviceFaces"], "parts"> & { parts: FacePart[] };
export type DeviceFace = S["DeviceFace"];
export type FaceSpot = S["FaceSpot"];
export type DeviceTypeChoice = S["DeviceTypeChoice"];
export type DeviceTypeCandidate = S["DeviceTypeCandidate"];
export type AirflowCheck = S["AirflowCheck"];
export type AirflowWarning = S["AirflowWarning"];
export type AirflowPart = S["AirflowPart"];
export type DeviceAirflow = S["DeviceAirflow"];

export type Page<T> = { total: number; limit: number; offset: number; truncated?: boolean; items: T[] };

/** The query parameters of a GET operation, as the document declares them. */
export type QueryOf<P extends keyof paths> = paths[P] extends { get: { parameters: { query?: infer Q } } }
  ? NonNullable<Q>
  : never;

export type ProposalStatus = NonNullable<QueryOf<"/proposals">["status"]>;
export type DiscrepancyStatus = NonNullable<QueryOf<"/discrepancies">["status"]>;
export type CollectorStatus = NonNullable<QueryOf<"/collectors/{kind}/instances">["status"]>;

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
 * document but not in the generated union, and the generator renames the
 * discriminant ("Object.device" instead of "device"), so the UI types objects
 * with the common part, which carries every field the server sends.
 */
export type ObjectView = S["ObjectBase"];
export type SearchPage = Omit<S["SearchPage"], "items"> & { items: ObjectView[] };

export type Me = S["Me"];
export type Problem = S["Problem"];
export type Provenance = S["Provenance"];
export type ProvenanceEntry = S["ProvenanceEntry"];
export type Claim = S["Claim"];
export type RelationView = S["RelationView"];
export type Change = S["Change"];
export type Proposal = S["Proposal"];
export type PlannedChange = S["PlannedChange"];
export type ApplyResult = S["ApplyResult"];
export type Batch = S["Batch"];
export type DecisionOutcome = S["DecisionOutcome"];
export type Discrepancy = S["Discrepancy"];
export type SourceHealth = S["SourceHealth"];
export type SchemaDoc = S["SchemaDoc"];
export type TypeDoc = S["TypeDoc"];
export type AttributeDoc = S["AttributeDoc"];
export type RelationDoc = S["RelationDoc"];
export type Label = S["Label"];

export type Page<T> = { total: number; limit: number; offset: number; truncated?: boolean; items: T[] };

/** The query parameters of a GET operation, as the document declares them. */
export type QueryOf<P extends keyof paths> = paths[P] extends { get: { parameters: { query?: infer Q } } }
  ? NonNullable<Q>
  : never;

export type ProposalStatus = NonNullable<QueryOf<"/proposals">["status"]>;
export type DiscrepancyStatus = NonNullable<QueryOf<"/discrepancies">["status"]>;

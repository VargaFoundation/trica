import { QueryClient } from "@tanstack/react-query";
import { render, type RenderResult } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { App, Providers } from "../App";
import type { Locale } from "../i18n";
import type { ThemePreference } from "../theme";
import { setToken } from "../api/token";
import {
  APPROVER,
  collectorArista,
  collectorAgent,
  collectorCatalogue,
  collectorUnifi,
  instanceReported,
  instanceSwA1,
  instanceSwA2,
  instanceSwA3,
  componentsSrv01,
  discrepancyMismatch,
  healthFailing,
  healthOk,
  ID,
  mountsLinks,
  page,
  proposalApplied,
  proposalPending,
  proposalPendingDetail,
  provenanceSwA1,
  rackR12,
  relationsSwA1,
  schema,
  searchSw,
  seriesOf,
  siteParis,
  srv01,
  swA1,
  timelineSwA1,
} from "./fixtures";

type Reply = { status?: number; body?: unknown };
export type Handler = object | string | number | boolean | null | ((call: Call) => unknown);

export interface Call {
  method: string;
  path: string;
  query: URLSearchParams;
  headers: Headers;
  /** The JSON body, parsed; undefined without one. */
  body?: unknown;
}

const isReply = (v: unknown): v is Reply => typeof v === "object" && v !== null && "status" in v && "body" in v;

export function problem(status: number, slug: string, detail = ""): Reply {
  return { status, body: { type: `urn:trica:problem:${slug}`, title: slug, status, detail } };
}

/**
 * Replaces fetch with a table of answers keyed by "METHOD /path" (the path is
 * relative to /api/v1, the query string is not part of the key). A function
 * receives the call, to answer by query. Everything is recorded in `calls`;
 * a path nobody declared gets a 404 and is listed in `unmatched`.
 */
export function mockApi(routes: Record<string, Handler>) {
  const calls: Call[] = [];
  const unmatched: string[] = [];
  const spy = vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, "http://localhost");
    // The page asks the server for its start-up settings before anything else;
    // unless a test declares "GET /ui-config.json", there is no single sign-on
    // and the request is not part of what the test counts.
    if (url.pathname === "/ui-config.json" && !("GET /ui-config.json" in routes)) {
      return Promise.resolve(new Response(JSON.stringify({ oidc: null }), { status: 200, headers: { "Content-Type": "application/json" } }));
    }
    const call: Call = {
      method: init?.method ?? "GET",
      path: url.pathname.replace(/^\/api\/v1/, ""),
      query: url.searchParams,
      headers: new Headers(init?.headers),
      body: typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : undefined,
    };
    calls.push(call);
    const key = `${call.method} ${call.path}`;
    let reply: unknown;
    if (key in routes) {
      const h = routes[key];
      reply = typeof h === "function" ? (h as (c: Call) => unknown)(call) : h;
    } else {
      unmatched.push(key);
      reply = problem(404, "not-found", `no mock for ${key}`);
    }
    const { status, body } = isReply(reply) ? { status: reply.status ?? 200, body: reply.body } : { status: 200, body: reply };
    return Promise.resolve(
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json" },
      }),
    );
  });
  return { calls, unmatched, spy, count: (key: string) => calls.filter((c) => `${c.method} ${c.path}` === key).length };
}

export interface Opts {
  at?: string;
  locale?: Locale;
  theme?: ThemePreference;
  signedIn?: boolean;
}

/** Renders the whole app at an address, signed in by default. */
export function renderApp(opts: Opts = {}): RenderResult {
  if (opts.signedIn !== false) setToken("trica_test_token");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 0, gcTime: Infinity } } });
  return render(
    <Providers client={client} locale={opts.locale ?? "en"} theme={opts.theme}>
      <MemoryRouter initialEntries={[opts.at ?? "/"]}>
        <App />
      </MemoryRouter>
    </Providers>,
  );
}

/** The answers every signed-in test needs; tests override what they care about. */
export function defaultRoutes(me: Handler = APPROVER): Record<string, Handler> {
  const objects = [swA1, srv01, rackR12, siteParis];
  const routes: Record<string, Handler> = {
    "GET /me": me,
    "GET /schema": schema,
    // A device's components are found with parent:"<id>"; every other query answers searchSw.
    "GET /search": (c) => {
      const m = /^parent:"?([^"]+)"?$/.exec(c.query.get("q") ?? "");
      if (!m) return searchSw;
      const items = m[1] === ID.srv01 ? componentsSrv01 : [];
      return { ...searchSw, summary: { ...searchSw.summary, query: c.query.get("q") ?? "", total: items.length, by_type: {} }, total: items.length, items };
    },
    "GET /objects": { ...searchSw, items: [swA1], total: 2, limit: 50 },
    "GET /relations": (c) => ((c.query.get("type") ?? "").split(",").includes("mounted_in") ? mountsLinks : page([])),
    [`GET /objects/${ID.srv01}/relations`]: page([]),
    [`GET /objects/${ID.srv01}/timeline`]: page([]),
    [`GET /objects/${ID.srv01}/provenance`]: { object_id: ID.srv01, attributes: [] },
    [`GET /objects/${ID.swA1}/provenance`]: provenanceSwA1,
    [`GET /objects/${ID.swA1}/relations`]: relationsSwA1,
    [`GET /objects/${ID.swA1}/timeline`]: timelineSwA1,
    "GET /proposals": page([proposalPending, proposalApplied]),
    "GET /proposals/12": proposalPendingDetail,
    "GET /proposals/11": proposalApplied,
    "GET /discrepancies": page([discrepancyMismatch]),
    "GET /health/collection": page([healthOk, healthFailing]),
    "GET /collectors": collectorCatalogue,
    "GET /collectors/arista": collectorArista,
    "GET /collectors/agent": collectorAgent,
    "GET /collectors/unifi": collectorUnifi,
    "GET /collectors/arista/instances": (c: Call) => {
      const status = c.query.get("status");
      const all = [instanceSwA2, instanceSwA3, instanceSwA1];
      return page(status ? all.filter((i) => i.status === status) : all);
    },
    "GET /collectors/agent/instances": page([instanceReported]),
    "GET /collectors/unifi/instances": page([]),
    "GET /collectors/arista/instances/sw-a1": instanceSwA1,
    "GET /collectors/arista/instances/sw-a2": instanceSwA2,
    "GET /collectors/agent/instances/host-01": instanceReported,
  };
  for (const o of objects) routes[`GET /objects/${o.id}`] = o;
  for (const o of [swA1, srv01]) routes[`GET /objects/${o.id}/series`] = (c: Call) => seriesOf(o.id, c.query);
  // No model, so nothing to draw: the tests of the faces declare their own.
  for (const o of [swA1, srv01]) routes[`GET /objects/${o.id}/faces`] = { faces: [], parts: [], truncated: false };
  // Nothing the library knows: no model section. The tests of the chooser declare their own.
  for (const o of [swA1, srv01])
    routes[`GET /objects/${o.id}/device-type-candidates`] = {
      device: { id: o.id, type: o.type, display: o.display },
      certain: false,
      reason: "none",
      declarations: [],
      candidates: [],
      can_choose: false,
    };
  return routes;
}

import type { Problem } from "./types";
import { renewSession } from "../auth/renew";
import { getRenewal, getToken, rejectToken } from "./token";

export const API_BASE = "/api/v1";

/** An error answered by the API (RFC 9457 problem+json) or a network failure (status 0). */
export class ApiError extends Error {
  readonly status: number;
  /** `urn:trica:problem:<slug>`, or "" when the answer was not a problem document. */
  readonly type: string;
  readonly title: string;
  readonly detail: string;

  constructor(status: number, problem: Partial<Problem>) {
    super(problem.detail || problem.title || `HTTP ${status}`);
    this.name = "ApiError";
    this.status = status;
    this.type = problem.type ?? "";
    this.title = problem.title ?? "";
    this.detail = problem.detail ?? "";
  }
}

type QueryValue = string | number | boolean | null | undefined;

export function buildQuery(query: object | undefined): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries((query ?? {}) as Record<string, QueryValue>)) {
    if (v === undefined || v === null || v === "") continue;
    sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}

export interface RequestOptions {
  method?: "GET" | "POST" | "PUT" | "DELETE";
  query?: object;
  /** Sent as JSON. Only the batch calls have one: the UI writes nothing else. */
  body?: unknown;
  /** Sent as is, with its own content type: the one upload whose body is a file (a theme archive). */
  raw?: { body: Blob; type: string };
  signal?: AbortSignal;
}

/**
 * One call to the API. The credential is the bearer token of the session; a 401
 * means it is missing, expired or revoked. A single sign-on session is renewed
 * once and the call replayed (a tab asleep in the background may hold a token
 * that expired meanwhile); otherwise, or when renewal fails, the token is
 * dropped and the app shows the sign-in form again.
 */
export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const token = getToken();
  let sent = token;
  let res = await send(path, opts, token);
  if (res.status === 401 && token && getRenewal()) {
    // Another call may already have renewed the session.
    const renewed = getToken() !== token || (await renewSession());
    const next = getToken();
    if (renewed && next && next !== token) res = await send(path, opts, (sent = next));
  }

  if (!res.ok) {
    let problem: Partial<Problem> = {};
    try {
      problem = (await res.json()) as Partial<Problem>;
    } catch {
      /* not JSON: keep the status only */
    }
    // Only the token that was refused is dropped, not one set meanwhile.
    if (res.status === 401 && sent && getToken() === sent) rejectToken();
    throw new ApiError(res.status, problem);
  }
  // 204: the call succeeded and has nothing to say (a delete, a reset).
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

async function send(path: string, opts: RequestOptions, token: string | null): Promise<Response> {
  const headers: Record<string, string> = { Accept: "application/json, application/problem+json" };
  if (token) headers.Authorization = `Bearer ${token}`;
  if (opts.raw) headers["Content-Type"] = opts.raw.type;
  else if (opts.body !== undefined) headers["Content-Type"] = "application/json";
  try {
    return await fetch(`${API_BASE}${path}${buildQuery(opts.query)}`, {
      method: opts.method ?? "GET",
      headers,
      body: opts.raw ? opts.raw.body : opts.body === undefined ? undefined : JSON.stringify(opts.body),
      signal: opts.signal,
      credentials: "omit",
    });
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") throw e;
    throw new ApiError(0, { title: "network", detail: e instanceof Error ? e.message : "network error" });
  }
}

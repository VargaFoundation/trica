import type { Problem } from "./types";
import { getToken, rejectToken } from "./token";

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
  method?: "GET" | "POST";
  query?: object;
  /** Sent as JSON. Only the batch calls have one: the UI writes nothing else. */
  body?: unknown;
  signal?: AbortSignal;
}

/**
 * One call to the API. The credential is the bearer token of the session; a 401
 * means it is missing, expired or revoked, so it is dropped and the app shows
 * the sign-in form again.
 */
export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json, application/problem+json" };
  const token = getToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  if (opts.body !== undefined) headers["Content-Type"] = "application/json";

  let res: Response;
  try {
    res = await fetch(`${API_BASE}${path}${buildQuery(opts.query)}`, {
      method: opts.method ?? "GET",
      headers,
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      signal: opts.signal,
      credentials: "omit",
    });
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") throw e;
    throw new ApiError(0, { title: "network", detail: e instanceof Error ? e.message : "network error" });
  }

  if (!res.ok) {
    let problem: Partial<Problem> = {};
    try {
      problem = (await res.json()) as Partial<Problem>;
    } catch {
      /* not JSON: keep the status only */
    }
    if (res.status === 401 && token) rejectToken();
    throw new ApiError(res.status, problem);
  }
  return (await res.json()) as T;
}

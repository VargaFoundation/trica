// The bearer credential the API asks for (an API token or an OIDC access
// token). It lives in memory and in sessionStorage, so that a reload of the tab
// keeps the session and closing the tab ends it. It is only ever sent to the
// same origin, as an Authorization header, and never logged.
//
// A single sign-on session also keeps what renews the token (auth/renew.ts): the
// provider's refresh token and where to spend it. It lives and dies with the
// bearer token: same storage, dropped with it.
const KEY = "trica.token";
const RENEWAL_KEY = "trica.oidc.renewal";

/** What renews an OIDC session without sending the reader back to the provider. */
export interface Renewal {
  refresh_token: string;
  token_endpoint: string;
  client_id: string;
  bearer: "access_token" | "id_token";
}

export interface AuthState {
  token: string | null;
  /** True when the API answered 401 to the token we had: the sign-in form says so. */
  rejected: boolean;
}

let state: AuthState | null = null;
const listeners = new Set<() => void>();

function readStored(): string | null {
  try {
    return sessionStorage.getItem(KEY);
  } catch {
    return null; // storage can throw (private window, blocked site data)
  }
}

function persist(token: string | null): void {
  try {
    if (token) sessionStorage.setItem(KEY, token);
    else sessionStorage.removeItem(KEY);
  } catch {
    /* the in-memory copy is enough for this page */
  }
}

function persistRenewal(r: Renewal | null): void {
  try {
    if (r) sessionStorage.setItem(RENEWAL_KEY, JSON.stringify(r));
    else sessionStorage.removeItem(RENEWAL_KEY);
  } catch {
    /* without storage a reload signs in again */
  }
}

/** The stored renewal, or null when there is none or it is not well formed. */
export function getRenewal(): Renewal | null {
  try {
    const raw = sessionStorage.getItem(RENEWAL_KEY);
    if (!raw) return null;
    const r = JSON.parse(raw) as Partial<Renewal>;
    if (typeof r.refresh_token !== "string" || typeof r.token_endpoint !== "string" || typeof r.client_id !== "string") return null;
    return { refresh_token: r.refresh_token, token_endpoint: r.token_endpoint, client_id: r.client_id, bearer: r.bearer === "id_token" ? "id_token" : "access_token" };
  } catch {
    return null;
  }
}

function set(next: AuthState): void {
  state = next;
  listeners.forEach((l) => l());
}

/** The same object until something changes: fit for useSyncExternalStore. */
export function getAuthState(): AuthState {
  state ??= { token: readStored(), rejected: false };
  return state;
}

export function getToken(): string | null {
  return getAuthState().token;
}

/**
 * Starts (or replaces) the session. A pasted API token comes without a renewal,
 * which drops any renewal left from a single sign-on session.
 */
export function setToken(token: string, renewal: Renewal | null = null): void {
  const t = token.trim() || null;
  persist(t);
  persistRenewal(t ? renewal : null);
  set({ token: t, rejected: false });
}

export function clearToken(): void {
  setToken("");
}

/** The API refused the token (401): forget it and tell the sign-in form why. */
export function rejectToken(): void {
  persist(null);
  persistRenewal(null);
  set({ token: null, rejected: true });
}

export function subscribeAuth(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Test helper: forget everything, including what was loaded from storage. */
export function resetAuthForTest(): void {
  state = null;
  listeners.clear();
}

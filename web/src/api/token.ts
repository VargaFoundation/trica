// The bearer credential the API asks for (an API token or an OIDC access
// token). It lives in memory and in sessionStorage, so that a reload of the tab
// keeps the session and closing the tab ends it. It is only ever sent to the
// same origin, as an Authorization header, and never logged.
const KEY = "trica.token";

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

export function setToken(token: string): void {
  const t = token.trim() || null;
  persist(t);
  set({ token: t, rejected: false });
}

export function clearToken(): void {
  setToken("");
}

/** The API refused the token (401): forget it and tell the sign-in form why. */
export function rejectToken(): void {
  persist(null);
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

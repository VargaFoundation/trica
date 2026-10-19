// Keeps a single sign-on session alive (ADR 0019). An access token from the
// provider lives a few minutes (five with Keycloak's defaults); without renewal
// the reader was sent back to the sign-in form every five minutes. With the
// refresh token the provider gave at sign-in, the page asks for a new access
// token shortly before the current one expires, and once more when the API
// answers 401 (a tab in the background may have slept past the expiry).
//
// The refresh token is a public client's: no secret is sent. It is spent at the
// token endpoint that discovery validated at sign-in (https, the issuer's
// origin). When the provider refuses it (the session ended, was revoked, or hit
// its maximum lifetime), the session is over and the sign-in form says so.
import { getAuthState, getRenewal, rejectToken, setToken, subscribeAuth } from "../api/token";
import { jwtPayload } from "./oidc";

/** Renew this long before the token's expiry. */
export const RENEW_AHEAD_MS = 60_000;
/** Never wait less than this between two renewals (a token that is already stale). */
const MIN_DELAY_MS = 5_000;

let inflight: Promise<boolean> | null = null;

/**
 * Asks the provider for a new token with the stored refresh token. True when the
 * session now carries a new token; false when there is no renewal or the
 * provider refused it (then the session is dropped). A network failure keeps
 * the session as it is and answers false. Concurrent callers share one request.
 */
export function renewSession(): Promise<boolean> {
  inflight ??= doRenew().finally(() => {
    inflight = null;
  });
  return inflight;
}

async function doRenew(): Promise<boolean> {
  const r = getRenewal();
  if (!r) return false;
  const body = new URLSearchParams({ grant_type: "refresh_token", refresh_token: r.refresh_token, client_id: r.client_id });
  let res: Response;
  try {
    res = await fetch(r.token_endpoint, {
      method: "POST",
      credentials: "omit",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body,
    });
  } catch {
    return false;
  }
  let tokens: { access_token?: string; id_token?: string; refresh_token?: string } = {};
  try {
    tokens = (await res.json()) as typeof tokens;
  } catch {
    /* handled below as a refusal when the status says so */
  }
  const token = r.bearer === "id_token" ? tokens.id_token : tokens.access_token;
  if (!res.ok || !token) {
    // 5xx: the provider is in trouble, not the session; try again later.
    if (res.status >= 500) return false;
    rejectToken();
    return false;
  }
  // Keycloak rotates refresh tokens when told to: keep the newest one.
  setToken(token, { ...r, refresh_token: tokens.refresh_token ?? r.refresh_token });
  return true;
}

/** Milliseconds until the token should be renewed; null when it carries no expiry. */
export function renewDelay(token: string, now = Date.now()): number | null {
  const exp = jwtPayload(token)?.exp;
  if (typeof exp !== "number") return null;
  return Math.max(MIN_DELAY_MS, exp * 1000 - RENEW_AHEAD_MS - now);
}

/**
 * Renews the session ahead of each expiry for as long as it has a renewal.
 * Returns the function that stops it. Started once by the app.
 */
export function startRenewal(): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const plan = () => {
    clearTimeout(timer);
    timer = undefined;
    const { token } = getAuthState();
    if (!token || !getRenewal()) return;
    const delay = renewDelay(token);
    if (delay !== null) timer = setTimeout(() => void renewSession(), delay);
  };
  // A tab coming back from the background may hold a token that expired while
  // its timers were throttled.
  const onVisible = () => {
    if (document.visibilityState !== "visible") return;
    const { token } = getAuthState();
    if (token && getRenewal() && renewDelay(token) === MIN_DELAY_MS) void renewSession();
  };
  const unsubscribe = subscribeAuth(plan);
  document.addEventListener("visibilitychange", onVisible);
  plan();
  return () => {
    clearTimeout(timer);
    unsubscribe();
    document.removeEventListener("visibilitychange", onVisible);
  };
}

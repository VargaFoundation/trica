// Browser sign-in: OpenID Connect authorisation code flow with PKCE (RFC 7636,
// S256), as a public client (ADR 0019). There is no client secret anywhere in
// this file or in the page: the code verifier is the proof, and it never leaves
// the tab. The server does not take part in the flow; it keeps verifying the
// bearer token like any other (ADR 0013).
//
// Everything here is a plain function over fetch, sessionStorage and WebCrypto,
// so the flow is tested without a browser.

import type { Renewal } from "../api/token";

/** The path the identity provider redirects back to. Register `<origin>/oidc/callback` as the client's redirect URI. */
export const CALLBACK_PATH = "/oidc/callback";

const PENDING_KEY = "trica.oidc.pending";
/** A sign-in that was started and not finished this long ago is forgotten. */
const PENDING_TTL_MS = 10 * 60 * 1000;

export interface OidcClient {
  issuer: string;
  client_id: string;
  scopes: string[];
  /** The token the API gets: the access token (default) or the ID token. */
  bearer: "access_token" | "id_token";
}

export interface UiConfig {
  oidc: OidcClient | null;
}

export interface Endpoints {
  authorization_endpoint: string;
  token_endpoint: string;
}

interface Pending {
  state: string;
  verifier: string;
  nonce: string;
  returnTo: string;
  startedAt: number;
}

export class OidcError extends Error {
  /** Stable code the page maps to a translated message. */
  readonly code: "config" | "discovery" | "state" | "provider" | "exchange" | "nonce" | "expired";
  constructor(code: OidcError["code"], message: string) {
    super(message);
    this.name = "OidcError";
    this.code = code;
  }
}

/** What the server tells the page at start-up. Anything unexpected means "no single sign-on": the token form still works. */
export async function loadUiConfig(signal?: AbortSignal): Promise<UiConfig> {
  try {
    const res = await fetch("/ui-config.json", { credentials: "omit", cache: "no-store", signal, headers: { Accept: "application/json" } });
    if (!res.ok) return { oidc: null };
    const body: unknown = await res.json();
    return { oidc: readOidc(body) };
  } catch {
    return { oidc: null };
  }
}

function readOidc(body: unknown): OidcClient | null {
  const o = (body as { oidc?: Partial<OidcClient> | null } | null)?.oidc;
  if (!o || typeof o.issuer !== "string" || typeof o.client_id !== "string" || !o.issuer || !o.client_id) return null;
  const scopes = Array.isArray(o.scopes) ? o.scopes.filter((s): s is string => typeof s === "string" && s !== "") : [];
  return {
    issuer: o.issuer,
    client_id: o.client_id,
    scopes: scopes.length ? scopes : ["openid"],
    bearer: o.bearer === "id_token" ? "id_token" : "access_token",
  };
}

export function redirectUri(): string {
  return `${window.location.origin}${CALLBACK_PATH}`;
}

// ---- PKCE ---------------------------------------------------------------

export function base64url(bytes: Uint8Array): string {
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/** `n` random bytes from the platform generator, base64url: 32 bytes give the 43 characters RFC 7636 asks for at least. */
export function randomToken(n = 32): string {
  return base64url(crypto.getRandomValues(new Uint8Array(n)));
}

/** code_challenge = BASE64URL(SHA-256(code_verifier)): the S256 method, the only one used. */
export async function challengeFor(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier));
  return base64url(new Uint8Array(digest));
}

// ---- discovery ----------------------------------------------------------

const origin = (u: string): string => new URL(u).origin;

/**
 * Reads <issuer>/.well-known/openid-configuration and keeps only what is safe
 * to use: the issuer must match, both endpoints must be https, and the token
 * endpoint must be on the issuer's origin, because the page's
 * Content-Security-Policy lets scripts reach that origin and no other.
 */
export async function discover(cfg: OidcClient, signal?: AbortSignal): Promise<Endpoints> {
  let doc: Record<string, unknown>;
  try {
    const res = await fetch(`${cfg.issuer.replace(/\/+$/, "")}/.well-known/openid-configuration`, {
      credentials: "omit",
      signal,
      headers: { Accept: "application/json" },
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    doc = (await res.json()) as Record<string, unknown>;
  } catch (e) {
    throw new OidcError("discovery", e instanceof Error ? e.message : String(e));
  }
  if (doc.issuer !== cfg.issuer) throw new OidcError("discovery", "the provider announces another issuer");
  const authz = doc.authorization_endpoint;
  const token = doc.token_endpoint;
  if (typeof authz !== "string" || typeof token !== "string") throw new OidcError("discovery", "endpoints missing");
  for (const u of [authz, token]) {
    if (!/^https:\/\//.test(u)) throw new OidcError("discovery", "endpoints must be https");
  }
  if (origin(token) !== origin(cfg.issuer)) throw new OidcError("discovery", "the token endpoint is not on the issuer's origin");
  const methods = doc.code_challenge_methods_supported;
  if (Array.isArray(methods) && !methods.includes("S256")) throw new OidcError("discovery", "the provider does not support PKCE S256");
  return { authorization_endpoint: authz, token_endpoint: token };
}

// ---- start --------------------------------------------------------------

function savePending(p: Pending): void {
  try {
    sessionStorage.setItem(PENDING_KEY, JSON.stringify(p));
  } catch {
    throw new OidcError("state", "session storage is not available: the sign-in cannot be completed in this tab");
  }
}

function takePending(): Pending | null {
  try {
    const raw = sessionStorage.getItem(PENDING_KEY);
    sessionStorage.removeItem(PENDING_KEY); // one shot: a replayed callback finds nothing
    return raw ? (JSON.parse(raw) as Pending) : null;
  } catch {
    return null;
  }
}

/** Where the reader was going, kept so that they land there after signing in. Only same-origin paths are honoured. */
export function safeReturnTo(path: string | undefined): string {
  return path && path.startsWith("/") && !path.startsWith("//") && !path.startsWith(CALLBACK_PATH) ? path : "/";
}

/**
 * Prepares a sign-in and returns the address to send the browser to. The state
 * (against forged callbacks), the verifier (PKCE) and the nonce are kept in
 * sessionStorage until the provider redirects back.
 */
export async function buildAuthorizationUrl(cfg: OidcClient, endpoints: Endpoints, returnTo: string): Promise<string> {
  const verifier = randomToken(32);
  const pending: Pending = {
    state: randomToken(16),
    verifier,
    nonce: randomToken(16),
    returnTo: safeReturnTo(returnTo),
    startedAt: Date.now(),
  };
  savePending(pending);
  const u = new URL(endpoints.authorization_endpoint);
  u.searchParams.set("response_type", "code");
  u.searchParams.set("client_id", cfg.client_id);
  u.searchParams.set("redirect_uri", redirectUri());
  u.searchParams.set("scope", cfg.scopes.join(" "));
  u.searchParams.set("state", pending.state);
  u.searchParams.set("nonce", pending.nonce);
  u.searchParams.set("code_challenge", await challengeFor(verifier));
  u.searchParams.set("code_challenge_method", "S256");
  return u.toString();
}

/** Discovery, then the redirect. */
export async function startSignIn(cfg: OidcClient, returnTo: string, go: (url: string) => void = (u) => window.location.assign(u)): Promise<void> {
  const endpoints = await discover(cfg);
  go(await buildAuthorizationUrl(cfg, endpoints, returnTo));
}

// ---- finish -------------------------------------------------------------

export interface SignedIn {
  /** The bearer credential for the API. */
  token: string;
  returnTo: string;
  /** How to renew the token before it expires; null when the provider gave no refresh token. */
  renewal: Renewal | null;
}

/** The claims of a JWT, unverified (the API verifies); null when it is not one. */
export function jwtPayload(jwt: string): Record<string, unknown> | null {
  const part = jwt.split(".")[1];
  if (!part) return null;
  try {
    const b64 = part.replace(/-/g, "+").replace(/_/g, "/");
    const json = new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)));
    return JSON.parse(json) as Record<string, unknown>;
  } catch {
    return null;
  }
}

/**
 * Finishes a sign-in from the query string the provider redirected with:
 * checks the state, exchanges the code (with the verifier, without any secret),
 * checks the nonce of the ID token and returns the bearer token. The API, not
 * this function, verifies the token's signature and claims.
 */
export async function completeSignIn(cfg: OidcClient, search: string, signal?: AbortSignal): Promise<SignedIn> {
  const params = new URLSearchParams(search);
  const pending = takePending();
  const err = params.get("error");
  if (err) throw new OidcError("provider", params.get("error_description") || err);
  if (!pending) throw new OidcError("state", "no sign-in is in progress in this tab");
  if (Date.now() - pending.startedAt > PENDING_TTL_MS) throw new OidcError("expired", "the sign-in took too long");
  const state = params.get("state");
  if (!state || state !== pending.state) throw new OidcError("state", "the state does not match");
  const code = params.get("code");
  if (!code) throw new OidcError("provider", "no code in the answer");

  const endpoints = await discover(cfg, signal);
  const body = new URLSearchParams({
    grant_type: "authorization_code",
    code,
    redirect_uri: redirectUri(),
    client_id: cfg.client_id,
    code_verifier: pending.verifier,
  });
  let res: Response;
  try {
    res = await fetch(endpoints.token_endpoint, {
      method: "POST",
      credentials: "omit",
      signal,
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body,
    });
  } catch (e) {
    throw new OidcError("exchange", e instanceof Error ? e.message : String(e));
  }
  let tokens: { access_token?: string; id_token?: string; refresh_token?: string; error?: string; error_description?: string };
  try {
    tokens = (await res.json()) as typeof tokens;
  } catch {
    throw new OidcError("exchange", `HTTP ${res.status}`);
  }
  if (!res.ok) throw new OidcError("exchange", tokens.error_description || tokens.error || `HTTP ${res.status}`);

  if (tokens.id_token) {
    const claims = jwtPayload(tokens.id_token);
    if (claims && claims.nonce !== undefined && claims.nonce !== pending.nonce) throw new OidcError("nonce", "the ID token carries another nonce");
  }
  const token = cfg.bearer === "id_token" ? tokens.id_token : tokens.access_token;
  if (!token) throw new OidcError("exchange", `the answer has no ${cfg.bearer}`);
  const renewal: Renewal | null = tokens.refresh_token
    ? { refresh_token: tokens.refresh_token, token_endpoint: endpoints.token_endpoint, client_id: cfg.client_id, bearer: cfg.bearer }
    : null;
  return { token, returnTo: pending.returnTo, renewal };
}

// Where to send the reader once the token is in place. The token appearing
// swaps the sign-in screen for the application, whose /oidc/callback route
// then redirects here; handing it over in a variable avoids racing the router.
let landing = "/";
export function setLanding(path: string): void {
  landing = safeReturnTo(path);
}
export function takeLanding(): string {
  const p = landing;
  landing = "/";
  return p;
}

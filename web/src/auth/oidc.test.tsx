import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { getRenewal, getToken } from "../api/token";
import { expectNoA11yViolations } from "../test/axe";
import { defaultRoutes, mockApi, renderApp } from "../test/support";
import { buildAuthorizationUrl, challengeFor, completeSignIn, discover, loadUiConfig, safeReturnTo, startSignIn, type OidcClient } from "./oidc";

const ISSUER = "https://idp.example.test/realms/trica";
const CFG: OidcClient = { issuer: ISSUER, client_id: "trica-ui", scopes: ["openid", "profile"], bearer: "access_token" };
const DISCOVERY = {
  issuer: ISSUER,
  authorization_endpoint: `${ISSUER}/protocol/openid-connect/auth`,
  token_endpoint: `${ISSUER}/protocol/openid-connect/token`,
  code_challenge_methods_supported: ["S256", "plain"],
};

function jwt(claims: object): string {
  const enc = (o: object) => btoa(JSON.stringify(o)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  return `${enc({ alg: "none" })}.${enc(claims)}.sig`;
}

interface Idp {
  tokenCalls: URLSearchParams[];
}

/** A fake identity provider and ui-config on top of fetch. */
function fakeIdp(opts: { config?: unknown; discovery?: unknown; token?: (form: URLSearchParams) => { status?: number; body: unknown } } = {}): Idp {
  const idp: Idp = { tokenCalls: [] };
  vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, "http://localhost");
    const json = (body: unknown, status = 200) => Promise.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
    if (url.pathname === "/ui-config.json") return json(opts.config ?? { oidc: CFG });
    if (url.href === `${ISSUER}/.well-known/openid-configuration`) return json(opts.discovery ?? DISCOVERY);
    if (url.href === DISCOVERY.token_endpoint) {
      const form = new URLSearchParams(String(init?.body));
      idp.tokenCalls.push(form);
      const r = opts.token?.(form) ?? { body: { access_token: "at-1" } };
      return json(r.body, r.status ?? 200);
    }
    return json({ type: "urn:trica:problem:not-found", status: 404 }, 404);
  });
  return idp;
}

/** Starts a sign-in the way the page does and returns what the provider would see, plus the stored nonce. */
async function begin(returnTo = "/objects/abc"): Promise<{ url: URL; nonce: string }> {
  const url = new URL(await buildAuthorizationUrl(CFG, DISCOVERY, returnTo));
  return { url, nonce: url.searchParams.get("nonce") ?? "" };
}

describe("PKCE", () => {
  it("derives the S256 challenge of RFC 7636 appendix B", async () => {
    expect(await challengeFor("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")).toBe("E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM");
  });

  it("the authorisation request carries the challenge of the stored verifier, S256, state, nonce and the callback", async () => {
    const { url } = await begin();
    const q = url.searchParams;
    expect(url.origin + url.pathname).toBe(DISCOVERY.authorization_endpoint);
    expect(q.get("response_type")).toBe("code");
    expect(q.get("client_id")).toBe("trica-ui");
    expect(q.get("scope")).toBe("openid profile");
    expect(q.get("redirect_uri")).toBe(`${window.location.origin}/oidc/callback`);
    expect(q.get("code_challenge_method")).toBe("S256");
    const stored = JSON.parse(sessionStorage.getItem("trica.oidc.pending") ?? "{}") as { verifier: string; state: string };
    expect(q.get("code_challenge")).toBe(await challengeFor(stored.verifier));
    expect(q.get("state")).toBe(stored.state);
    expect(stored.verifier.length).toBeGreaterThanOrEqual(43);
    expect(q.has("client_secret")).toBe(false);
    expect(url.href).not.toContain(stored.verifier);
  });

  it("every sign-in gets a fresh state and verifier", async () => {
    await begin();
    const a = sessionStorage.getItem("trica.oidc.pending");
    await begin();
    expect(sessionStorage.getItem("trica.oidc.pending")).not.toBe(a);
  });
});

describe("completeSignIn", () => {
  it("exchanges the code with the verifier, without any secret, and returns the access token and where to go", async () => {
    const idp = fakeIdp();
    const { url, nonce } = await begin("/objects/abc?tab=history");
    const stored = JSON.parse(sessionStorage.getItem("trica.oidc.pending") ?? "{}") as { verifier: string };
    const state = url.searchParams.get("state") ?? "";
    idp.tokenCalls.length = 0;
    const out = await completeSignIn(CFG, `?code=the-code&state=${state}`);
    expect(out).toEqual({ token: "at-1", returnTo: "/objects/abc?tab=history", renewal: null });
    expect(idp.tokenCalls).toHaveLength(1);
    const form = idp.tokenCalls[0]!;
    expect(Object.fromEntries(form)).toEqual({
      grant_type: "authorization_code",
      code: "the-code",
      redirect_uri: `${window.location.origin}/oidc/callback`,
      client_id: "trica-ui",
      code_verifier: stored.verifier,
    });
    expect(form.has("client_secret")).toBe(false);
    expect(nonce).not.toBe("");
  });

  it("sends the ID token when the configuration says so", async () => {
    const { url, nonce } = await begin();
    fakeIdp({ token: () => ({ body: { access_token: "at-1", id_token: jwt({ nonce }) } }) });
    const out = await completeSignIn({ ...CFG, bearer: "id_token" }, `?code=c&state=${url.searchParams.get("state")}`);
    expect(out.token).toBe(jwt({ nonce }));
  });

  it("keeps the refresh token with the endpoint, client and bearer it belongs to", async () => {
    const { url } = await begin();
    fakeIdp({ token: () => ({ body: { access_token: "at-1", refresh_token: "rt-1" } }) });
    const out = await completeSignIn(CFG, `?code=c&state=${url.searchParams.get("state")}`);
    expect(out.renewal).toEqual({ refresh_token: "rt-1", token_endpoint: DISCOVERY.token_endpoint, client_id: "trica-ui", bearer: "access_token" });
  });

  it("rejects a callback whose state is not the one that was stored", async () => {
    const idp = fakeIdp();
    await begin();
    await expect(completeSignIn(CFG, "?code=c&state=forged")).rejects.toMatchObject({ code: "state" });
    expect(idp.tokenCalls).toHaveLength(0);
  });

  it("rejects a callback with no state, and one with no sign-in in progress", async () => {
    const idp = fakeIdp();
    await begin();
    await expect(completeSignIn(CFG, "?code=c")).rejects.toMatchObject({ code: "state" });
    await expect(completeSignIn(CFG, "?code=c&state=x")).rejects.toMatchObject({ code: "state" });
    expect(idp.tokenCalls).toHaveLength(0);
  });

  it("a callback address works once: replaying it fails", async () => {
    fakeIdp();
    const { url } = await begin();
    const search = `?code=c&state=${url.searchParams.get("state")}`;
    await completeSignIn(CFG, search);
    await expect(completeSignIn(CFG, search)).rejects.toMatchObject({ code: "state" });
  });

  it("a sign-in left open for more than ten minutes is refused", async () => {
    fakeIdp();
    const { url } = await begin();
    const p = JSON.parse(sessionStorage.getItem("trica.oidc.pending") ?? "{}") as { startedAt: number };
    p.startedAt -= 11 * 60 * 1000;
    sessionStorage.setItem("trica.oidc.pending", JSON.stringify(p));
    await expect(completeSignIn(CFG, `?code=c&state=${url.searchParams.get("state")}`)).rejects.toMatchObject({ code: "expired" });
  });

  it("reports the provider's refusal", async () => {
    fakeIdp();
    await begin();
    await expect(completeSignIn(CFG, "?error=access_denied&error_description=User+said+no")).rejects.toMatchObject({
      code: "provider",
      message: "User said no",
    });
  });

  it("rejects an ID token issued for another nonce", async () => {
    const { url } = await begin();
    fakeIdp({ token: () => ({ body: { access_token: "at-1", id_token: jwt({ nonce: "someone-else" }) } }) });
    await expect(completeSignIn(CFG, `?code=c&state=${url.searchParams.get("state")}`)).rejects.toMatchObject({ code: "nonce" });
  });

  it("reports a refused code exchange", async () => {
    const { url } = await begin();
    fakeIdp({ token: () => ({ status: 400, body: { error: "invalid_grant", error_description: "Code not valid" } }) });
    await expect(completeSignIn(CFG, `?code=c&state=${url.searchParams.get("state")}`)).rejects.toMatchObject({ code: "exchange", message: "Code not valid" });
  });
});

describe("discovery", () => {
  it("returns the two endpoints", async () => {
    fakeIdp();
    expect(await discover(CFG)).toEqual({ authorization_endpoint: DISCOVERY.authorization_endpoint, token_endpoint: DISCOVERY.token_endpoint });
  });

  it.each([
    ["another issuer", { ...DISCOVERY, issuer: "https://evil.example.test" }],
    ["a token endpoint on another origin", { ...DISCOVERY, token_endpoint: "https://evil.example.test/token" }],
    ["a plain-http endpoint", { ...DISCOVERY, authorization_endpoint: "http://idp.example.test/auth" }],
    ["no S256", { ...DISCOVERY, code_challenge_methods_supported: ["plain"] }],
    ["no endpoints", { issuer: ISSUER }],
  ])("refuses %s", async (_name, doc) => {
    fakeIdp({ discovery: doc });
    await expect(discover(CFG)).rejects.toMatchObject({ code: "discovery" });
  });

  it("startSignIn navigates to the authorisation URL", async () => {
    fakeIdp();
    const go = vi.fn();
    await startSignIn(CFG, "/health", go);
    expect(go).toHaveBeenCalledTimes(1);
    expect(String(go.mock.calls[0]?.[0])).toContain(`${DISCOVERY.authorization_endpoint}?response_type=code`);
  });
});

describe("loadUiConfig and safeReturnTo", () => {
  it("reads the client settings", async () => {
    fakeIdp({ config: { oidc: { issuer: ISSUER, client_id: "trica-ui", scopes: ["openid"], bearer: "id_token" } } });
    expect((await loadUiConfig()).oidc).toMatchObject({ client_id: "trica-ui", bearer: "id_token" });
  });

  it.each([["null oidc", { oidc: null }], ["an empty client id", { oidc: { issuer: ISSUER, client_id: "" } }], ["junk", "nope"]])("means no SSO for %s", async (_n, config) => {
    fakeIdp({ config });
    expect((await loadUiConfig()).oidc).toBeNull();
  });

  it("means no SSO when the endpoint is missing or the network fails", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("offline"));
    expect((await loadUiConfig()).oidc).toBeNull();
  });

  it("only same-origin paths are honoured as a destination", () => {
    expect(safeReturnTo("/objects/1?x=2")).toBe("/objects/1?x=2");
    for (const bad of ["//evil.example.test", "https://evil.example.test", "javascript:1", "", undefined, "/oidc/callback?code=1"]) {
      expect(safeReturnTo(bad), String(bad)).toBe("/");
    }
  });
});

describe("the sign-in page", () => {
  it("offers single sign-on next to the token form when the server configures it, and is accessible", async () => {
    fakeIdp();
    renderApp({ signedIn: false });
    expect(await screen.findByRole("button", { name: "Sign in with SSO" })).toBeInTheDocument();
    expect(screen.getByLabelText("Access token")).toBeInTheDocument();
    await expectNoA11yViolations();
  });

  it("offers only the token form when there is no SSO", async () => {
    fakeIdp({ config: { oidc: null } });
    renderApp({ signedIn: false });
    expect(await screen.findByLabelText("Access token")).toBeInTheDocument();
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: "Sign in with SSO" })).not.toBeInTheDocument();
  });

  it("still accepts a pasted token when SSO is on", async () => {
    fakeIdp();
    renderApp({ signedIn: false });
    await screen.findByRole("button", { name: "Sign in with SSO" });
    await userEvent.type(screen.getByLabelText("Access token"), "pasted-token");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(getToken()).toBe("pasted-token");
  });

  it("shows why the single sign-on could not start", async () => {
    fakeIdp({ discovery: { ...DISCOVERY, issuer: "https://evil.example.test" } });
    renderApp({ signedIn: false });
    await userEvent.click(await screen.findByRole("button", { name: "Sign in with SSO" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("could not be reached or answered something unusable");
  });

  it("completes the flow on the callback address, stores the token and lands on the page that was asked for", async () => {
    const { url } = await begin("/health");
    const routes = defaultRoutes();
    const api = mockApi({ ...routes, "GET /health/collection": routes["GET /health/collection"]! });
    // Layer the identity provider over the API mock.
    const base = api.spy.getMockImplementation()!;
    const idp = fakeIdpOver(base);
    renderApp({ signedIn: false, at: `/oidc/callback?code=the-code&state=${url.searchParams.get("state")}` });
    await screen.findByRole("heading", { name: /^health$/i, level: 1 });
    expect(getToken()).toBe("at-1");
    expect(getRenewal()?.refresh_token).toBe("rt-1");
    expect(idp.tokenCalls).toHaveLength(1);
    expect(api.calls.find((c) => c.path === "/me")?.headers.get("Authorization")).toBe("Bearer at-1");
  });

  it("a forged callback shows an error and signs nobody in", async () => {
    fakeIdp();
    await begin();
    renderApp({ signedIn: false, at: "/oidc/callback?code=c&state=forged" });
    expect(await screen.findByRole("alert")).toHaveTextContent("not started from this tab");
    expect(getToken()).toBeNull();
    await expectNoA11yViolations();
  });
});

/** Routes the provider's URLs and ui-config to fakes and everything else to the API mock. */
function fakeIdpOver(api: typeof fetch): Idp {
  const idp: Idp = { tokenCalls: [] };
  vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, "http://localhost");
    const json = (body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }));
    if (url.pathname === "/ui-config.json") return json({ oidc: CFG });
    if (url.href === `${ISSUER}/.well-known/openid-configuration`) return json(DISCOVERY);
    if (url.href === DISCOVERY.token_endpoint) {
      idp.tokenCalls.push(new URLSearchParams(String(init?.body)));
      return json({ access_token: "at-1", refresh_token: "rt-1" });
    }
    return api(input, init);
  });
  return idp;
}

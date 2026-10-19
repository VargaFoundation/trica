import { request } from "../api/client";
import { clearToken, getAuthState, getRenewal, getToken, setToken, type Renewal } from "../api/token";
import { RENEW_AHEAD_MS, renewDelay, renewSession, startRenewal } from "./renew";

const TOKEN_ENDPOINT = "https://idp.example.test/realms/trica/protocol/openid-connect/token";
const RENEWAL: Renewal = { refresh_token: "rt-1", token_endpoint: TOKEN_ENDPOINT, client_id: "trica-ui", bearer: "access_token" };

function jwt(claims: object): string {
  const enc = (o: object) => btoa(JSON.stringify(o)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  return `${enc({ alg: "none" })}.${enc(claims)}.sig`;
}

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const unauthorized = () => json({ type: "urn:trica:problem:unauthorized", status: 401 }, 401);

interface Fake {
  grants: URLSearchParams[];
  inits: RequestInit[];
  api: { path: string; auth: string | null }[];
}

/**
 * The identity provider's token endpoint plus an API that accepts only the
 * tokens listed in valid.
 */
function fake(opts: { grant?: () => Response | Promise<Response>; valid?: string[] } = {}): Fake {
  const f: Fake = { grants: [], inits: [], api: [] };
  vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    if (url === TOKEN_ENDPOINT) {
      f.grants.push(new URLSearchParams(String(init?.body)));
      f.inits.push(init ?? {});
      return Promise.resolve(opts.grant?.() ?? json({ access_token: "at-2", refresh_token: "rt-2" }));
    }
    const auth = new Headers(init?.headers).get("Authorization");
    f.api.push({ path: new URL(url, "http://localhost").pathname, auth });
    const ok = opts.valid?.some((t) => auth === `Bearer ${t}`) ?? true;
    return Promise.resolve(ok ? json({ subject: "alice" }) : unauthorized());
  });
  return f;
}

describe("renewDelay", () => {
  it("renews a minute before the token expires", () => {
    const now = 1_000_000_000_000;
    expect(renewDelay(jwt({ exp: now / 1000 + 300 }), now)).toBe(300_000 - RENEW_AHEAD_MS);
  });

  it("waits a few seconds at least, even for a token that is already stale", () => {
    const now = 1_000_000_000_000;
    expect(renewDelay(jwt({ exp: now / 1000 - 10 }), now)).toBe(5_000);
  });

  it("has nothing to plan for a token without expiry (a pasted API token)", () => {
    expect(renewDelay("trica_abc_secret")).toBeNull();
    expect(renewDelay(jwt({ sub: "x" }))).toBeNull();
  });
});

describe("renewSession", () => {
  it("spends the refresh token at the token endpoint, without secret or cookie, and keeps the rotated one", async () => {
    const f = fake();
    setToken("at-1", RENEWAL);
    expect(await renewSession()).toBe(true);
    expect(Object.fromEntries(f.grants[0]!)).toEqual({ grant_type: "refresh_token", refresh_token: "rt-1", client_id: "trica-ui" });
    expect(f.inits[0]).toMatchObject({ method: "POST", credentials: "omit" });
    expect(getToken()).toBe("at-2");
    expect(getRenewal()).toEqual({ ...RENEWAL, refresh_token: "rt-2" });
  });

  it("keeps the refresh token when the provider does not rotate it", async () => {
    fake({ grant: () => json({ access_token: "at-2" }) });
    setToken("at-1", RENEWAL);
    await renewSession();
    expect(getRenewal()?.refresh_token).toBe("rt-1");
  });

  it("uses the ID token when that is the bearer", async () => {
    fake({ grant: () => json({ access_token: "at-2", id_token: "idt-2" }) });
    setToken("idt-1", { ...RENEWAL, bearer: "id_token" });
    await renewSession();
    expect(getToken()).toBe("idt-2");
  });

  it("ends the session when the provider refuses the refresh token", async () => {
    fake({ grant: () => json({ error: "invalid_grant" }, 400) });
    setToken("at-1", RENEWAL);
    expect(await renewSession()).toBe(false);
    expect(getToken()).toBeNull();
    expect(getAuthState().rejected).toBe(true);
    expect(getRenewal()).toBeNull();
    expect(sessionStorage.getItem("trica.oidc.renewal")).toBeNull();
  });

  it("keeps the session through a network failure or a provider in trouble", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValueOnce(new TypeError("Failed to fetch"));
    setToken("at-1", RENEWAL);
    expect(await renewSession()).toBe(false);
    fake({ grant: () => json({ error: "temporarily_unavailable" }, 503) });
    expect(await renewSession()).toBe(false);
    expect(getToken()).toBe("at-1");
    expect(getRenewal()).toEqual(RENEWAL);
  });

  it("concurrent callers share one request", async () => {
    const f = fake();
    setToken("at-1", RENEWAL);
    expect(await Promise.all([renewSession(), renewSession(), renewSession()])).toEqual([true, true, true]);
    expect(f.grants).toHaveLength(1);
  });

  it("does nothing without a renewal", async () => {
    const f = fake();
    setToken("trica_abc_secret");
    expect(await renewSession()).toBe(false);
    expect(f.grants).toHaveLength(0);
  });
});

describe("the renewal lives and dies with the token", () => {
  it("pasting a token forgets the renewal of the previous session", () => {
    setToken("at-1", RENEWAL);
    expect(getRenewal()).toEqual(RENEWAL);
    setToken("trica_abc_secret");
    expect(getRenewal()).toBeNull();
  });

  it("signing out forgets it", () => {
    setToken("at-1", RENEWAL);
    clearToken();
    expect(getRenewal()).toBeNull();
    expect(sessionStorage.getItem("trica.oidc.renewal")).toBeNull();
  });
});

describe("startRenewal", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("renews before each expiry, again and again", async () => {
    vi.useFakeTimers();
    const now = Date.now();
    let n = 1;
    const f = fake({ grant: () => json({ access_token: jwt({ exp: Math.floor(Date.now() / 1000) + 300, n: ++n }) }) });
    setToken(jwt({ exp: Math.floor(now / 1000) + 300 }), RENEWAL);
    const stop = startRenewal();
    await vi.advanceTimersByTimeAsync(300_000 - RENEW_AHEAD_MS - 1_000);
    expect(f.grants).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(2_000);
    expect(f.grants).toHaveLength(1);
    // The new token is planned in turn.
    await vi.advanceTimersByTimeAsync(300_000);
    expect(f.grants).toHaveLength(2);
    stop();
    await vi.advanceTimersByTimeAsync(600_000);
    expect(f.grants).toHaveLength(2);
  });

  it("plans nothing for a pasted token", async () => {
    vi.useFakeTimers();
    const f = fake();
    setToken(jwt({ exp: Math.floor(Date.now() / 1000) + 300 }));
    const stop = startRenewal();
    await vi.advanceTimersByTimeAsync(600_000);
    expect(f.grants).toHaveLength(0);
    stop();
  });
});

describe("a 401 from the API", () => {
  it("renews the session once and replays the call, without signing out", async () => {
    const f = fake({ valid: ["at-2"] });
    setToken("at-1", RENEWAL);
    await expect(request("/me")).resolves.toEqual({ subject: "alice" });
    expect(f.api.map((c) => c.auth)).toEqual(["Bearer at-1", "Bearer at-2"]);
    expect(getToken()).toBe("at-2");
    expect(getAuthState().rejected).toBe(false);
  });

  it("signs out when the renewal is refused", async () => {
    fake({ valid: [], grant: () => json({ error: "invalid_grant" }, 400) });
    setToken("at-1", RENEWAL);
    await expect(request("/me")).rejects.toMatchObject({ status: 401 });
    expect(getToken()).toBeNull();
    expect(getAuthState().rejected).toBe(true);
  });

  it("signs out when the renewed token is refused too, without looping", async () => {
    const f = fake({ valid: [] });
    setToken("at-1", RENEWAL);
    await expect(request("/me")).rejects.toMatchObject({ status: 401 });
    expect(f.grants).toHaveLength(1);
    expect(f.api).toHaveLength(2);
    expect(getToken()).toBeNull();
  });

  it("does not drop a token set while the refused call was in flight", async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    vi.spyOn(globalThis, "fetch").mockImplementation(async () => {
      await gate;
      return unauthorized();
    });
    setToken("old");
    const call = request("/me");
    setToken("new");
    release();
    await expect(call).rejects.toMatchObject({ status: 401 });
    expect(getToken()).toBe("new");
  });
});

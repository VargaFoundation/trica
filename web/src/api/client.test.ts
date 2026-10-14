import { mockApi, problem } from "../test/support";
import { ApiError, buildQuery, request } from "./client";
import { getAuthState, getToken, setToken } from "./token";

describe("buildQuery", () => {
  it("drops empty values and encodes the rest", () => {
    expect(buildQuery({ q: "type:device vendor:arista", limit: 50, offset: 0, kind: "", status: undefined })).toBe(
      "?q=type%3Adevice+vendor%3Aarista&limit=50&offset=0",
    );
    expect(buildQuery(undefined)).toBe("");
  });
});

describe("request", () => {
  it("sends the bearer token and only asks for JSON", async () => {
    const api = mockApi({ "GET /me": { subject: "x" } });
    setToken("trica_abc_secret");
    await request("/me");
    const call = api.calls[0];
    expect(call?.headers.get("Authorization")).toBe("Bearer trica_abc_secret");
    expect(call?.headers.get("Accept")).toContain("application/json");
    expect(api.spy.mock.calls[0]?.[1]).toMatchObject({ credentials: "omit" });
  });

  it("sends no Authorization header without a token", async () => {
    const api = mockApi({ "GET /me": {} });
    await request("/me");
    expect(api.calls[0]?.headers.has("Authorization")).toBe(false);
  });

  it("turns a problem+json answer into an ApiError", async () => {
    mockApi({ "GET /objects/x": problem(404, "not-found", "no such object") });
    const err = await request("/objects/x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 404, type: "urn:trica:problem:not-found", detail: "no such object" });
    expect((err as ApiError).message).toBe("no such object");
  });

  it("keeps the status when the body is not JSON", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("<html>bad gateway</html>", { status: 502 }));
    await expect(request("/me")).rejects.toMatchObject({ status: 502, type: "" });
  });

  it("reports a network failure as status 0", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("Failed to fetch"));
    await expect(request("/me")).rejects.toMatchObject({ status: 0 });
  });

  it("drops the token on 401 and remembers why", async () => {
    mockApi({ "GET /me": problem(401, "unauthorized") });
    setToken("revoked");
    await expect(request("/me")).rejects.toMatchObject({ status: 401 });
    expect(getToken()).toBeNull();
    expect(getAuthState().rejected).toBe(true);
    expect(sessionStorage.getItem("trica.token")).toBeNull();
  });

  it("keeps the token on 403 (the caller is known, just not allowed)", async () => {
    mockApi({ "GET /me": problem(403, "forbidden") });
    setToken("viewer");
    await expect(request("/me")).rejects.toMatchObject({ status: 403 });
    expect(getToken()).toBe("viewer");
  });
});

describe("token store", () => {
  it("keeps the token in sessionStorage, not localStorage", () => {
    setToken("  trica_x_y ");
    expect(sessionStorage.getItem("trica.token")).toBe("trica_x_y");
    expect(localStorage.length).toBe(0);
  });

  it("works when storage throws", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    setToken("abc");
    expect(getToken()).toBe("abc");
  });
});

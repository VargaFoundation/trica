import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expectNoA11yViolations } from "../test/axe";
import { APPROVER, meWith, READER } from "../test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp } from "../test/support";

const ADMIN = meWith(["read", "propose", "approve", "admin"]);
const SHA = "a".repeat(64);
const ref = (name: string, source: string, sha?: string) => ({
  name,
  title: `${name} theme`,
  version: "1.0.0",
  source,
  ...(sha ? { sha256: sha } : {}),
});
const state = (over: Record<string, unknown> = {}) => ({
  active: ref("default", "config"),
  deployment_default: ref("default", "config"),
  locked: false,
  builtins: [ref("default", "builtin"), ref("flat", "builtin")],
  themes: [
    {
      sha256: SHA,
      name: "acme",
      title: "Acme",
      version: "2.1.0",
      size: 1200,
      origin: "upload",
      uploaded_by: "user:alice",
      uploaded_at: "2026-10-01T10:00:00Z",
    },
  ],
  ...over,
});

let reload: ReturnType<typeof vi.fn>;
beforeEach(() => {
  reload = vi.fn();
  vi.stubGlobal("location", { ...window.location, reload });
});
afterEach(() => vi.unstubAllGlobals());

describe("the themes page", () => {
  it("is in the navigation of an administrator only", async () => {
    mockApi({ ...defaultRoutes(READER), "GET /ui/themes": state() });
    const view = renderApp();
    const nav = await screen.findByRole("navigation", { name: "Main" });
    await screen.findAllByText(/Monitor/);
    expect(
      within(nav).queryByRole("link", { name: "Themes" }),
    ).not.toBeInTheDocument();
    view.unmount();

    mockApi({ ...defaultRoutes(ADMIN), "GET /ui/themes": state() });
    renderApp();
    expect(await screen.findByRole("link", { name: "Themes" })).toHaveAttribute(
      "href",
      "/admin/themes",
    );
  });

  it("does not ask the server for anything when the caller is not an administrator", async () => {
    const api = mockApi({
      ...defaultRoutes(APPROVER),
      "GET /ui/themes": state(),
    });
    renderApp({ at: "/admin/themes" });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /do not allow this/,
    );
    expect(api.count("GET /ui/themes")).toBe(0);
  });

  it("shows what is in force, the built-in themes and the kept archives", async () => {
    mockApi({
      ...defaultRoutes(ADMIN),
      "GET /ui/themes": state({ active: ref("acme", "stored", SHA) }),
    });
    renderApp({ at: "/admin/themes" });
    expect(
      await screen.findByRole("heading", { level: 1, name: "Themes" }),
    ).toBeInTheDocument();
    expect(
      await screen.findByText(/chosen here, uploaded/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Deployment default: default theme/),
    ).toBeInTheDocument();
    const row = screen.getByRole("row", { name: /Acme/ });
    expect(within(row).getByText("in force")).toBeInTheDocument();
    expect(
      within(row).queryByRole("button", { name: /Delete/ }),
    ).not.toBeInTheDocument(); // the one in force stays
    expect(screen.getByRole("button", { name: /Use flat/ })).toBeEnabled();
    expect(
      screen.getByRole("button", { name: "Back to the deployment default" }),
    ).toBeInTheDocument();
  });

  it("chooses a built-in theme and reloads", async () => {
    const user = userEvent.setup();
    const api = mockApi({
      ...defaultRoutes(ADMIN),
      "GET /ui/themes": state(),
      "PUT /ui/themes/active": state(),
    });
    renderApp({ at: "/admin/themes" });
    await user.click(await screen.findByRole("button", { name: /Use flat/ }));
    await waitFor(() => expect(reload).toHaveBeenCalled());
    expect(api.calls.find((c) => c.method === "PUT")?.body).toEqual({
      builtin: "flat",
    });
  });

  it("chooses a kept archive by its identity", async () => {
    const user = userEvent.setup();
    const api = mockApi({
      ...defaultRoutes(ADMIN),
      "GET /ui/themes": state(),
      "PUT /ui/themes/active": state(),
    });
    renderApp({ at: "/admin/themes" });
    await user.click(await screen.findByRole("button", { name: /Use acme/ }));
    await waitFor(() => expect(reload).toHaveBeenCalled());
    expect(api.calls.find((c) => c.method === "PUT")?.body).toEqual({
      sha256: SHA,
    });
  });

  it("goes back to the deployment default", async () => {
    const user = userEvent.setup();
    const api = mockApi({
      ...defaultRoutes(ADMIN),
      "GET /ui/themes": state({ active: ref("flat", "builtin") }),
      "DELETE /ui/themes/active": { status: 204, body: undefined },
    });
    renderApp({ at: "/admin/themes" });
    await user.click(
      await screen.findByRole("button", {
        name: "Back to the deployment default",
      }),
    );
    await waitFor(() => expect(reload).toHaveBeenCalled());
    expect(api.count("DELETE /ui/themes/active")).toBe(1);
  });

  it("uploads the archive as it is, and says it is kept, not used", async () => {
    const user = userEvent.setup();
    const api = mockApi({
      ...defaultRoutes(ADMIN),
      "GET /ui/themes": state(),
      "POST /ui/themes": { status: 201, body: state().themes[0] },
    });
    renderApp({ at: "/admin/themes" });
    const file = new File([new Uint8Array([0x50, 0x4b, 3, 4])], "acme.zip", {
      type: "application/zip",
    });
    await user.upload(await screen.findByLabelText("Theme archive"), file);
    expect(await screen.findByText(/Acme is kept/)).toBeInTheDocument();
    const call = api.calls.find((c) => c.method === "POST");
    expect(call?.headers.get("Content-Type")).toBe("application/zip");
    expect(call?.raw?.size).toBe(4);
    expect(reload).not.toHaveBeenCalled();
    expect(api.count("GET /ui/themes")).toBeGreaterThan(1); // the list is read again
  });

  it("shows why an archive was refused", async () => {
    const user = userEvent.setup();
    mockApi({
      ...defaultRoutes(ADMIN),
      "GET /ui/themes": state(),
      "POST /ui/themes": problem(
        422,
        "invalid-theme",
        "theme.json: colour text on surface has contrast 2.1, needs 4.5",
      ),
    });
    renderApp({ at: "/admin/themes" });
    await user.upload(
      await screen.findByLabelText("Theme archive"),
      new File(["x"], "bad.zip", { type: "application/zip" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "contrast 2.1, needs 4.5",
    );
  });

  it("asks before deleting, then deletes", async () => {
    const user = userEvent.setup();
    const api = mockApi({
      ...defaultRoutes(ADMIN),
      "GET /ui/themes": state(),
      [`DELETE /ui/themes/${SHA}`]: { status: 204, body: undefined },
    });
    renderApp({ at: "/admin/themes" });
    await user.click(
      await screen.findByRole("button", { name: /Delete acme/ }),
    );
    expect(api.count(`DELETE /ui/themes/${SHA}`)).toBe(0);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(api.count(`DELETE /ui/themes/${SHA}`)).toBe(0);
    await user.click(screen.getByRole("button", { name: /Delete acme/ }));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(api.count(`DELETE /ui/themes/${SHA}`)).toBe(1));
  });

  it("says so when the deployment locks the theme, and offers no choice", async () => {
    mockApi({
      ...defaultRoutes(ADMIN),
      "GET /ui/themes": state({ locked: true, active: ref("flat", "config") }),
    });
    renderApp({ at: "/admin/themes" });
    expect(
      await screen.findByText("The theme is locked by the deployment"),
    ).toBeInTheDocument();
    for (const b of screen.getAllByRole("button", { name: /^Use / }))
      expect(b).toBeDisabled();
    expect(
      screen.queryByRole("button", { name: "Back to the deployment default" }),
    ).not.toBeInTheDocument();
  });

  it("shows the server's refusal when a choice is refused", async () => {
    const user = userEvent.setup();
    mockApi({
      ...defaultRoutes(ADMIN),
      "GET /ui/themes": state(),
      "PUT /ui/themes/active": problem(
        409,
        "theme-locked",
        "the deployment configuration (web.theme.locked) decides the theme",
      ),
    });
    renderApp({ at: "/admin/themes" });
    await user.click(await screen.findByRole("button", { name: /Use flat/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "web.theme.locked",
    );
    expect(reload).not.toHaveBeenCalled();
  });

  it("is in French", async () => {
    mockApi({ ...defaultRoutes(ADMIN), "GET /ui/themes": state() });
    renderApp({ at: "/admin/themes", locale: "fr" });
    expect(
      await screen.findByRole("heading", { level: 1, name: "Thèmes" }),
    ).toBeInTheDocument();
    expect(await screen.findByText("Thèmes intégrés")).toBeInTheDocument();
  });

  it("has no accessibility violation", async () => {
    mockApi({
      ...defaultRoutes(ADMIN),
      "GET /ui/themes": state({ locked: true }),
    });
    renderApp({ at: "/admin/themes" });
    await screen.findByRole("heading", { name: "Kept archives" });
    await expectNoA11yViolations();
  }, 30_000);
});

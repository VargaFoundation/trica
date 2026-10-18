import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { getToken } from "./api/token";
import { ID } from "./test/fixtures";
import { defaultRoutes, mockApi, problem, renderApp } from "./test/support";

describe("signing in", () => {
  it("shows the sign-in form and calls nothing while there is no token", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ signedIn: false });
    expect(await screen.findByRole("heading", { level: 1, name: "Sign in" })).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Main" })).not.toBeInTheDocument();
    expect(api.calls).toHaveLength(0);
  });

  it("the token field is a password field, not remembered by the browser", () => {
    mockApi(defaultRoutes());
    renderApp({ signedIn: false });
    const field = screen.getByLabelText("Access token");
    expect(field).toHaveAttribute("type", "password");
    expect(field).toHaveAttribute("autocomplete", "off");
  });

  it("a token opens the page that was asked for, and is sent as a bearer credential", async () => {
    const api = mockApi(defaultRoutes());
    renderApp({ signedIn: false, at: "/discrepancies" });
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Access token"), "trica_abc_secret{Enter}");
    expect(await screen.findByRole("heading", { level: 1, name: "Discrepancies" })).toBeInTheDocument();
    expect(getToken()).toBe("trica_abc_secret");
    expect(api.calls.every((c) => c.headers.get("Authorization") === "Bearer trica_abc_secret")).toBe(true);
    expect(api.calls[0]?.path).toBe("/me");
  });

  it("a token the server refuses sends the reader back to the form, with the reason, and is forgotten", async () => {
    mockApi({ ...defaultRoutes(), "GET /me": problem(401, "unauthorized", "invalid token") });
    renderApp({ signedIn: false });
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Access token"), "revoked{Enter}");
    expect(await screen.findByRole("alert")).toHaveTextContent("The server refused this token");
    expect(getToken()).toBeNull();
    expect(screen.getByRole("heading", { level: 1, name: "Sign in" })).toBeInTheDocument();
  });

  it("a token that expires while browsing lands on the form too", async () => {
    mockApi({ ...defaultRoutes(), [`GET /objects/${ID.swA1}`]: problem(401, "unauthorized") });
    renderApp({ at: `/objects/${ID.swA1}` });
    expect(await screen.findByRole("alert")).toHaveTextContent("The server refused this token");
    expect(getToken()).toBeNull();
  });

  it("signing out forgets the token and shows nothing of the inventory", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/proposals" });
    const user = userEvent.setup();
    await screen.findByRole("link", { name: "#12" });
    await user.click(screen.getByRole("button", { name: "Sign out" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Sign in" })).toBeInTheDocument();
    expect(getToken()).toBeNull();
    expect(screen.queryByRole("link", { name: "#12" })).not.toBeInTheDocument();
  });

  it("an unreachable server is said, with a retry, instead of a blank page", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("Failed to fetch"));
    renderApp();
    expect(await screen.findByRole("alert")).toHaveTextContent("The server could not be reached.");
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});

describe("keyboard", () => {
  it("the search field is named by its label, not only by its placeholder", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/" });
    const field = await screen.findByLabelText("Search the inventory");
    expect(field).toHaveAttribute("type", "search");
  });

  it("/ focuses the search field", async () => {
    mockApi(defaultRoutes());
    renderApp();
    const field = await screen.findByRole("searchbox", { name: "Search the inventory" });
    expect(field).not.toHaveFocus();
    await userEvent.setup().keyboard("/");
    expect(field).toHaveFocus();
    expect(field).toHaveValue("");
  });

  it.each([
    ["Ctrl+K", "{Control>}k{/Control}"],
    ["Cmd+K", "{Meta>}k{/Meta}"],
  ])("%s focuses the search field", async (_name, keys) => {
    mockApi(defaultRoutes());
    renderApp();
    const field = await screen.findByRole("searchbox");
    await userEvent.setup().keyboard(keys);
    expect(field).toHaveFocus();
  });

  it("/ is a character, not a shortcut, while typing in a field", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/proposals" });
    const user = userEvent.setup();
    const kind = await screen.findByRole("combobox", { name: "Status" });
    kind.focus();
    await user.keyboard("/");
    expect(kind).toHaveFocus();

    const field = screen.getByRole("searchbox");
    await user.click(field);
    await user.keyboard("a/b");
    expect(field).toHaveValue("a/b");
  });

  it("Ctrl+K works from inside another field", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/discrepancies" });
    const user = userEvent.setup();
    const kind = await screen.findByRole("textbox", { name: "Kind" });
    await user.click(kind);
    expect(kind).toHaveFocus();
    await user.keyboard("{Control>}k{/Control}");
    expect(screen.getByRole("searchbox")).toHaveFocus();
  });

  it("the search field says which keys reach it", async () => {
    mockApi(defaultRoutes());
    renderApp();
    expect(await screen.findByRole("searchbox")).toHaveAttribute("aria-keyshortcuts", "/ Control+K Meta+K");
  });

  it("the skip link is the first stop of the tab order and leads to the content", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/health" });
    await screen.findByRole("heading", { level: 1, name: "Health" });
    await userEvent.setup().tab();
    const skip = screen.getByRole("link", { name: "Skip to content" });
    expect(skip).toHaveFocus();
    expect(skip).toHaveAttribute("href", "#main");
    expect(document.getElementById("main")).toBeInTheDocument();
  });

  it("moves focus to the content and retitles the page on navigation", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/" });
    const user = userEvent.setup();
    const nav = await screen.findByRole("navigation", { name: "Main" });
    await user.click(within(nav).getByRole("link", { name: "Proposals" }));
    await screen.findByRole("heading", { level: 1, name: "Proposals" });
    expect(document.getElementById("main")).toHaveFocus();
    expect(document.title).toBe("Proposals · trica");
    await user.click(within(nav).getByRole("link", { name: "Health" }));
    await screen.findByRole("heading", { level: 1, name: "Health" });
    expect(document.getElementById("main")).toHaveFocus();
    expect(document.title).toBe("Health · trica");
  });

  it("marks the current section in the navigation", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/proposals" });
    expect(await screen.findByRole("link", { name: "Proposals" })).toHaveAttribute("aria-current", "page");
    expect(screen.getByRole("link", { name: "Discrepancies" })).not.toHaveAttribute("aria-current");
  });
});

describe("language and theme", () => {
  it("renders in French when asked, labels from the schema included", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: `/objects/${ID.swA1}`, locale: "fr" });
    expect(await screen.findByRole("heading", { level: 1, name: "sw-a1" })).toBeInTheDocument();
    expect((await screen.findAllByText("Version de l'OS")).length).toBeGreaterThan(0);
    expect((await screen.findAllByText("Équipement")).length).toBeGreaterThan(0);
    expect(screen.getByRole("link", { name: "Écarts" })).toBeInTheDocument();
    expect(document.documentElement.lang).toBe("fr");
  });

  it("the language selector switches at once, sets <html lang> and is remembered", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/discrepancies" });
    const user = userEvent.setup();
    await user.selectOptions(await screen.findByRole("combobox", { name: "Language" }), "fr");
    expect(await screen.findByRole("heading", { level: 1, name: "Écarts" })).toBeInTheDocument();
    expect(document.documentElement.lang).toBe("fr");
    expect(localStorage.getItem("trica.locale")).toBe("fr");
  });

  it("the theme selector sets data-theme on <html>, and System removes it", async () => {
    mockApi(defaultRoutes());
    renderApp();
    const user = userEvent.setup();
    const select = await screen.findByRole("combobox", { name: "Theme" });
    await user.selectOptions(select, "dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(localStorage.getItem("trica.theme")).toBe("dark");
    await user.selectOptions(select, "light");
    expect(document.documentElement.dataset.theme).toBe("light");
    await user.selectOptions(select, "system");
    expect(document.documentElement.dataset.theme).toBeUndefined();
    expect(localStorage.getItem("trica.theme")).toBe("system");
  });

  it("starts from the stored theme", async () => {
    mockApi(defaultRoutes());
    renderApp({ theme: "dark" });
    await screen.findByRole("combobox", { name: "Theme" });
    expect(document.documentElement.dataset.theme).toBe("dark");
  });

  it("keeps working when storage is blocked", async () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    mockApi(defaultRoutes());
    renderApp({ at: "/discrepancies" });
    const user = userEvent.setup();
    await user.selectOptions(await screen.findByRole("combobox", { name: "Language" }), "fr");
    expect(await screen.findByRole("heading", { level: 1, name: "Écarts" })).toBeInTheDocument();
  });
});

describe("deep links", () => {
  it.each([
    ["/", "Inventory"],
    ["/search?q=sw", "Search: sw"],
    [`/objects/${ID.swA1}`, "sw-a1"],
    ["/discrepancies", "Discrepancies"],
    ["/proposals", "Proposals"],
    ["/proposals/12", "Proposal 12"],
    ["/health", "Health"],
  ])("%s opens %s directly", async (at, heading) => {
    mockApi(defaultRoutes());
    renderApp({ at });
    expect(await screen.findByRole("heading", { level: 1, name: heading })).toBeInTheDocument();
  });

  it("an address nobody serves is a not-found page with a way home", async () => {
    mockApi(defaultRoutes());
    renderApp({ at: "/nothing/here" });
    expect(await screen.findByRole("heading", { level: 1, name: "Page not found" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Back to the inventory" })).toHaveAttribute("href", "/");
  });

  it("the home page offers search examples that are deep links", async () => {
    mockApi(defaultRoutes());
    renderApp();
    const link = await screen.findByRole("link", { name: "type:device role:switch" });
    expect(link.getAttribute("href")).toBe("/search?q=type%3Adevice%20role%3Aswitch");
    await waitFor(() => expect(screen.getByRole("link", { name: "status:absent" })).toBeInTheDocument());
  });
});

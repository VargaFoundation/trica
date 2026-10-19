import { useCallback, useEffect, useRef, useState } from "react";
import { Link, NavLink, Outlet, useLocation, useNavigationType } from "react-router";
import { clearToken } from "../api/token";
import { LOCALES, useI18n, type Locale, type MessageKey } from "../i18n";
import { THEMES, useTheme, type ThemePreference } from "../theme";
import { CrumbsContext } from "./crumbs";
import { SearchBar } from "./SearchBar";
import { Breadcrumbs, type Crumb } from "./ui/Breadcrumbs";
import { Icon, type IconName } from "./ui/icons";

type Item = { to: string; label: MessageKey; icon: IconName; end?: boolean };

/** The sidebar, in groups: what is going on, what there is, what waits for a decision. */
export const NAV: { title: MessageKey; items: Item[] }[] = [
  {
    title: "nav.group.monitor",
    items: [
      { to: "/", label: "nav.overview", icon: "overview", end: true },
      { to: "/health", label: "nav.health", icon: "health" },
    ],
  },
  {
    title: "nav.group.inventory",
    items: [
      { to: "/objects", label: "nav.objects", icon: "object" },
      { to: "/search", label: "nav.search", icon: "search" },
      { to: "/rooms", label: "nav.rooms", icon: "rack" },
      { to: "/topology", label: "nav.topology", icon: "topology" },
    ],
  },
  {
    title: "nav.group.review",
    items: [
      { to: "/discrepancies", label: "nav.discrepancies", icon: "discrepancy" },
      { to: "/proposals", label: "nav.proposals", icon: "proposal" },
    ],
  },
];

const SIDEBAR_KEY = "trica.sidebar";
type Sidebar = "full" | "rail";

function initialSidebar(): Sidebar {
  try {
    return localStorage.getItem(SIDEBAR_KEY) === "rail" ? "rail" : "full";
  } catch {
    return "full";
  }
}

/** The trail of a page that sets none: its section. */
function defaultTrail(pathname: string, t: (k: MessageKey) => string): Crumb[] {
  if (pathname === "/") return [{ label: t("nav.overview") }];
  for (const g of NAV)
    for (const it of g.items)
      if (it.to !== "/" && (pathname === it.to || pathname.startsWith(`${it.to}/`)))
        return pathname === it.to
          ? [{ label: t(it.label) }]
          : [{ label: t(it.label), to: it.to }, { label: decodeURIComponent(pathname.slice(it.to.length + 1)) }];
  return [];
}

export function Layout() {
  const { t, locale, setLocale } = useI18n();
  const { theme, setTheme } = useTheme();
  const location = useLocation();
  const mainRef = useRef<HTMLElement>(null);
  const first = useRef(true);
  const navigationType = useNavigationType();
  const lastPath = useRef(location.pathname);
  const [sidebar, setSidebar] = useState<Sidebar>(initialSidebar);
  const [crumbs, setCrumbs] = useState<{ path: string; trail: Crumb[] } | null>(null);

  // On every navigation after the first, move focus to the content: a keyboard
  // or screen-reader user would otherwise stay on the link they activated. A
  // page that only rewrites its own address (a tab, a filter: a replace on the
  // same path) keeps the focus where it is.
  useEffect(() => {
    const samePage = navigationType === "REPLACE" && lastPath.current === location.pathname;
    lastPath.current = location.pathname;
    if (first.current) {
      first.current = false;
      return;
    }
    if (!samePage) mainRef.current?.focus();
  }, [location.key, location.pathname, navigationType]);

  const toggle = () => {
    const next = sidebar === "rail" ? "full" : "rail";
    setSidebar(next);
    try {
      localStorage.setItem(SIDEBAR_KEY, next);
    } catch {
      /* not persisted */
    }
  };

  const setTrail = useCallback((path: string, trail: Crumb[] | null) => {
    setCrumbs((cur) => (trail ? { path, trail } : cur?.path === path ? null : cur));
  }, []);
  const trail = crumbs?.path === location.pathname ? crumbs.trail : defaultTrail(location.pathname, t);
  const rail = sidebar === "rail";

  return (
    <CrumbsContext.Provider value={setTrail}>
      <a href="#main" className="skip-link">
        {t("app.skip")}
      </a>
      <div className="shell" data-sidebar={sidebar}>
        <aside className="sidebar" id="sidebar" aria-label={t("nav.sidebar")}>
          <div className="sidebar-inner">
            <div className="sidebar-brand">
              <Link to="/" className="brand">
                <span className="brand-mark" aria-hidden="true">
                  <Icon name="object" />
                </span>
                <span className="rail-hide">{t("app.name")}</span>
              </Link>
            </div>
            <nav aria-label={t("nav.main")} className="sidebar-nav">
              {NAV.map((g) => (
                <div key={g.title} className="nav-group">
                  <p className="nav-group-title" aria-hidden="true">
                    {t(g.title)}
                  </p>
                  <ul>
                    {g.items.map((it) => (
                      <li key={it.to}>
                        <NavLink to={it.to} end={it.end} className="nav-link" title={rail ? t(it.label) : undefined}>
                          <Icon name={it.icon} />
                          <span className="rail-hide">{t(it.label)}</span>
                        </NavLink>
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </nav>
            <div className="sidebar-foot">
              <button
                type="button"
                className="sidebar-toggle"
                onClick={toggle}
                aria-expanded={!rail}
                aria-controls="sidebar"
                title={rail ? t("nav.expand") : undefined}
              >
                <Icon name={rail ? "expand" : "collapse"} />
                <span className="rail-hide">{rail ? t("nav.expand") : t("nav.collapse")}</span>
              </button>
            </div>
          </div>
        </aside>
        <div className="shell-main">
          <header className="topbar">
            <Breadcrumbs trail={trail} />
            <SearchBar />
            <div className="settings">
              <label>
                <span className="sr-only">{t("settings.language")}</span>
                <select value={locale} onChange={(e) => setLocale(e.target.value as Locale)}>
                  {LOCALES.map((l) => (
                    <option key={l} value={l} lang={l}>
                      {l.toUpperCase()}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span className="sr-only">{t("settings.theme")}</span>
                <select value={theme} onChange={(e) => setTheme(e.target.value as ThemePreference)}>
                  {THEMES.map((th) => (
                    <option key={th} value={th}>
                      {t(`theme.${th}`)}
                    </option>
                  ))}
                </select>
              </label>
              <button type="button" className="ghost" onClick={clearToken}>
                <Icon name="signOut" />
                {t("settings.signOut")}
              </button>
            </div>
          </header>
          <main id="main" ref={mainRef} tabIndex={-1}>
            <Outlet />
          </main>
        </div>
      </div>
    </CrumbsContext.Provider>
  );
}

import { useEffect, useRef } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router";
import { clearToken } from "../api/token";
import { LOCALES, useI18n, type Locale } from "../i18n";
import { THEMES, useTheme, type ThemePreference } from "../theme";
import { SearchBar } from "./SearchBar";

export function Layout() {
  const { t, locale, setLocale } = useI18n();
  const { theme, setTheme } = useTheme();
  const location = useLocation();
  const mainRef = useRef<HTMLElement>(null);
  const first = useRef(true);

  // On every navigation after the first, move focus to the content: a keyboard
  // or screen-reader user would otherwise stay on the link they activated.
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    mainRef.current?.focus();
  }, [location.key]);

  return (
    <>
      <a href="#main" className="skip-link">
        {t("app.skip")}
      </a>
      <header className="app-header">
        <Link to="/" className="brand">
          {t("app.name")}
        </Link>
        <SearchBar />
        <nav aria-label={t("nav.main")} className="app-nav">
          <NavLink to="/discrepancies">{t("nav.discrepancies")}</NavLink>
          <NavLink to="/rooms">{t("nav.rooms")}</NavLink>
          <NavLink to="/topology">{t("nav.topology")}</NavLink>
          <NavLink to="/proposals">{t("nav.proposals")}</NavLink>
          <NavLink to="/health">{t("nav.health")}</NavLink>
        </nav>
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
          <button type="button" onClick={clearToken}>
            {t("settings.signOut")}
          </button>
        </div>
      </header>
      <main id="main" ref={mainRef} tabIndex={-1}>
        <Outlet />
      </main>
    </>
  );
}

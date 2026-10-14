import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

export type ThemePreference = "system" | "light" | "dark";
export const THEMES: readonly ThemePreference[] = ["system", "light", "dark"];
const STORAGE_KEY = "trica.theme";

interface ThemeCtx {
  theme: ThemePreference;
  setTheme: (t: ThemePreference) => void;
}

const Ctx = createContext<ThemeCtx | null>(null);

function initial(): ThemePreference {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    if (v === "light" || v === "dark" || v === "system") return v;
  } catch {
    /* unavailable */
  }
  return "system";
}

/**
 * The preference is applied as data-theme on <html>. "system" removes the
 * attribute, and styles/tokens.css then follows prefers-color-scheme.
 */
export function ThemeProvider({ children, initialTheme }: { children: ReactNode; initialTheme?: ThemePreference }) {
  const [theme, setThemeState] = useState<ThemePreference>(() => initialTheme ?? initial());

  useEffect(() => {
    const root = document.documentElement;
    if (theme === "system") delete root.dataset.theme;
    else root.dataset.theme = theme;
  }, [theme]);

  const setTheme = useCallback((t: ThemePreference) => {
    setThemeState(t);
    try {
      localStorage.setItem(STORAGE_KEY, t);
    } catch {
      /* not persisted */
    }
  }, []);

  const value = useMemo(() => ({ theme, setTheme }), [theme, setTheme]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useTheme(): ThemeCtx {
  const v = useContext(Ctx);
  if (!v) throw new Error("useTheme outside ThemeProvider");
  return v;
}

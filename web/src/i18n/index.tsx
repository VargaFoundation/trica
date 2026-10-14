import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { en, type MessageKey } from "./en";
import { fr } from "./fr";

export type Locale = "en" | "fr";
export const LOCALES: readonly Locale[] = ["en", "fr"];
export type { MessageKey };

const catalogues: Record<Locale, Record<MessageKey, string>> = { en, fr };
const STORAGE_KEY = "trica.locale";

type Params = Record<string, string | number>;

/** Keys that have a .one and a .other form. */
export type PluralKey = {
  [K in MessageKey]: K extends `${infer B}.one` ? B : never;
}[MessageKey];

export function interpolate(template: string, params?: Params): string {
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (whole, name: string) => {
    const v = params[name];
    return v === undefined ? whole : String(v);
  });
}

export function detectLocale(languages: readonly string[], stored: string | null): Locale {
  if (stored === "en" || stored === "fr") return stored;
  for (const l of languages) {
    const base = l.toLowerCase().split("-")[0];
    if (base === "fr") return "fr";
    if (base === "en") return "en";
  }
  return "en";
}

function initialLocale(): Locale {
  let stored: string | null = null;
  try {
    stored = localStorage.getItem(STORAGE_KEY);
  } catch {
    /* unavailable: fall back to the browser language */
  }
  const langs = typeof navigator === "undefined" ? [] : navigator.languages?.length ? navigator.languages : [navigator.language];
  return detectLocale(langs, stored);
}

export interface I18n {
  locale: Locale;
  setLocale: (l: Locale) => void;
  t: (key: MessageKey, params?: Params) => string;
  /** Plural form chosen by the locale's rules from params.count. */
  tp: (key: PluralKey, count: number, params?: Params) => string;
  /** A label the API gives in both languages. */
  label: (l: { en: string; fr: string } | undefined, fallback: string) => string;
  dateTime: (iso: string | undefined) => string;
  number: (n: number) => string;
}

const Ctx = createContext<I18n | null>(null);

export function I18nProvider({ children, initial }: { children: ReactNode; initial?: Locale }) {
  const [locale, setLocaleState] = useState<Locale>(() => initial ?? initialLocale());

  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  const setLocale = useCallback((l: Locale) => {
    setLocaleState(l);
    try {
      localStorage.setItem(STORAGE_KEY, l);
    } catch {
      /* not persisted: the choice still holds for this page */
    }
  }, []);

  const value = useMemo<I18n>(() => {
    const cat = catalogues[locale];
    const rules = new Intl.PluralRules(locale);
    const dtf = new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" });
    const nf = new Intl.NumberFormat(locale);
    const t = (key: MessageKey, params?: Params) => interpolate(cat[key], params);
    return {
      locale,
      setLocale,
      t,
      tp: (key, count, params) => {
        const form = rules.select(count) === "one" ? "one" : "other";
        return t(`${key}.${form}` as MessageKey, { ...params, count: nf.format(count) });
      },
      label: (l, fallback) => (l ? l[locale] || l.en : fallback) || fallback,
      dateTime: (iso) => {
        if (!iso) return "";
        const d = new Date(iso);
        return Number.isNaN(d.getTime()) ? iso : dtf.format(d);
      },
      number: (n) => nf.format(n),
    };
  }, [locale, setLocale]);

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useI18n(): I18n {
  const v = useContext(Ctx);
  if (!v) throw new Error("useI18n outside I18nProvider");
  return v;
}

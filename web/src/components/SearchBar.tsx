import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router";
import { useI18n } from "../i18n";

/** True when typing in this element must not be taken for a shortcut. */
export function isEditable(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  const tag = target.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
}

/**
 * "/" and Ctrl/Cmd+K focus the search field from anywhere. "/" is left alone
 * while typing in a field (it is a character there) and with a modifier held;
 * Ctrl/Cmd+K works even inside a field, as in most web apps.
 */
export function useSearchShortcuts(focus: () => void): void {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      const isK = e.key.toLowerCase() === "k" && (e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey;
      const isSlash = e.key === "/" && !e.ctrlKey && !e.metaKey && !e.altKey && !isEditable(e.target);
      if (isK || isSlash) {
        e.preventDefault();
        focus();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [focus]);
}

function SearchForm({ initial }: { initial: string }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const inputRef = useRef<HTMLInputElement>(null);
  const [value, setValue] = useState(initial);

  useSearchShortcuts(
    useCallback(() => {
      inputRef.current?.focus();
      inputRef.current?.select();
    }, []),
  );

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const q = value.trim();
    if (q) void navigate(`/search?q=${encodeURIComponent(q)}`);
  };

  return (
    <form role="search" className="searchbar" onSubmit={submit}>
      <label htmlFor="search-global" className="sr-only">
        {t("search.label")}
      </label>
      <input
        id="search-global"
        ref={inputRef}
        type="search"
        name="q"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder={t("search.placeholder")}
        aria-keyshortcuts="/ Control+K Meta+K"
        autoComplete="off"
        spellCheck={false}
      />
      <button type="submit">{t("search.submit")}</button>
      <kbd className="kbd-hint" aria-hidden="true">
        /
      </kbd>
    </form>
  );
}

/** The global search bar. It starts from the query in the address on /search, so deep links and back/forward keep it in step. */
export function SearchBar() {
  const location = useLocation();
  const [params] = useSearchParams();
  const urlQuery = location.pathname === "/search" ? (params.get("q") ?? "") : "";
  return <SearchForm key={urlQuery} initial={urlQuery} />;
}

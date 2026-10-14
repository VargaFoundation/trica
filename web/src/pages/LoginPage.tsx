import { useState, type FormEvent } from "react";
import { setToken } from "../api/token";
import { useI18n } from "../i18n";
import { usePageTitle } from "../usePageTitle";

/**
 * The API takes bearer credentials only, so signing in is pasting one. The
 * page is shown in place of whatever address was opened (the address is not
 * touched), so after signing in the reader lands on the page they asked for.
 */
export function LoginPage({ rejected }: { rejected: boolean }) {
  const { t } = useI18n();
  const [value, setValue] = useState("");
  usePageTitle(t("login.title"));

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (value.trim()) setToken(value);
  };

  return (
    <main id="main" className="login">
      <h1>{t("login.title")}</h1>
      <p>{t("login.intro")}</p>
      {rejected ? (
        <p role="alert" className="notice notice-error">
          {t("login.rejected")}
        </p>
      ) : null}
      <form onSubmit={submit}>
        <label htmlFor="token">{t("login.token")}</label>
        <input
          id="token"
          type="password"
          name="token"
          autoComplete="off"
          spellCheck={false}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          required
        />
        <button type="submit">{t("login.submit")}</button>
      </form>
    </main>
  );
}

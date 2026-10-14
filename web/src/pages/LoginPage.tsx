import { useState, type FormEvent } from "react";
import { useLocation } from "react-router";
import { setToken } from "../api/token";
import { OidcError, startSignIn, type OidcClient } from "../auth/oidc";
import { useI18n } from "../i18n";
import { usePageTitle } from "../usePageTitle";

/**
 * The API takes bearer credentials only, so signing in is pasting one. The
 * page is shown in place of whatever address was opened (the address is not
 * touched), so after signing in the reader lands on the page they asked for.
 */
export function LoginPage({ rejected, sso = null }: { rejected: boolean; sso?: OidcClient | null }) {
  const { t } = useI18n();
  const [value, setValue] = useState("");
  const [ssoError, setSsoError] = useState<OidcError | null>(null);
  const [busy, setBusy] = useState(false);
  const here = useLocation();
  usePageTitle(t("login.title"));

  const signInWithSso = () => {
    if (!sso) return;
    setBusy(true);
    setSsoError(null);
    startSignIn(sso, here.pathname + here.search).catch((e: unknown) => {
      setBusy(false);
      setSsoError(e instanceof OidcError ? e : new OidcError("discovery", String(e)));
    });
  };

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
      {sso ? (
        <section aria-labelledby="sso-title" className="sso">
          <h2 id="sso-title">{t("login.sso.title")}</h2>
          <button type="button" onClick={signInWithSso} disabled={busy}>
            {t("login.sso.button")}
          </button>
          {ssoError ? (
            <div role="alert" className="notice notice-error">
              <p>{t(`login.sso.error.${ssoError.code}`)}</p>
              <p className="detail">{ssoError.message}</p>
            </div>
          ) : null}
          <h2>{t("login.token.title")}</h2>
        </section>
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

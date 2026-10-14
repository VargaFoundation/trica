import { useEffect, useState } from "react";
import { useLocation } from "react-router";
import { setToken } from "../api/token";
import { Loading } from "../components/Async";
import { completeSignIn, OidcError, setLanding, type OidcClient, type SignedIn } from "../auth/oidc";
import { useI18n } from "../i18n";
import { usePageTitle } from "../usePageTitle";

// A callback address is single use (the stored state is consumed on the first
// read). React's StrictMode runs effects twice in development, so the exchange
// is shared per query string instead of being started twice.
const inflight = new Map<string, Promise<SignedIn>>();

function exchange(cfg: OidcClient, search: string): Promise<SignedIn> {
  let p = inflight.get(search);
  if (!p) {
    p = completeSignIn(cfg, search);
    inflight.set(search, p);
  }
  return p;
}

/** Where the identity provider sends the browser back (CALLBACK_PATH). Shown only while signed out. */
export function OidcCallbackPage({ cfg }: { cfg: OidcClient | null }) {
  const { t } = useI18n();
  const { search } = useLocation();
  const [error, setError] = useState<OidcError | null>(null);
  usePageTitle(t("login.title"));

  useEffect(() => {
    if (!cfg) return;
    let live = true;
    exchange(cfg, search).then(
      (done) => {
        inflight.delete(search);
        if (!live) return;
        setLanding(done.returnTo);
        setToken(done.token);
      },
      (e: unknown) => {
        inflight.delete(search);
        if (live) setError(e instanceof OidcError ? e : new OidcError("exchange", String(e)));
      },
    );
    return () => {
      live = false;
    };
  }, [cfg, search]);

  if (!cfg) {
    return (
      <main id="main" className="login">
        <h1>{t("login.title")}</h1>
        <p role="alert" className="notice notice-error">
          {t("login.sso.unavailable")}
        </p>
        <a href="/">{t("login.sso.back")}</a>
      </main>
    );
  }
  if (error) {
    return (
      <main id="main" className="login">
        <h1>{t("login.title")}</h1>
        <div role="alert" className="notice notice-error">
          <p>{t(`login.sso.error.${error.code}`)}</p>
          <p className="detail">{error.message}</p>
        </div>
        <a href="/">{t("login.sso.back")}</a>
      </main>
    );
  }
  return (
    <main id="main" className="login">
      <h1>{t("login.title")}</h1>
      <Loading />
    </main>
  );
}

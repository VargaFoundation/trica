import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { ApiError } from "../api/client";
import { api } from "../api/endpoints";
import type { StoredTheme, ThemeRef, ThemeSelection } from "../api/types";
import { Async } from "../components/Async";
import { Time } from "../components/Time";
import { Alert } from "../components/ui/Alert";
import { Card } from "../components/ui/Card";
import { useCan } from "../hooks";
import { useI18n } from "../i18n";
import { usePageTitle } from "../usePageTitle";

/** The reason a call was refused, as the server wrote it. */
function reason(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.detail || e.title || fallback : fallback;
}

/**
 * The console's look, chosen by an administrator (ADR 0054): the theme in
 * force, the built-in ones, the archives kept in the database, an upload. A
 * change reaches every replica; this page reloads so that the new theme shows
 * at once. When the deployment configuration locks the theme the page says so
 * and offers no choice. The API checks the admin action again on every call.
 */
export function ThemesPage() {
  const { t } = useI18n();
  usePageTitle(t("themes.title"));
  const admin = useCan("admin");
  const client = useQueryClient();
  const query = useQuery({
    queryKey: ["themes"],
    queryFn: ({ signal }) => api.themes(signal),
    enabled: admin,
  });
  const [failure, setFailure] = useState("");
  const [uploaded, setUploaded] = useState<StoredTheme | null>(null);
  const [asking, setAsking] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);

  const refresh = () => client.invalidateQueries({ queryKey: ["themes"] });
  const changed = useMutation({
    mutationFn: async (sel: ThemeSelection | null) => {
      if (sel) await api.activateTheme(sel);
      else await api.resetTheme();
    },
    onSuccess: () => {
      setFailure("");
      // The stylesheet is linked by the page: load it again.
      window.location.reload();
    },
    onError: (e) => setFailure(reason(e, t("themes.failed"))),
  });
  const upload = useMutation({
    mutationFn: (file: File) => api.uploadTheme(file),
    onSuccess: (s) => {
      setFailure("");
      setUploaded(s);
      if (fileRef.current) fileRef.current.value = "";
      return refresh();
    },
    onError: (e) => {
      setUploaded(null);
      setFailure(reason(e, t("themes.failed")));
    },
  });
  const remove = useMutation({
    mutationFn: (sha: string) => api.deleteTheme(sha),
    onSuccess: () => {
      setFailure("");
      setAsking("");
      return refresh();
    },
    onError: (e) => {
      setAsking("");
      setFailure(reason(e, t("themes.failed")));
    },
  });

  if (!admin)
    return (
      <>
        <h1>{t("themes.title")}</h1>
        <Alert tone="warn" role="alert">
          {t("error.forbidden")}
        </Alert>
      </>
    );

  const busy = changed.isPending || upload.isPending || remove.isPending;
  return (
    <>
      <h1>{t("themes.title")}</h1>
      <p className="lead">{t("themes.intro")}</p>
      <Async query={query}>
        {(state) => {
          const locked = state.locked;
          const inForce = (sha?: string, name?: string) =>
            sha
              ? state.active.sha256 === sha
              : state.active.sha256 === undefined && state.active.name === name;
          const describe = (r: ThemeRef) => `${r.title || r.name} ${r.version}`;
          return (
            <>
              {locked ? (
                <Alert tone="info" role="note" title={t("themes.locked")}>
                  <p>{t("themes.lockedHelp")}</p>
                </Alert>
              ) : null}
              {failure ? (
                <Alert
                  tone="danger"
                  role="alert"
                  title={t("themes.failedTitle")}
                >
                  <p>{failure}</p>
                </Alert>
              ) : null}
              <Card title={t("themes.active")} titleId="themes-active">
                <p>
                  <strong>{describe(state.active)}</strong>{" "}
                  <span className="muted">
                    (
                    {t(
                      `themes.source.${state.active.source}` as "themes.source.config",
                    )}
                    )
                  </span>
                </p>
                <p className="muted">
                  {t("themes.default", {
                    name: describe(state.deployment_default),
                  })}
                </p>
                {!locked && state.active.source !== "config" ? (
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => changed.mutate(null)}
                  >
                    {t("themes.reset")}
                  </button>
                ) : null}
              </Card>

              <Card title={t("themes.builtins")} titleId="themes-builtins">
                <ul className="theme-list">
                  {state.builtins.map((b) => (
                    <li key={b.name}>
                      <strong>{describe(b)}</strong>{" "}
                      {inForce(undefined, b.name) ? (
                        <span className="badge badge-ok">
                          {t("themes.inForce")}
                        </span>
                      ) : (
                        <button
                          type="button"
                          disabled={locked || busy}
                          onClick={() => changed.mutate({ builtin: b.name })}
                        >
                          {t("themes.use")}{" "}
                          <span className="sr-only">{b.name}</span>
                        </button>
                      )}
                    </li>
                  ))}
                </ul>
              </Card>

              <Card title={t("themes.stored")} titleId="themes-stored">
                {state.themes.length === 0 ? (
                  <p>{t("themes.none")}</p>
                ) : (
                  <table>
                    <caption className="sr-only">{t("themes.stored")}</caption>
                    <thead>
                      <tr>
                        <th scope="col">{t("themes.name")}</th>
                        <th scope="col">{t("themes.origin")}</th>
                        <th scope="col">{t("themes.uploadedBy")}</th>
                        <th scope="col">{t("themes.uploadedAt")}</th>
                        <th scope="col">
                          <span className="sr-only">{t("themes.actions")}</span>
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {state.themes.map((s) => {
                        const current = inForce(s.sha256);
                        return (
                          <tr key={s.sha256}>
                            <th scope="row" data-label={t("themes.name")}>
                              {s.title || s.name}{" "}
                              <span className="muted">{s.version}</span>
                              <div className="muted" title={s.sha256}>
                                {s.sha256.slice(0, 12)}
                              </div>
                            </th>
                            <td data-label={t("themes.origin")}>
                              {t(
                                `themes.origin.${s.origin}` as "themes.origin.upload",
                              )}
                            </td>
                            <td data-label={t("themes.uploadedBy")}>
                              {s.uploaded_by}
                            </td>
                            <td data-label={t("themes.uploadedAt")}>
                              <Time iso={s.uploaded_at} />
                            </td>
                            <td data-label={t("themes.actions")}>
                              {current ? (
                                <span className="badge badge-ok">
                                  {t("themes.inForce")}
                                </span>
                              ) : null}
                              {!current ? (
                                <button
                                  type="button"
                                  disabled={locked || busy}
                                  onClick={() =>
                                    changed.mutate({ sha256: s.sha256 })
                                  }
                                >
                                  {t("themes.use")}{" "}
                                  <span className="sr-only">{s.name}</span>
                                </button>
                              ) : null}{" "}
                              {!current && asking !== s.sha256 ? (
                                <button
                                  type="button"
                                  className="danger"
                                  disabled={busy}
                                  onClick={() => setAsking(s.sha256)}
                                >
                                  {t("themes.delete")}{" "}
                                  <span className="sr-only">{s.name}</span>
                                </button>
                              ) : null}
                              {asking === s.sha256 ? (
                                <span
                                  role="group"
                                  aria-label={t("themes.confirmDelete", {
                                    name: s.name,
                                  })}
                                >
                                  {t("themes.confirmDelete", { name: s.name })}{" "}
                                  <button
                                    type="button"
                                    className="danger"
                                    disabled={busy}
                                    onClick={() => remove.mutate(s.sha256)}
                                  >
                                    {t("themes.confirm")}
                                  </button>{" "}
                                  <button
                                    type="button"
                                    onClick={() => setAsking("")}
                                  >
                                    {t("themes.cancel")}
                                  </button>
                                </span>
                              ) : null}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                )}
              </Card>

              <Card title={t("themes.upload")} titleId="themes-upload">
                <p>{t("themes.uploadHelp")}</p>
                <label>
                  <span>{t("themes.file")}</span>{" "}
                  <input
                    ref={fileRef}
                    type="file"
                    accept=".zip,application/zip"
                    disabled={busy}
                    onChange={(e) => {
                      const f = e.target.files?.[0];
                      if (f) upload.mutate(f);
                    }}
                  />
                </label>
                {uploaded ? (
                  <Alert tone="ok" role="status">
                    {t("themes.uploaded", {
                      name: uploaded.title || uploaded.name,
                    })}
                  </Alert>
                ) : null}
              </Card>
            </>
          );
        }}
      </Async>
    </>
  );
}

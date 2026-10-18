import { Link } from "react-router";
import { Card } from "../components/ui";
import { useI18n } from "../i18n";
import { Fleet } from "../inventory/Fleet";
import { usePageTitle } from "../usePageTitle";

/** Queries that work on any inventory: they name schema types and attributes, never a device. */
export const EXAMPLES = ["type:device role:switch", "type:device health:critical,warning", "type:disk wear_pct>70", "status:absent"];

export function SearchExamples() {
  const { t } = useI18n();
  return (
    <ul className="examples" aria-label={t("home.examples")}>
      {EXAMPLES.map((q) => (
        <li key={q}>
          <Link to={`/search?q=${encodeURIComponent(q)}`}>
            <code>{q}</code>
          </Link>
        </li>
      ))}
    </ul>
  );
}

export function HomePage() {
  const { t } = useI18n();
  usePageTitle(t("home.title"));
  return (
    <>
      <h1>{t("home.title")}</h1>
      <p className="lead">{t("home.intro")}</p>
      <Fleet defaultGroup="rack" />
      <Card title={t("home.examples")} titleId="home-examples-h">
        <p className="muted">
          {t("home.searchIntro")} {t("search.hint")}
        </p>
        <SearchExamples />
      </Card>
      <h2>{t("home.sections")}</h2>
      <ul className="cards">
        <li>
          <Link to="/discrepancies">{t("nav.discrepancies")}</Link>
          <p>{t("home.discrepanciesText")}</p>
        </li>
        <li>
          <Link to="/proposals">{t("nav.proposals")}</Link>
          <p>{t("home.proposalsText")}</p>
        </li>
        <li>
          <Link to="/health">{t("nav.health")}</Link>
          <p>{t("home.healthText")}</p>
        </li>
      </ul>
    </>
  );
}

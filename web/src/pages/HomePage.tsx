import { Link } from "react-router";
import { useI18n } from "../i18n";
import { usePageTitle } from "../usePageTitle";

const EXAMPLES = ["sw-a1", "192.0.2.13", "type:device vendor:arista", "status:absent"];

export function HomePage() {
  const { t } = useI18n();
  usePageTitle(t("home.title"));
  return (
    <>
      <h1>{t("home.title")}</h1>
      <p>{t("home.intro")}</p>
      <p className="hint">{t("search.hint")}</p>
      <h2>{t("home.examples")}</h2>
      <ul className="examples">
        {EXAMPLES.map((q) => (
          <li key={q}>
            <Link to={`/search?q=${encodeURIComponent(q)}`}>
              <code>{q}</code>
            </Link>
          </li>
        ))}
      </ul>
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

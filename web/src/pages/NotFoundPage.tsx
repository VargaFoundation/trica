import { Link } from "react-router";
import { useI18n } from "../i18n";
import { usePageTitle } from "../usePageTitle";

export function NotFoundPage() {
  const { t } = useI18n();
  usePageTitle(t("notFound.title"));
  return (
    <>
      <h1>{t("notFound.title")}</h1>
      <p>{t("notFound.text")}</p>
      <p>
        <Link to="/">{t("notFound.home")}</Link>
      </p>
    </>
  );
}

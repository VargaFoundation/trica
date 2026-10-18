import { Link } from "react-router";
import { useI18n } from "../../i18n";

export interface Crumb {
  label: string;
  to?: string;
}

/** Where the page sits: links up to the last crumb, which is the page itself. */
export function Breadcrumbs({ trail }: { trail: Crumb[] }) {
  const { t } = useI18n();
  if (trail.length === 0) return <div className="breadcrumbs" />;
  return (
    <nav aria-label={t("nav.breadcrumbs")} className="breadcrumbs">
      <ol>
        {trail.map((c, i) => {
          const last = i === trail.length - 1;
          return (
            <li key={`${i}-${c.label}`}>
              {last || !c.to ? (
                <span aria-current={last ? "page" : undefined}>{c.label}</span>
              ) : (
                <Link to={c.to}>{c.label}</Link>
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

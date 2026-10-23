import type { LocationConflictDetails } from "../discrepancies";
import { useI18n } from "../i18n";
import { ObjectLink } from "./ObjectLink";

/** The places a device is declared in, the site each leads to and who said so (ADR 0061). */
export function LocationConflict({ details }: { details: LocationConflictDetails }) {
  const { t } = useI18n();
  return (
    <ul className="location-conflict">
      {details.reason === "rack" && details.rack_id ? (
        <li data-rack={details.rack_id}>
          {t("discrepancies.location.rack")} <ObjectLink id={details.rack_id} />
          {details.rack_site_id ? (
            <>
              {" "}
              (<ObjectLink id={details.rack_site_id} />)
            </>
          ) : null}
          : <span className="muted">{t("discrepancies.location.rackWins")}</span>
        </li>
      ) : null}
      {details.reason === "racks" ? (
        <>
          <li data-racks className="muted">
            {t("discrepancies.location.racks")}
          </li>
          {(details.racks ?? []).map((m) => (
            <li key={m.rack_id} data-rack={m.rack_id}>
              {t("discrepancies.location.rack")} <ObjectLink id={m.rack_id} />{" "}
              {m.site_id ? (
                <>
                  {t("discrepancies.location.inSite")} <ObjectLink id={m.site_id} />
                </>
              ) : (
                <span className="muted">{t("discrepancies.location.noSite")}</span>
              )}{" "}
              <span className="muted">({m.sources.join(", ")})</span>
            </li>
          ))}
        </>
      ) : null}
      {details.places.map((p) => (
        <li key={p.place_id} data-place={p.place_id}>
          <ObjectLink id={p.place_id} />{" "}
          {p.site_id ? (
            <>
              {t("discrepancies.location.inSite")} <ObjectLink id={p.site_id} />
            </>
          ) : (
            <span className="muted">{t("discrepancies.location.noSite")}</span>
          )}{" "}
          <span className="muted">({p.sources.join(", ")})</span>
        </li>
      ))}
    </ul>
  );
}

import { useI18n, type MessageKey } from "../../i18n";

const UTIL = ["normal", "warn", "crit", "down", "unknown"] as const;
const CONF = ["declared", "observed", "deduced"] as const;
const DASH: Record<(typeof CONF)[number], string | undefined> = { declared: undefined, observed: "7 4", deduced: "2 5" };

export function MapLegend({ live }: { live: boolean }) {
  const { t } = useI18n();
  return (
    <div className="iso-legend" role="group" aria-label={t("topology.map.legend")}>
      <ul className="topology-legend" aria-label={t("topology.legend")}>
        {CONF.map((c) => (
          <li key={c}>
            <svg width="44" height="10" aria-hidden="true" focusable="false">
              <line className={`legend-line-${c}`} x1="1" y1="5" x2="43" y2="5" strokeWidth="3" strokeDasharray={DASH[c]} />
            </svg>
            {t(`topology.legend.${c}` as MessageKey)}
          </li>
        ))}
      </ul>
      {live ? (
        <ul className="topology-legend" aria-label={t("topology.live.legend")}>
          {UTIL.map((c) => (
            <li key={c}>
              <svg width="44" height="10" aria-hidden="true" focusable="false">
                <line className={`legend-live-${c}`} x1="1" y1="5" x2="43" y2="5" strokeWidth="3" strokeDasharray={c === "down" ? "5 4" : undefined} />
              </svg>
              {t(`topology.live.util.${c}` as MessageKey)}
            </li>
          ))}
        </ul>
      ) : null}
      <p className="hint">{t("topology.map.legend.width")}</p>
    </div>
  );
}

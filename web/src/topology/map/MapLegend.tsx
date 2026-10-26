import { useI18n, type MessageKey } from "../../i18n";
import { BAND_WIDTH, SPEED_BANDS } from "./speed";
import { TUNNEL_DASH, UPLINKED_DASH } from "./scene";
import { STATE_DASH } from "./flow";

const UTIL = ["normal", "warn", "crit", "down", "unknown"] as const;

export function MapLegend({ live, uplinks, tunnels }: { live: boolean; uplinks: boolean; tunnels: boolean }) {
  const { t } = useI18n();
  return (
    <div className="iso-legend" role="group" aria-label={t("topology.map.legend")}>
      <ul className="topology-legend" aria-label={t("topology.map.legend.speedTitle")}>
        {SPEED_BANDS.map((b) => (
          <li key={b}>
            <svg width="44" height="10" aria-hidden="true" focusable="false">
              <line className={`legend-speed legend-speed-${b}`} x1="1" y1="5" x2="43" y2="5" strokeWidth={BAND_WIDTH[b]} strokeLinecap="round" />
            </svg>
            {t(`topology.map.speed.${b}` as MessageKey)}
          </li>
        ))}
        <li>
          <svg width="44" height="10" aria-hidden="true" focusable="false">
            <line className="legend-line-down" x1="1" y1="5" x2="43" y2="5" strokeWidth="2.6" strokeLinecap="round" strokeDasharray={STATE_DASH.down} />
          </svg>
          {t("topology.map.legend.stateDown")}
        </li>
        <li>
          <svg width="44" height="10" aria-hidden="true" focusable="false">
            <line className="legend-line-unknown" x1="1" y1="5" x2="43" y2="5" strokeWidth="2.6" strokeLinecap="round" strokeDasharray={STATE_DASH.unknown} />
          </svg>
          {t("topology.map.legend.stateUnknown")}
        </li>
        <li>
          <svg width="44" height="14" aria-hidden="true" focusable="false">
            <line className="legend-speed legend-speed-10g" x1="1" y1="7" x2="43" y2="7" strokeWidth="4.4" strokeLinecap="round" />
            <rect className="legend-badge" x="14" y="1" width="16" height="12" rx="6" />
          </svg>
          {t("topology.map.legend.bundle")}
        </li>
        {tunnels ? (
          <li>
            <svg width="44" height="10" aria-hidden="true" focusable="false">
              <line className="legend-line-tunnel" x1="1" y1="5" x2="43" y2="5" strokeWidth="3" strokeDasharray={TUNNEL_DASH} />
            </svg>
            {t("topology.map.legend.tunnel")}
          </li>
        ) : null}
        {uplinks ? (
          <li>
            <svg width="44" height="10" aria-hidden="true" focusable="false">
              <line className="legend-line-uplink" x1="1" y1="5" x2="35" y2="5" strokeWidth="3" strokeDasharray={UPLINKED_DASH} />
              <polygon className="legend-arrow-uplink" points="43,5 35,1.5 35,8.5" />
            </svg>
            {t("topology.map.legend.uplinkedTo")}
          </li>
        ) : null}
      </ul>
      {live ? (
        <ul className="topology-legend" aria-label={t("topology.live.legend")}>
          {UTIL.map((c) => (
            <li key={c}>
              <svg width="44" height="10" aria-hidden="true" focusable="false">
                <line className={`legend-live-${c}`} x1="1" y1="5" x2="43" y2="5" strokeWidth="6" strokeOpacity="0.5" strokeLinecap="round" strokeDasharray={c === "down" ? STATE_DASH.down : undefined} />
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

// Picks a device to place: the ones that are in no rack yet. Placing a device
// that is already mounted somewhere is a move, so those are hidden until the
// person asks for them (ADR 0052). The server does the filtering
// (GET /objects?mounted=false); the page only asks and shows.
import { useQuery } from "@tanstack/react-query";
import { useId, useState } from "react";
import { api } from "../api/endpoints";
import { Pill } from "../components/ui";
import { useI18n } from "../i18n";

export interface PickedDevice {
  id: string;
  display: string;
}

export interface DevicePickerProps {
  /** Devices to leave out: those already in the rack being edited. */
  exclude?: ReadonlySet<string>;
  picked?: string;
  onPick: (d: PickedDevice) => void;
  /** Takes the focus when it opens (the dialog's first control). */
  autoFocus?: boolean;
  /** The label of the search box. */
  label?: string;
}

/** The TQL that names both statuses: an absent device is still a device the inventory knows (and the API hides it unless asked). */
const BOTH_STATUSES = "(status:active OR status:absent)";

export function DevicePicker({
  exclude,
  picked,
  onPick,
  autoFocus,
  label,
}: DevicePickerProps) {
  const { t } = useI18n();
  const uid = useId();
  const [text, setText] = useState("");
  const [includeMounted, setIncludeMounted] = useState(false);
  const term = text.trim();
  const q = term ? `${term} ${BOTH_STATUSES}` : BOTH_STATUSES;
  const search = useQuery({
    queryKey: ["device-picker", term, includeMounted],
    queryFn: ({ signal }) =>
      api.objects(
        {
          q,
          type: "device",
          sort: "name",
          limit: 20,
          ...(includeMounted ? {} : { mounted: "false" as const }),
        },
        signal,
      ),
    staleTime: 15_000,
  });
  const items = (search.data?.items ?? []).filter((d) => !exclude?.has(d.id));

  return (
    <div className="picker">
      <div className="field">
        <label htmlFor={`${uid}-search`}>
          {label ?? t("quick.picker.search")}
        </label>
        <input
          id={`${uid}-search`}
          type="search"
          value={text}
          onChange={(e) => setText(e.target.value)}
          autoComplete="off"
          data-autofocus={autoFocus ? "" : undefined}
        />
      </div>
      {search.isError ? <p role="alert">{t("quick.picker.failed")}</p> : null}
      {search.isPending ? (
        <p role="status" className="loading">
          {t("app.loading")}
        </p>
      ) : items.length === 0 ? (
        <p className="hint">
          {term || includeMounted
            ? t("quick.picker.emptyOther")
            : t("quick.picker.empty")}
        </p>
      ) : (
        <ul className="picker-list" aria-label={t("quick.picker.results")}>
          {items.map((d) => (
            <li key={d.id}>
              <label>
                <input
                  type="radio"
                  name={`${uid}-device`}
                  checked={picked === d.id}
                  onChange={() => onPick({ id: d.id, display: d.display })}
                />
                <span className="picker-name">{d.display}</span>
                {d.status === "absent" ? (
                  <Pill tone="warn">{t("quick.picker.absent")}</Pill>
                ) : null}
              </label>
            </li>
          ))}
        </ul>
      )}
      <label className="check">
        <input
          type="checkbox"
          checked={includeMounted}
          onChange={(e) => setIncludeMounted(e.target.checked)}
        />{" "}
        {t("quick.picker.includeOther")}
      </label>
      {includeMounted ? (
        <p className="hint">{t("quick.picker.otherNote")}</p>
      ) : null}
    </div>
  );
}

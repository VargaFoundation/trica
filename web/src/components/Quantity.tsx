import { useI18n } from "../i18n";
import { formatQuantity, type DurationWords } from "../inventory/format";
import { Value } from "./Value";

export function useDurationWords(): DurationWords {
  const { t } = useI18n();
  return { d: t("duration.d"), h: t("duration.h"), min: t("duration.min"), s: t("duration.s") };
}

/** A value with its unit, formatted for the reader's locale (bytes, W, °C, Mb/s, durations), or shown as it is. */
export function Quantity({ value, unit }: { value: unknown; unit?: string }) {
  const { locale } = useI18n();
  const w = useDurationWords();
  const text = formatQuantity(value, unit, locale, w);
  if (text === undefined) return <Value value={value} unit={unit} />;
  return (
    <data className="value num" value={String(value)}>
      {text}
    </data>
  );
}

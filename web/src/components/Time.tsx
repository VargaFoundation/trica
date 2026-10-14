import { useI18n } from "../i18n";

/** A timestamp in the reader's locale; the machine-readable value is in dateTime and title. */
export function Time({ iso }: { iso: string | undefined | null }) {
  const { dateTime } = useI18n();
  if (!iso) return <Empty />;
  return (
    <time dateTime={iso} title={iso}>
      {dateTime(iso)}
    </time>
  );
}

/** "No value", readable by a screen reader (a bare dash is read as nothing). */
export function Empty() {
  const { t } = useI18n();
  return (
    <>
      <span aria-hidden="true">—</span>
      <span className="sr-only">{t("app.none")}</span>
    </>
  );
}

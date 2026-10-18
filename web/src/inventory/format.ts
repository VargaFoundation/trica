// Quantities in the reader's language: the unit is the schema's (`unit` of an
// attribute), the number is formatted by the locale, and a few units are
// scaled to what a person reads (bytes to GiB, seconds to days and hours,
// Mb/s to Gb/s, W to kW). An unknown unit is kept as the schema gives it. A
// number without a unit is left alone: it is as often an identifier.

export interface DurationWords {
  d: string;
  h: string;
  min: string;
  s: string;
}

const BYTES = ["B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"];

function nf(locale: string, digits: number): Intl.NumberFormat {
  return new Intl.NumberFormat(locale, { maximumFractionDigits: digits });
}

/** 1 536 → "1.5 KiB"; binary multiples, as memory and disks are counted. */
export function formatBytes(n: number, locale: string): string {
  let v = Math.abs(n);
  let i = 0;
  while (v >= 1024 && i < BYTES.length - 1) {
    v /= 1024;
    i++;
  }
  const digits = i === 0 || v >= 100 ? 0 : v >= 10 ? 1 : 2;
  return `${nf(locale, digits).format(Math.sign(n) * v)} ${BYTES[i]}`;
}

/** 93 784 s → "1 d 2 h"; the two largest parts, never more. */
export function formatDuration(seconds: number, locale: string, w: DurationWords): string {
  const f = nf(locale, 0);
  let s = Math.max(0, Math.round(seconds));
  const d = Math.floor(s / 86400);
  s -= d * 86400;
  const h = Math.floor(s / 3600);
  s -= h * 3600;
  const m = Math.floor(s / 60);
  s -= m * 60;
  const parts: [number, string][] = [
    [d, w.d],
    [h, w.h],
    [m, w.min],
    [s, w.s],
  ];
  const first = parts.findIndex(([v]) => v > 0);
  if (first < 0) return `0 ${w.s}`;
  return parts
    .slice(first, first + 2)
    .filter(([v]) => v > 0)
    .map(([v, u]) => `${f.format(v)} ${u}`)
    .join(" ");
}

/**
 * A value with its unit, formatted. Returns undefined when the value is not
 * a number: the caller shows it as it is.
 */
export function formatQuantity(value: unknown, unit: string | undefined, locale: string, w: DurationWords): string | undefined {
  if (typeof value !== "number" || !Number.isFinite(value)) return undefined;
  switch (unit) {
    case "B":
      return formatBytes(value, locale);
    case "s":
      return formatDuration(value, locale, w);
    case "h":
      return value >= 48 ? `${nf(locale, 0).format(value)} ${w.h} (${formatDuration(value * 3600, locale, w)})` : `${nf(locale, 0).format(value)} ${w.h}`;
    case "d":
      return `${nf(locale, 0).format(value)} ${w.d}`;
    case "Mb/s":
      return value >= 1000 ? `${nf(locale, 1).format(value / 1000)} Gb/s` : `${nf(locale, 0).format(value)} Mb/s`;
    case "W":
      return Math.abs(value) >= 10000 ? `${nf(locale, 1).format(value / 1000)} kW` : `${nf(locale, value < 100 ? 1 : 0).format(value)} W`;
    case "°C":
    case "%":
    case "V":
    case "dBm":
      return `${nf(locale, 1).format(value)}${unit === "%" ? " %" : ` ${unit}`}`;
    case undefined:
    case "":
      // No unit: an identifier as often as a quantity (a VLAN ID, a port number); not grouped.
      return undefined;
    default:
      return `${nf(locale, 2).format(value)} ${unit}`;
  }
}

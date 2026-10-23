// The closed vocabularies of the Collectors pages: instance status, origin,
// deployment, evidence, credential use and option type. Everything else a
// module says (its title, protocol, options) comes from its descriptor and is
// shown as the API gives it. No module is named here: a new collector appears
// without a change in this folder (ADR 0055).
import { en } from "../i18n/en";
import { useI18n, type MessageKey } from "../i18n";
import type { Tone } from "../inventory/health";

/** Failing first: what needs attention leads. */
export const STATUS_ORDER = ["failing", "stale", "pending", "ok"] as const;

export const STATUS_TONE: Record<string, Tone> = { ok: "ok", failing: "danger", stale: "warn", pending: "neutral" };

type Vocabulary = "status" | "origin" | "deployment" | "evidence" | "use" | "optionType";

/**
 * Translates a value of a closed vocabulary; a value this UI does not know (a
 * newer server) is shown as it is.
 */
export function useWord(): (vocabulary: Vocabulary, value: string) => string {
  const { t } = useI18n();
  return (vocabulary, value) => {
    const key = `collectors.${vocabulary}.${value}`;
    return key in en ? t(key as MessageKey) : value;
  };
}

/** The TQL that finds what a source reports, quoted since a name may hold any character. */
export const sourceQuery = (source: string) => `source:"${source.replace(/["\\]/g, "\\$&")}"`;

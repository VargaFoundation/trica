import { useEffect, useState } from "react";
import { loadUiConfig, type UiConfig } from "./oidc";

/** The server's start-up settings for the page; null while loading. A failed load is "no single sign-on". */
export function useUiConfig(): UiConfig | null {
  const [cfg, setCfg] = useState<UiConfig | null>(null);
  useEffect(() => {
    const ctl = new AbortController();
    void loadUiConfig(ctl.signal).then((c) => {
      if (!ctl.signal.aborted) setCfg(c);
    });
    return () => ctl.abort();
  }, []);
  return cfg;
}

import { lazy, Suspense } from "react";
import { useSearchParams } from "react-router";
import { Loading } from "../components/Async";
import { TabPanel, Tabs } from "../components/ui";
import { useI18n } from "../i18n";
import { usePageTitle } from "../usePageTitle";
import { MapPage } from "./map/MapPage";
import { parseAddress, type View } from "./map/url";

// The network view carries cytoscape and the ELK layout engine (about 1.5 MB):
// a separate chunk, fetched only when that tab is opened. The map must never
// import it (guarded by map/chunk.test.ts).
const TopologyPage = lazy(() => import("./TopologyPage").then((m) => ({ default: m.TopologyPage })));

/** /topology: the isometric map (the default) and the network graph, as two tabs of one page. */
export function TopologyShell() {
  const { t } = useI18n();
  usePageTitle(t("topology.title"));
  const [params, setParams] = useSearchParams();
  const view = parseAddress(params).view;
  const select = (id: string) => {
    // A tab starts the other view clean: the filters of one mean nothing to the other.
    setParams(id === "network" ? new URLSearchParams({ view: "network" }) : new URLSearchParams(), { replace: false });
  };
  return (
    <>
      <h1>{t("topology.title")}</h1>
      <Tabs
        idPrefix="topology"
        label={t("topology.views")}
        active={view}
        onSelect={select}
        tabs={[
          { id: "map" satisfies View, label: t("topology.view.map") },
          { id: "network" satisfies View, label: t("topology.view.network") },
        ]}
      />
      <TabPanel idPrefix="topology" id={view}>
        {view === "map" ? (
          <MapPage />
        ) : (
          <Suspense fallback={<Loading />}>
            <TopologyPage embedded />
          </Suspense>
        )}
      </TabPanel>
    </>
  );
}

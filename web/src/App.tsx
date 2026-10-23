import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { lazy, Suspense, useState, type ReactNode } from "react";
import { Navigate, Route, Routes } from "react-router";
import { ApiError } from "./api/client";
import { AuthGate } from "./auth";
import { takeLanding } from "./auth/oidc";
import { Layout } from "./components/Layout";
import { I18nProvider, useI18n, type Locale } from "./i18n";
import { CollectorInstancePage } from "./collectors/CollectorInstancePage";
import { CollectorPage } from "./collectors/CollectorPage";
import { CollectorsPage } from "./collectors/CollectorsPage";
import { DiscrepanciesPage } from "./pages/DiscrepanciesPage";
import { HealthPage } from "./pages/HealthPage";
import { HomePage } from "./pages/HomePage";
import { NotFoundPage } from "./pages/NotFoundPage";
import { ObjectPage } from "./pages/ObjectPage";
import { ObjectsPage } from "./pages/ObjectsPage";
import { ProposalPage, ProposalsPage } from "./pages/ProposalsPage";
import { SearchPage } from "./pages/SearchPage";
import { RackPage } from "./racks/RackPage";
import { RoomPage, RoomsPage } from "./racks/RoomPages";
import { ThemeProvider, type ThemePreference } from "./theme";
import { ThemesPage } from "./themes/ThemesPage";

// The topology page (the isometric map and, behind a tab, the network graph) is its own chunk,
// fetched when the page is opened; the graph's cytoscape and ELK (about 1.5 MB) are a further one.
const TopologyShell = lazy(() =>
  import("./topology/TopologyShell").then((m) => ({ default: m.TopologyShell })),
);

function Lazy({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  return (
    <Suspense
      fallback={
        <p role="status" className="loading">
          {t("app.loading")}
        </p>
      }
    >
      {children}
    </Suspense>
  );
}

export function makeQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // A 4xx will answer the same next time: do not hammer the server.
        retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2,
        staleTime: 15_000,
        refetchOnWindowFocus: false,
      },
    },
  });
}

/** Everything the app needs around the router. Tests pass their own client and initial locale/theme. */
export function Providers({
  children,
  client,
  locale,
  theme,
}: {
  children: ReactNode;
  client?: QueryClient;
  locale?: Locale;
  theme?: ThemePreference;
}) {
  const [own] = useState(makeQueryClient);
  return (
    <I18nProvider initial={locale}>
      <ThemeProvider initialTheme={theme}>
        <QueryClientProvider client={client ?? own}>{children}</QueryClientProvider>
      </ThemeProvider>
    </I18nProvider>
  );
}

/** The callback address, once signed in: on to the page the reader had asked for. */
function AfterSignIn() {
  const [to] = useState(takeLanding);
  return <Navigate to={to} replace />;
}

/** Every page has its own address: /objects?type=, /objects/:id, /search?q=, /discrepancies, /proposals/:id, /topology, /health, /collectors, /collectors/:kind/:name. */
export function App() {
  return (
    <AuthGate>
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<HomePage />} />
          <Route path="search" element={<SearchPage />} />
          <Route path="objects" element={<ObjectsPage />} />
          <Route path="objects/:id" element={<ObjectPage />} />
          <Route path="discrepancies" element={<DiscrepanciesPage />} />
          <Route path="proposals" element={<ProposalsPage />} />
          <Route path="proposals/:id" element={<ProposalPage />} />
          <Route path="rooms" element={<RoomsPage />} />
          <Route path="rooms/:id" element={<RoomPage />} />
          <Route path="racks/:id" element={<RackPage />} />
          <Route
            path="topology"
            element={
              <Lazy>
                <TopologyShell />
              </Lazy>
            }
          />
          <Route path="health" element={<HealthPage />} />
          <Route path="collectors" element={<CollectorsPage />} />
          <Route path="collectors/:kind" element={<CollectorPage />} />
          <Route path="collectors/:kind/:name" element={<CollectorInstancePage />} />
          <Route path="admin/themes" element={<ThemesPage />} />
          <Route path="oidc/callback" element={<AfterSignIn />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </AuthGate>
  );
}

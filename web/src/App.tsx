import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { Route, Routes } from "react-router";
import { ApiError } from "./api/client";
import { AuthGate } from "./auth";
import { Layout } from "./components/Layout";
import { I18nProvider, type Locale } from "./i18n";
import { DiscrepanciesPage } from "./pages/DiscrepanciesPage";
import { HealthPage } from "./pages/HealthPage";
import { HomePage } from "./pages/HomePage";
import { NotFoundPage } from "./pages/NotFoundPage";
import { ObjectPage } from "./pages/ObjectPage";
import { ProposalPage, ProposalsPage } from "./pages/ProposalsPage";
import { SearchPage } from "./pages/SearchPage";
import { ThemeProvider, type ThemePreference } from "./theme";

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

/** Every page has its own address: /objects/:id, /search?q=, /discrepancies, /proposals/:id, /health. */
export function App() {
  return (
    <AuthGate>
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<HomePage />} />
          <Route path="search" element={<SearchPage />} />
          <Route path="objects/:id" element={<ObjectPage />} />
          <Route path="discrepancies" element={<DiscrepanciesPage />} />
          <Route path="proposals" element={<ProposalsPage />} />
          <Route path="proposals/:id" element={<ProposalPage />} />
          <Route path="health" element={<HealthPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </AuthGate>
  );
}

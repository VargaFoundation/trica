import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useSyncExternalStore, type ReactNode } from "react";
import { getAuthState, subscribeAuth } from "./api/token";
import { ErrorBox, Loading } from "./components/Async";
import { useMe } from "./hooks";
import { LoginPage } from "./pages/LoginPage";

/**
 * Shows the sign-in form until there is a token, then loads who the caller is
 * (GET /me) before showing the app, so that the first render already knows
 * which actions to offer. A 401 anywhere clears the token (see api/client) and
 * lands back here.
 */
export function AuthGate({ children }: { children: ReactNode }) {
  const auth = useSyncExternalStore(subscribeAuth, getAuthState);
  const client = useQueryClient();

  // Nothing cached for one caller may be shown to the next.
  useEffect(() => {
    if (!auth.token) client.clear();
  }, [auth.token, client]);

  if (!auth.token) return <LoginPage rejected={auth.rejected} />;
  return <Authenticated>{children}</Authenticated>;
}

function Authenticated({ children }: { children: ReactNode }) {
  const me = useMe();
  if (me.isPending)
    return (
      <main id="main">
        <Loading />
      </main>
    );
  if (me.isError)
    return (
      <main id="main">
        <ErrorBox error={me.error} onRetry={() => void me.refetch()} />
      </main>
    );
  return <>{children}</>;
}

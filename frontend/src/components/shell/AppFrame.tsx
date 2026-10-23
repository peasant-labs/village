"use client";

import { Suspense, useSyncExternalStore, type ReactNode } from "react";
import { usePathname } from "next/navigation";
import { AppSidebarLayout } from "@peasant-labs/fairtrade/ui";
import { useAuth } from "@/providers/AuthProvider";
import { useTheme } from "@/hooks/useTheme";
import OnlineSidebar from "@/components/shell/OnlineSidebar";
import PublicTopBar from "@/components/shell/PublicTopBar";
import { LOGIN_PATH } from "@/lib/signIn";
import { hasAuthTokenCookie } from "@/lib/api";

const noSubscription = () => () => {};

/** Routes drawn with no chrome at all: the sign-in steps carry their own. */
const BARE = new Set([LOGIN_PATH, "/auth/callback"]);

/**
 * Which chrome a page is drawn in.
 *
 * A signed-in person gets fairtrade's sidebar beside the page. Somebody reading
 * without an account (a shared transcript, a public collective, a pull
 * request's prompts) gets the public bar with one way to sign in. The sign-in
 * steps get none.
 */
export default function AppFrame({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const { isLoading, isLoggedIn, user } = useAuth();
  // The stored theme applies on every route, the bare sign-in steps included,
  // whichever chrome (if any) draws the switch.
  useTheme();
  // While the session read is in flight, a browser holding a session cookie is
  // drawn in the app's chrome rather than the public bar, so a signed-in page
  // does not jump sideways when the answer arrives. The server render cannot
  // see the cookie and draws the public frame; hydration then corrects it.
  const holdsCookie = useSyncExternalStore(noSubscription, hasAuthTokenCookie, () => false);
  const signedIn = isLoading ? holdsCookie : isLoggedIn && user?.username_chosen === true;

  if (BARE.has(pathname)) {
    return <main id="main">{children}</main>;
  }

  if (signedIn) {
    return (
      <AppSidebarLayout
        className="on-shell"
        sidebar={
          <Suspense>
            <OnlineSidebar user={isLoggedIn ? user : null} />
          </Suspense>
        }
      >
        <main id="main">{children}</main>
      </AppSidebarLayout>
    );
  }

  return (
    <>
      <Suspense>
        <PublicTopBar offerSignIn={!isLoading && !isLoggedIn} />
      </Suspense>
      <main id="main">{children}</main>
    </>
  );
}

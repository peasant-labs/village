"use client";

import { Suspense, useEffect, useSyncExternalStore, type ReactNode } from "react";
import { usePathname } from "next/navigation";
import { AppSidebarLayout } from "@peasant-labs/fairtrade/ui";
import { useAuth } from "@/providers/AuthProvider";
import { useTheme } from "@/hooks/useTheme";
import OnlineSidebar from "@/components/shell/OnlineSidebar";
import PublicTopBar from "@/components/shell/PublicTopBar";
import { LOGIN_PATH, SIGN_IN_START_PATH, isOpenSignedOut, returnAddressFor } from "@/lib/signIn";
import { MARKETING_URL, leaveForSignIn } from "@/lib/marketing";
import { hasAuthTokenCookie } from "@/lib/api";

const noSubscription = () => () => {};

/** Routes drawn with no chrome at all: the sign-in steps carry their own. */
const BARE = new Set([LOGIN_PATH, SIGN_IN_START_PATH, "/auth/callback"]);

/**
 * Which chrome a page is drawn in, and whether it is drawn at all.
 *
 * A signed-in person gets fairtrade's sidebar beside the page. The app keeps no
 * signed-out view: the proxy sends a page load without a session to the
 * marketing site's log-in popup, and once `GET /auth/me` answers this frame does
 * the same for a session cookie that turned out to be stale, coming back to the
 * same page once signed in. The pages open without a session (the sign-in steps
 * and the privacy notice) are the exception; the privacy notice is drawn in the
 * public bar, and the sign-in steps in no chrome at all. `/login` hands the
 * visitor over itself, with the address it was asked to return to.
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
  const leaving = !isLoading && !isLoggedIn && !isOpenSignedOut(pathname) && pathname !== LOGIN_PATH;

  useEffect(() => {
    if (leaving) leaveForSignIn(returnAddressFor(window.location.pathname, window.location.search));
  }, [leaving]);

  if (leaving) {
    return (
      <main id="main">
        <div className="on-login" data-testid="leaving-for-sign-in">
          <p className="on-muted" role="status">
            taking you to log in on{" "}
            <a className="on-link" href={MARKETING_URL}>
              {MARKETING_URL.replace(/^https?:\/\//, "")}
            </a>
          </p>
        </div>
      </main>
    );
  }

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

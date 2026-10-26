"use client";

import { Suspense, useEffect, useState, useSyncExternalStore, type ReactNode } from "react";
import { usePathname } from "next/navigation";
import { AppSidebarLayout } from "@peasant-labs/fairtrade/ui";
import { useAuth } from "@/providers/AuthProvider";
import { useTheme } from "@/hooks/useTheme";
import OnlineSidebar from "@/components/shell/OnlineSidebar";
import PublicTopBar from "@/components/shell/PublicTopBar";
import { SignedOutDeclarationContext, type SignedOutDeclaration } from "@/components/shell/signedOutDeclaration";
import { LOGIN_PATH, SIGN_IN_START_PATH, isOpenSignedOut, isTranscriptPage, returnAddressFor } from "@/lib/signIn";
import { MARKETING_URL, leaveForSignIn, reloadPage } from "@/lib/marketing";
import { clearAuthTokenCookie, hasAuthTokenCookie } from "@/lib/api";

const noSubscription = () => () => {};

/** Routes drawn with no chrome at all: the sign-in steps carry their own. */
const BARE = new Set([LOGIN_PATH, SIGN_IN_START_PATH, "/auth/callback"]);

/**
 * Which chrome a page is drawn in, and whether it is drawn at all.
 *
 * A signed-in person gets fairtrade's sidebar beside the page. The proxy sends
 * a page load without a session to the marketing site's log-in popup, and once
 * `GET /auth/me` answers this frame does the same for a session cookie that
 * turned out to be stale, coming back to the same page once signed in. The
 * pages open without a session (the sign-in steps and the privacy notice) are
 * the exception; the privacy notice is drawn in the public bar, and the sign-in
 * steps in no chrome at all. `/login` hands the visitor over itself, with the
 * address it was asked to return to.
 *
 * A transcript page decides for itself on the server, and tells this frame
 * (`useDeclareSignedOutReadable`). One the server marked public is drawn for a
 * signed-out visitor in the public bar, with its way to sign in; any other is
 * left for sign-in like every other page. Until the page has said which, the
 * frame waits rather than leave: the server has already refused a signed-out
 * load of a transcript that is not public, so the page only says so for a
 * stale session cookie or an older browser.
 *
 * A stale session cookie hid the visitor from that decision: the server
 * served the page to the cookie without reading the transcript. So on a
 * transcript page, once the session read answers 401, the frame drops the
 * cookie and loads the page again, and the server decides as for anybody
 * signed out: a public transcript opens, anything else is sent to sign in.
 * The reload happens only once the cookie is gone, so it cannot repeat.
 */
export default function AppFrame({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const { isLoading, isLoggedIn, user, sessionRefused } = useAuth();
  // The stored theme applies on every route, the bare sign-in steps included,
  // whichever chrome (if any) draws the switch.
  useTheme();
  // While the session read is in flight, a browser holding a session cookie is
  // drawn in the app's chrome rather than the public bar, so a signed-in page
  // does not jump sideways when the answer arrives. The server render cannot
  // see the cookie and draws the public frame; hydration then corrects it.
  const holdsCookie = useSyncExternalStore(noSubscription, hasAuthTokenCookie, () => false);
  const signedIn = isLoading ? holdsCookie : isLoggedIn && user?.username_chosen === true;
  // A declaration is kept until another replaces it, and read only on the page
  // it was made on: forgetting it as the page unmounts would bring the page
  // back to say it again.
  const [declared, setDeclared] = useState<SignedOutDeclaration | null>(null);
  const pageDecides = isTranscriptPage(pathname);
  const pageAnswer = pageDecides && declared?.pathname === pathname ? declared.readable : null;
  const leaving =
    !isLoading &&
    !isLoggedIn &&
    !isOpenSignedOut(pathname) &&
    pathname !== LOGIN_PATH &&
    (!pageDecides || pageAnswer === false);

  const staleCookieOnPageThatDecides = leaving && pageDecides && sessionRefused;
  useEffect(() => {
    if (!leaving) return;
    if (staleCookieOnPageThatDecides && hasAuthTokenCookie()) {
      clearAuthTokenCookie();
      if (!hasAuthTokenCookie()) {
        reloadPage();
        return;
      }
    }
    leaveForSignIn(returnAddressFor(window.location.pathname, window.location.search));
  }, [leaving, staleCookieOnPageThatDecides]);

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

  const page = (
    <SignedOutDeclarationContext.Provider value={setDeclared}>{children}</SignedOutDeclarationContext.Provider>
  );

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
        <main id="main">{page}</main>
      </AppSidebarLayout>
    );
  }

  return (
    <>
      <Suspense>
        <PublicTopBar offerSignIn={!isLoading && !isLoggedIn} />
      </Suspense>
      <main id="main">{page}</main>
    </>
  );
}

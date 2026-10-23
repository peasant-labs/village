import { NextResponse, type NextRequest } from "next/server";
import { AUTH_COOKIE } from "@/lib/authCookieName";
import { signedOutDestination } from "@/lib/marketing";
import { isOpenSignedOut } from "@/lib/signIn";

/**
 * A visitor with no session who opens any page of the app is sent to the
 * marketing site's log-in popup before the page is served, carrying the page
 * as where to come back to once signed in. The hosted app keeps no signed-out
 * view of its own.
 *
 * Three pages are served without a session (see `isOpenSignedOut`): the two
 * steps of signing in (`/auth/start`, where the popup's button lands, and
 * `/auth/callback`, where the provider returns) and the privacy notice the
 * popup links to.
 *
 * Only a page load is redirected. A fetch the page makes (Next prefetching or
 * navigating inside the app) is served the app, and the app's frame then sends
 * a signed-out visitor on itself: redirecting that fetch would point a
 * same-origin fetch at another origin, which the browser refuses with a CORS
 * error. Next strips its own `RSC` header before the proxy runs, so the fetch
 * is told apart by the browser's `Sec-Fetch-Dest`, which is `document` for a
 * page load. A client that sends no `Sec-Fetch-Dest` (curl, a crawler) is
 * treated as a page load.
 *
 * Only the cookie's presence is read here. The cookie can still be stale (expired
 * or signed out elsewhere), so the app's frame repeats the decision once `GET
 * /auth/me` answers.
 */
export function proxy(request: NextRequest) {
  const { pathname, search } = request.nextUrl;
  if (isOpenSignedOut(pathname)) return NextResponse.next();
  if (request.cookies.get(AUTH_COOKIE)?.value) return NextResponse.next();
  const destination = request.headers.get("sec-fetch-dest");
  if (destination !== null && destination !== "document") return NextResponse.next();
  return NextResponse.redirect(signedOutDestination(pathname, search), 307);
}

export const config = {
  // Every page; never the build's own assets or the files served from public/.
  matcher: ["/((?!_next/|api/|favicon\\.ico$|.*\\.(?:svg|png|jpg|jpeg|gif|webp|ico|txt|xml|webmanifest|woff2?)$).*)"],
};

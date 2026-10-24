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
 * Link previews are the one exception. When a transcript link is pasted into
 * X, Slack, Discord, LinkedIn or Facebook, their crawler must read the page's
 * meta tags and the two share images, and a crawler holds no session. So a
 * signed-out request is served:
 *
 * - the share images (`/transcripts/<id>/opengraph-image` and
 *   `/twitter-image`) whatever its headers, because they are images, never
 *   pages;
 * - the transcript page (`/transcripts/<id>`, one segment) only when it sends
 *   no `Sec-Fetch-Dest`, as crawlers do. Current browsers send `document` for
 *   a page load, so a person is still sent to sign in here. An older browser
 *   that sends no Fetch Metadata is served the page, and the app frame's own
 *   signed-out redirect then sends that visitor to sign in.
 *
 * This redirect is not what keeps a transcript private. The metadata and the
 * images read the transcript as an anonymous visitor on every request, and
 * anything that is not public gets the site's generic preview, the same for a
 * private id as for one that does not exist.
 *
 * Only the cookie's presence is read here. The cookie can still be stale (expired
 * or signed out elsewhere), so the app's frame repeats the decision once `GET
 * /auth/me` answers.
 */
/** A transcript page, or one of its two share images. */
const TRANSCRIPT_PREVIEW = /^\/transcripts\/[^/]+(\/(?:opengraph|twitter)-image)?$/;

export function proxy(request: NextRequest) {
  const { pathname, search } = request.nextUrl;
  if (isOpenSignedOut(pathname)) return NextResponse.next();
  if (request.cookies.get(AUTH_COOKIE)?.value) return NextResponse.next();
  const destination = request.headers.get("sec-fetch-dest");
  const preview = TRANSCRIPT_PREVIEW.exec(pathname);
  if (preview && (preview[1] !== undefined || destination === null)) return NextResponse.next();
  if (destination !== null && destination !== "document") return NextResponse.next();
  return NextResponse.redirect(signedOutDestination(pathname, search), 307);
}

export const config = {
  // Every page; never the build's own assets or the files served from public/.
  matcher: ["/((?!_next/|api/|favicon\\.ico$|.*\\.(?:svg|png|jpg|jpeg|gif|webp|ico|txt|xml|webmanifest|woff2?)$).*)"],
};

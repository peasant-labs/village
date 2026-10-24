import { NextResponse, type NextRequest } from "next/server";
import { AUTH_COOKIE } from "@/lib/authCookieName";
import { signedOutDestination } from "@/lib/marketing";
import { isOpenSignedOut } from "@/lib/signIn";

/**
 * A visitor with no session who opens any page of the app is sent to the
 * marketing site's log-in popup before the page is served, carrying the page
 * as where to come back to once signed in. The hosted app's one signed-out view
 * is a public transcript.
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
 * A transcript is the exception, and its own page decides. A signed-out
 * request is passed through for:
 *
 * - the transcript page (`/transcripts/<id>`, one segment), whatever its
 *   headers. The page reads the transcript as an anonymous visitor on every
 *   request (`decideTranscriptPage`): a public transcript is served and opens
 *   without signing in; anything else (private, shared, missing, an API
 *   failure) is sent to the same log-in popup this proxy would have sent it
 *   to, before anything is served. A client that sends no `Sec-Fetch-Dest`, as
 *   link-preview crawlers do, is still served the page so it can read the meta
 *   tags, which are the site's generic card for anything that is not public.
 * - the share images (`/transcripts/<id>/opengraph-image` and
 *   `/twitter-image`) whatever its headers, because they are images, never
 *   pages, and fail closed to the limited-visibility card on their own.
 *
 * This redirect is not what keeps a transcript private. The API serves a
 * transcript that is not public to nobody without a session, and the page,
 * its metadata and the images all read it as an anonymous visitor.
 *
 * Only the cookie's presence is read here. The cookie can still be stale (expired
 * or signed out elsewhere), so the app's frame repeats the decision once `GET
 * /auth/me` answers.
 */
/** A transcript page, or one of its two share images. */
const TRANSCRIPT_ROUTE = /^\/transcripts\/[^/]+(\/(?:opengraph|twitter)-image)?$/;

export function proxy(request: NextRequest) {
  const { pathname, search } = request.nextUrl;
  if (isOpenSignedOut(pathname)) return NextResponse.next();
  if (request.cookies.get(AUTH_COOKIE)?.value) return NextResponse.next();
  if (TRANSCRIPT_ROUTE.test(pathname)) return NextResponse.next();
  const destination = request.headers.get("sec-fetch-dest");
  if (destination !== null && destination !== "document") return NextResponse.next();
  return NextResponse.redirect(signedOutDestination(pathname, search), 307);
}

export const config = {
  // Every page; never the build's own assets or the files served from public/.
  matcher: ["/((?!_next/|api/|favicon\\.ico$|.*\\.(?:svg|png|jpg|jpeg|gif|webp|ico|txt|xml|webmanifest|woff2?)$).*)"],
};

import { NextResponse, type NextRequest } from "next/server";
import { AUTH_COOKIE } from "@/lib/authCookieName";
import { MARKETING_URL } from "@/lib/marketing";

/**
 * A visitor with no session who opens `/` is sent to the marketing site
 * before any page is served.
 *
 * Only a page load is redirected. A fetch the page makes (Next prefetching or
 * navigating to `/` inside the app) is served the app, and the root page then
 * sends a signed-out visitor on itself: redirecting that fetch would point a
 * same-origin fetch at another origin, which the browser refuses with a CORS
 * error. Next strips its own `RSC` header before the proxy runs, so the fetch
 * is told apart by the browser's `Sec-Fetch-Dest`, which is `document` for a
 * page load. A client that sends no `Sec-Fetch-Dest` (curl, a crawler) is
 * treated as a page load.
 *
 * Only the cookie's presence is read here. The cookie can still be stale (expired
 * or signed out elsewhere), so the root page repeats the decision once `GET
 * /auth/me` answers. Every other route is served as before: shared transcripts,
 * public collectives, the privacy notice and pull request pages keep working
 * signed out, and `/login` is where signing in starts.
 */
export function proxy(request: NextRequest) {
  if (request.cookies.get(AUTH_COOKIE)?.value) return NextResponse.next();
  const destination = request.headers.get("sec-fetch-dest");
  if (destination !== null && destination !== "document") return NextResponse.next();
  return NextResponse.redirect(MARKETING_URL, 307);
}

export const config = {
  matcher: "/",
};

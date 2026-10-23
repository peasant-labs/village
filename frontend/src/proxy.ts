import { NextResponse, type NextRequest } from "next/server";
import { AUTH_COOKIE } from "@/lib/authCookieName";
import { MARKETING_URL } from "@/lib/marketing";

/**
 * A visitor with no session who opens `/` is sent to the marketing site
 * before any page is served.
 *
 * Only the cookie's presence is read here. The cookie can still be stale (expired
 * or signed out elsewhere), so the root page repeats the decision once `GET
 * /auth/me` answers. Every other route is served as before: shared transcripts,
 * public collectives, the privacy notice and pull request pages keep working
 * signed out, and `/login` is where signing in starts.
 */
export function proxy(request: NextRequest) {
  if (request.cookies.get(AUTH_COOKIE)?.value) return NextResponse.next();
  return NextResponse.redirect(MARKETING_URL, 307);
}

export const config = {
  matcher: "/",
};

import { AUTH_COOKIE_MAX_AGE_SECONDS } from "@/lib/api";
import { MARKETING_URL } from "@/lib/marketing";

/**
 * A hint, for the marketing site, that this browser is signed in to the online
 * app: a cookie on the domain the two sites share, holding the person's handle
 * and nothing else. The marketing site reads it to show "open online" in place
 * of "log in". It carries no token and grants nothing: the online app still
 * decides who is signed in, so a stale hint only leads to its log-in popup.
 *
 * It is written whenever `GET /auth/me` names the person, and cleared when it
 * answers that nobody is signed in, and on signing out. It lives as long as the
 * session cookie does.
 */
export const ONLINE_HINT_COOKIE = "peasant_online";

/**
 * The domain the app and the marketing site share, which the hint is set on:
 * the marketing site's own host (without `www.`) when the app is served from
 * it or a subdomain of it. Null when they share none (a local or staging app),
 * and then no hint is written.
 */
export function sharedCookieDomain(appHost: string, marketingUrl: string): string | null {
  let site: string;
  try {
    site = new URL(marketingUrl).hostname.toLowerCase().replace(/^www\./, "");
  } catch {
    return null;
  }
  const host = appHost.toLowerCase();
  if (!site.includes(".")) return null;
  return host === site || host.endsWith(`.${site}`) ? site : null;
}

/** The `document.cookie` assignment that writes the hint for `handle`, or clears it for null. */
export function onlineHintCookie(handle: string | null, domain: string): string {
  const attributes = `path=/; domain=${domain}; secure; samesite=lax`;
  return handle
    ? `${ONLINE_HINT_COOKIE}=${encodeURIComponent(handle)}; max-age=${AUTH_COOKIE_MAX_AGE_SECONDS}; ${attributes}`
    : `${ONLINE_HINT_COOKIE}=; max-age=0; ${attributes}`;
}

/** Writes (or, for null, clears) the hint on the shared domain. Does nothing without one. */
export function writeOnlineHint(handle: string | null): void {
  if (typeof document === "undefined") return;
  const domain = sharedCookieDomain(window.location.hostname, MARKETING_URL);
  if (domain) document.cookie = onlineHintCookie(handle, domain);
}

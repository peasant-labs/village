import { NEXT_PARAM, returnAddressFor, safeNextPath } from "@/lib/signIn";

/**
 * The peasant marketing site: where somebody without a session is sent, and
 * where signing in starts.
 *
 * The hosted app's one signed-out view is a public transcript (its page decides
 * that on the server); every other page sends a visitor without a session here.
 * The product is introduced
 * on the marketing site, and its log-in popup holds the sign-in the app's own
 * page used to: the GitHub button (which comes back through `/auth/start`) and
 * the terminal command. An operator can point it elsewhere (a staging site, a
 * mirror) with `NEXT_PUBLIC_MARKETING_URL`, read at build time like every other
 * public value.
 */
export const DEFAULT_MARKETING_URL = "https://peasantlabs.org";

export const MARKETING_URL: string =
  process.env.NEXT_PUBLIC_MARKETING_URL?.trim() || DEFAULT_MARKETING_URL;

/** The marketing site's query flag that opens its log-in popup. */
export const LOGIN_FLAG = "login";

/**
 * The marketing site with its log-in popup open, carrying where to come back
 * to once signed in when that is somewhere other than home:
 * `https://peasantlabs.org/?login&next=%2Fcollectives%2Fabc`.
 */
export function marketingLoginURL(next?: string | null): string {
  const base = `${MARKETING_URL.replace(/\/+$/, "")}/?${LOGIN_FLAG}`;
  const safe = safeNextPath(next);
  return safe && safe !== "/" ? `${base}&${NEXT_PARAM}=${encodeURIComponent(safe)}` : base;
}

/** Where a signed-out visit to a page of this app is sent: the log-in popup, coming back to that page. */
export function signedOutDestination(pathname: string, search: string): string {
  return marketingLoginURL(returnAddressFor(pathname, search));
}

/**
 * Leaves the app for the marketing site's log-in popup, coming back to `next`
 * once signed in, and replacing this entry in the history.
 */
export function leaveForSignIn(next: string | null): void {
  window.location.replace(marketingLoginURL(next));
}

/** Leaves the app for the marketing site itself, as somebody who just signed out. */
export function leaveForMarketingSite(): void {
  window.location.replace(MARKETING_URL);
}

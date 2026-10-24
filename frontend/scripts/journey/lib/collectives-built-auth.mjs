/* The signed-in session the built collectives proof carries on BOTH sides of a
 * page load: the browser context that drives the mounted journey, and the
 * server-side readiness read that proves the served HTML is this build's own
 * page.
 *
 * The cookie's NAME is the app's own constant (`src/lib/authCookieName.ts`),
 * read from the production module rather than copied here. The proxy and the
 * API both key on that constant, so renaming it breaks this proof loudly
 * instead of letting it pass on a private fork of the name.
 *
 * The session VALUE is the one the composed journey fixtures answer for (the
 * signed-in viewer `alice-dev`); the browser and the readiness read share this
 * single declaration so they can never drift into two different sessions.
 */
import { AUTH_COOKIE } from '../../../src/lib/authCookieName.ts'

/** The session token every built collectives fixture answers as signed in. */
export const BUILT_SESSION_TOKEN = 'demo-token'

/**
 * The cookie a Playwright browser context carries so every page of the built
 * app is served signed in, exactly as a person's browser would be.
 * @param {string} appURL the built app's origin, e.g. `http://127.0.0.1:3000`
 */
export function signedInCookies(appURL) {
  if (typeof appURL !== 'string' || appURL.length === 0) {
    throw new Error(
      'the built collectives proof needs the built app origin to scope its session cookie; ' +
      'COLLECTIVES_BUILT_URL was empty, so run it through `pnpm run journey:collectives-built`, which owns the server and exports the origin',
    )
  }
  return [{ name: AUTH_COOKIE, value: BUILT_SESSION_TOKEN, url: appURL }]
}

/**
 * The same session as headers for ONE server-side page load.
 *
 * `sec-fetch-dest: document` is what a browser sends for a navigation, and it
 * is the header `src/proxy.ts` reads to tell a page load from a fetch the page
 * makes. Sending it keeps this read on the production page-load path instead
 * of relying on the proxy's "no fetch metadata is a page load" fallback.
 */
export function signedInPageLoadHeaders() {
  return {
    cookie: `${AUTH_COOKIE}=${encodeURIComponent(BUILT_SESSION_TOKEN)}`,
    'sec-fetch-dest': 'document',
  }
}

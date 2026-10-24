import "server-only";

/**
 * The two server-only settings the link-preview routes need. Either one
 * missing disables the preview data path: callers get `null` and serve the
 * generic card instead of throwing.
 *
 * - `VILLAGE_INTERNAL_API_URL`: the API base (including `/api/v1`) that the
 *   Next server can reach. Not the browser-facing `NEXT_PUBLIC_API_URL`.
 * - `VILLAGE_PUBLIC_ORIGIN`: the public origin of this site, used to build
 *   absolute preview URLs.
 */
export function shareCardEnv(): { apiBase: string; origin: URL } | null {
  const rawBase = process.env.VILLAGE_INTERNAL_API_URL?.trim();
  const origin = publicOrigin();
  if (!rawBase || !origin) return null;
  try {
    if (!/^https?:$/.test(new URL(rawBase).protocol)) return null;
    return { apiBase: rawBase.replace(/\/+$/, ""), origin };
  } catch {
    return null;
  }
}

/**
 * The site's public origin (scheme, host and port) from `VILLAGE_PUBLIC_ORIGIN`
 * alone, or `null` when it is unset or not an http(s) URL. It needs no API
 * URL: it only makes preview URLs absolute.
 */
export function publicOrigin(): URL | null {
  const raw = process.env.VILLAGE_PUBLIC_ORIGIN?.trim();
  if (!raw) return null;
  try {
    const url = new URL(raw);
    if (!/^https?:$/.test(url.protocol)) return null;
    return new URL(url.origin);
  } catch {
    return null;
  }
}

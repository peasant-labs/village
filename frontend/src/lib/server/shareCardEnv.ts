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
  const rawOrigin = process.env.VILLAGE_PUBLIC_ORIGIN?.trim();
  if (!rawBase || !rawOrigin) return null;
  try {
    const base = new URL(rawBase);
    const origin = new URL(rawOrigin);
    if (!/^https?:$/.test(base.protocol) || !/^https?:$/.test(origin.protocol)) return null;
    return { apiBase: rawBase.replace(/\/+$/, ""), origin: new URL(origin.origin) };
  } catch {
    return null;
  }
}

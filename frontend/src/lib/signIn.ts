import { API_URL_BASE } from "@/lib/api";

/**
 * The providers village offers at sign-in, in the order fairtrade's
 * `SignInProviders` renders them (`[0]` is the primary button, the rest go
 * behind its chevron).
 *
 * GitHub only. The other providers are hidden, not deleted: their server
 * routes stay mounted, and a provider an operator has not configured already
 * answers 503. The label is lowercase chrome like every other control;
 * fairtrade's split button lowercases it too.
 */
export const SIGN_IN_PROVIDERS: { id: string; label: string }[] = [
  { id: "github", label: "github" },
];

/** The sign-in page. Every "sign in" link in the app leads here. */
export const LOGIN_PATH = "/login";

/** The query parameter that carries where to go once signed in. */
export const NEXT_PARAM = "next";

/**
 * Where the browser returns after the OAuth round trip, kept for the length of
 * the tab. The API's callback always lands on `/auth/callback`, which carries
 * no return address of its own, so the address is remembered here on the way
 * out and read back there.
 */
const NEXT_STORAGE_KEY = "peasant-sign-in-next";

/**
 * A return address the app will follow, or null.
 *
 * Only a path on this site is followed: it starts with one `/` (never `//`,
 * which a browser reads as another host, and never a backslash, which some
 * read the same way), and it is not a step of the sign-in itself, which would
 * send a person round in a loop.
 */
export function safeNextPath(raw: string | null | undefined): string | null {
  if (!raw) return null;
  const path = raw.trim();
  if (!path.startsWith("/") || path.startsWith("//") || path.includes("\\")) return null;
  if (/[\u0000-\u001f]/.test(path)) return null;
  const bare = path.split(/[?#]/)[0];
  if (bare === LOGIN_PATH || bare.startsWith("/auth/")) return null;
  return path;
}

/** The sign-in page's address, with where to come back to when there is one. */
export function loginHref(next?: string | null): string {
  const safe = safeNextPath(next);
  return safe && safe !== "/" ? `${LOGIN_PATH}?${NEXT_PARAM}=${encodeURIComponent(safe)}` : LOGIN_PATH;
}

/** Remembers where to return after the OAuth round trip. Best effort. */
export function rememberNextPath(next: string | null | undefined): void {
  const safe = safeNextPath(next);
  try {
    if (safe) globalThis.sessionStorage?.setItem(NEXT_STORAGE_KEY, safe);
    else globalThis.sessionStorage?.removeItem(NEXT_STORAGE_KEY);
  } catch {
    // Without storage the person still signs in; they land on home instead.
  }
}

/** Reads and forgets the remembered return address. */
export function takeNextPath(): string | null {
  try {
    const stored = globalThis.sessionStorage?.getItem(NEXT_STORAGE_KEY) ?? null;
    globalThis.sessionStorage?.removeItem(NEXT_STORAGE_KEY);
    return safeNextPath(stored);
  } catch {
    return null;
  }
}

/** Leaves the app for the provider's OAuth start route. */
export function startSignIn(providerId: string): void {
  window.location.href = `${API_URL_BASE}/auth/${providerId}`;
}

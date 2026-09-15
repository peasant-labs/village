const API_URL = process.env.NEXT_PUBLIC_API_URL || "https://localhost/api/v1";

const AUTH_COOKIE = "peasant_token";

/**
 * Error thrown by {@link api} for any non-2xx response. Carries the HTTP
 * `status` so callers can branch on it — notably the config-gated GitHub App
 * endpoints, which return 501 when no App is registered on the server. That is
 * a clean "not configured" state, not a failure, so hooks check `status === 501`
 * rather than surfacing an error toast.
 */
export class ApiError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

/** True when an unknown thrown value is an {@link ApiError} with the given status. */
export function isApiErrorStatus(err: unknown, status: number): boolean {
  return err instanceof ApiError && err.status === status;
}

function getToken(): string | null {
  if (typeof document === "undefined") return null;
  const match = document.cookie.match(
    new RegExp(`(?:^|; )${AUTH_COOKIE}=([^;]*)`)
  );
  return match ? decodeURIComponent(match[1]) : null;
}

export async function api<T>(
  path: string,
  options?: RequestInit
): Promise<T> {
  const token = getToken();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(options?.headers as Record<string, string>),
  };
  if (token) {
    headers["Authorization"] = `Bearer ${token}`;
  }

  const res = await fetch(`${API_URL}${path}`, {
    ...options,
    headers,
  });

  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.error || `API error: ${res.status}`);
  }

  return res.json();
}

export function getAuthHeaders(): Record<string, string> {
  const token = getToken();
  return token ? { Authorization: `Bearer ${token}` } : {};
}

const AUTH_COOKIE_MAX_AGE_SECONDS = 7 * 24 * 60 * 60;

/**
 * The auth cookie's `domain` attribute, or undefined for a host-only cookie.
 *
 * Host-only is the default and keeps the token off the API's own host. But the
 * cookie is also the only credential the API can read when the browser reaches
 * it by top-level navigation — the GitHub App install handshake — where no
 * `Authorization` header can be sent. So when the API is served from this host
 * or a subdomain of it (production serves the API at api.village.peasantlabs.org
 * under the app's village.peasantlabs.org), scope the cookie to the shared
 * parent. Anything else stays host-only.
 */
export function authCookieDomainFor(
  host: string,
  apiUrl: string
): string | undefined {
  let apiHost: string;
  try {
    apiHost = new URL(apiUrl).hostname;
  } catch {
    return undefined;
  }
  if (!host || apiHost === host || !apiHost.endsWith(`.${host}`)) {
    return undefined;
  }
  return host;
}

function authCookieAttributes(): string {
  let domain: string | undefined;
  if (typeof window !== "undefined") {
    domain = authCookieDomainFor(window.location.hostname, API_URL);
  }
  return `path=/; secure; samesite=lax${domain ? `; domain=${domain}` : ""}`;
}

export function setAuthTokenCookie(token: string): void {
  if (typeof document === "undefined") return;
  document.cookie = `${AUTH_COOKIE}=${encodeURIComponent(token)}; max-age=${AUTH_COOKIE_MAX_AGE_SECONDS}; ${authCookieAttributes()}`;
}

export function clearAuthTokenCookie(): void {
  if (typeof document === "undefined") return;
  document.cookie = `${AUTH_COOKIE}=; max-age=0; ${authCookieAttributes()}`;
}

export const API_URL_BASE = API_URL;

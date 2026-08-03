const API_URL = process.env.NEXT_PUBLIC_API_URL || "https://localhost/api/v1";

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
  const match = document.cookie.match(/(?:^|; )peasant_token=([^;]*)/);
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

export const API_URL_BASE = API_URL;

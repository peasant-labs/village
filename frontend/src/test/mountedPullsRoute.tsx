import { StrictMode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import PullsRoute from "@/app/pulls/page";
import type { PullsIndexCase } from "@/test/pullsIndexFixtures";
import type { User } from "@/lib/types";

/**
 * Support for tests that mount the REAL `/pulls` route inside the real
 * `AuthProvider`, with `fetch` stubbed to answer as the server does for one
 * fixture case. Every request is recorded by path, and an unexpected one
 * throws, so a new call on the page shows up as a named failure.
 */

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

const VIEWER: User = {
  id: "user-alice",
  github_id: 1,
  github_username: "alice-dev",
  display_name: "alice-dev",
  avatar_url: null,
  created_at: "2026-01-01T00:00:00.000Z",
  updated_at: "2026-01-01T00:00:00.000Z",
  is_discoverable: true,
  username_chosen: true,
  provider_username: "alice-dev",
  provider: "github",
};

export function installPullsRouteREST(c: PullsIndexCase): string[] {
  const reads: string[] = [];
  let listReads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const path = url.slice(url.indexOf("/api/v1") + "/api/v1".length);
      const method = (init?.method ?? "GET").toUpperCase();
      reads.push(`${method} ${path}`);
      if (method === "GET" && path === "/auth/me") {
        return c.signedIn ? json(VIEWER) : json({ error: "Authentication required" }, 401);
      }
      if (method === "GET" && path === "/users/me/prompt-requests") {
        listReads += 1;
        const fails = c.requestFailure === "always" || (c.requestFailure === "first" && listReads === 1);
        if (fails) return json({ error: "Could not read your prompt requests" }, 500);
        return json({ requests: c.requests });
      }
      throw new Error(`mounted pulls route fixture received an unexpected ${method} ${url}`);
    }),
  );
  return reads;
}

/** Renders the real route registered at `/pulls`, under StrictMode as `next dev` does. */
export async function renderPullsRoute(): Promise<void> {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  await act(async () => {
    render(
      <StrictMode>
        <QueryClientProvider client={client}>
          <AuthProvider>
            <PullsRoute />
          </AuthProvider>
        </QueryClientProvider>
      </StrictMode>,
    );
  });
}

/** Shared teardown; call once at module scope in each mounted-pulls test file. */
export function installPullsRouteTeardown(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });
}

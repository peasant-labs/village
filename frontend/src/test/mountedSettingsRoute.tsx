import { StrictMode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import SettingsRoute from "@/app/settings/page";
import Navbar from "@/components/layout/Navbar";
import { SETTINGS_VIEWER, type SettingsCase } from "@/test/accountSettingsFixtures";
import type { User } from "@/lib/types";

/**
 * Support for tests that mount the REAL `/settings` route inside the real
 * `AuthProvider`, with `fetch` stubbed to answer as village does for one
 * fixture case.
 *
 * Every write the page sends is RECORDED as `METHOD path body`, because what a
 * settings page sent is the observable half of a setting; a test that only
 * looked at the switch could not tell a saved change from a drawn one. An
 * unexpected request throws, so a new call on the page shows up as a named
 * failure rather than a silent hang.
 */

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function userFor(handle: string, discoverable: boolean): User {
  return {
    id: "user-alice",
    github_id: 1,
    github_username: handle,
    display_name: handle,
    avatar_url: null,
    created_at: "2026-01-01T00:00:00.000Z",
    updated_at: "2026-01-01T00:00:00.000Z",
    is_discoverable: discoverable,
    username_chosen: true,
    provider_username: SETTINGS_VIEWER,
    provider: "github",
  };
}

export interface MountedSettingsBackend {
  /** Every non-GET request, as `METHOD path` plus its JSON body when it has one. */
  writes: string[];
  promptReads: number;
}

export function installSettingsRouteREST(c: SettingsCase, prompt?: { initial: boolean; read: "ok" | "failed"; write: "ok" | "failed" }): MountedSettingsBackend {
  const writes: string[] = [];
  let handle = SETTINGS_VIEWER;
  let discoverable = c.discoverable;
  const revoked = new Set(c.keys.filter((key) => key.revoked).map((key) => key.id));
  let revocations = 0;
  let automatic = prompt?.initial ?? false;
  const backend: MountedSettingsBackend = { writes, promptReads: 0 };

  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const path = url.slice(url.indexOf("/api/v1") + "/api/v1".length);
      const method = (init?.method ?? "GET").toUpperCase();
      if (method !== "GET") {
        const body = typeof init?.body === "string" ? ` ${init.body}` : "";
        writes.push(`${method} ${path}${body}`);
      }

      if (method === "GET" && path === "/auth/me") return json(userFor(handle, discoverable));
      if (method === "GET" && path === "/users/me/settings") {
        backend.promptReads += 1;
        if (prompt?.read === "failed" && backend.promptReads === 1) return json({ error: "the settings service is unavailable" }, 500);
        return json({ auto_attach_pull_requests: automatic, preview_before_attach: false });
      }
      if (method === "PATCH" && path === "/users/me/settings") {
        if (prompt?.write === "failed") return json({ error: "the settings service is unavailable" }, 500);
        automatic = JSON.parse(String(init?.body)).auto_attach_pull_requests;
        return json({ auto_attach_pull_requests: automatic, preview_before_attach: false });
      }
      if (method === "GET" && path === "/auth/api-keys") {
        return json(
          c.keys.map((key) => ({
            id: key.id,
            key_prefix: `pk_${key.id}`,
            label: key.label,
            created_at: "2026-09-01T12:00:00Z",
            last_used: key.lastUsed,
            revoked_at: revoked.has(key.id) ? "2026-09-30T11:00:00Z" : null,
          })),
        );
      }
      if (method === "PATCH" && path === "/auth/me/username") {
        if (c.server === "conflict") return json({ error: "That username is already taken" }, 409);
        if (c.server === "failure") return json({ error: "the account service is unavailable" }, 500);
        handle = JSON.parse(String(init?.body)).username;
        return json(userFor(handle, discoverable));
      }
      if (method === "PATCH" && path === "/auth/me/settings") {
        if (c.server === "failure") return json({ error: "the settings service is unavailable" }, 500);
        discoverable = JSON.parse(String(init?.body)).is_discoverable;
        return json(userFor(handle, discoverable));
      }
      if (method === "DELETE" && path.startsWith("/auth/api-keys/")) {
        revocations += 1;
        if (c.failOnKey != null && revocations === c.failOnKey) {
          return json({ error: "the key service is unavailable" }, 500);
        }
        revoked.add(decodeURIComponent(path.slice("/auth/api-keys/".length)));
        return json({ status: "revoked" });
      }
      if (method === "DELETE" && path === "/auth/me") return json({ status: "deleted" });
      if (method === "POST" && path === "/auth/logout") return json({ status: "logged out" });
      throw new Error(`mounted settings route fixture received an unexpected ${method} ${url}`);
    }),
  );
  return backend;
}

/**
 * Mounted under React's StrictMode, as `next dev` mounts every page: a row
 * that only settles its writes when mounted once would pass a plain render and
 * still sit at "saving" forever in development.
 */
async function renderWithProviders(element: React.ReactElement): Promise<void> {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  await act(async () => {
    render(
      <StrictMode>
        <QueryClientProvider client={client}>
          <AuthProvider>{element}</AuthProvider>
        </QueryClientProvider>
      </StrictMode>,
    );
  });
}

/** Renders the real route registered at `/settings`. */
export function renderSettingsRoute(): Promise<void> {
  return renderWithProviders(<SettingsRoute />);
}

/** Renders the real header, whose account menu leads to the settings route. */
export function renderHeader(): Promise<void> {
  return renderWithProviders(<Navbar />);
}

/** Shared teardown; call once at module scope in each mounted-settings test file. */
export function installSettingsRouteTeardown(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.useRealTimers();
    globalThis.localStorage?.clear();
  });
}

import { StrictMode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import SettingsRoute from "@/app/settings/page";
import AppFrame from "@/components/shell/AppFrame";
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

function userFor(handle: string, discoverable: boolean, id = "user-alice"): User {
  return {
    id,
    github_id: 1,
    github_username: handle,
    display_name: handle,
    avatar_url: null,
    created_at: "2026-01-01T00:00:00.000Z",
    updated_at: "2026-01-01T00:00:00.000Z",
    is_discoverable: discoverable,
    username_chosen: true,
    provider_username: id === "user-alice" ? SETTINGS_VIEWER : handle,
    provider: "github",
  };
}

export interface MountedSettingsBackend {
  /** Every non-GET request, as `METHOD path` plus its JSON body when it has one. */
  writes: string[];
  promptReads: number;
  releaseProfile(): void;
  releaseLogout(): void;
  switchAccount(id: string, handle: string, discoverable: boolean): void;
}

/**
 * The GitHub organizations the settings page reads and links.
 *
 * `items` is the caller's whole list, linked or not, because that is what
 * `GET /auth/orgs` answers. A `read` or `write` of `"failed"` models the service
 * being unavailable, which is the only way to reach the page's error states.
 */
export interface MountedSettingsOrgs {
  items: Array<{ org_login: string; visible: boolean; avatar_url?: string | null }>;
  read?: "ok" | "failed";
  write?: "ok" | "failed";
}

/**
 * The caller's notification email as the server holds it. `sending` is how
 * setting an address answers: `ok`, `off` (this Village sends no email, 501)
 * or `limited` (too many verification emails, 429). `code` is the one
 * verification code the server accepts.
 */
export interface MountedSettingsEmail {
  address: string | null;
  verified: boolean;
  invitations: boolean;
  sending: "ok" | "off" | "limited";
  code: string;
}

export function installSettingsRouteREST(c: SettingsCase, prompt?: { initial: boolean; read: "ok" | "failed"; write: "ok" | "failed" }, controls?: { deferProfile?: "handle" | "discoverable"; failLogout?: boolean; deferLogout?: boolean; signedOut?: boolean }, orgs: MountedSettingsOrgs = { items: [] }, email: MountedSettingsEmail = { address: null, verified: false, invitations: true, sending: "ok", code: "" }): MountedSettingsBackend {
  const writes: string[] = [];
  let accountID = "user-alice";
  let handle = SETTINGS_VIEWER;
  let discoverable = c.discoverable;
  const revoked = new Set(c.keys.filter((key) => key.revoked).map((key) => key.id));
  let revocations = 0;
  let automatic = prompt?.initial ?? false;
  const settingsAnswer = () => ({
    auto_attach_pull_requests: automatic,
    preview_before_attach: false,
    notification_email: email.address === null ? null : { address: email.address, verified: email.verified },
    email_invitations: email.invitations,
  });
  let orgReads = 0;
  let releaseProfile = () => {};
  const pendingProfile = controls?.deferProfile ? new Promise<void>((resolve) => { releaseProfile = resolve; }) : null;
  let releaseLogout = () => {};
  const pendingLogout = controls?.deferLogout ? new Promise<void>((resolve) => { releaseLogout = resolve; }) : null;
  const backend: MountedSettingsBackend = { writes, promptReads: 0, releaseProfile, releaseLogout, switchAccount(id, nextHandle, nextDiscoverable) { accountID = id; handle = nextHandle; discoverable = nextDiscoverable; } };
  let deferred = false;
  async function profileAnswer(kind: "handle" | "discoverable") {
    const answer = userFor(handle, discoverable, accountID);
    if (!deferred && controls?.deferProfile === kind) {
      deferred = true;
      await pendingProfile;
    }
    return json(answer);
  }

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

      if (method === "GET" && path === "/auth/me") {
        if (controls?.signedOut) return json({ error: "Authentication required" }, 401);
        return json(userFor(handle, discoverable, accountID));
      }
      if (method === "GET" && path === "/auth/orgs") {
        orgReads += 1;
        // Only the first read fails, so the retry control has an answer to
        // reach: a list that stayed broken could not be told from a retry that
        // did nothing.
        if (orgs.read === "failed" && orgReads === 1) {
          return json({ error: "the organization service is unavailable" }, 500);
        }
        return json(
          orgs.items.map((org) => ({
            org_id: 1,
            org_login: org.org_login,
            avatar_url: org.avatar_url ?? null,
            visible: org.visible,
            fetched_at: "2026-09-01T00:00:00.000Z",
          })),
        );
      }
      if (method === "PATCH" && path.startsWith("/auth/orgs/") && path.endsWith("/visibility")) {
        if (orgs.write === "failed") return json({ error: "the organization service is unavailable" }, 500);
        const login = decodeURIComponent(path.slice("/auth/orgs/".length, -"/visibility".length));
        const visible = Boolean(JSON.parse(String(init?.body)).visible);
        const item = orgs.items.find((org) => org.org_login === login);
        if (item) item.visible = visible;
        return json({ status: "updated", visible });
      }
      if (method === "GET" && path === "/users/me/settings") {
        backend.promptReads += 1;
        if (prompt?.read === "failed" && backend.promptReads === 1) return json({ error: "the settings service is unavailable" }, 500);
        return json(settingsAnswer());
      }
      if (method === "PATCH" && path === "/users/me/settings") {
        if (prompt?.write === "failed") return json({ error: "the settings service is unavailable" }, 500);
        const sent = JSON.parse(String(init?.body)) as { auto_attach_pull_requests?: boolean; email_invitations?: boolean };
        if (sent.auto_attach_pull_requests !== undefined) automatic = sent.auto_attach_pull_requests;
        if (sent.email_invitations !== undefined) email.invitations = sent.email_invitations;
        return json(settingsAnswer());
      }
      if (method === "PUT" && path === "/users/me/email") {
        if (email.sending === "off") return json({ error: "this village sends no email" }, 501);
        if (email.sending === "limited") return json({ error: "too many verification emails" }, 429);
        email.address = JSON.parse(String(init?.body)).address;
        email.verified = false;
        return json(settingsAnswer());
      }
      if (method === "POST" && path === "/users/me/email/verify") {
        if (email.address === null || JSON.parse(String(init?.body)).token !== email.code) {
          return json({ error: "the verification token is not valid" }, 400);
        }
        email.verified = true;
        return json(settingsAnswer());
      }
      if (method === "DELETE" && path === "/users/me/email") {
        email.address = null;
        email.verified = false;
        return json(settingsAnswer());
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
        return profileAnswer("handle");
      }
      if (method === "PATCH" && path === "/auth/me/settings") {
        if (c.server === "failure") return json({ error: "the settings service is unavailable" }, 500);
        discoverable = JSON.parse(String(init?.body)).is_discoverable;
        return profileAnswer("discoverable");
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
      if (method === "POST" && path === "/auth/logout") {
        await pendingLogout;
        return controls?.failLogout ? json({ error: "the account service is unavailable" }, 500) : json({ status: "logged out" });
      }
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
async function renderWithProviders(element: React.ReactElement): Promise<QueryClient> {
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
  return client;
}

/** Renders the real route registered at `/settings`. */
export function renderSettingsRoute(): Promise<QueryClient> {
  return renderWithProviders(<SettingsRoute />);
}

/** Renders the real app chrome, whose account menu leads to the settings route. */
export function renderHeader(): Promise<QueryClient> {
  return renderWithProviders(<AppFrame>{null}</AppFrame>);
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

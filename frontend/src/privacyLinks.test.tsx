import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LayoutShell } from "@/components/LayoutShell";
import PublishPage from "@/app/publish/page";
import { AuthProvider } from "@/providers/AuthProvider";
import type { User } from "@/lib/types";
import { loadPrivacyNoticeFixtures } from "@/test/privacyNoticeFixtures";

// The privacy notice is reachable from two production places: the persistent
// chrome every page renders through LayoutShell, and the publish page, where
// a person is about to grant a license. Both are mounted here for real, with
// the same GET /auth/me the app uses deciding who the visitor is.

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/publish",
}));

const fixtures = loadPrivacyNoticeFixtures();

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function userFixture(username: string): User {
  return {
    id: `user-${username}`,
    github_id: 1,
    github_username: username,
    display_name: username,
    avatar_url: null,
    created_at: "2026-01-01T00:00:00.000Z",
    updated_at: "2026-01-01T00:00:00.000Z",
    is_discoverable: true,
    username_chosen: true,
    provider_username: username,
  };
}

/** Stubs every request the shell and the publish page make; anything else throws. */
function installBackend(viewerUsername: string | null): void {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      const path = url.slice(url.indexOf("/api/v1") + "/api/v1".length);
      if (path === "/auth/me") {
        return viewerUsername == null
          ? json({ error: "not signed in" }, 401)
          : json(userFixture(viewerUsername));
      }
      if (path.startsWith("/transcripts")) {
        return json({ transcripts: [], total: 0, agent_total: 0, page: 1, limit: 5 });
      }
      throw new Error(`privacy link test received an unexpected request to ${url}`);
    }),
  );
}

async function mount(element: React.ReactElement): Promise<void> {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <AuthProvider>{element}</AuthProvider>
      </QueryClientProvider>,
    );
  });
}

describe("the privacy notice link", () => {
  for (const c of fixtures.linkCases) {
    it(`is in the chrome every page renders through LayoutShell (${c.name})`, async () => {
      installBackend(c.viewerUsername);
      await mount(
        <LayoutShell>
          <div data-testid="page-body" />
        </LayoutShell>,
      );
      const link = await screen.findByRole("link", { name: "privacy" });
      expect(link).toHaveAttribute("href", "/privacy");
      expect(screen.getByTestId("page-body")).toBeInTheDocument();
    });

    it(`is on the publish page (${c.name})`, async () => {
      installBackend(c.viewerUsername);
      await mount(<PublishPage />);
      const link = await screen.findByRole("link", { name: "privacy notice" });
      expect(link).toHaveAttribute("href", "/privacy");
    });
  }
});

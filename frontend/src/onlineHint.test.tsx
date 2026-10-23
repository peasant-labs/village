import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import { onlineHintCookie, sharedCookieDomain, writeOnlineHint } from "@/lib/onlineHint";
import { loadOnlineHintFixtures } from "@/test/onlineHintFixtures";

// The hint cookie the marketing site reads, driven by
// `src/testdata/online-hint.yaml`. The domain and cookie rules are the
// production functions; the provider cases mount the real AuthProvider with
// `GET /auth/me` answered, and record what it asks to write (a jsdom page on
// localhost cannot hold a cookie for another domain).
vi.mock("@/lib/onlineHint", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/onlineHint")>();
  return { ...actual, writeOnlineHint: vi.fn() };
});

const fixtures = loadOnlineHintFixtures();

beforeEach(() => vi.mocked(writeOnlineHint).mockClear());
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("the domain the hint is shared on", () => {
  for (const c of fixtures.domainCases) {
    it(c.name, () => expect(sharedCookieDomain(c.appHost, c.marketingUrl), c.why).toBe(c.expect));
  }
});

describe("the hint cookie", () => {
  for (const c of fixtures.cookieCases) {
    it(c.name, () => expect(onlineHintCookie(c.handle, c.domain), c.why).toBe(c.expect));
  }
});

describe("what the app tells the marketing site once the session is read", () => {
  for (const c of fixtures.providerCases) {
    it(c.name, async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () =>
          c.meStatus === 200
            ? new Response(JSON.stringify({ id: "u1", github_username: "alice-dev", username_chosen: true }), { status: 200 })
            : new Response(JSON.stringify({ error: "no" }), { status: c.meStatus }),
        ),
      );
      const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      await act(async () => {
        render(
          <QueryClientProvider client={client}>
            <AuthProvider>
              <span />
            </AuthProvider>
          </QueryClientProvider>,
        );
      });
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 20));
      });
      expect(vi.mocked(writeOnlineHint).mock.calls.map(([handle]) => handle), c.why).toEqual(c.expectWrites);
    });
  }
});

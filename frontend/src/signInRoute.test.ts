import { NextRequest } from "next/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { proxy } from "@/proxy";
import { readPublicTranscript } from "@/lib/server/loadPublicTranscript";
import { decideTranscriptPage, searchString } from "@/lib/server/transcriptPageAccess";
import { loginHref, rememberNextPath, safeNextPath, takeNextPath } from "@/lib/signIn";
import { loadSignInRouteFixtures, type TranscriptPageCase } from "@/test/signInRouteFixtures";

// The production proxy and the production return-address rules, driven by
// `src/testdata/sign-in-route.yaml`. The proxy is called with a real
// NextRequest, so the cookie it reads is parsed the way Next parses it.

const fixtures = loadSignInRouteFixtures();

describe("the proxy: who is sent to the marketing site's log-in popup", () => {
  for (const c of fixtures.proxyCases) {
    it(c.name, () => {
      const headers = new Headers();
      if (c.cookie !== null) headers.set("cookie", `peasant_token=${c.cookie}`);
      if (c.fetchDest !== null) headers.set("sec-fetch-dest", c.fetchDest);
      const response = proxy(new NextRequest(new URL(c.path, "https://village.example"), { headers }));
      if (c.expect.status === null) {
        // Served: Next's pass-through answer carries no redirect.
        expect(response.headers.get("location"), c.why).toBeNull();
        expect(response.headers.get("x-middleware-next"), c.why).toBe("1");
      } else {
        expect(response.status, c.why).toBe(c.expect.status);
        expect(response.headers.get("location"), c.why).toBe(c.expect.location);
      }
    });
  }
});

/** The API as the page's anonymous read finds it, counting every request. */
function apiAnswering(api: TranscriptPageCase["api"]) {
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  const fetchFn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(input), init });
    const transcript = (visibility: string) =>
      new Response(JSON.stringify({ transcript: { id: "t1", title: "a transcript", visibility } }), { status: 200 });
    switch (api) {
      case "public":
        return transcript("public");
      case "non-public-body":
        return transcript("shared");
      case "error":
        return new Response("{}", { status: 500 });
      case "unreachable":
        throw new TypeError("fetch failed");
      default:
        return new Response("{}", { status: 404 });
    }
  }) as unknown as typeof fetch;
  return { fetchFn, calls };
}

describe("the transcript page: who it serves without a session", () => {
  beforeEach(() => {
    vi.stubEnv("VILLAGE_INTERNAL_API_URL", "http://backend:8080/api/v1");
    vi.stubEnv("VILLAGE_PUBLIC_ORIGIN", "https://village.example");
  });
  afterEach(() => vi.unstubAllEnvs());

  for (const c of fixtures.transcriptPageCases) {
    it(c.name, async () => {
      if (c.api === "no-config") vi.stubEnv("VILLAGE_INTERNAL_API_URL", "");
      const { fetchFn, calls } = apiAnswering(c.api);
      const decision = await decideTranscriptPage(
        { id: c.id, search: searchString(c.searchParams), hasSession: c.session, fetchDest: c.fetchDest },
        (id) => readPublicTranscript(id, { fetch: fetchFn }),
      );
      expect(decision, c.why).toEqual(
        c.expect.serve
          ? { serve: true, openSignedOut: c.expect.openSignedOut }
          : { serve: false, destination: c.expect.location },
      );
      expect(calls, c.why).toHaveLength(c.reads);
      for (const call of calls) {
        // The read is anonymous: it carries nothing of the visitor's.
        expect(call.url).toBe(`http://backend:8080/api/v1/transcripts/${encodeURIComponent(c.id)}`);
        expect(call.init?.credentials).toBe("omit");
        expect(call.init?.cache).toBe("no-store");
        const headers = new Headers(call.init?.headers);
        expect(headers.has("cookie")).toBe(false);
        expect(headers.has("authorization")).toBe(false);
      }
    });
  }

  it("answers every transcript it will not open in exactly the same way", async () => {
    const refused = fixtures.transcriptPageCases.filter(
      (c) => !c.expect.serve && c.id === "7f1d2c3b" && Object.keys(c.searchParams).length === 0,
    );
    expect(new Set(refused.map((c) => c.expect.location)).size).toBe(1);
    expect(new Set(refused.map((c) => c.api))).toEqual(
      new Set(["not-found", "non-public-body", "error", "unreachable", "no-config"]),
    );
  });
});

describe("the return address a sign-in follows", () => {
  beforeEach(() => sessionStorage.clear());

  for (const c of fixtures.nextCases) {
    it(c.name, () => {
      expect(safeNextPath(c.next), c.why).toBe(c.expect);
      // The same rule holds across the OAuth round trip: what /login remembers
      // is what /auth/callback follows, and it is read once.
      rememberNextPath(c.next);
      expect(takeNextPath(), c.why).toBe(c.expect);
      expect(takeNextPath(), "the address is forgotten once it is read").toBeNull();
      // And the sign-in link a page builds carries only an address it would follow.
      const href = loginHref(c.next);
      expect(href, c.why).toBe(c.expect && c.expect !== "/" ? `/login?next=${encodeURIComponent(c.expect)}` : "/login");
    });
  }
});

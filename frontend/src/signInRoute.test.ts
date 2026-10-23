import { NextRequest } from "next/server";
import { beforeEach, describe, expect, it } from "vitest";
import { proxy } from "@/proxy";
import { loginHref, rememberNextPath, safeNextPath, takeNextPath } from "@/lib/signIn";
import { loadSignInRouteFixtures } from "@/test/signInRouteFixtures";

// The production proxy and the production return-address rules, driven by
// `src/testdata/sign-in-route.yaml`. The proxy is called with a real
// NextRequest, so the cookie it reads is parsed the way Next parses it.

const fixtures = loadSignInRouteFixtures();

describe("the root proxy: who is sent to the marketing site", () => {
  for (const c of fixtures.proxyCases) {
    it(c.name, () => {
      const headers = new Headers();
      if (c.cookie !== null) headers.set("cookie", `peasant_token=${c.cookie}`);
      if (c.fetchDest !== null) headers.set("sec-fetch-dest", c.fetchDest);
      const response = proxy(new NextRequest("https://village.example/", { headers }));
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

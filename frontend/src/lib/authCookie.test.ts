import { describe, expect, it } from "vitest";
import { authCookieDomainFor } from "@/lib/api";

/**
 * The install handshake is the one flow where the API is reached by top-level
 * navigation (the Connect GitHub button, then GitHub's post-install redirect),
 * so no `Authorization` header can be sent and the auth cookie is the only
 * credential the API can read. The cookie is host-only by default, which keeps
 * it off the API's host; these cases pin when it may be widened, and — more
 * importantly — when it may not.
 */
describe("the auth cookie is scoped to the API's host only when the API is under ours", () => {
  it("stays host-only when the API is this same host", () => {
    expect(
      authCookieDomainFor("village.peasantlabs.org", "https://village.peasantlabs.org/api/v1"),
    ).toBeUndefined();
  });

  it("scopes to the shared parent for the production shape, where the API is our subdomain", () => {
    // village.peasantlabs.org serving api.village.peasantlabs.org: a top-level
    // navigation to the API carries a cookie scoped to the parent.
    expect(
      authCookieDomainFor("village.peasantlabs.org", "https://api.village.peasantlabs.org/api/v1"),
    ).toBe("village.peasantlabs.org");
  });

  it("scopes to the shared parent however deep the API host is", () => {
    expect(
      authCookieDomainFor("village.peasantlabs.org", "https://api.staging.village.peasantlabs.org/api/v1"),
    ).toBe("village.peasantlabs.org");
  });

  it("stays host-only for an unrelated API host, so the token is not sent to a domain we do not share", () => {
    expect(
      authCookieDomainFor("village.peasantlabs.org", "https://api.example.com/api/v1"),
    ).toBeUndefined();
  });

  it("does not mistake a lookalike suffix for a shared parent", () => {
    // "notvillage.peasantlabs.org" ends with "village.peasantlabs.org" as a
    // string but is a different site; the boundary must be the leading dot.
    expect(
      authCookieDomainFor("village.peasantlabs.org", "https://notvillage.peasantlabs.org/api/v1"),
    ).toBeUndefined();
  });

  it("stays host-only when the API URL is relative, which is the same origin anyway", () => {
    expect(authCookieDomainFor("village.peasantlabs.org", "/api/v1")).toBeUndefined();
  });

  it("stays host-only when there is no host to scope from", () => {
    expect(authCookieDomainFor("", "https://api.village.peasantlabs.org/api/v1")).toBeUndefined();
  });
});

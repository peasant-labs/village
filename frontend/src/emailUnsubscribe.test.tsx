import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import UnsubscribePage from "@/app/email/unsubscribe/UnsubscribePage";
import { assertCaseNames, readFixture, textOf } from "@/test/mountedCollectivesSurfaces";

/**
 * The one-click unsubscribe page, driven by `src/testdata/email-unsubscribe.yaml`:
 * nothing is sent on arrival, pressing the button sends the token, and a token
 * the API refuses, or no token at all, points the person to settings.
 */

interface UnsubscribeCase {
  name: string;
  why: string;
  token: string | null;
  answer: number;
  press: boolean;
  expect: { requests: string[]; shows: string; button: boolean };
}

const cases = readFixture("email-unsubscribe.yaml").cases as UnsubscribeCase[];
assertCaseNames("email-unsubscribe.yaml cases", cases.map((c) => c.name), [
  "arriving-sends-nothing",
  "pressing-stops-invitation-emails",
  "a-forged-or-expired-token-is-refused",
  "a-link-without-a-token-is-invalid",
]);

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("the unsubscribe page", () => {
  it.each(cases.map((c) => [c.name, c] as const))("%s", async (_name, c) => {
    const requests: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input), "https://village.test");
        requests.push(`${(init?.method ?? "GET").toUpperCase()} ${url.pathname.replace(/^\/api\/v1/, "")}${url.search}`);
        const body = c.answer === 200 ? { status: "unsubscribed" } : { error: "the token is not valid" };
        return new Response(JSON.stringify(body), { status: c.answer, headers: { "content-type": "application/json" } });
      }),
    );
    const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
    await act(async () => {
      render(
        <QueryClientProvider client={client}>
          <UnsubscribePage token={c.token} />
        </QueryClientProvider>,
      );
    });
    if (c.press) {
      await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "stop invitation emails" }));
      });
    }
    await waitFor(() => expect(textOf(screen.getByTestId("unsubscribe-page")), c.why).toContain(c.expect.shows));
    expect(requests).toEqual(c.expect.requests);
    expect(screen.queryByRole("button", { name: "stop invitation emails" }) !== null).toBe(c.expect.button);
  });
});

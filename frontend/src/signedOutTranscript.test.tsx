import { Suspense } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import AppFrame from "@/components/shell/AppFrame";
import { useDeclareSignedOutReadable } from "@/components/shell/signedOutDeclaration";
import TranscriptDetailPage from "@/app/transcripts/[id]/TranscriptDetailPage";
import { AuthProvider } from "@/providers/AuthProvider";
import { MARKETING_URL, leaveForSignIn, reloadPage } from "@/lib/marketing";
import { hasAuthTokenCookie } from "@/lib/api";
import { installHomeRouteREST, renderHeaderAt } from "@/test/mountedHomeRoute";
import { installMountedRouteTeardown, installRESTFixture } from "@/test/mountedProductionRoute";
import { resetNextNavigation } from "@/test/nextNavigationMock";
import { loadSignedOutTranscriptFixtures, type FrameCase } from "@/test/signedOutTranscriptFixtures";

// A transcript page read by somebody who is not signed in: the REAL app frame
// (`AppFrame`) inside the real `AuthProvider`, with the session answered by
// `GET /auth/me`. Leaving for the marketing site is the one boundary stubbed
// (`leaveForSignIn`), because it navigates the document away.

const fx = loadSignedOutTranscriptFixtures();
const TRANSCRIPT_ID = "7c0a17e2-5b2e-4c1d-9a8f-2d6e7b40e21a";
const TITLE = "Fix flaky ingest test";

installMountedRouteTeardown();

beforeEach(() => {
  vi.mocked(leaveForSignIn).mockClear();
  vi.mocked(reloadPage).mockClear();
  resetNextNavigation();
});

afterEach(() => {
  for (const path of ["/", "/transcripts"]) document.cookie = `peasant_token=; max-age=0; path=${path}`;
});

function Declares({ readable }: { readable: boolean }) {
  useDeclareSignedOutReadable(readable);
  return <p>the page</p>;
}

function standIn(c: FrameCase) {
  if (c.declares === "nothing") return <p>the page</p>;
  return <Declares readable={c.declares === "public"} />;
}

describe("the app frame on a transcript page, signed out", () => {
  for (const c of fx.frameCases) {
    it(c.name, async () => {
      installHomeRouteREST({ viewerUsername: c.signedIn ? "alice-dev" : null, transcripts: [], collectives: [] });
      // A stale cookie is one the session read refuses (no viewer: 401). One
      // scoped to a narrower path is out of reach of the app's own clearing.
      if (c.cookie === "stale") document.cookie = "peasant_token=stale; path=/";
      if (c.cookie === "stale-uncleared") document.cookie = "peasant_token=stale; path=/transcripts";
      await renderHeaderAt(c.pathname, standIn(c));
      await waitFor(() => {
        if (c.expectChrome === "sections") expect(document.querySelector(".asb-foot .asb-account")).not.toBeNull();
        else if (c.expectChrome === "public") expect(screen.getByRole("link", { name: "sign in" })).toBeInTheDocument();
        else expect(screen.getByTestId("leaving-for-sign-in")).toBeInTheDocument();
      });
      await waitFor(() =>
        expect(vi.mocked(leaveForSignIn).mock.calls.length + vi.mocked(reloadPage).mock.calls.length, c.why).toBe(
          c.expectChrome === "leaving" ? 1 : 0,
        ),
      );
      expect(vi.mocked(reloadPage).mock.calls.length, c.why).toBe(c.expectReload ? 1 : 0);
      // The page is reloaded only once the stale cookie is gone.
      if (c.expectReload) expect(hasAuthTokenCookie()).toBe(false);
      expect(screen.queryByTestId("public-top-bar") !== null, c.why).toBe(c.expectChrome === "public");
      // The page is drawn only where the frame keeps the visitor.
      expect(screen.queryByText("the page") !== null, c.why).toBe(c.expectChrome !== "leaving");
    });
  }
});

function detail(): SessionDetailPayload {
  return {
    id: "session-signed-out",
    harness: "claude-code",
    startTime: "2026-08-21T09:00:00.000Z",
    endTime: "2026-08-21T09:02:00.000Z",
    durationMins: 2,
    totalTokens: 200,
    tokensIn: 120,
    tokensOut: 80,
    turnCount: 2,
    toolCallCount: 0,
    project: "village",
    model: "anthropic/claude-fable-5",
    turns: [
      { index: 0, role: "user", content: "why is ingest dropping commits?", timestamp: "2026-08-21T09:00:00.000Z", depth: 0 },
      { index: 1, role: "assistant", content: "Looking at the detector now.", timestamp: "2026-08-21T09:01:00.000Z", depth: 0 },
    ],
  };
}

/** App pages that need a session: the root, and every section under it. */
const SESSION_ONLY = /^\/(?:$|[?#]|(?:collectives|pulls|users|settings|explore|discover|groups|publish|welcome)(?:[/?#]|$))/;

describe("the transcript page, signed out", () => {
  for (const c of fx.pageCases) {
    it(c.name, async () => {
      const requested: string[] = [];
      installRESTFixture(
        TRANSCRIPT_ID,
        {
          transcript: {
            id: TRANSCRIPT_ID,
            local_id: "session-signed-out",
            visibility: "public",
            title: TITLE,
            description: null,
            project_name: "village",
          },
          // The owner is somebody else; nobody is signed in.
          owner: { id: "user-alice", github_username: "alice" },
          pull_requests: c.pullRequests,
          enriched_shares: [
            {
              transcript_id: TRANSCRIPT_ID,
              group_id: "c-guild",
              group_name: "Infra Guild",
              acceptance_mode: "open",
              status: "approved",
              shared_at: "2026-08-01T00:00:00.000Z",
            },
          ],
        },
        detail(),
        "signed-out-transcript",
        undefined,
        (url) => {
          const path = url.slice(url.indexOf("/api/v1") + "/api/v1".length);
          requested.push(path);
          if (path === "/auth/me") return new Response(JSON.stringify({ error: "not signed in" }), { status: 401 });
          if (path === "/collectives") throw new Error("the page asked for the collectives of nobody signed in");
          return undefined;
        },
      );
      window.history.replaceState({}, "", `/transcripts/${TRANSCRIPT_ID}${c.search}`);
      const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
      await act(async () => {
        render(
          <QueryClientProvider client={client}>
            <AuthProvider>
              <AppFrame>
                <Suspense fallback={<div>loading mounted transcript</div>}>
                  <TranscriptDetailPage
                    params={Promise.resolve({ id: TRANSCRIPT_ID })}
                    openSignedOut={c.openSignedOut}
                  />
                </Suspense>
              </AppFrame>
            </AuthProvider>
          </QueryClientProvider>,
        );
      });

      if (c.expectLeaving) {
        // Swapped for the frame's note, nothing of the transcript drawn, and
        // the visitor sent to sign in once.
        await waitFor(() => expect(screen.getByTestId("leaving-for-sign-in")).toBeInTheDocument());
        expect(vi.mocked(leaveForSignIn)).toHaveBeenCalledTimes(1);
        expect(document.querySelector(".txn-title")).toBeNull();
        expect(screen.queryByTestId("public-top-bar")).toBeNull();
        return;
      }

      // The transcript is read, in the public bar with its one way in.
      await waitFor(() => expect(document.querySelector(".txn-title")?.textContent).toBe(TITLE));
      await waitFor(() => expect(screen.getByRole("link", { name: "sign in" })).toBeInTheDocument());
      expect(screen.getByTestId("public-top-bar")).toBeInTheDocument();
      expect(screen.queryByTestId("leaving-for-sign-in")).toBeNull();
      expect(vi.mocked(leaveForSignIn)).not.toHaveBeenCalled();
      expect(requested).toContain("/auth/me");
      expect(requested).not.toContain("/collectives");

      // The trail names the place without linking it.
      const nav = document.querySelector('nav[aria-label="breadcrumb"]');
      const crumbs = [...(nav?.querySelectorAll(".link, .cur") ?? [])].map((crumb) => {
        const href = crumb.getAttribute("href");
        return href ? `${crumb.textContent ?? ""} -> ${href}` : crumb.textContent ?? "";
      });
      expect(crumbs).toEqual(c.expectCrumbs);

      // Each pull request is named, as text.
      if (c.pullRequests.length > 0) {
        await waitFor(() =>
          expect(screen.getAllByTestId("transcript-pull-request")).toHaveLength(c.pullRequests.length),
        );
        for (const row of screen.getAllByTestId("transcript-pull-request")) {
          expect(row.querySelector("a"), row.textContent ?? "").toBeNull();
        }
        expect(screen.getByTestId("transcript-pull-request")).toHaveTextContent("acme/widgets #7");
      }

      // Nothing links to a page that needs a session.
      for (const a of document.querySelectorAll("a")) {
        const href = a.getAttribute("href") ?? "";
        expect(SESSION_ONLY.test(href), `${a.textContent} -> ${href}`).toBe(false);
      }
      expect(screen.getAllByRole("link", { name: "peasant home" })[0]).toHaveAttribute("href", MARKETING_URL);

      // No owner or member action: no labels, no contributing, and the menu
      // offers downloads only.
      expect(screen.queryByRole("button", { name: "Add label" })).toBeNull();
      expect(screen.queryByTestId("transcript-access-caption")).toBeNull();
      const actions = screen.getByTestId("transcript-header-actions");
      fireEvent.click(within(actions).getByRole("button", { name: "more" }));
      const items = await screen.findAllByRole("menuitem");
      expect(items.map((item) => item.textContent?.trim())).toEqual([
        "download markdown",
        "download json",
        "download jsonl",
      ]);
      expect(screen.queryByText(/contribute/i)).toBeNull();
    });
  }
});

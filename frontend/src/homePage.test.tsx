import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  installHomeRouteREST,
  installHomeRouteTeardown,
  renderAppRoute,
} from "@/test/mountedHomeRoute";
import { loadHomePageFixtures } from "@/test/homePageFixtures";
import { mostRecentFirst } from "@/app/HomePage";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import type { TranscriptListItem } from "@/lib/types";

// Mounts the REAL production routes: the root route (`/`), which serves the
// signed-in person's home page or the public discovery list depending on who
// is asking, and the explore route (`/explore`). Every assertion is on what
// lands in the DOM and on the requests the routes actually issue, so a
// regression cannot hide behind a prop snapshot.

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn() }),
}));

const fixtures = loadHomePageFixtures();

installHomeRouteTeardown();

function homeSurface(): Element | null {
  return document.querySelector('[data-testid="home-page"]');
}

function exploreSurface(): Element | null {
  return document.querySelector('[data-testid="session-list-results"]');
}

function homeErrorSurface(): Element | null {
  return document.querySelector('[data-testid="home-page-error"]');
}

function noHandleSurface(): Element | null {
  return document.querySelector('[data-testid="home-page-no-handle"]');
}

function skeletonSurface(): Element | null {
  // The pending shell carries no testid of its own; it is the only shimmer the
  // route renders once the session is known.
  return document.querySelector(".animate-shimmer");
}

/** The endpoint the failure message must name, as the page names it. */
const LIST_ENDPOINT = "/api/v1/transcripts";

async function settled(): Promise<void> {
  await waitFor(() =>
    expect(document.querySelector('[data-testid="root-route-pending"]')).toBeNull(),
  );
  await waitFor(() =>
    expect(
      homeSurface() ?? exploreSurface() ?? homeErrorSurface() ?? noHandleSurface(),
    ).not.toBeNull(),
  );
}

describe("mounted routes: which surface each visitor lands on", () => {
  for (const c of fixtures.routeCases) {
    it(c.name, async () => {
      installHomeRouteREST({ viewerUsername: c.viewerUsername, transcripts: [] });
      await renderAppRoute(c.path);
      await settled();

      expect(homeSurface() !== null).toBe(c.expectSurface === "home");
      expect(exploreSurface() !== null).toBe(c.expectSurface === "explore");
    });
  }

});

describe("mounted home route: recent sessions, then projects", () => {
  for (const c of fixtures.homeCases) {
    it(c.name, async () => {
      const requested = installHomeRouteREST({
        viewerUsername: c.viewerUsername,
        transcripts: c.transcripts,
        ownerRequestFailure: c.requestFailure,
        usernameChosen: c.usernameChosen,
      });
      await renderAppRoute("/");

      const ownerRequests = () => requested.filter((p) => p.includes("owner="));

      // Without a handle the page must ask NOTHING. A blank owner filter is
      // dropped by the list handler, so the request would answer a narrow
      // question with the whole commons, under a heading that says "your".
      if (c.expectHomeSurface === "skeleton" || c.expectHomeSurface === "no-handle") {
        if (c.expectHomeSurface === "no-handle") {
          await waitFor(() => expect(noHandleSurface()).not.toBeNull());
          const alert = noHandleSurface()!.querySelector('[role="alert"]');
          expect(alert).not.toBeNull();
          expect(alert!.textContent).toContain("your account has no handle");
        } else {
          // Still on its way to the handle step: the page holds still, and in
          // particular does not fall through to any terminal answer.
          await waitFor(() => expect(skeletonSurface()).not.toBeNull());
          expect(noHandleSurface()).toBeNull();
        }
        expect(homeSurface()).toBeNull();
        expect(homeErrorSurface()).toBeNull();
        expect(document.querySelector('[data-testid="home-empty-state"]')).toBeNull();
        expect(requested.filter((p) => p.startsWith("/transcripts"))).toEqual([]);
        return;
      }

      await settled();

      // A request that FAILED is not an empty library. The page owes the
      // person a surface that says so and a way to ask again; the teaching
      // empty state and its "publish your first transcript" invitation would
      // tell somebody with a full shelf that it is bare.
      if (c.expectHomeSurface === "failure") {
        const failure = homeErrorSurface();
        expect(failure).not.toBeNull();
        expect(document.querySelector('[data-testid="home-empty-state"]')).toBeNull();
        expect(homeSurface()).toBeNull();
        const alert = failure!.querySelector('[role="alert"]');
        expect(alert).not.toBeNull();
        // The whole message, not merely its heading. The sentence that says a
        // failure is not an emptiness IS the fix; a body that regressed to
        // nothing would otherwise pass.
        const text = (alert!.textContent ?? "").replace(/\s+/g, " ");
        expect(text).toContain("Failed to load your sessions");
        expect(text).toContain(LIST_ENDPOINT);
        expect(text).toContain("A failed request is not an empty library");
        expect(text).toContain("nothing has been deleted");
        // The server's own reported cause reaches the reader, rather than a
        // fixed string that would read identically for every failure.
        expect(text).toContain("the session list is unavailable");

        // Retry must re-issue the SAME owner-scoped request, not merely
        // re-render: a button that only cleared the panel would leave the
        // person on a page that can never recover.
        const before = ownerRequests().length;
        expect(before).toBeGreaterThan(0);
        const retry = screen.getByRole("button", { name: /retry/i });
        await act(async () => {
          fireEvent.click(retry);
        });
        await waitFor(() => expect(ownerRequests().length).toBeGreaterThan(before));
        return;
      }

      const home = homeSurface();
      expect(home).not.toBeNull();
      expect(homeErrorSurface()).toBeNull();

      // A refresh that fails AFTER rows arrived must keep them. Replacing a
      // person's whole library with an error panel because a later request
      // failed is the same lie as calling it empty, one shape over.
      if (c.expectHomeSurface === "stale") {
        const answered = ownerRequests().length;
        // The real refetch path: the app's query client refetches on focus, so
        // the notice is reached the way a person reaches it, not by reaching
        // into the cache.
        await act(async () => {
          document.dispatchEvent(new Event("visibilitychange", { bubbles: true }));
        });
        await waitFor(() => expect(ownerRequests().length).toBeGreaterThan(answered));
        const notice = await waitFor(() => {
          const found = document.querySelector('[data-testid="home-stale-notice"]');
          expect(found).not.toBeNull();
          return found!;
        });
        expect(notice.getAttribute("role")).toBe("alert");
        expect(notice.textContent).toContain("could not be refreshed");
        expect(notice.textContent).toContain("the session list is unavailable");
        // The rows the server did confirm are still on screen, and the failure
        // panel did not take the page.
        expect(homeSurface()).not.toBeNull();
        expect(homeErrorSurface()).toBeNull();
        expect(document.querySelector('[data-testid="home-empty-state"]')).toBeNull();
        const stillListed = [
          ...document.querySelectorAll('[data-testid="home-recent-sessions"] a[aria-label]'),
        ].map((a) => (a.getAttribute("aria-label") ?? "").replace(/^Open transcript /, ""));
        expect(stillListed).toEqual(c.expectRecentTitles);
      }

      // The page reads the viewer's OWN transcripts. A request without the
      // owner filter would list the whole commons on a page titled "your".
      const listRequests = requested.filter((p) => p.startsWith("/transcripts"));
      expect(listRequests.length).toBeGreaterThan(0);
      for (const p of listRequests) {
        const query = new URLSearchParams(p.slice(p.indexOf("?") + 1));
        expect(query.get("owner")).toBe(c.viewerUsername);
      }

      // A row that arrived with no project identity is a server contract
      // violation. It is reported and left out of the project list; it is never
      // dropped from the page, and never folded into an invented project.
      const notice = document.querySelector('[data-testid="home-malformed-notice"]');
      expect(notice !== null).toBe(c.malformedCount > 0);
      if (c.malformedCount > 0) {
        const text = (notice!.textContent ?? "").replace(/\s+/g, " ");
        expect(text).toContain(
          `${c.malformedCount} transcript${c.malformedCount !== 1 ? "s" : ""} could not be grouped by project`,
        );
        expect(notice!.getAttribute("role")).toBe("alert");
      }

      const empty = document.querySelector('[data-testid="home-empty-state"]');
      expect(empty !== null).toBe(c.expectHomeSurface === "empty");

      const recentSection = document.querySelector('[data-testid="home-recent-sessions"]');
      if (c.expectHomeSurface === "empty") {
        expect(recentSection).toBeNull();
        expect(document.querySelector('[data-testid="home-projects"]')).toBeNull();
        return;
      }

      const recentTitles = [...recentSection!.querySelectorAll("a[aria-label]")].map((a) =>
        (a.getAttribute("aria-label") ?? "").replace(/^Open transcript /, ""),
      );
      expect(recentTitles).toEqual(c.expectRecentTitles);

      const rows = [...document.querySelectorAll('[data-testid="home-project-row"]')];
      expect(rows.map((r) => r.getAttribute("href"))).toEqual(
        c.expectProjectRows.map((r) => r.href),
      );
      expect(
        rows.map((r) => [...r.children].map((child) => (child.textContent ?? "").trim())),
      ).toEqual(
        c.expectProjectRows.map((r) => [
          r.displayName,
          `${r.sessionCount} session${r.sessionCount !== 1 ? "s" : ""}`,
        ]),
      );

      // Recent sessions come FIRST. Order is part of the acceptance, and a
      // page that rendered both sections in the other order would otherwise
      // satisfy every assertion above.
      const sections = [...home!.querySelectorAll("[data-testid]")]
        .map((e) => e.getAttribute("data-testid"))
        .filter((id) => id === "home-recent-sessions" || id === "home-projects");
      expect(sections).toEqual(["home-recent-sessions", "home-projects"]);
    });
  }
});

describe("recent-first ordering, including timestamps the server should never send", () => {
  for (const c of fixtures.sortCases) {
    it(c.name, () => {
      // Built through the shared wire-row builder, so the sort is exercised on
      // the same shape the page receives rather than on a hand-made stub.
      const rows = c.given.map((row) => ({
        transcript: makeTranscriptFixture({
          id: row.id,
          local_id: row.id,
          published_at: row.publishedAt,
        }),
        tags: [],
        owner: null,
      })) as unknown as TranscriptListItem[];

      expect(mostRecentFirst(rows).map((r) => r.transcript.id)).toEqual(c.expectOrder);
      // The sort does not mutate what it was given: the page groups the SAME
      // array afterwards, and a sort in place would reorder that too.
      expect(rows.map((r) => r.transcript.id)).toEqual(c.given.map((r) => r.id));
    });
  }
});

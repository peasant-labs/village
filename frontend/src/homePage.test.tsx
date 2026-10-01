import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  installHomeRouteREST,
  installHomeRouteTeardown,
  renderAppRoute,
} from "@/test/mountedHomeRoute";
import { loadHomePageFixtures } from "@/test/homePageFixtures";
import { mostRecentGroupFirst } from "@/app/HomePage";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import type { TranscriptListItem } from "@/lib/types";

// Mounts the REAL production routes: the root route (`/`), which serves the
// signed-in person's home page or the GitHub sign-in page depending on who is
// asking, and the explore route (`/explore`), under the REAL header for the nav
// cases. Every assertion is on what
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

function signInSurface(): Element | null {
  return document.querySelector('[data-testid="sign-in-page"]');
}

function homeErrorSurface(): Element | null {
  return document.querySelector('[data-testid="home-page-error"]');
}

/** Every owner the recorded requests asked about, in order. */
function requestedOwners(requested: string[]): string[] {
  return requested
    .filter((p) => p.includes("owner="))
    .map((p) => new URLSearchParams(p.slice(p.indexOf("?") + 1)).get("owner") ?? "");
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
      homeSurface() ??
        exploreSurface() ??
        signInSurface() ??
        homeErrorSurface() ??
        noHandleSurface(),
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
      expect(signInSurface() !== null).toBe(c.expectSurface === "sign-in");
      expect(exploreSurface() !== null).toBe(c.expectSurface === "explore");
    });
  }

});

/** The top-level rows of the transcripts table, as the reader sees them. */
function tableRows(): HTMLTableRowElement[] {
  return [...document.querySelectorAll<HTMLTableRowElement>('[data-testid="home-transcripts"] tr.tbl-row')];
}

/** The title a table row leads with. */
function rowTitle(tr: Element): string {
  return (
    tr.querySelector('[data-testid="home-transcript-row"] .iu-session-text > a.iu-session-title')
      ?.textContent ?? ""
  );
}

function tableTitles(): string[] {
  return tableRows().map(rowTitle);
}

function rowTitled(title: string): HTMLTableRowElement {
  const found = tableRows().filter((tr) => rowTitle(tr) === title);
  if (found.length !== 1) throw new Error(`the table lists ${found.length} rows titled "${title}"`);
  return found[0];
}

function text(el: Element | null | undefined): string {
  return (el?.textContent ?? "").replace(/\s+/g, " ").trim();
}

describe("mounted home route: your transcripts, their totals and your collectives", () => {
  for (const c of fixtures.homeCases) {
    it(c.name, async () => {
      const backend = installHomeRouteREST({
        viewerUsername: c.viewerUsername,
        transcripts: c.transcripts,
        ownerRequestFailure: c.requestFailure,
        usernameChosen: c.usernameChosen,
        stats: c.stats,
        collectives: c.collectives,
        contributions: c.contributions,
      });
      await renderAppRoute("/");

      const requested = backend.requested;
      const ownerRequests = () =>
        requested.filter((p) => p.startsWith("/transcripts?") && !p.includes("view=grouped"));

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
        // Nothing of the person's own is asked for: not the list, and not the
        // totals or collectives that would sit beside it.
        expect(requested.filter((p) => p !== "/auth/me")).toEqual([]);
        return;
      }

      await settled();
      await waitFor(() => expect(document.querySelector('[data-testid="home-list-loading"]')).toBeNull());

      // A request that FAILED is not an empty library. The page owes the
      // person a surface that says so and a way to ask again; the teaching
      // empty state and its first-publish invitation would tell somebody with
      // a full shelf that it is bare.
      if (c.expectHomeSurface === "failure") {
        const failure = homeErrorSurface();
        expect(failure).not.toBeNull();
        expect(document.querySelector('[data-testid="home-empty-state"]')).toBeNull();
        expect(document.querySelector('[data-testid="home-transcripts"]')).toBeNull();
        const alert = failure!.querySelector('[role="alert"]');
        expect(alert).not.toBeNull();
        // The whole message, not merely its heading. The sentence that says a
        // failure is not an emptiness IS the fix; a body that regressed to
        // nothing would otherwise pass.
        const message = text(alert);
        expect(message).toContain("your transcripts could not be loaded");
        expect(message).toContain(LIST_ENDPOINT);
        expect(message).toContain("a failed request is not an empty library");
        expect(message).toContain("nothing has been deleted");
        // The server's own reported cause reaches the reader, rather than a
        // fixed string that would read identically for every failure.
        expect(message).toContain("the session list is unavailable");

        // Retry must re-issue the SAME owner-scoped request, not merely
        // re-render: a button that only cleared the panel would leave the
        // person on a page that can never recover.
        const before = ownerRequests().length;
        expect(before).toBeGreaterThan(0);
        const retry = screen.getByRole("button", { name: /^retry$/i });

        // The panel must SURVIVE its own retry. With no rows to fall back on
        // the query returns to its pending state mid-flight, so a page reading
        // the error flag directly would swap this whole surface for a loading
        // state the moment the button was pressed, taking the alert, the focus
        // and the retry with it.
        backend.hold();
        retry.focus();
        await act(async () => {
          fireEvent.click(retry);
        });
        const busyPanel = await waitFor(() => {
          const b = homeErrorSurface()!.querySelector<HTMLButtonElement>("button")!;
          expect(b.getAttribute("aria-disabled")).toBe("true");
          return b;
        });
        expect(busyPanel.textContent, "panel retry: busy label").toContain("retrying");
        expect(homeErrorSurface(), "panel survives its own retry").not.toBeNull();
        expect(document.querySelector('[data-testid="home-list-loading"]')).toBeNull();
        // A real `disabled` would hand focus back to the document, and jsdom
        // does NOT model that blur, so asserting the attribute's absence is
        // what actually holds the line; activeElement only says the node was
        // neither unmounted nor replaced.
        expect(busyPanel.hasAttribute("disabled"), "panel retry: not truly disabled").toBe(
          false,
        );
        expect(document.activeElement, "panel retry: node kept").toBe(busyPanel);
        // Busy means the press is REFUSED, not merely announced: a control that
        // only looked busy would stack a request on every press.
        const whileBusyPanel = ownerRequests().length;
        await act(async () => {
          fireEvent.click(busyPanel);
        });
        expect(ownerRequests().length, "panel retry: press refused while busy").toBe(
          whileBusyPanel,
        );
        expect(document.querySelector('[data-testid="home-status"]')?.textContent).toContain(
          "reloading your transcripts",
        );
        await act(async () => {
          backend.release();
        });
        await waitFor(() => expect(ownerRequests().length).toBeGreaterThan(before));

        // Still failing, so the surface stays; and it recovers when the server
        // does, rather than being a panel a person can never leave.
        await waitFor(() => expect(homeErrorSurface()).not.toBeNull());
        backend.heal();
        await act(async () => {
          fireEvent.click(homeErrorSurface()!.querySelector("button")!);
        });
        await waitFor(() => expect(homeErrorSurface()).toBeNull());
        // This person really has published nothing, so the answered list is
        // the empty library, now that it is an answer.
        expect(document.querySelector('[data-testid="home-empty-state"]')).not.toBeNull();
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
        // TanStack's focus manager really does listen for this event, so the
        // refetch is triggered the way the browser triggers it rather than by
        // reaching into the cache. What makes it fire IMMEDIATELY here is this
        // harness's own `staleTime: 0`; in the app a refresh within the stale
        // window is skipped, and arrives on the next focus after it.
        await act(async () => {
          document.dispatchEvent(new Event("visibilitychange", { bubbles: true }));
        });
        await waitFor(() => expect(ownerRequests().length).toBeGreaterThan(answered));
        const notice = await waitFor(() => {
          const found = document.querySelector('[data-testid="home-stale-notice"]');
          expect(found).not.toBeNull();
          return found!;
        });
        // The SENTENCE is the alert, not the whole notice: the control's label
        // changes while a retry runs, and an atomic alert would re-announce
        // everything on that change.
        const noticeAlert = notice.querySelector('[role="alert"]');
        expect(noticeAlert).not.toBeNull();
        expect(noticeAlert!.querySelector("button")).toBeNull();
        expect(notice.textContent).toContain("could not be refreshed");
        expect(notice.textContent).toContain("the session list is unavailable");
        // The rows the server did confirm are still on screen, and the failure
        // panel did not take the page.
        expect(homeErrorSurface()).toBeNull();
        expect(document.querySelector('[data-testid="home-empty-state"]')).toBeNull();
        expect(tableTitles()).toEqual(c.expectRowTitles);

        // The notice's OWN retry is a second control, and the only way back
        // from a failed refresh. A dead handler here would leave a person
        // pressing a button that never asks again.
        const noticeRetry = document.querySelector<HTMLButtonElement>(
          '[data-testid="home-stale-retry"]',
        );
        expect(noticeRetry).not.toBeNull();
        const beforeNoticeRetry = ownerRequests().length;
        await act(async () => {
          fireEvent.click(noticeRetry!);
        });
        await waitFor(() =>
          expect(ownerRequests().length).toBeGreaterThan(beforeNoticeRetry),
        );

        // While a retry is in flight the control says so and refuses further
        // presses. A retry that fails again renders the SAME words, so without
        // this a person cannot tell a working button from a dead one.
        backend.hold();
        const pressed = document.querySelector<HTMLButtonElement>(
          '[data-testid="home-stale-retry"]',
        )!;
        // Activated the way a keyboard user activates it, so the assertion
        // below is about keeping focus rather than about never having it.
        pressed.focus();
        expect(document.activeElement).toBe(pressed);
        await act(async () => {
          fireEvent.click(pressed);
        });
        const busy = await waitFor(() => {
          const b = document.querySelector<HTMLButtonElement>(
            '[data-testid="home-stale-retry"]',
          )!;
          // `aria-disabled`, not `disabled`: a real disabled attribute on the
          // control the reader just pressed hands focus back to the document.
          expect(b.getAttribute("aria-disabled")).toBe("true");
          expect(b.hasAttribute("disabled"), "notice retry: not truly disabled").toBe(false);
          return b;
        });
        expect(busy.textContent).toContain("retrying");
        // Focus survived the state change. A real `disabled` here would have
        // handed it back to the document body and not returned it.
        expect(document.activeElement).toBe(busy);
        // And it refuses the press rather than stacking requests.
        const whileBusy = ownerRequests().length;
        await act(async () => {
          fireEvent.click(busy);
        });
        expect(ownerRequests().length).toBe(whileBusy);
        expect(document.querySelector('[data-testid="home-status"]')?.textContent).toContain(
          "reloading your transcripts",
        );
        await act(async () => {
          backend.release();
        });
        await waitFor(() =>
          expect(
            document
              .querySelector('[data-testid="home-stale-retry"]')
              ?.getAttribute("aria-disabled"),
          ).toBeNull(),
        );

        // And it recovers: once the server answers again the notice goes, and
        // the rows are the refreshed ones rather than a permanent warning.
        backend.heal();
        await act(async () => {
          fireEvent.click(
            document.querySelector<HTMLButtonElement>('[data-testid="home-stale-retry"]')!,
          );
        });
        await waitFor(() =>
          expect(document.querySelector('[data-testid="home-stale-notice"]')).toBeNull(),
        );
        expect(homeErrorSurface()).toBeNull();
      }

      // The page reads the viewer's OWN transcripts, a page at a time. A
      // request without the owner filter would list the whole commons on a
      // page titled "your".
      const listRequests = requested.filter((p) => p.startsWith("/transcripts"));
      expect(listRequests.length).toBeGreaterThan(0);
      for (const p of listRequests) {
        const query = new URLSearchParams(p.slice(p.indexOf("?") + 1));
        expect(query.get("owner")).toBe(c.viewerUsername);
      }
      for (const p of ownerRequests()) {
        const query = new URLSearchParams(p.slice(p.indexOf("?") + 1));
        expect(query.get("page"), `${p} names its page`).not.toBeNull();
        expect(query.get("limit"), `${p} names its page size`).not.toBeNull();
      }

      // A row that arrived with no project identity is a server contract
      // violation. It is reported and still listed; it is never dropped from
      // the page, and never linked to an invented project.
      const notice = document.querySelector('[data-testid="home-malformed-notice"]');
      expect(notice !== null).toBe(c.malformedCount > 0);
      if (c.malformedCount > 0) {
        const message = text(notice);
        expect(message).toContain(
          `${c.malformedCount} transcript${c.malformedCount !== 1 ? "s" : ""} could not be grouped by project`,
        );
        expect(message).toContain("still listed below");
        expect(notice!.getAttribute("role")).toBe("alert");
      }

      const empty = document.querySelector('[data-testid="home-empty-state"]');
      expect(empty !== null).toBe(c.expectHomeSurface === "empty");
      if (c.expectHomeSurface === "empty") {
        expect(document.querySelector('[data-testid="home-transcripts"]')).toBeNull();
        // Nothing to search, so no search box offering to.
        expect(screen.queryByRole("searchbox")).toBeNull();
        return;
      }

      expect(tableTitles()).toEqual(c.expectRowTitles);
      // Every title leads to its transcript.
      for (const tr of tableRows()) {
        const id = tr.querySelector('[data-testid="home-transcript-row"]')!.getAttribute("data-transcript-id");
        expect(
          tr.querySelector("a.iu-session-title")!.getAttribute("href"),
        ).toBe(`/transcripts/${id}`);
      }

      for (const link of c.expectProjectLinks) {
        const sub = rowTitled(link.title).querySelector(
          '[data-testid="home-transcript-row"] .iu-session-text > .iu-session-sub',
        );
        expect(sub, `${link.title} names its project`).not.toBeNull();
        expect(sub!.querySelector("a")?.getAttribute("href") ?? null).toBe(link.href);
      }

      for (const cell of c.expectSharedWithCells ?? []) {
        const td = rowTitled(cell.title).querySelectorAll("td")[1];
        const named = [...td.querySelectorAll(".cmg-shared > span")].map(text);
        expect(named.length > 0 ? named : [text(td)]).toEqual(cell.text);
      }
      for (const mark of c.expectProviderMarks ?? []) {
        const row = rowTitled(mark.title);
        const icons = [...row.querySelectorAll<SVGElement>('[data-testid="home-transcript-row"] svg[data-brand]')];
        expect(icons.map((icon) => icon.dataset.brand)).toEqual(mark.brand === null ? [] : [mark.brand]);
        if (mark.label !== null) {
          expect(icons[0]).toHaveAttribute("role", "img");
          expect(icons[0]).toHaveAttribute("aria-label", mark.label);
          expect(row.querySelector(".iu-session-sub")).not.toHaveTextContent(mark.label);
        }
      }

      for (const cell of c.expectPullRequestCells ?? []) {
        const tr = rowTitled(cell.title);
        const td = tr.querySelectorAll("td")[2];
        const numbers = () => [...td.querySelectorAll(".ovl-item a")].map(text);
        if (cell.shown.length === 0) {
          expect(text(td)).toBe("none");
          continue;
        }
        expect(numbers()).toEqual(cell.shown);
        // Every number leads to its pull request page.
        for (const a of td.querySelectorAll<HTMLAnchorElement>(".ovl-item a")) {
          expect(a.getAttribute("href")).toMatch(/^\/pulls\/[^/]+\/[^/]+\/\d+$/);
        }
        const more = td.querySelector(".ovl-more");
        const row = c.transcripts.find((t) => t.title === cell.title)!;
        const count = row.pullRequests?.count ?? 0;
        if (cell.more === "none") {
          expect(more).toBeNull();
        } else if (cell.more === "reveal") {
          // Every reference is in hand, so `+N` reveals them in place.
          expect(more!.tagName).toBe("BUTTON");
          expect(text(more)).toBe(`+${count - cell.shown.length}`);
          await act(async () => {
            fireEvent.click(more!);
          });
          expect(numbers()).toEqual(row.pullRequests!.recent.map((ref) => `#${ref.number}`));
        } else {
          // The row counts more than it carries, so `+N` leads to the
          // transcript page, which lists them all; N is still the true count.
          expect(more!.tagName).toBe("A");
          expect(text(more)).toBe(`+${count - cell.shown.length}`);
          expect(more!.getAttribute("href")).toBe(`/transcripts/${row.id}`);
        }
      }

      if (c.expectStats !== undefined) {
        const strip = await screen.findByTestId("home-stats");
        await waitFor(() =>
          expect([...strip.querySelectorAll("li")].map(text)).toEqual(c.expectStats),
        );
      }

      if (c.expectRailCollectives !== undefined) {
        await waitFor(() =>
          expect(document.querySelectorAll('[data-testid="home-rail-collective"]')).toHaveLength(
            c.expectRailCollectives!.length,
          ),
        );
        const listed = [...document.querySelectorAll('[data-testid="home-rail-collective"]')];
        expect(
          listed.map((li) => ({ name: text(li.querySelector("a")), meta: text(li.querySelector(".cmg-rail-meta")) })),
        ).toEqual(c.expectRailCollectives);
        expect(listed.map((li) => li.querySelector("a")!.getAttribute("href"))).toEqual(
          c.collectives!.map((collective) => `/groups/${collective.id}`),
        );
        expect(
          screen.getByRole("link", { name: "view all" }).getAttribute("href"),
        ).toBe("/groups");
      }

      if (c.expectWaiting !== undefined) {
        // The contributions read has landed once its request was answered.
        await waitFor(() =>
          expect(requested).toContain("/users/me/collectives/contributions"),
        );
        if (c.expectWaiting.length === 0) {
          expect(document.querySelector('[data-testid="home-rail-waiting"]')).toBeNull();
        } else {
          const section = await screen.findByTestId("home-rail-waiting");
          expect(
            [...section.querySelectorAll('[data-testid="home-rail-waiting-collective"] a')].map(text),
          ).toEqual(c.expectWaiting);
          for (const name of c.expectWaiting) {
            const pending = c.contributions!.find((x) => x.name === name)!.pending;
            expect(text(section)).toContain(`${pending} transcript${pending === 1 ? "" : "s"} waiting`);
          }
        }
      }

      if (c.search !== undefined) {
        const box = screen.getByRole("searchbox", { name: "search your transcripts" });
        await act(async () => {
          fireEvent.change(box, { target: { value: c.search!.query } });
        });
        // The query reaches the server as `q` on the owner-scoped list.
        await waitFor(() =>
          expect(
            ownerRequests().some(
              (p) => new URLSearchParams(p.slice(p.indexOf("?") + 1)).get("q") === c.search!.query,
            ),
          ).toBe(true),
        );
        if (c.search.expectRowTitles.length === 0) {
          const noMatch = await screen.findByTestId("home-no-match");
          expect(text(noMatch)).toContain(c.search.query);
          // An answered search with no rows is not an empty library.
          expect(document.querySelector('[data-testid="home-empty-state"]')).toBeNull();
          await act(async () => {
            fireEvent.click(screen.getByRole("button", { name: "clear search" }));
          });
          await waitFor(() => expect(tableTitles()).toEqual(c.expectRowTitles));
        } else {
          await waitFor(() => expect(tableTitles()).toEqual(c.search!.expectRowTitles));
        }
      }

      if (c.loadMore !== undefined) {
        expect(text(screen.getByTestId("home-count"))).toBe(
          `${c.expectRowTitles.length} of ${c.transcripts.length} transcripts`,
        );
        const more = screen.getByTestId("home-load-more");
        expect(text(more)).toBe(`show ${c.transcripts.length - c.expectRowTitles.length} more`);
        await act(async () => {
          fireEvent.click(more);
        });
        await waitFor(() => expect(tableTitles()).toEqual(c.loadMore!.expectRowTitles));
        // The next page was asked for as the NEXT page, not the first again.
        expect(
          ownerRequests().some((p) => new URLSearchParams(p.slice(p.indexOf("?") + 1)).get("page") === "2"),
        ).toBe(true);
        expect(screen.queryByTestId("home-load-more")).toBeNull();
        expect(text(screen.getByTestId("home-count"))).toBe(
          `${c.transcripts.length} of ${c.transcripts.length} transcripts`,
        );
      } else if (c.expectHomeSurface === "rows") {
        // Everything fits on the first page, so nothing offers more.
        expect(screen.queryByTestId("home-load-more")).toBeNull();
      }
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

      // Rows that started nothing rank by their own timestamps, so the page's
      // one ranking is exercised here on its plainest input.
      expect(mostRecentGroupFirst(rows, new Map()).map((r) => r.transcript.id)).toEqual(
        c.expectOrder,
      );
      // The ranking does not mutate what it was given: the page folds the SAME
      // rows, and a sort in place would reorder those too.
      expect(rows.map((r) => r.transcript.id)).toEqual(c.given.map((r) => r.id));
    });
  }
});

describe("mounted home route: a failure belongs to the handle it came from", () => {
  for (const c of fixtures.viewerChangeCases) {
    it(c.name, async () => {
      const backend = installHomeRouteREST({
        viewerUsername: c.firstViewer,
        transcripts: [],
        ownerRequestFailure: "always",
      });
      await renderAppRoute("/");
      await settled();
      expect(homeErrorSurface(), "the first person's list failed").not.toBeNull();

      // The handle changes while the page stays mounted: the session query is
      // an ordinary query with focus refetching left on. The next person's list
      // request has no rows of its own, so the memory's clear condition cannot
      // fire, and only the owner-keyed read stops them being shown a failure
      // that belongs to somebody else's library.
      backend.setViewer(c.secondViewer);
      backend.hold();
      await act(async () => {
        document.dispatchEvent(new Event("visibilitychange", { bubbles: true }));
      });
      await waitFor(() =>
        expect(
          requestedOwners(backend.requested),
          "the new handle's own list was requested",
        ).toContain(c.secondViewer),
      );

      expect(
        homeErrorSurface(),
        "the previous handle's failure must not describe this one's first load",
      ).toBeNull();
      expect(document.querySelector('[data-testid="home-empty-state"]')).toBeNull();

      // And when the new handle's own request fails with the SAME words, that
      // failure is its own and must be announced. A memory that recorded on the
      // message alone would keep the previous handle on the record, the keyed
      // read would miss, and this person would be told their library is empty.
      await act(async () => {
        backend.release();
      });
      await waitFor(() =>
        expect(
          homeErrorSurface(),
          "the new handle's own failure must be announced, not swallowed",
        ).not.toBeNull(),
      );
      expect(document.querySelector('[data-testid="home-empty-state"]')).toBeNull();
    });
  }
});

import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { loadPullsIndexFixtures, type PullsIndexDay } from "@/test/pullsIndexFixtures";
import {
  installPullsRouteREST,
  installPullsRouteTeardown,
  renderPullsRoute,
} from "@/test/mountedPullsRoute";

// Mounts the REAL `/pulls` route inside the real auth provider. Every assertion
// is on what lands in the DOM and on the requests the page sends.

installPullsRouteTeardown();

function spoken(el: Element | null | undefined): string {
  return (el?.textContent ?? "").replace(/\s+/g, " ").trim();
}

const LIST = "GET /users/me/prompt-requests";

/** The days the page shows, in order, each with its cards in order. */
function shownDays(): PullsIndexDay[] {
  return screen.queryAllByTestId("pulls-day").map((day) => ({
    label: spoken(day.querySelector("h2")),
    cards: within(day)
      .getAllByTestId("pulls-request")
      .map((card) => ({
        heading: spoken(card.querySelector("h3")),
        meta: spoken(card.querySelector(".on-prcard-meta")),
        href: card.querySelector("h3 a")?.getAttribute("href") ?? "",
      })),
  }));
}

describe("the pull requests route", () => {
  for (const c of loadPullsIndexFixtures()) {
    it(c.name, async () => {
      // Ages and day labels read the clock, so it is frozen where the case says.
      // Only Date is faked: the page's requests still resolve on real timers.
      vi.useFakeTimers({ toFake: ["Date"] });
      vi.setSystemTime(new Date(c.now));
      const reads = installPullsRouteREST(c);
      await renderPullsRoute();

      switch (c.expectSurface) {
        case "signed-out": {
          await screen.findByTestId("pulls-signed-out");
          const signIn = screen.getByTestId("pulls-sign-in");
          expect(spoken(signIn)).toBe("sign in");
          // Signing in comes back to this list, never to the root.
          expect(signIn.getAttribute("href")).toBe(`/login?next=${encodeURIComponent("/pulls")}`);
          // Nobody's list is asked for without a session.
          expect(reads).not.toContain(LIST);
          return;
        }
        case "empty": {
          const empty = await screen.findByTestId("pulls-empty");
          expect(spoken(empty.querySelector("h2"))).toBe("nothing is waiting on you");
          // One line on how a pull request comes to wait here, naming the real command.
          expect(spoken(empty.querySelector("p"))).toBe(
            "A pull request waits here when you comment /peasant attach on it before you publish a session that matches it.",
          );
          expect(screen.queryAllByTestId("pulls-request")).toEqual([]);
          expect(screen.queryByTestId("pulls-failure")).toBeNull();
          return;
        }
        case "failure": {
          const failure = await screen.findByTestId("pulls-failure");
          const alert = [...failure.querySelectorAll('[role="alert"] p')].map((line) => spoken(line));
          expect(alert).toEqual([
            "your pull requests could not be loaded",
            "The request for the pull requests waiting on you failed, so this is not an empty list. Retrying asks again.",
          ]);
          // A failed read is not an empty list, and shows no rows.
          expect(screen.queryByTestId("pulls-empty")).toBeNull();
          expect(screen.queryAllByTestId("pulls-request")).toEqual([]);

          await act(async () => {
            fireEvent.click(screen.getByTestId("pulls-retry"));
          });
          // The retry asks the same route again.
          await waitFor(() => expect(reads.filter((read) => read === LIST)).toHaveLength(2));
          if (c.requestFailure === "always") {
            await waitFor(() => expect(screen.getByTestId("pulls-retry").getAttribute("aria-disabled")).toBeNull());
            expect(screen.getByTestId("pulls-failure")).toBeInTheDocument();
            expect(shownDays()).toEqual([]);
            return;
          }
          await waitFor(() => expect(shownDays()).toEqual(c.expectDays));
          expect(screen.queryByTestId("pulls-failure")).toBeNull();
          return;
        }
        case "rows": {
          await waitFor(() => expect(shownDays()).toEqual(c.expectDays));
          expect(screen.queryByTestId("pulls-empty")).toBeNull();
          expect(screen.queryByTestId("pulls-failure")).toBeNull();
          // Each card's state is the server's own word for it.
          const states = screen
            .getAllByTestId("pulls-request")
            .map((card) => spoken(card.querySelector(".on-tag")));
          expect(states).toEqual(states.map(() => "waiting"));
          // And each card offers its pull request on GitHub.
          const github = screen
            .getAllByTestId("pulls-request")
            .map((card) => card.querySelector(".on-pr-gh")?.getAttribute("href"));
          expect(github).toEqual(
            c.expectDays.flatMap((day) =>
              day.cards.map((card) => `https://github.com${card.href.replace(/^\/pulls\/([^/]+)\/([^/]+)\/(\d+)$/, "/$1/$2/pull/$3")}`),
            ),
          );
          return;
        }
      }
    });
  }
});

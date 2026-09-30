import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import { describe, expect, it } from "vitest";
import { PULL_REQUESTS_SHOWN } from "@/components/transcript/TranscriptPullRequests";
import {
  installMountedRouteTeardown,
  installRESTFixture,
  renderProductionRoute,
  type MountedRouteTranscriptMetadata,
} from "@/test/mountedProductionRoute";
import { loadTranscriptPullRequestsFixtures } from "@/test/transcriptPullRequestsFixtures";

// The pull requests a transcript is bound to, mounted through the real route
// with `GET /transcripts/{id}/pulls` faked: none, one, more than the rows shown
// before `show all`, and a failed read.
const fx = loadTranscriptPullRequestsFixtures();

const detail: SessionDetailPayload = {
  id: "session-pull-requests",
  harness: "claude-code",
  startTime: "2026-08-21T09:00:00.000Z",
  endTime: "2026-08-21T09:02:00.000Z",
  durationMins: 2,
  totalTokens: 200,
  tokensIn: 120,
  tokensOut: 80,
  turnCount: 1,
  toolCallCount: 0,
  project: "village",
  model: "anthropic/claude-fable-5",
  turns: [{ index: 0, role: "user", content: "fix the flaky ingest test", timestamp: "2026-08-21T09:00:00.000Z", depth: 0 }],
};

/** One row as a person reads it: the link's text, then the state and title. */
function rows(): string[] {
  return screen.queryAllByTestId("transcript-pull-request").map((row) => {
    const link = row.querySelector("a")!;
    const rest = row.querySelector(".cmg-none")?.textContent?.trim() ?? "";
    return `${link.textContent?.replace(/\s+/g, " ").trim()} · ${rest}`;
  });
}

installMountedRouteTeardown();

describe("mounted transcript route: pull request list", () => {
  it("shows as many rows as the fixture says before show all", () => {
    expect(PULL_REQUESTS_SHOWN).toBe(fx.shownLimit);
  });

  for (const c of fx.cases) {
    it(c.name, async () => {
      const transcriptID = `pulls-${c.name}`;
      const metadata: MountedRouteTranscriptMetadata = {
        transcript: {
          id: transcriptID,
          local_id: "session-pull-requests",
          visibility: "shared",
          title: "Fix flaky ingest test",
          description: null,
          project_name: "village",
        },
        owner: { id: "fixture-owner" },
        enriched_shares: [],
        pull_requests: c.pullRequests,
      };
      const fetchMock = installRESTFixture(transcriptID, metadata, detail, "transcript-pull-requests", undefined, (url) =>
        c.status !== 200 && url.endsWith(`/transcripts/${transcriptID}/pulls`)
          ? new Response(JSON.stringify({ error: "read failed" }), { status: c.status })
          : undefined,
      );
      await renderProductionRoute(transcriptID);
      await waitFor(() => expect(document.querySelector(".txn-title")).not.toBeNull());
      await waitFor(() =>
        expect(fetchMock.mock.calls.some((call) => String(call[0]).endsWith(`/transcripts/${transcriptID}/pulls`))).toBe(true),
      );

      if (c.status !== 200) {
        const section = await screen.findByTestId("transcript-pull-requests");
        expect(within(section).getByRole("alert")).toHaveTextContent("the pull requests could not load.");
        const reads = () => fetchMock.mock.calls.filter((call) => String(call[0]).endsWith("/pulls")).length;
        const before = reads();
        fireEvent.click(within(section).getByRole("button", { name: "retry" }));
        await waitFor(() => expect(reads()).toBeGreaterThan(before));
        return;
      }

      if (c.pullRequests.length === 0) {
        // Nothing bound: no section, and no empty band under the meta row.
        await waitFor(() => expect(document.querySelector(".txn-prs")).toBeNull());
        expect(screen.queryByTestId("transcript-pull-requests")).toBeNull();
        return;
      }

      const section = await screen.findByTestId("transcript-pull-requests");
      expect(within(section).getByText("pull requests")).toBeInTheDocument();
      expect(rows()).toEqual(c.expectShown);
      const shown = screen.getAllByTestId("transcript-pull-request");
      shown.forEach((row, i) => {
        const pr = c.pullRequests[i];
        expect(row.querySelector("a")?.getAttribute("href")).toBe(`/pulls/${pr.owner}/${pr.name}/${pr.number}`);
      });

      const showAll = within(section).queryByRole("button", { name: /^show all/ });
      if (c.expectShowAll == null) {
        expect(showAll).toBeNull();
        return;
      }
      expect(showAll).toHaveTextContent(c.expectShowAll);
      fireEvent.click(showAll!);
      expect(rows()).toEqual(c.expectAfterShowAll);
      expect(within(section).queryByRole("button", { name: /^show all/ })).toBeNull();
      // Focus lands on the first row the button revealed.
      expect(document.activeElement).toBe(screen.getAllByTestId("transcript-pull-request")[fx.shownLimit].querySelector("a"));
    });
  }
});

import { act, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import {
  eventKey,
  installMountedProfileTeardown,
  installProfileRESTFixture,
  renderProfileRoute,
  type MountedProfileFixture,
} from "@/test/mountedProfileRoute";
import {
  loadProfileCollectivesFixtures,
  type ContributedCollectiveFixture,
} from "@/test/profileCollectivesFixtures";
import type { CollectiveSubmissionPair, ContributedCollective, ShareEvent } from "@/lib/types";

// Mounts the REAL profile route (`UserProfilePage` inside the real
// `AuthProvider`, with REST stubbed) — the surface a person actually loads —
// and asserts the contributed-collectives section on it. Nothing here renders
// the section component with hand-built props: the point of most of these
// cases is whether the mounted page decides to render it at all.
const fixtures = loadProfileCollectivesFixtures();

const OWNER = "fixture-owner";
const CONTRIBUTIONS_PATH = "/users/me/collectives/contributions";

function toWire(g: ContributedCollectiveFixture): ContributedCollective {
  return {
    id: g.id,
    name: g.name,
    description: g.description,
    linked_github_org: null,
    approved_count: g.approvedCount,
    pending_count: g.pendingCount,
    rejected_attempt_count: g.rejectedAttemptCount,
    withdrawn_attempt_count: g.withdrawnAttemptCount,
  };
}

async function mount(fixture: MountedProfileFixture): Promise<string[]> {
  const requested = installProfileRESTFixture(fixture);
  await renderProfileRoute(fixture.profileUsername);
  // Let the auth query settle so the page has decided who is looking before
  // any assertion about the section's presence is made.
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
  return requested;
}

function section(): HTMLElement | null {
  return document.querySelector<HTMLElement>('[data-testid="profile-collectives"]');
}

function collectiveRows(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>('[data-testid="contributed-collective"]')];
}

installMountedProfileTeardown();

describe("mounted profile route: the collectives section exists only for the owner", () => {
  for (const c of fixtures.viewerCases) {
    it(c.name, async () => {
      const viewerUsername =
        c.viewer === "owner" ? OWNER : c.viewer === "other-signed-in" ? "someone-else" : null;
      const requested = await mount({
        profileUsername: OWNER,
        viewerUsername,
        contributions: [toWire(fixtures.contributionCases[0].collectives[0])],
      });

      if (c.expectSection) {
        await waitFor(() => expect(section()).not.toBeNull());
      } else {
        expect(section()).toBeNull();
        // Not merely invisible: the page must not have ASKED either. A
        // request fired for a viewer who may not read the answer is the same
        // disclosure moved onto the network.
        expect(document.body.textContent).not.toMatch(/collectives you contribute to/i);
      }
      expect(requested.includes(CONTRIBUTIONS_PATH)).toBe(c.expectContributionsRequest);
    });
  }
});

describe("mounted profile route: every contributed collective is listed", () => {
  for (const c of fixtures.contributionCases) {
    it(c.name, async () => {
      await mount({
        profileUsername: OWNER,
        viewerUsername: OWNER,
        contributions: c.collectives.map(toWire),
      });
      await waitFor(() => expect(collectiveRows()).toHaveLength(c.collectives.length));

      const rows = collectiveRows();
      // Order and membership: a collective with nothing approved is still a
      // row in its served position, never hidden, collapsed or sorted away.
      expect(rows.map((row) => row.querySelector("a")?.textContent)).toEqual(
        c.expectedCollectiveNames,
      );

      rows.forEach((row, i) => {
        const expected = c.expectedCounters[i];
        const read = (testId: string) =>
          row.querySelector<HTMLElement>(`[data-testid="${testId}"]`);

        expect(read("counter-approved")?.textContent).toContain(String(expected.approved));
        expect(read("counter-pending")?.textContent).toContain(String(expected.pending));
        expect(read("counter-rejected-attempts")?.textContent).toContain(
          String(expected.rejectedAttempts),
        );
        expect(read("counter-withdrawn")?.textContent).toContain(
          String(expected.withdrawnAttempts),
        );
      });
    });
  }
});

describe("mounted profile route: the units sentence stays accurate for four counters", () => {
  it("states both the transcript-counting pair and the event-counting pair", async () => {
    await mount({
      profileUsername: OWNER,
      viewerUsername: OWNER,
      contributions: [toWire(fixtures.contributionCases[0].collectives[0])],
    });
    await waitFor(() => expect(section()).not.toBeNull());
    const explanation = section()!.querySelector("p")!.textContent!;
    expect(explanation).toMatch(/approved and awaiting review count transcripts/);
    expect(explanation).toMatch(/rejected and withdrawn count submission/);
  });
});

describe("mounted profile route: the submissions panel reads the pairs endpoint", () => {
  const group = fixtures.contributionCases[1].collectives[0];

  function submissionPairsRows(): HTMLElement[] {
    return [...document.querySelectorAll<HTMLElement>('[data-testid="collective-submission"]')];
  }

  for (const c of fixtures.submissionPairCases) {
    it(c.name, async () => {
      const pairs: CollectiveSubmissionPair[] | null =
        c.pairs === null
          ? null
          : c.pairs.map((p) => ({
              transcript_id: p.transcriptId,
              group_id: group.id,
              title: p.title,
              status: p.status,
              event_num: p.eventNum,
              recorded_at: p.recordedAt,
            }));

      // The first pair's own history, so opening its history control actually
      // shows a populated log rather than proving nothing beyond "it did not
      // crash".
      const firstPairEvents: ShareEvent[] =
        c.pairs === null
          ? []
          : [
              {
                event_num: c.pairs[0].eventNum,
                status: c.pairs[0].status,
                recorded_at: c.pairs[0].recordedAt,
                decided_at: c.pairs[0].status === "pending" ? null : c.pairs[0].recordedAt,
                decided_by_actor:
                  c.pairs[0].status === "pending"
                    ? ""
                    : c.pairs[0].status === "retracted"
                      ? "owner"
                      : c.pairs[0].status === "revoked"
                        ? "collective"
                        : "moderator",
              },
            ];

      await mount({
        profileUsername: OWNER,
        viewerUsername: OWNER,
        contributions: [toWire(group)],
        // Omitting the key models the server's real 404-when-empty
        // disposition; only a non-null pairs case supplies one.
        submissionsByGroupId: pairs === null ? {} : { [group.id]: pairs },
        eventsByGroupAndTranscript:
          c.pairs === null
            ? {}
            : { [eventKey(group.id, c.pairs[0].transcriptId)]: firstPairEvents },
      });

      await waitFor(() => expect(collectiveRows()).toHaveLength(1));
      const user = userEvent.setup();
      await user.click(collectiveRows()[0].querySelector("button")!);

      if (c.pairs === null) {
        // The 404 must render as the ordinary empty state, never as an error
        // or a crash.
        await waitFor(() =>
          expect(
            document.querySelector('[data-testid="collective-submissions-empty"]'),
          ).not.toBeNull(),
        );
        expect(document.querySelector('[data-testid="collective-submission"]')).toBeNull();
        expect(submissionPairsRows()).toHaveLength(0);
        return;
      }

      await waitFor(() => expect(submissionPairsRows()).toHaveLength(c.pairs!.length));
      // The empty-state copy must be genuinely ABSENT as a node, not merely
      // visually hidden — a CSS-only defect can leave textContent unchanged
      // while a person sees nothing wrong, so this checks for the node.
      expect(
        document.querySelector('[data-testid="collective-submissions-empty"]'),
      ).toBeNull();

      const rows = submissionPairsRows();
      rows.forEach((row, i) => {
        expect(row.dataset.transcriptId).toBe(c.pairs![i].transcriptId);
        const chip = row.querySelector<HTMLElement>('[data-testid="collective-submission-status"]');
        expect(chip?.textContent?.trim()).toBe(c.expectedChips[i]);
      });

      // Every pair — including a fully-withdrawn one — keeps its history
      // control, and the control still opens the real event log.
      await user.click(rows[0].querySelector("button")!);
      await waitFor(() =>
        expect(document.querySelector('[data-testid="share-event-log"]')).not.toBeNull(),
      );
    });
  }
});

describe("mounted profile route: the per-collective history is a full event log", () => {
  const group = fixtures.contributionCases[1].collectives[0];
  const transcriptId = "transcript-under-review";

  for (const c of fixtures.eventHistoryCases) {
    it(c.name, async () => {
      const events: ShareEvent[] = c.events.map((e) => ({
        event_num: e.eventNum,
        status: e.status,
        recorded_at: e.recordedAt,
        decided_at: e.decidedAt,
        decided_by_actor: e.decidedByActor,
      }));

      await mount({
        profileUsername: OWNER,
        viewerUsername: OWNER,
        contributions: [toWire(group)],
        submissionsByGroupId: {
          [group.id]: [
            {
              transcript_id: transcriptId,
              group_id: group.id,
              title: "Refactoring the ingest pipeline",
              status: events[events.length - 1].status,
              event_num: events.length,
              recorded_at: events[events.length - 1].recorded_at,
            },
          ],
        },
        eventsByGroupAndTranscript: { [eventKey(group.id, transcriptId)]: events },
      });

      await waitFor(() => expect(collectiveRows()).toHaveLength(1));
      const user = userEvent.setup();

      await user.click(collectiveRows()[0].querySelector("button")!);
      await waitFor(() =>
        expect(document.querySelector('[data-testid="collective-submission"]')).not.toBeNull(),
      );

      const submission = document.querySelector<HTMLElement>(
        '[data-testid="collective-submission"]',
      )!;
      await user.click(submission.querySelector("button")!);
      await waitFor(() =>
        expect(document.querySelector('[data-testid="share-event-log"]')).not.toBeNull(),
      );

      const rows = [...document.querySelectorAll<HTMLElement>('[data-testid="share-event"]')];
      // Every state change is its own numbered row, oldest first — including
      // the withdrawals nobody submitted, which is why the ordinal reads
      // "event" and never "attempt".
      expect(rows).toHaveLength(c.events.length);
      expect(rows.map((r) => r.dataset.eventNum)).toEqual(
        c.events.map((e) => String(e.eventNum)),
      );
      expect(rows.map((r) => r.dataset.eventStatus)).toEqual(c.events.map((e) => e.status));

      rows.forEach((row, i) => {
        expect(row.textContent).toContain(`event ${c.events[i].eventNum}`);
        expect(row.textContent).toContain(c.expectedLabels[i]);
        expect(row.textContent).not.toContain("attempt ");
        // A withdrawal reads as a withdrawal, by actor. It is never filed
        // under a refusal and never phrased as another submission.
        if (c.events[i].status !== "rejected") {
          expect(row.textContent).not.toContain("rejected");
        }
      });

      // The log claims to read oldest first, and the timestamps must agree with
      // that claim. Each row shows the decision time once decided and the
      // submission time while still open; reading down the column must never
      // go backwards, or the log contradicts itself in front of the person it
      // exists to inform.
      const shownTimes = [
        ...document.querySelectorAll<HTMLElement>('[data-testid="share-event-time"]'),
      ].map((el) => el.textContent!.trim());
      expect(shownTimes).toEqual(c.events.map((e) => e.expectedDisplayedAt));
      for (let i = 1; i < shownTimes.length; i += 1) {
        expect(
          shownTimes[i] >= shownTimes[i - 1],
          `event ${i + 1} shows ${shownTimes[i]}, which is BEFORE event ${i}'s ${shownTimes[i - 1]}`,
        ).toBe(true);
      }

      // The actor is a CLASS and the wire carries no name, so nothing here is
      // looked up and nothing can be reported as missing.
      const log = document.querySelector('[data-testid="share-event-log"]')!;
      expect(log.textContent).not.toMatch(/unknown|unavailable|anonymous moderator/i);
    });
  }
});

// The malformed-project notice is SHARED with the home page. It was lifted
// because the two pages had drifted apart on it — one announced the violation
// to assistive technology and the other did not — so the surface that motivated
// the extraction is the one that most needs holding.
describe("mounted profile route: a row with no project identity is reported", () => {
  const HASH = "7".repeat(64);

  it("reports the count, announces it, and still renders the well-formed project", async () => {
    installProfileRESTFixture({
      profileUsername: OWNER,
      viewerUsername: OWNER,
      contributions: [],
      // Two rows cannot be grouped; one can. The count is the page's own claim
      // about the wire it received, so it is asserted exactly.
      library: [
        ["groupable", HASH],
        ["no-identity-a", ""],
        ["no-identity-b", ""],
      ],
    });
    await renderProfileRoute(OWNER);

    const notice = await waitFor(() => {
      const found = document.querySelector('[data-testid="profile-malformed-notice"]');
      expect(found, "the profile page must report a row it could not group").not.toBeNull();
      return found!;
    });
    expect(notice.getAttribute("role")).toBe("alert");
    const text = (notice.textContent ?? "").replace(/\s+/g, " ");
    expect(text).toContain("2 transcripts could not be grouped by project");
    expect(text).toContain("omitted from the project list below");
  });

  it("says nothing when every row carries its project identity", async () => {
    installProfileRESTFixture({
      profileUsername: OWNER,
      viewerUsername: OWNER,
      contributions: [],
      library: [["groupable", HASH]],
    });
    await renderProfileRoute(OWNER);

    // Wait for the same request the other case waits on, so the absence below
    // is an answered page rather than one that simply has not loaded yet.
    await waitFor(() =>
      expect(document.body.textContent ?? "").toContain(OWNER),
    );
    expect(document.querySelector('[data-testid="profile-malformed-notice"]')).toBeNull();
  });
});

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
import { CONTRIBUTION_COUNTER_UNITS } from "@/lib/shareEvents";
import type { ContributedCollective, ShareEvent } from "@/lib/types";

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

        // The three numbers do not measure the same thing, and the UI has to
        // say so where the numbers are: approved and awaiting review count
        // TRANSCRIPTS, rejected counts SUBMISSION ATTEMPTS. Printing them
        // side by side without their units invites a comparison that is not
        // meaningful.
        expect(read("counter-approved-unit")?.textContent?.trim()).toBe(
          CONTRIBUTION_COUNTER_UNITS.approved,
        );
        expect(read("counter-pending-unit")?.textContent?.trim()).toBe(
          CONTRIBUTION_COUNTER_UNITS.pending,
        );
        expect(read("counter-rejected-attempts-unit")?.textContent?.trim()).toBe(
          "submission attempts",
        );
        expect(read("counter-rejected-attempts-unit")?.textContent?.trim()).not.toBe(
          "transcripts",
        );
      });
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
        recorded_at: "2026-08-01T00:00:00.000Z",
        decided_at: e.decidedByActor === "" ? null : "2026-08-02T00:00:00.000Z",
        decided_by_actor: e.decidedByActor,
      }));

      await mount({
        profileUsername: OWNER,
        viewerUsername: OWNER,
        contributions: [toWire(group)],
        sharesByGroupId: {
          [group.id]: [{ id: transcriptId, title: "Refactoring the ingest pipeline", status: "pending" }],
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

      // The actor is a CLASS and the wire carries no name, so nothing here is
      // looked up and nothing can be reported as missing.
      const log = document.querySelector('[data-testid="share-event-log"]')!;
      expect(log.textContent).not.toMatch(/unknown|unavailable|anonymous moderator/i);
    });
  }
});

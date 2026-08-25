import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys } from "@/test/fixtureAssertions";
import { shareEventLabel, submissionPairChip } from "@/lib/shareEvents";
import type { ShareEventActor, ShareEventStatus } from "@/lib/types";

/** Who is looking at the profile in a viewer case. */
export type ProfileViewer = "owner" | "other-signed-in" | "anonymous";

export type ProfileViewerCase = {
  name: string;
  viewer: ProfileViewer;
  expectSection: boolean;
  expectContributionsRequest: boolean;
};

export type ContributedCollectiveFixture = {
  id: string;
  name: string;
  description: string | null;
  approvedCount: number;
  pendingCount: number;
  rejectedAttemptCount: number;
  withdrawnAttemptCount: number;
};

export type ExpectedCounters = {
  approved: number;
  pending: number;
  rejectedAttempts: number;
  withdrawnAttempts: number;
};

export type ContributionCase = {
  name: string;
  collectives: ContributedCollectiveFixture[];
  expectedCollectiveNames: string[];
  expectedCounters: ExpectedCounters[];
};

export type ShareEventFixture = {
  eventNum: number;
  status: ShareEventStatus;
  decidedByActor: ShareEventActor;
  /** When the attempt was opened. */
  recordedAt: string;
  /** When it was decided, or null while it is still open. */
  decidedAt: string | null;
  /** The timestamp the row must SHOW: the decision time once decided, the
   *  submission time while still open. Authored here rather than derived from
   *  the component, so the assertion has an independent expectation to fail
   *  against. */
  expectedDisplayedAt: string;
};

export type EventHistoryCase = {
  name: string;
  events: ShareEventFixture[];
  expectedLabels: string[];
};

/** One (transcript, collective) pair as the owner-only pairs endpoint serves it. */
export type SubmissionPairFixture = {
  transcriptId: string;
  status: ShareEventStatus;
  eventNum: number;
  recordedAt: string;
};

export type SubmissionPairCase = {
  name: string;
  pairs: SubmissionPairFixture[];
  /** The chip text expected for each pair, in the SAME order as `pairs`. */
  expectedChips: string[];
};

export type TranscriptCollectiveFixture = {
  id: string;
  name: string;
};

export type TranscriptCollectivesCase = {
  name: string;
  httpStatus: number;
  collectives: TranscriptCollectiveFixture[];
  expectedCollectiveNames: string[];
};

export type ProfileCollectivesFixtures = {
  viewerCases: ProfileViewerCase[];
  contributionCases: ContributionCase[];
  eventHistoryCases: EventHistoryCase[];
  submissionPairCases: SubmissionPairCase[];
  transcriptCollectivesCases: TranscriptCollectivesCase[];
};

// Required-NAME manifests, not counts. A deleted case fails the loader because
// its name goes missing from the set; adding a case touches this list only.
const requiredViewerCaseNames = [
  "owner-sees-the-section",
  "other-signed-in-viewer-sees-no-section",
  "anonymous-viewer-sees-no-section",
] as const;

const requiredContributionCaseNames = [
  "pending-only-collective-is-listed",
  "refused-then-withdrawn-keeps-its-refusal-count-with-no-transcripts-left",
  "two-approved-one-pending-two-rejected-attempts",
  "withdrawn-once-still-lists-with-a-nonzero-withdrawn-counter",
  "several-collectives-render-in-the-order-served",
] as const;

const requiredSubmissionPairCaseNames = [
  "a-fully-withdrawn-pair-renders-as-a-row-not-empty-copy",
  "a-genuinely-empty-pairs-list-shows-the-none-on-record-copy",
  "a-mixed-collective-renders-every-pair-with-its-own-chip",
] as const;

const requiredEventHistoryCaseNames = [
  "all-five-states-in-ascending-order",
  "timestamps-never-run-backwards-across-a-long-history",
  "repeated-rejection-then-still-pending",
  "withdrawn-by-owner-is-never-phrased-as-a-resubmission",
  "removed-by-the-collective-is-distinct-from-a-refusal",
] as const;

const requiredTranscriptCollectivesCaseNames = [
  "approved-memberships-render-as-named-chips",
  "owner-not-opted-in-returns-an-empty-list-not-a-refusal",
  "transcript-in-no-collective-renders-nothing",
] as const;

const viewerCaseKeys = ["name", "viewer", "expectSection", "expectContributionsRequest"];
const collectiveKeys = [
  "id",
  "name",
  "description",
  "approvedCount",
  "pendingCount",
  "rejectedAttemptCount",
  "withdrawnAttemptCount",
];
const contributionCaseKeys = [
  "name",
  "collectives",
  "expectedCollectiveNames",
  "expectedCounters",
];
const expectedCounterKeys = ["approved", "pending", "rejectedAttempts", "withdrawnAttempts"];
const submissionPairKeys = ["transcriptId", "status", "eventNum", "recordedAt"];
const submissionPairCaseKeys = ["name", "pairs", "expectedChips"];
const eventKeys = [
  "eventNum",
  "status",
  "decidedByActor",
  "recordedAt",
  "decidedAt",
  "expectedDisplayedAt",
];
const eventHistoryCaseKeys = ["name", "events", "expectedLabels"];
const transcriptCollectiveKeys = ["id", "name"];
const transcriptCollectivesCaseKeys = [
  "name",
  "httpStatus",
  "collectives",
  "expectedCollectiveNames",
];

const VIEWERS: readonly ProfileViewer[] = ["owner", "other-signed-in", "anonymous"];
const STATUSES: readonly ShareEventStatus[] = [
  "pending",
  "approved",
  "rejected",
  "retracted",
  "revoked",
];
const ACTORS: readonly ShareEventActor[] = ["", "owner", "collective", "moderator"];

function assertNamesMatch(actual: string[], required: readonly string[], label: string): void {
  const got = [...actual].sort();
  const want = [...required].sort();
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    throw new Error(`${label} case names differ: got ${got.join(", ")}; want ${want.join(", ")}`);
  }
  if (new Set(actual).size !== actual.length) {
    throw new Error(`${label} fixture case names must be unique`);
  }
}

export function loadProfileCollectivesFixtures(): ProfileCollectivesFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/profile-collectives.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("profile-collectives fixture root must be an object");
  }
  assertExactKeys(
    parsed,
    [
      "viewerCases",
      "contributionCases",
      "eventHistoryCases",
      "submissionPairCases",
      "transcriptCollectivesCases",
    ],
    "fixture root",
  );
  const fixtures = parsed as ProfileCollectivesFixtures;

  assertNamesMatch(
    fixtures.viewerCases.map((c) => c.name),
    requiredViewerCaseNames,
    "profile-collectives viewerCases",
  );
  for (const c of fixtures.viewerCases) {
    assertExactKeys(c, viewerCaseKeys, `viewer case ${c.name}`);
    if (!VIEWERS.includes(c.viewer)) {
      throw new Error(
        `viewer case ${c.name}: viewer must be one of ${VIEWERS.join(", ")}, got ${c.viewer}`,
      );
    }
    // The section's absence and the request's absence are ONE requirement, not
    // two: a request fired for a viewer who may not read the answer is the same
    // disclosure moved onto the network. A case that separates them would let a
    // leaking build pass, so the fixture refuses to express one.
    if (c.expectSection !== c.expectContributionsRequest) {
      throw new Error(
        `viewer case ${c.name}: expectSection and expectContributionsRequest must agree — ` +
          `a viewer who sees no section must also cause no contributions request`,
      );
    }
    if ((c.viewer === "owner") !== c.expectSection) {
      throw new Error(
        `viewer case ${c.name}: the section exists for the profile's OWNER and for nobody else, ` +
          `so expectSection must be true exactly when viewer is "owner"`,
      );
    }
  }

  assertNamesMatch(
    fixtures.contributionCases.map((c) => c.name),
    requiredContributionCaseNames,
    "profile-collectives contributionCases",
  );
  for (const c of fixtures.contributionCases) {
    assertExactKeys(c, contributionCaseKeys, `contribution case ${c.name}`);
    for (const g of c.collectives) {
      assertExactKeys(g, collectiveKeys, `contribution case ${c.name} collective`);
    }
    if (
      c.expectedCollectiveNames.length !== c.collectives.length ||
      c.expectedCounters.length !== c.collectives.length
    ) {
      throw new Error(
        `contribution case ${c.name}: expectedCollectiveNames and expectedCounters must each ` +
          `have one entry per served collective, in the order served — every collective the ` +
          `server returns is rendered, including one with no approved contributions`,
      );
    }
    c.expectedCounters.forEach((counters, i) => {
      assertExactKeys(counters, expectedCounterKeys, `contribution case ${c.name} counters[${i}]`);
      const g = c.collectives[i];
      if (
        counters.approved !== g.approvedCount ||
        counters.pending !== g.pendingCount ||
        counters.rejectedAttempts !== g.rejectedAttemptCount ||
        counters.withdrawnAttempts !== g.withdrawnAttemptCount
      ) {
        throw new Error(
          `contribution case ${c.name}: expectedCounters[${i}] must restate the served counters ` +
            `for ${g.name} exactly; got ${JSON.stringify(counters)} against served ` +
            `${g.approvedCount}/${g.pendingCount}/${g.rejectedAttemptCount}/${g.withdrawnAttemptCount}`,
        );
      }
      if (c.expectedCollectiveNames[i] !== g.name) {
        throw new Error(
          `contribution case ${c.name}: expectedCollectiveNames[${i}] must be the served ` +
            `collective's name (${g.name}), got ${c.expectedCollectiveNames[i]}`,
        );
      }
    });
  }

  assertNamesMatch(
    fixtures.eventHistoryCases.map((c) => c.name),
    requiredEventHistoryCaseNames,
    "profile-collectives eventHistoryCases",
  );
  for (const c of fixtures.eventHistoryCases) {
    assertExactKeys(c, eventHistoryCaseKeys, `event-history case ${c.name}`);
    if (c.events.length !== c.expectedLabels.length) {
      throw new Error(
        `event-history case ${c.name}: every event must have exactly one expected label, ` +
          `because the log renders every state change as its own row`,
      );
    }
    c.events.forEach((e, i) => {
      assertExactKeys(e, eventKeys, `event-history case ${c.name} events[${i}]`);
      if (!STATUSES.includes(e.status)) {
        throw new Error(
          `event-history case ${c.name} events[${i}]: status must be one of ` +
            `${STATUSES.join(", ")}, got ${e.status}`,
        );
      }
      if (!ACTORS.includes(e.decidedByActor)) {
        throw new Error(
          `event-history case ${c.name} events[${i}]: decidedByActor must be one of the closed ` +
            `actor classes (${ACTORS.filter((a) => a !== "").join(", ")}) or empty for an ` +
            `undecided event, and is never a user id; got ${e.decidedByActor}`,
        );
      }
      if ((e.status === "pending") !== (e.decidedByActor === "")) {
        throw new Error(
          `event-history case ${c.name} events[${i}]: an undecided (pending) event has no actor ` +
            `and a decided event always names one`,
        );
      }
      if (e.eventNum !== i + 1) {
        throw new Error(
          `event-history case ${c.name} events[${i}]: the server sends the history in event_num ` +
            `ASCENDING order, so events[${i}] must carry eventNum ${i + 1}, got ${e.eventNum}`,
        );
      }
      // Only the LAST row may still be open: uq_share_attempt_open allows one
      // pending attempt per (transcript, collective), and a row stops being
      // pending the moment anything happens to it. An interior pending row is
      // a state the write paths cannot produce, so a case containing one could
      // not fail for a reason a real regression would.
      if (e.status === "pending" && i !== c.events.length - 1) {
        throw new Error(
          `event-history case ${c.name} events[${i}]: only the LAST event may be pending — at most ` +
            `one attempt per (transcript, collective) is ever open, so an interior pending row is ` +
            `a history no write path can produce`,
        );
      }
      const recorded = Date.parse(e.recordedAt);
      const decided = e.decidedAt == null ? null : Date.parse(e.decidedAt);
      if (Number.isNaN(recorded) || (e.decidedAt != null && Number.isNaN(decided!))) {
        throw new Error(
          `event-history case ${c.name} events[${i}]: recordedAt/decidedAt must be parseable ` +
            `timestamps, got ${e.recordedAt} / ${e.decidedAt}`,
        );
      }
      if ((e.decidedAt == null) !== (e.status === "pending")) {
        throw new Error(
          `event-history case ${c.name} events[${i}]: decidedAt is null exactly while the event is ` +
            `still open, and set for every decided event`,
        );
      }
      if (decided != null && decided < recorded) {
        throw new Error(
          `event-history case ${c.name} events[${i}]: an event cannot be decided before it was ` +
            `recorded (${e.decidedAt} < ${e.recordedAt})`,
        );
      }
      if (i > 0) {
        const prev = c.events[i - 1];
        const prevClosed = Date.parse(prev.decidedAt ?? prev.recordedAt);
        if (recorded < prevClosed) {
          throw new Error(
            `event-history case ${c.name} events[${i}]: a new attempt is only opened after the ` +
              `previous one closed, so recordedAt (${e.recordedAt}) cannot precede the previous ` +
              `event's own time (${prev.decidedAt ?? prev.recordedAt})`,
          );
        }
      }
      // The timestamp the row shows, stated in the fixture and checked against
      // the fields it must come from. A log whose displayed times run backwards
      // contradicts its own "oldest first" claim, so the fixture refuses to
      // express one.
      const displayedIso = e.decidedAt ?? e.recordedAt;
      const displayed = `${displayedIso.slice(0, 10)} ${displayedIso.slice(11, 16)}`;
      if (e.expectedDisplayedAt !== displayed) {
        throw new Error(
          `event-history case ${c.name} events[${i}]: expectedDisplayedAt is ` +
            `"${e.expectedDisplayedAt}" but the decided-else-recorded time is "${displayed}"`,
        );
      }
      if (i > 0 && e.expectedDisplayedAt < c.events[i - 1].expectedDisplayedAt) {
        throw new Error(
          `event-history case ${c.name} events[${i}]: the displayed time runs BACKWARDS ` +
            `("${e.expectedDisplayedAt}" after "${c.events[i - 1].expectedDisplayedAt}") — the log ` +
            `is read oldest first, so its times must never decrease`,
        );
      }
      // The expected label is checked against the production labeller here so a
      // fixture cannot quietly encode wording the app does not produce, and so
      // the case rows stay readable as the sentences a person will see.
      const produced = shareEventLabel({ status: e.status, decided_by_actor: e.decidedByActor });
      if (produced !== c.expectedLabels[i]) {
        throw new Error(
          `event-history case ${c.name} events[${i}]: expectedLabels[${i}] is ` +
            `"${c.expectedLabels[i]}" but the application labels this event "${produced}"`,
        );
      }
    });
    // Withdrawals must stay distinguishable from a refusal in the rendered
    // wording, which is the whole reason the three terminal states are kept
    // apart on the wire.
    c.events.forEach((e, i) => {
      const label = c.expectedLabels[i];
      if (e.status === "retracted" && !label.includes("by owner")) {
        throw new Error(
          `event-history case ${c.name} events[${i}]: a retraction must read as the OWNER's act`,
        );
      }
      if (e.status === "revoked" && !label.includes("by collective")) {
        throw new Error(
          `event-history case ${c.name} events[${i}]: a revocation must read as the COLLECTIVE's act`,
        );
      }
      if (e.status !== "rejected" && label.includes("rejected")) {
        throw new Error(
          `event-history case ${c.name} events[${i}]: only a refusal may read as "rejected"; ` +
            `a withdrawal folded into that wording makes the log unreadable`,
        );
      }
    });
  }

  assertNamesMatch(
    fixtures.submissionPairCases.map((c) => c.name),
    requiredSubmissionPairCaseNames,
    "profile-collectives submissionPairCases",
  );
  for (const c of fixtures.submissionPairCases) {
    assertExactKeys(c, submissionPairCaseKeys, `submission-pair case ${c.name}`);
    if (c.pairs.length !== c.expectedChips.length) {
      throw new Error(
        `submission-pair case ${c.name}: every served pair must have exactly one expected chip, ` +
          `in the order served`,
      );
    }
    // The defect this fixture family exists to prevent: the "none on record"
    // copy appearing beside a nonempty list, or a row silently missing when
    // the list is genuinely empty. Tying both cases to the SAME boolean is
    // what makes it impossible for a fixture to express the contradiction.
    const isGenuinelyEmpty = c.name === "a-genuinely-empty-pairs-list-shows-the-none-on-record-copy";
    if (isGenuinelyEmpty !== (c.pairs.length === 0)) {
      throw new Error(
        `submission-pair case ${c.name}: only the case named for a genuinely empty pairs list may ` +
          `serve zero pairs; every other case must serve at least one pair, or it cannot prove the ` +
          `row-instead-of-vanishing behavior this endpoint exists for`,
      );
    }
    c.pairs.forEach((p, i) => {
      assertExactKeys(p, submissionPairKeys, `submission-pair case ${c.name} pairs[${i}]`);
      if (!STATUSES.includes(p.status)) {
        throw new Error(
          `submission-pair case ${c.name} pairs[${i}]: status must be one of ` +
            `${STATUSES.join(", ")}, got ${p.status}`,
        );
      }
      if (!Number.isInteger(p.eventNum) || p.eventNum < 1) {
        throw new Error(
          `submission-pair case ${c.name} pairs[${i}]: eventNum must be a positive integer, got ${p.eventNum}`,
        );
      }
      if (Number.isNaN(Date.parse(p.recordedAt))) {
        throw new Error(
          `submission-pair case ${c.name} pairs[${i}]: recordedAt must be a parseable timestamp, ` +
            `got ${p.recordedAt}`,
        );
      }
      // The chip is checked against the production chip labeller so the
      // fixture cannot encode wording the app does not actually produce, the
      // same discipline `shareEventLabel` gets above for the history rows.
      const produced = submissionPairChip(p.status);
      if (produced !== c.expectedChips[i]) {
        throw new Error(
          `submission-pair case ${c.name} pairs[${i}]: expectedChips[${i}] is ` +
            `"${c.expectedChips[i]}" but the application chips this pair "${produced}"`,
        );
      }
      // retracted and revoked BOTH chip as "withdrawn" — the whole point of
      // this endpoint is that a withdrawn pair renders as a row, not as a
      // disappearance.
      if ((p.status === "retracted" || p.status === "revoked") && c.expectedChips[i] !== "withdrawn") {
        throw new Error(
          `submission-pair case ${c.name} pairs[${i}]: a ${p.status} pair must chip as "withdrawn", ` +
            `got "${c.expectedChips[i]}"`,
        );
      }
    });
  }

  assertNamesMatch(
    fixtures.transcriptCollectivesCases.map((c) => c.name),
    requiredTranscriptCollectivesCaseNames,
    "profile-collectives transcriptCollectivesCases",
  );
  for (const c of fixtures.transcriptCollectivesCases) {
    assertExactKeys(c, transcriptCollectivesCaseKeys, `transcript-collectives case ${c.name}`);
    if (c.httpStatus !== 200) {
      throw new Error(
        `transcript-collectives case ${c.name}: this endpoint answers 200 with a list in every ` +
          `case, including one where the visibility rule or the owner's contributor opt-in ` +
          `withholds everything — a refusal status would itself confirm that memberships exist, ` +
          `so no case may declare one (got ${c.httpStatus})`,
      );
    }
    for (const g of c.collectives) {
      assertExactKeys(g, transcriptCollectiveKeys, `transcript-collectives case ${c.name} collective`);
    }
    if (
      c.expectedCollectiveNames.length !== c.collectives.length ||
      c.expectedCollectiveNames.some((n, i) => n !== c.collectives[i].name)
    ) {
      throw new Error(
        `transcript-collectives case ${c.name}: expectedCollectiveNames must restate the served ` +
          `collective names in the order served`,
      );
    }
  }

  return fixtures;
}

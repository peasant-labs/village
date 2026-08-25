import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys } from "@/test/fixtureAssertions";
import { shareEventLabel } from "@/lib/shareEvents";
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
};

export type ExpectedCounters = {
  approved: number;
  pending: number;
  rejectedAttempts: number;
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
};

export type EventHistoryCase = {
  name: string;
  events: ShareEventFixture[];
  expectedLabels: string[];
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
  "two-approved-one-pending-two-rejected-attempts",
  "a-fully-withdrawn-collective-still-lists-with-every-counter-zero",
  "several-collectives-render-in-the-order-served",
] as const;

const requiredEventHistoryCaseNames = [
  "all-five-states-in-ascending-order",
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
];
const contributionCaseKeys = [
  "name",
  "collectives",
  "expectedCollectiveNames",
  "expectedCounters",
];
const expectedCounterKeys = ["approved", "pending", "rejectedAttempts"];
const eventKeys = ["eventNum", "status", "decidedByActor"];
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
    ["viewerCases", "contributionCases", "eventHistoryCases", "transcriptCollectivesCases"],
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
        counters.rejectedAttempts !== g.rejectedAttemptCount
      ) {
        throw new Error(
          `contribution case ${c.name}: expectedCounters[${i}] must restate the served counters ` +
            `for ${g.name} exactly; got ${JSON.stringify(counters)} against served ` +
            `${g.approvedCount}/${g.pendingCount}/${g.rejectedAttemptCount}`,
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

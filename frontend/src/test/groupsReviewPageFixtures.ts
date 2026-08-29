import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys } from "@/test/fixtureAssertions";
import type { PendingShare } from "@/lib/review/types";

/** Terse per-row shorthand as written in the YAML: only the fields a case
 *  cares about are set. {@link toPendingShare} fills the rest with a
 *  realistic constant. */
export interface PendingShareSpec {
  transcript_id: string;
  local_id: string;
  title?: string | null;
  parent_session_id?: string | null;
  project_hash: string;
  project_name?: string | null;
  branch?: string | null;
  owner_id?: string;
  owner_username: string;
  owner_is_discoverable?: boolean;
  model_provider?: string;
}

const ROW_SPEC_KEYS = [
  "transcript_id",
  "local_id",
  "title",
  "parent_session_id",
  "project_hash",
  "project_name",
  "branch",
  "owner_id",
  "owner_username",
  "owner_is_discoverable",
  "model_provider",
];

export function toPendingShare(spec: PendingShareSpec): PendingShare {
  return {
    transcript_id: spec.transcript_id,
    title: spec.title ?? spec.transcript_id,
    model_provider: spec.model_provider ?? "claude-code",
    // A queue can hold several publishers' rows, so the owner defaults to one
    // derived from the username rather than a single shared constant: two rows
    // by different publishers must never fold into each other.
    owner_id: spec.owner_id ?? `user-${spec.owner_username}`,
    local_id: spec.local_id,
    parent_session_id: spec.parent_session_id ?? null,
    project_hash: spec.project_hash,
    project_name: spec.project_name ?? null,
    branch: spec.branch ?? "main",
    owner_username: spec.owner_username,
    owner_is_discoverable: spec.owner_is_discoverable ?? true,
    shared_at: "2026-08-20T10:00:00Z",
  };
}

export interface ReviewPageCase {
  name: string;
  why: string;
  /** The signed-in viewer's handle. */
  viewer: string;
  /** `your_role` the `/groups/{id}` payload reports. */
  role: "member" | "owner";
  pending: PendingShareSpec[];
  /** The queue as a REFETCH answers it, when a case needs the list to change
   *  under the reviewer. Omitted means the queue answers the same rows every
   *  time. */
  pendingAfterRefetch?: string[];
  /** The transcript ids the reviewer ticks before acting. */
  select: string[];
  /** What the batch endpoint answers. */
  decided: string[];
  alreadyDecided: string[];
  expect: Record<string, unknown>;
}

const CASE_KEYS = [
  "name",
  "why",
  "viewer",
  "role",
  "pending",
  "pendingAfterRefetch",
  "select",
  "decided",
  "alreadyDecided",
  "expect",
];

/**
 * Deletion guard: the fixture must carry exactly these cases, by exact NAME
 * (never a bare count). Each one exists because losing it hides a distinct
 * real failure: a flat queue nobody can navigate, a child submission read as
 * unrelated work, one publisher's submission captured under another's, a
 * selection made entirely of rows nobody can see, one request per project instead of one per action, a page
 * that can only approve, a stale row that vanishes without explanation, a
 * non-owner shown a reviewer's controls, and an empty queue that looks like a
 * failed load.
 */
const requiredCaseNames = [
  "owner_reads_the_queue_grouped_by_project",
  "child_session_folds_under_its_parent",
  "two_publishers_sharing_a_session_id_do_not_fold",
  "hidden_child_selection_is_never_invisible",
  "a_row_that_leaves_the_queue_leaves_the_selection",
  "a_filtered_row_keeps_its_selection",
  "approve_selection_sends_one_request",
  "reject_selection_sends_the_reject_decision",
  "already_decided_row_is_marked_stale",
  "non_owner_sees_the_owner_only_notice",
  "empty_queue_says_nothing_is_waiting",
] as const;

export function loadGroupsReviewPageFixtures(): ReviewPageCase[] {
  const fixturePath = resolve(process.cwd(), "src/testdata/groups-review-page.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("groups-review-page fixture root must be an object");
  }
  assertExactKeys(parsed, ["cases"], "fixture root");
  const cases = (parsed as { cases: ReviewPageCase[] }).cases;

  const names = cases.map((c) => c.name).sort();
  const wanted = [...requiredCaseNames].sort();
  if (JSON.stringify(names) !== JSON.stringify(wanted)) {
    throw new Error(
      `groups-review-page fixture is missing required case(s) or carries unknown case(s): got ${names.join(", ")}; want ${wanted.join(", ")}`,
    );
  }

  for (const testCase of cases) {
    // pendingAfterRefetch is optional: only a case that needs the queue to
    // change under the reviewer carries it. Its presence must still be EXACT,
    // so the wanted key set is computed per case rather than allowed blindly.
    const wantedKeys = CASE_KEYS.filter(
      (key) => key !== "pendingAfterRefetch" || "pendingAfterRefetch" in testCase,
    );
    assertExactKeys(testCase as unknown as object, wantedKeys, `case ${testCase.name}`);
    if (testCase.why.trim() === "") {
      throw new Error(`case ${testCase.name}: states no reason it exists`);
    }
    for (const row of testCase.pending) {
      assertExactKeys(
        row as unknown as object,
        ROW_SPEC_KEYS.filter((key) => key in row),
        `case ${testCase.name} row ${row.transcript_id}`,
      );
    }
  }
  return cases;
}

export function reviewCaseByName(cases: ReviewPageCase[], name: string): ReviewPageCase {
  const found = cases.find((c) => c.name === name);
  if (!found) throw new Error(`groups-review-page fixture has no case named ${name}`);
  return found;
}

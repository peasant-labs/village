import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { expect, it } from "vitest";
import type { VillageSessionListItem } from "@peasant-labs/schema";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { groupedContributionBatches, groupedReviewSelection, mergeContributionBatches } from "./groupedSelection";

/**
 * The collective grouped selection contract, driven by
 * `src/testdata/collective-grouped-selection.yaml`:
 *
 *  • a contribution batch is the UNION of the ordinary tree selection and the
 *    helper members ticked individually, keyed by project, one identity once;
 *  • a review decision names explicit pending submission ids only — a context
 *    container or a row that is not a live submission is refused rather than
 *    sent.
 */

interface RowSpec {
  name: string;
  id: string;
  project: string;
}

interface MergeCase {
  name: string;
  tree: Record<string, string[]>;
  helpers: string[];
  expected: Record<string, string[]>;
}

type ReviewKind = "pending" | "contributable" | "context";

interface ReviewCase {
  name: string;
  kind: ReviewKind;
  selected: string[];
  expected_ids?: string[];
  error_contains?: string;
}

interface Fixture {
  rows: RowSpec[];
  merge_cases: MergeCase[];
  review_cases: ReviewCase[];
}

const REQUIRED_MERGE = [
  "merge-keeps-the-tree-selection",
  "merge-adds-helper-ids-under-their-own-project",
  "merge-sends-a-repeated-identity-once",
];
const REQUIRED_REVIEW = [
  "review-names-the-explicit-individual-ids",
  "review-names-a-repeated-identity-once",
  "review-refuses-a-context-container",
  "review-refuses-a-row-that-is-not-pending",
];

function requireNames(actual: string[], required: string[], label: string): void {
  const got = [...actual].sort();
  const want = [...required].sort();
  if (new Set(actual).size !== actual.length || JSON.stringify(got) !== JSON.stringify(want)) {
    throw new Error(`collective grouped selection ${label} names differ: got ${got.join(", ")}; want ${want.join(", ")}`);
  }
}

const fixture: Fixture = parse(
  readFileSync(resolve(process.cwd(), "src/testdata/collective-grouped-selection.yaml"), "utf8"),
  { strict: true },
);
const byName = new Map(fixture.rows.map((row) => [row.name, row]));
requireNames(fixture.merge_cases.map((value) => value.name), REQUIRED_MERGE, "merge case");
requireNames(fixture.review_cases.map((value) => value.name), REQUIRED_REVIEW, "review case");

function helperItem(row: RowSpec, kind: ReviewKind): VillageSessionListItem {
  const session = makeTranscriptFixture({
    id: row.id,
    local_id: `ses_${row.name.toLowerCase()}`,
    owner_id: "30000000-0000-4000-8000-000000000020",
    title: row.name,
    project_hash: row.project,
    project_display_name: row.project,
  });
  if (kind === "contributable") {
    return {
      kind: "transcript",
      transcript: {
        session,
        contributable: {
          id: row.id,
          local_id: session.local_id,
          title: row.name,
          visibility: session.visibility,
          project_hash: session.project_hash,
          project_display_name: session.project_display_name,
          project_name_source: session.project_name_source,
          git_branch: session.git_branch,
          parent_session_id: session.parent_session_id,
          session_origin: session.session_origin,
          model_provider: session.model_provider,
          published_at: session.published_at,
          already_shared: false,
        },
      },
    } as VillageSessionListItem;
  }
  return {
    kind: "transcript",
    transcript: {
      session,
      pending: {
        transcript_id: row.id,
        title: row.name,
        model_provider: session.model_provider,
        owner_id: session.owner_id,
        local_id: session.local_id,
        parent_session_id: null,
        project_hash: session.project_hash,
        project_name: session.project_name,
        branch: session.git_branch,
        owner_username: "member-owner",
        owner_is_discoverable: true,
        shared_at: "2026-09-01T10:00:00Z",
      },
    },
  } as VillageSessionListItem;
}

for (const testCase of fixture.merge_cases) {
  it(testCase.name, () => {
    // The fixture states identities by their readable row names; the wire
    // carries ids, so each side is mapped through the one row table.
    const ids = (names: string[]) => names.map((name) => byName.get(name)!.id);
    const treeSelection = new Map(
      Object.entries(testCase.tree).map(([project, names]) => [project, ids(names)]),
    );
    // The helper side is built by the SAME production function the page calls,
    // so the merge is exercised as the mounted path composes it.
    const helpers = testCase.helpers.map((name) => helperItem(byName.get(name)!, "contributable"));
    const merged = mergeContributionBatches(treeSelection, groupedContributionBatches(helpers));
    const expected = new Map(
      Object.entries(testCase.expected).map(([project, names]) => [project, ids(names)]),
    );
    expect(merged).toEqual(expected);
  });
}

for (const testCase of fixture.review_cases) {
  it(testCase.name, () => {
    const selected = testCase.selected.map((name) => {
      const row = byName.get(name)!;
      if (testCase.kind === "context") {
        return {
          kind: "context_container",
          context: { groupId: "hg_context", ownerStatus: "known_unavailable" },
        } as VillageSessionListItem;
      }
      return helperItem(row, testCase.kind);
    });
    if (testCase.error_contains) {
      expect(() => groupedReviewSelection(selected)).toThrow(testCase.error_contains);
    } else {
      expect(groupedReviewSelection(selected)).toEqual(
        (testCase.expected_ids ?? []).map((name) => byName.get(name)!.id),
      );
    }
  });
}

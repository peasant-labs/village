import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys } from "@/test/fixtureAssertions";
import type { ContributableTranscript } from "@/lib/contribute/types";

/** Terse per-row shorthand as written in the YAML: only the fields a case
 *  cares about are set. {@link toContributableTranscript} fills the rest with
 *  a realistic constant. */
export interface RowSpec {
  id: string;
  local_id: string;
  title?: string | null;
  visibility?: "public" | "private" | "shared";
  project_hash: string;
  project_display_name: string;
  git_branch?: string | null;
  parent_session_id?: string | null;
  model_provider?: string;
  already_shared?: boolean;
}

const ROW_SPEC_KEYS = [
  "id",
  "local_id",
  "title",
  "visibility",
  "project_hash",
  "project_display_name",
  "git_branch",
  "parent_session_id",
  "model_provider",
  "already_shared",
];

export function toContributableTranscript(spec: RowSpec): ContributableTranscript {
  return {
    id: spec.id,
    local_id: spec.local_id,
    title: spec.title ?? spec.id,
    visibility: spec.visibility ?? "public",
    project_hash: spec.project_hash,
    project_display_name: spec.project_display_name,
    project_name_source: "consented",
    git_branch: spec.git_branch ?? null,
    parent_session_id: spec.parent_session_id ?? null,
    session_origin: "user",
    model_provider: spec.model_provider ?? "claude-code",
    published_at: "2026-08-20T10:00:00Z",
    already_shared: spec.already_shared ?? false,
  };
}

export type ContributeTreeCaseKind = "tree" | "post" | "page" | "preview";

interface BaseCase {
  name: string;
  kind: ContributeTreeCaseKind;
  why: string;
  rows: RowSpec[];
  expect: Record<string, unknown>;
}

export interface TreeCase extends BaseCase {
  kind: "tree";
}

export interface PostCase extends BaseCase {
  kind: "post";
  selectionIds: string[];
  failing: string[];
  failureMessage?: string;
}

export interface PageCase extends BaseCase {
  kind: "page";
  /** Failed HTTP reads, in order, before a successful transcript response. */
  readFailures?: number[];
}

export interface PreviewCase extends BaseCase {
  kind: "preview";
  readFailures?: number[];
}

export type ContributeTreeCase = TreeCase | PostCase | PageCase | PreviewCase;

const KIND_KEYS: Record<ContributeTreeCaseKind, string[]> = {
  tree: ["name", "kind", "why", "rows", "expect"],
  post: ["name", "kind", "why", "rows", "selectionIds", "failing", "failureMessage", "expect"],
  page: ["name", "kind", "why", "rows", "readFailures", "expect"],
  preview: ["name", "kind", "why", "rows", "readFailures", "expect"],
};

/** Deletion guard: every row this slice's spec names must be present, by
 *  exact name -- never a bare count (a deleted or renamed row fails loudly
 *  here instead of silently shrinking the suite). */
const requiredCaseNames = [
  "two_branch_project_collapses_children",
  "orphans_under_synthetic_node",
  "orphans_node_never_sent",
  "private_selection_opens_confirm",
  "multi_harness_counts",
  "search_narrows_rows",
  "already_shared_disabled",
  "one_post_per_project",
  "failure_continues_and_reports",
  "failed_projects_stay_selected",
  "header_counts_selected_and_sessions",
  "select_all_selects_every_leaf",
  "starter_selection_takes_its_started_session",
  "preview_renders_on_click",
  "preview_hides_graph_and_owner_actions",
  "failed_preview_metadata_retries_the_same_transcript",
  "refused_preview_metadata_is_not_not_found",
  "missing_preview_metadata_stays_not_found",
  "failed_contributable_read_can_retry_to_real_rows",
  "refused_contributable_read_is_not_empty_success",
] as const;

export function loadGroupsContributeTreeFixtures(): ContributeTreeCase[] {
  const fixturePath = resolve(process.cwd(), "src/testdata/groups-contribute-tree.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("groups-contribute-tree fixture root must be an object");
  }
  assertExactKeys(parsed, ["cases"], "fixture root");
  const cases = (parsed as { cases: ContributeTreeCase[] }).cases;

  const names = cases.map((c) => c.name).sort();
  const wanted = [...requiredCaseNames].sort();
  if (JSON.stringify(names) !== JSON.stringify(wanted)) {
    throw new Error(
      `groups-contribute-tree fixture is missing required row(s) or carries unknown row(s): got ${names.join(", ")}; want ${wanted.join(", ")}`,
    );
  }

  for (const c of cases) {
    const keys = KIND_KEYS[c.kind];
    if (!keys) {
      throw new Error(`case ${c.name}: unknown kind ${String(c.kind)}`);
    }
    // Optional mutation messages and failed read sequences belong only to
    // cases exercising those responses; all other fields stay exact.
    const wantedKeys = keys.filter((k) => !["failureMessage", "readFailures"].includes(k) || k in c);
    assertExactKeys(c as unknown as object, wantedKeys, `case ${c.name}`);
    if (c.why.trim() === "") {
      throw new Error(`case ${c.name}: "why" must be a non-empty justification -- a row nobody can justify cannot be maintained`);
    }
    for (const row of c.rows) {
      const unknown = Object.keys(row).filter((k) => !ROW_SPEC_KEYS.includes(k));
      if (unknown.length > 0) {
        throw new Error(
          `case ${c.name} row ${row.id}: unknown field(s) ${unknown.join(", ")}; valid fields are ${ROW_SPEC_KEYS.join(", ")}`,
        );
      }
      if (!("id" in row) || !("local_id" in row) || !("project_hash" in row) || !("project_display_name" in row)) {
        throw new Error(`case ${c.name} row missing a required field (id/local_id/project_hash/project_display_name)`);
      }
    }
  }

  return cases;
}

export function caseByName<K extends ContributeTreeCaseKind>(
  cases: ContributeTreeCase[],
  kind: K,
  name: (typeof requiredCaseNames)[number],
): Extract<ContributeTreeCase, { kind: K }> {
  const found = cases.find((c) => c.name === name);
  if (!found) {
    throw new Error(`groups-contribute-tree fixture has no case named ${name}`);
  }
  if (found.kind !== kind) {
    throw new Error(`groups-contribute-tree case ${name} is kind ${found.kind}, expected ${kind}`);
  }
  return found as Extract<ContributeTreeCase, { kind: K }>;
}

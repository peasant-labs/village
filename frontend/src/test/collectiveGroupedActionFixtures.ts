import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { zHelperGroupSummary, type HelperGroupSummary, type VillageSessionListItem } from "@peasant-labs/schema";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import { memberUUID } from "@/test/groupedHelperMountFixtures";
import type { ContributableTranscript } from "@/lib/contribute/types";
import type { PendingShare } from "@/lib/review/types";

/**
 * Typed loader for `src/testdata/collective-grouped-actions.yaml`.
 *
 * The corpus is the one place a mounted collective helper case is stated: the
 * tree row the group hangs under, the group the server grouped with it, the
 * member a person ticks individually, and the exact explicit-id body the route
 * must send. This loader refuses a corpus that loses a required NAME, repeats
 * one, states an unknown field, selects a member no declared page serves, or
 * declares an expectation its own rows cannot produce.
 */

export const GROUP_KEYS = ["owner", "other", "context"] as const;
export type GroupKey = (typeof GROUP_KEYS)[number];
export type ActionSurface = "browse" | "contribute" | "review";

export interface MemberSpec {
  name: string;
  title: string;
}

export interface RowSpec {
  name: string;
  id: string;
  local_id: string;
  title: string;
}

export interface CollectiveActionCase {
  name: string;
  surface: ActionSurface;
  why: string;
  row: string;
  group: GroupKey;
  select?: string;
  context?: boolean;
  /** Member labels whose explicit ids the mutation body must name, in order. */
  expected_ids: string[];
}

export interface CollectiveGroupedActionFixtures {
  groupId: string;
  projectHash: string;
  projectName: string;
  groups: Record<GroupKey, HelperGroupSummary>;
  members: Record<GroupKey, MemberSpec[]>;
  rows: RowSpec[];
  cases: CollectiveActionCase[];
}

const REQUIRED_CASES = [
  "browse-renders-the-group-under-its-owner-row",
  "contribute-selects-one-helper-only",
  "contribute-context-container-is-not-selectable",
  "review-selects-one-helper-only",
  "review-other-owner-group-remains-independent",
];

const CASE_FIELDS = new Set([
  "name",
  "surface",
  "why",
  "row",
  "group",
  "select",
  "context",
  "expected_ids",
]);

function fail(where: string, reason: string): never {
  throw new Error(`collective grouped action fixture ${where} ${reason}`);
}

export function loadCollectiveGroupedActionFixtures(): CollectiveGroupedActionFixtures {
  const root = parse(
    readFileSync(resolve(process.cwd(), "src/testdata/collective-grouped-actions.yaml"), "utf8"),
    { strict: true },
  );
  assertExactKeys(root, ["groupId", "projectHash", "projectName", "groups", "members", "rows", "cases"], "root");

  const groups = {} as Record<GroupKey, HelperGroupSummary>;
  for (const key of GROUP_KEYS) {
    groups[key] = zHelperGroupSummary.parse(root.groups[key]);
  }
  const members = {} as Record<GroupKey, MemberSpec[]>;
  for (const key of GROUP_KEYS) {
    const declared: MemberSpec[] = root.members[key];
    if (!Array.isArray(declared) || declared.length === 0) {
      fail(`members.${key}`, "must declare at least one member");
    }
    for (const member of declared) {
      assertExactKeys(member, ["name", "title"], `members.${key}`);
    }
    members[key] = declared;
  }

  const rows: RowSpec[] = root.rows;
  const rowByName = new Map(rows.map((row) => [row.name, row]));
  for (const row of rows) {
    assertExactKeys(row, ["name", "id", "local_id", "title"], `row ${row.name}`);
  }

  const rawCases: CollectiveActionCase[] = root.cases;
  assertNamesMatch(rawCases.map((value) => value.name), REQUIRED_CASES, "collective grouped helper action");

  const memberNames = new Set<string>();
  for (const key of GROUP_KEYS) {
    for (const member of members[key]) memberNames.add(member.name);
  }

  const cases = rawCases.map((value): CollectiveActionCase => {
    const name = value.name;
    for (const field of Object.keys(value)) {
      if (!CASE_FIELDS.has(field)) fail(`case "${name}"`, `states an unknown field "${field}"`);
    }
    if (value.surface !== "contribute" && value.surface !== "review" && value.surface !== "browse") {
      fail(`case "${name}"`, `states an unknown surface "${String(value.surface)}"`);
    }
    if (!value.why?.trim()) fail(`case "${name}"`, "states no reason it exists");
    if (!rowByName.has(value.row)) fail(`case "${name}"`, `names an undeclared row "${value.row}"`);
    if (!(GROUP_KEYS as readonly string[]).includes(value.group)) {
      fail(`case "${name}"`, `names an unknown group "${String(value.group)}"`);
    }
    if (!Array.isArray(value.expected_ids)) fail(`case "${name}"`, "must state expected_ids as a list");
    for (const label of value.expected_ids) {
      if (!memberNames.has(label)) fail(`case "${name}"`, `expects member "${label}", which no member page serves`);
    }
    if (value.group === "context" && value.select !== undefined) {
      fail(`case "${name}"`, "selects a member of a helper-only context container, which has no selectable row");
    }
    if (value.select !== undefined && !members[value.group].some((member) => member.name === value.select)) {
      fail(`case "${name}"`, `selects "${value.select}", which group "${value.group}" does not serve`);
    }
    if (value.select !== undefined && !value.expected_ids.includes(value.select)) {
      fail(`case "${name}"`, `selects "${value.select}" but does not expect its id in the body`);
    }
    if (value.select === undefined && value.expected_ids.length > 0) {
      fail(`case "${name}"`, "expects selected ids but states nothing to select");
    }
    return value;
  });

  return {
    groupId: root.groupId,
    projectHash: root.projectHash,
    projectName: root.projectName,
    groups,
    members,
    rows,
    cases,
  };
}

/** The published display row for one declared member. */
export function memberItem(label: string, title: string, surface: ActionSurface, groupKey: GroupKey): VillageSessionListItem {
  const id = memberUUID(label);
  const localID = `ses_${label.toLowerCase()}`;
  const session = makeTranscriptFixture({
    id,
    local_id: localID,
    owner_id: `30000000-0000-4000-8000-${String(GROUP_KEYS.indexOf(groupKey) + 1).padStart(12, "0")}`,
    title,
    project_hash: "a".repeat(64),
    project_name: "peasant",
    project_display_name: "peasant",
    parent_session_id: null,
    session_origin: "user",
  });
  if (surface === "browse") {
    return { kind: "transcript", transcript: { session } } as VillageSessionListItem;
  }
  if (surface === "contribute") {
    return {
      kind: "transcript",
      transcript: {
        session,
        contributable: {
          id,
          local_id: localID,
          title,
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
        transcript_id: id,
        title,
        model_provider: session.model_provider,
        owner_id: session.owner_id,
        local_id: localID,
        parent_session_id: session.parent_session_id,
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

/** The member page the group's own opaque scope is answered from. */
export function memberPage(fixtures: CollectiveGroupedActionFixtures, groupKey: GroupKey, surface: ActionSurface) {
  const members = fixtures.members[groupKey].map((member) =>
    memberItem(member.name, member.title, surface, groupKey),
  );
  return { members, page: 1, limit: 20, total: members.length };
}

/** The flat tree rows the route renders under its project/branch fold.
 *
 * The flat route is the SAME submission set the grouped view nests, so the
 * helper members appear here too — as ordinary rows, exactly as they did before
 * the grouping existed. The grouped read only adds the disclosure that hangs
 * them off their owner. */
export function flatContributeRows(fixtures: CollectiveGroupedActionFixtures): ContributableTranscript[] {
  const owned: ContributableTranscript[] = fixtures.rows.map((row) => ({
    id: row.id,
    local_id: row.local_id,
    title: row.title,
    visibility: "public",
    project_hash: fixtures.projectHash,
    project_display_name: fixtures.projectName,
    project_name_source: "consented",
    git_branch: "main",
    parent_session_id: null,
    session_origin: "user",
    model_provider: "claude-code",
    published_at: "2026-09-01T10:00:00Z",
    already_shared: false,
  }));
  const members = GROUP_KEYS.flatMap((key) =>
    fixtures.members[key].map((member) => {
      const item = memberItem(member.name, member.title, "contribute", key);
      return item.transcript!.contributable as ContributableTranscript;
    }),
  );
  return [...owned, ...members];
}

/** The flat collective browse rows: the same submission set the grouped page
 *  nests, drawn by the pre-existing list. */
export function flatBrowseRows(fixtures: CollectiveGroupedActionFixtures) {
  return fixtures.rows.map((row) => ({
    ...makeTranscriptFixture({
      id: row.id,
      local_id: row.local_id,
      owner_id: "30000000-0000-4000-8000-000000000010",
      title: row.title,
      project_hash: fixtures.projectHash,
      project_name: fixtures.projectName,
      project_display_name: fixtures.projectName,
      parent_session_id: null,
      visibility: "shared",
    }),
    owner_username: "member-owner",
    owner_avatar_url: null,
    owner_is_discoverable: true,
  }));
}

export function flatPendingRows(fixtures: CollectiveGroupedActionFixtures): PendingShare[] {
  const owned: PendingShare[] = fixtures.rows.map((row) => ({
    transcript_id: row.id,
    title: row.title,
    model_provider: "claude-code",
    owner_id: "30000000-0000-4000-8000-000000000010",
    local_id: row.local_id,
    parent_session_id: null,
    project_hash: fixtures.projectHash,
    project_name: fixtures.projectName,
    branch: "main",
    owner_username: "member-owner",
    owner_is_discoverable: true,
    shared_at: "2026-09-01T10:00:00Z",
  }));
  const members = GROUP_KEYS.flatMap((key) =>
    fixtures.members[key].map((member) => {
      const item = memberItem(member.name, member.title, "review", key);
      return item.transcript!.pending as PendingShare;
    }),
  );
  return [...owned, ...members];
}

/**
 * The grouped items one case's read serves: the owner row the case names, with
 * the declared group attached, plus a helper-only context container when the
 * case asks for one.
 */
export function groupedItems(
  fixtures: CollectiveGroupedActionFixtures,
  testCase: CollectiveActionCase,
): VillageSessionListItem[] {
  const items: VillageSessionListItem[] = fixtures.rows.map((row) => {
    const groups = row.name === testCase.row ? [fixtures.groups[testCase.group]] : [];
    return {
      kind: "transcript",
      transcript: {
        session: makeTranscriptFixture({
          id: row.id,
          local_id: row.local_id,
          owner_id: "30000000-0000-4000-8000-000000000010",
          title: row.title,
          project_hash: fixtures.projectHash,
          project_name: fixtures.projectName,
          project_display_name: fixtures.projectName,
          parent_session_id: null,
        }),
      },
      helperGroups: groups.length > 0 ? groups : undefined,
    } as VillageSessionListItem;
  });
  if (testCase.context) {
    items.push({
      kind: "context_container",
      context: { groupId: fixtures.groups.context.groupId, ownerStatus: "known_unavailable" },
      helperGroups: [fixtures.groups.context],
    } as VillageSessionListItem);
  }
  return items;
}

/** The `GET /groups/{id}?view=grouped` body: the flat collective metadata with
 *  only its transcript collection replaced by the grouped page. */
export function groupedDetailPayload(
  fixtures: CollectiveGroupedActionFixtures,
  testCase: CollectiveActionCase,
) {
  const items = groupedItems(fixtures, testCase);
  return {
    group: {
      id: fixtures.groupId,
      name: "grouped actions collective",
      description: null,
      created_by: "30000000-0000-4000-8000-000000000099",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      acceptance_mode: "curated",
      data_access: "members_only",
      linked_github_org: null,
      display_members: true,
      transcript_deletion_policy: "user_choice",
    },
    members: [],
    stats: {
      total_transcripts: fixtures.rows.length,
      contributor_count: 1,
      total_turns: 0,
      total_duration_ms: 0,
      total_tokens: 0,
    },
    models: [],
    contributors: [],
    can_read: true,
    your_role: "owner",
    pending_members: [],
    transcriptList: {
      items,
      page: 1,
      limit: 100,
      totalItems: items.length,
      ordinarySessionTotal: fixtures.rows.length,
      helperThreadTotal: 2,
    },
  };
}

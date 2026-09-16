import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { zHelperGroupSummary, type HelperGroupSummary, type VillageSessionListItem } from "@peasant-labs/schema";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import { memberUUID } from "@/test/groupedHelperMountFixtures";
import { ordinaryOwnerUUID } from "@/test/groupedHelperSurfaceFixtures";
import { GROUPED_TOP_LEVEL_PAGE_SIZE } from "@/lib/queries/helperGroups";
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

/** Whether a fallback case's flat result carries the case's owner row. */
export type FallbackFlat = "empty" | "rendered";
/** The grouped item a fallback case's page serves. */
export type FallbackGrouped = "owner" | "context";

export interface CollectiveFallbackCase {
  name: string;
  surface: ActionSurface;
  why: string;
  flat: FallbackFlat;
  grouped: FallbackGrouped;
  /** The owner row the group hangs under (declared even for a context case). */
  row: string;
  group: GroupKey;
}

export interface CollectiveContinuationCase {
  name: string;
  surface: ActionSurface;
  why: string;
  pageSize: number;
  firstPageItems: number;
  totalItems: number;
  laterRow: string;
  laterGroup: GroupKey;
  laterMember: string;
}

/** A case for an identity the flat rendering and a helper disclosure both carry. */
export type CollectiveOverlapCase = CollectiveActionCase;

export interface CollectiveGroupedActionFixtures {
  groupId: string;
  projectHash: string;
  projectName: string;
  groups: Record<GroupKey, HelperGroupSummary>;
  members: Record<GroupKey, MemberSpec[]>;
  rows: RowSpec[];
  cases: CollectiveActionCase[];
  overlapCases: CollectiveOverlapCase[];
  fallbackCases: CollectiveFallbackCase[];
  continuationCases: CollectiveContinuationCase[];
}

const REQUIRED_CASES = [
  "browse-renders-the-group-under-its-owner-row",
  "contribute-selects-one-helper-only",
  "contribute-context-container-is-not-selectable",
  "review-selects-one-helper-only",
  "review-other-owner-group-remains-independent",
];

const REQUIRED_OVERLAP_CASES = [
  "contribute-overlapping-identity-counts-once",
  "review-overlapping-identity-counts-once",
];

const REQUIRED_FALLBACK_CASES = [
  "browse-empty-flat-mounts-helper-only-context",
  "browse-grouped-owner-absent-from-flat-mounts-its-group",
  "browse-grouped-owner-in-flat-mounts-its-group-once",
  "contribute-empty-flat-mounts-helper-only-context",
  "contribute-grouped-owner-absent-from-flat-mounts-its-group",
  "contribute-grouped-owner-in-tree-mounts-its-group-once",
  "review-empty-flat-mounts-helper-only-context",
  "review-grouped-owner-absent-from-flat-mounts-its-group",
  "review-grouped-owner-in-tree-mounts-its-group-once",
];

const REQUIRED_CONTINUATION_CASES = [
  "browse-continuation-reaches-later-owner-group",
  "contribute-continuation-reaches-later-owner-group",
  "review-continuation-reaches-later-owner-group",
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

const FALLBACK_FIELDS = new Set([
  "name",
  "surface",
  "why",
  "flat",
  "grouped",
  "row",
  "group",
]);

const CONTINUATION_FIELDS = new Set([
  "name",
  "surface",
  "why",
  "pageSize",
  "firstPageItems",
  "totalItems",
  "laterRow",
  "laterGroup",
  "laterMember",
]);

function fail(where: string, reason: string): never {
  throw new Error(`collective grouped helper fixture ${where} ${reason}`);
}

export function loadCollectiveGroupedActionFixtures(): CollectiveGroupedActionFixtures {
  const root = parse(
    readFileSync(resolve(process.cwd(), "src/testdata/collective-grouped-actions.yaml"), "utf8"),
    { strict: true },
  );
  assertExactKeys(root, ["groupId", "projectHash", "projectName", "groups", "members", "rows", "cases", "overlapCases", "fallbackCases", "continuationCases"], "root");

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

  function loadActionCase(value: CollectiveActionCase, where: string): CollectiveActionCase {
    const name = value.name;
    for (const field of Object.keys(value)) {
      if (!CASE_FIELDS.has(field)) fail(`${where} "${name}"`, `states an unknown field "${field}"`);
    }
    if (value.surface !== "contribute" && value.surface !== "review" && value.surface !== "browse") {
      fail(`${where} "${name}"`, `states an unknown surface "${String(value.surface)}"`);
    }
    if (!value.why?.trim()) fail(`${where} "${name}"`, "states no reason it exists");
    if (!rowByName.has(value.row)) fail(`${where} "${name}"`, `names an undeclared row "${value.row}"`);
    if (!(GROUP_KEYS as readonly string[]).includes(value.group)) {
      fail(`${where} "${name}"`, `names an unknown group "${String(value.group)}"`);
    }
    if (!Array.isArray(value.expected_ids)) fail(`${where} "${name}"`, "must state expected_ids as a list");
    for (const label of value.expected_ids) {
      if (!memberNames.has(label)) fail(`${where} "${name}"`, `expects member "${label}", which no member page serves`);
    }
    if (value.group === "context" && value.select !== undefined) {
      fail(`${where} "${name}"`, "selects a member of a helper-only context container, which has no selectable row");
    }
    if (value.select !== undefined && !members[value.group].some((member) => member.name === value.select)) {
      fail(`${where} "${name}"`, `selects "${value.select}", which group "${value.group}" does not serve`);
    }
    if (value.select !== undefined && !value.expected_ids.includes(value.select)) {
      fail(`${where} "${name}"`, `selects "${value.select}" but does not expect its id in the body`);
    }
    if (value.select === undefined && value.expected_ids.length > 0) {
      fail(`${where} "${name}"`, "expects selected ids but states nothing to select");
    }
    return value;
  }

  const cases = rawCases.map((value) => loadActionCase(value, "case"));

  const rawOverlap = (root.overlapCases ?? []) as CollectiveOverlapCase[];
  assertNamesMatch(
    rawOverlap.map((value) => value.name),
    REQUIRED_OVERLAP_CASES,
    "collective grouped helper overlap",
  );
  // An overlap case is an ordinary action case whose identity both surfaces
  // carry, so it is held to every rule an action case is; it exists as its own
  // section only because its assertions span the two selection surfaces.
  const overlapCases = rawOverlap.map((value) => loadActionCase(value, "overlap case"));

  const rawFallback = (root.fallbackCases ?? []) as CollectiveFallbackCase[];
  assertNamesMatch(
    rawFallback.map((value) => value.name),
    REQUIRED_FALLBACK_CASES,
    "collective grouped helper fallback",
  );
  const fallbackCases = rawFallback.map((value): CollectiveFallbackCase => {
    const name = value.name;
    for (const field of Object.keys(value)) {
      if (!FALLBACK_FIELDS.has(field)) fail(`fallback case "${name}"`, `states an unknown field "${field}"`);
    }
    if (value.surface !== "contribute" && value.surface !== "review" && value.surface !== "browse") {
      fail(`fallback case "${name}"`, `states an unknown surface "${String(value.surface)}"`);
    }
    if (!value.why?.trim()) fail(`fallback case "${name}"`, "states no reason it exists");
    if (value.flat !== "empty" && value.flat !== "rendered") {
      fail(`fallback case "${name}"`, `states an unknown flat mode "${String(value.flat)}"`);
    }
    if (value.grouped !== "owner" && value.grouped !== "context") {
      fail(`fallback case "${name}"`, `states an unknown grouped item "${String(value.grouped)}"`);
    }
    if (!rowByName.has(value.row)) fail(`fallback case "${name}"`, `names an undeclared row "${value.row}"`);
    if (!(GROUP_KEYS as readonly string[]).includes(value.group)) {
      fail(`fallback case "${name}"`, `names an unknown group "${String(value.group)}"`);
    }
    if (value.grouped === "context" && value.group !== "context") {
      fail(`fallback case "${name}"`, `serves a context container but names group "${value.group}"`);
    }
    if (value.grouped === "owner" && value.group === "context") {
      fail(`fallback case "${name}"`, "serves an owner row but names the helper-only context group");
    }
    if (value.flat === "rendered" && value.grouped !== "owner") {
      fail(`fallback case "${name}"`, "renders a flat owner row but serves no owner group to hang under it");
    }
    return value;
  });

  const rawContinuation = (root.continuationCases ?? []) as CollectiveContinuationCase[];
  assertNamesMatch(
    rawContinuation.map((value) => value.name),
    REQUIRED_CONTINUATION_CASES,
    "collective grouped helper continuation",
  );
  const continuationCases = rawContinuation.map((value): CollectiveContinuationCase => {
    const name = value.name;
    for (const field of Object.keys(value)) {
      if (!CONTINUATION_FIELDS.has(field)) fail(`continuation case "${name}"`, `states an unknown field "${field}"`);
    }
    if (value.surface !== "contribute" && value.surface !== "review" && value.surface !== "browse") {
      fail(`continuation case "${name}"`, `states an unknown surface "${String(value.surface)}"`);
    }
    if (!value.why?.trim()) fail(`continuation case "${name}"`, "states no reason it exists");
    if (value.pageSize !== GROUPED_TOP_LEVEL_PAGE_SIZE) {
      fail(
        `continuation case "${name}"`,
        `states page size ${value.pageSize}; the route requests ${GROUPED_TOP_LEVEL_PAGE_SIZE}`,
      );
    }
    if (value.firstPageItems !== value.pageSize) {
      fail(
        `continuation case "${name}"`,
        `states a first page of ${value.firstPageItems}; the first grouped page must be full for a later page to exist`,
      );
    }
    if (value.totalItems <= value.firstPageItems) {
      fail(
        `continuation case "${name}"`,
        `states ${value.totalItems} grouped results for a first page of ${value.firstPageItems}, so no continuation exists to test`,
      );
    }
    if (!rowByName.has(value.laterRow)) {
      fail(`continuation case "${name}"`, `names an undeclared later row "${value.laterRow}"`);
    }
    if (!(GROUP_KEYS as readonly string[]).includes(value.laterGroup)) {
      fail(`continuation case "${name}"`, `names an unknown later group "${String(value.laterGroup)}"`);
    }
    if (value.laterGroup === "context") {
      fail(`continuation case "${name}"`, "names the helper-only context group as a later owner group");
    }
    if (!members[value.laterGroup].some((member) => member.name === value.laterMember)) {
      fail(
        `continuation case "${name}"`,
        `expects later member "${value.laterMember}", which group "${value.laterGroup}" does not serve`,
      );
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
    overlapCases,
    fallbackCases,
    continuationCases,
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

/** The declared row behind a name, or a loud failure for an undeclared one. */
function rowSpec(fixtures: CollectiveGroupedActionFixtures, name: string): RowSpec {
  const row = fixtures.rows.find((candidate) => candidate.name === name);
  if (row == null) throw new Error(`collective grouped action fixture: no declared row "${name}"`);
  return row;
}

/** One flat contributable row for a declared row identity. */
function contributableRow(
  fixtures: CollectiveGroupedActionFixtures,
  row: RowSpec,
): ContributableTranscript {
  return {
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
  };
}

/** The flat tree rows the route renders under its project/branch fold.
 *
 * The flat route is the SAME submission set the grouped view nests, so the
 * helper members appear here too — as ordinary rows, exactly as they did before
 * the grouping existed. The grouped read only adds the disclosure that hangs
 * them off their owner. */
export function flatContributeRows(fixtures: CollectiveGroupedActionFixtures): ContributableTranscript[] {
  const owned = fixtures.rows.map((row) => contributableRow(fixtures, row));
  const members = GROUP_KEYS.flatMap((key) =>
    fixtures.members[key].map((member) => {
      const item = memberItem(member.name, member.title, "contribute", key);
      return item.transcript!.contributable as ContributableTranscript;
    }),
  );
  return [...owned, ...members];
}

/** One flat collective browse row for a declared row identity. */
function collectiveBrowseRow(fixtures: CollectiveGroupedActionFixtures, row: RowSpec) {
  return {
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
  };
}

/** The flat collective browse rows: the same submission set the grouped page
 *  nests, drawn by the pre-existing list. */
export function flatBrowseRows(fixtures: CollectiveGroupedActionFixtures) {
  return fixtures.rows.map((row) => collectiveBrowseRow(fixtures, row));
}

/** One flat pending-share row for a declared row identity. */
function pendingShareRow(
  fixtures: CollectiveGroupedActionFixtures,
  row: RowSpec,
): PendingShare {
  return {
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
  };
}

export function flatPendingRows(fixtures: CollectiveGroupedActionFixtures): PendingShare[] {
  const owned = fixtures.rows.map((row) => pendingShareRow(fixtures, row));
  const members = GROUP_KEYS.flatMap((key) =>
    fixtures.members[key].map((member) => {
      const item = memberItem(member.name, member.title, "review", key);
      return item.transcript!.pending as PendingShare;
    }),
  );
  return [...owned, ...members];
}

/**
 * The flat rows a fallback case's own surface serves: none for an empty flat
 * result, the case's owner row alone for a rendered one. The route's flat list
 * is what decides whether the grouped content has a row to hang under, so a case
 * states exactly which of the two it is.
 */
export function fallbackFlatContributeRows(
  fixtures: CollectiveGroupedActionFixtures,
  testCase: CollectiveFallbackCase,
): ContributableTranscript[] {
  return testCase.flat === "empty" ? [] : [contributableRow(fixtures, rowSpec(fixtures, testCase.row))];
}

export function fallbackFlatPendingRows(
  fixtures: CollectiveGroupedActionFixtures,
  testCase: CollectiveFallbackCase,
): PendingShare[] {
  return testCase.flat === "empty" ? [] : [pendingShareRow(fixtures, rowSpec(fixtures, testCase.row))];
}

export function fallbackFlatBrowseRows(
  fixtures: CollectiveGroupedActionFixtures,
  testCase: CollectiveFallbackCase,
) {
  return testCase.flat === "empty" ? [] : [collectiveBrowseRow(fixtures, rowSpec(fixtures, testCase.row))];
}

/** One ordinary grouped owner item, with any saved helper groups attached. */
export function groupedOwnerItem(
  fixtures: CollectiveGroupedActionFixtures,
  row: RowSpec,
  groups: readonly HelperGroupSummary[] = [],
): VillageSessionListItem {
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
    helperGroups: groups.length > 0 ? [...groups] : undefined,
  } as VillageSessionListItem;
}

/** The helper-only context container of the declared context group. */
export function contextContainerItem(
  fixtures: CollectiveGroupedActionFixtures,
): VillageSessionListItem {
  return {
    kind: "context_container",
    context: { groupId: fixtures.groups.context.groupId, ownerStatus: "known_unavailable" },
    helperGroups: [fixtures.groups.context],
  } as VillageSessionListItem;
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
  const items: VillageSessionListItem[] = fixtures.rows.map((row) =>
    groupedOwnerItem(
      fixtures,
      row,
      row.name === testCase.row ? [fixtures.groups[testCase.group]] : [],
    ),
  );
  if (testCase.context) items.push(contextContainerItem(fixtures));
  return items;
}

/** One full grouped page, in the shared shape every grouped route validates. */
export interface GroupedPageShape {
  items: VillageSessionListItem[];
  page: number;
  limit: number;
  totalItems: number;
  ordinarySessionTotal: number;
  helperThreadTotal: number;
}

/** The `GET /groups/{id}?view=grouped` body carrying an explicit page. */
export function groupedDetailPage(
  fixtures: CollectiveGroupedActionFixtures,
  page: GroupedPageShape,
) {
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
    transcriptList: page,
  };
}

/** The `GET /groups/{id}?view=grouped` body: the flat collective metadata with
 *  only its transcript collection replaced by the grouped page. */
export function groupedDetailPayload(
  fixtures: CollectiveGroupedActionFixtures,
  testCase: CollectiveActionCase,
) {
  const items = groupedItems(fixtures, testCase);
  return groupedDetailPage(fixtures, {
    items,
    page: 1,
    limit: 100,
    totalItems: items.length,
    ordinarySessionTotal: fixtures.rows.length,
    helperThreadTotal: 2,
  });
}

/** The grouped items one fallback case's page serves. */
export function fallbackGroupedItems(
  fixtures: CollectiveGroupedActionFixtures,
  testCase: CollectiveFallbackCase,
): VillageSessionListItem[] {
  if (testCase.grouped === "context") return [contextContainerItem(fixtures)];
  const row = fixtures.rows.find((candidate) => candidate.name === testCase.row)!;
  return [groupedOwnerItem(fixtures, row, [fixtures.groups[testCase.group]])];
}

/** One grouped page for a continuation case: a full first page of ordinary
 *  owners that carry no saved helpers, then the later owner group. */
export function continuationPage(
  fixtures: CollectiveGroupedActionFixtures,
  testCase: CollectiveContinuationCase,
  page: number,
): GroupedPageShape {
  const items =
    page === 1
      ? Array.from({ length: testCase.firstPageItems }, (_, index) => ordinaryGroupedOwner(fixtures, index))
      : (() => {
          const row = fixtures.rows.find((candidate) => candidate.name === testCase.laterRow)!;
          return [groupedOwnerItem(fixtures, row, [fixtures.groups[testCase.laterGroup]])];
        })();
  return {
    items,
    page,
    limit: testCase.pageSize,
    totalItems: testCase.totalItems,
    ordinarySessionTotal: testCase.firstPageItems,
    helperThreadTotal: 1,
  };
}

/** One ordinary grouped owner with no saved helpers, for a full first page. */
function ordinaryGroupedOwner(
  fixtures: CollectiveGroupedActionFixtures,
  index: number,
): VillageSessionListItem {
  return {
    kind: "transcript",
    transcript: {
      session: makeTranscriptFixture({
        id: ordinaryOwnerUUID(index),
        local_id: `ses_ordinarygrouped${index}`,
        owner_id: "30000000-0000-4000-8000-000000000010",
        title: `ordinary grouped owner ${index}`,
        project_hash: fixtures.projectHash,
        project_name: fixtures.projectName,
        project_display_name: fixtures.projectName,
        parent_session_id: null,
      }),
    },
  } as VillageSessionListItem;
}

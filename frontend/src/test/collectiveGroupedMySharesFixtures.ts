import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import {
  zHelperGroupSummary,
  type HelperGroupSummary,
  type VillageSessionListItem,
  type VillageSessionListPayload,
} from "@peasant-labs/schema";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import { memberLocalID, memberUUID } from "@/test/groupedHelperMountFixtures";
import { ordinaryOwnerUUID } from "@/test/groupedHelperSurfaceFixtures";
import { GROUPED_TOP_LEVEL_PAGE_SIZE } from "@/lib/queries/helperGroups";
import type { HelperMemberPageFixture } from "@/test/mountedGroupRoute";
import type { UserGroupShare } from "@/lib/types";

/**
 * Typed loader for `src/testdata/collective-grouped-my-shares.yaml`.
 *
 * The corpus is the single place a mounted grouped-my-shares case is stated:
 * the contribution the saved helper group hangs under, the group the server
 * grouped with it, the members the disclosure reaches, and whether the flat
 * contributions the panel already draws carry the case's row. This loader
 * refuses a corpus that loses a required NAME, repeats one, states an unknown
 * field, or states a page the panel's own page size could not request.
 */

/** The groups a case may serve. `context` is the helper-only container. */
export const GROUP_KEYS = ["owner", "context"] as const;
export type MyShareGroupKey = (typeof GROUP_KEYS)[number];

export type MyShareStatus = "approved" | "pending";

export interface MyShareMemberSpec {
  name: string;
  title: string;
}

export interface MyShareRowSpec {
  name: string;
  id: string;
  local_id: string;
  title: string;
  status: MyShareStatus;
}

/** Whether the flat contributions carry the case's row. */
export type MyShareFlat = "rendered" | "empty";
/** The grouped item the case's grouped page serves. */
export type MyShareGrouped = "owner" | "context" | "empty";

export interface MyShareCase {
  name: string;
  why: string;
  row: string;
  flat: MyShareFlat;
  grouped: MyShareGrouped;
}

export interface MyShareContinuationCase {
  name: string;
  why: string;
  pageSize: number;
  firstPageItems: number;
  totalItems: number;
  laterRow: string;
}

export interface CollectiveMyShareFixtures {
  groupId: string;
  groups: Record<MyShareGroupKey, HelperGroupSummary>;
  members: Record<MyShareGroupKey, MyShareMemberSpec[]>;
  rows: MyShareRowSpec[];
  cases: MyShareCase[];
  continuationCases: MyShareContinuationCase[];
}

const REQUIRED_CASES = [
  "my-shares-nests-the-group-under-its-contribution",
  "my-shares-context-container-mounts-on-the-grouped-exit",
  "my-shares-empty-both-reads-mount-no-panel",
];

const REQUIRED_CONTINUATION_CASES = [
  "my-shares-continuation-reaches-a-later-owner-group",
];

const CASE_FIELDS = new Set(["name", "why", "row", "flat", "grouped"]);

const CONTINUATION_FIELDS = new Set([
  "name",
  "why",
  "pageSize",
  "firstPageItems",
  "totalItems",
  "laterRow",
]);

/** The signed-in contributor every row belongs to. */
export const MY_SHARES_VIEWER_ID = "60000000-0000-4000-8000-0000000000aa";
const MY_SHARES_PROVIDER = "claude-code";
const MY_SHARES_MODEL = "claude-fable-5";
const MY_SHARES_PUBLISHED_AT = "2026-09-01T09:00:00Z";
const MY_SHARES_SHARED_AT = "2026-09-01T10:00:00Z";

function fail(where: string, reason: string): never {
  throw new Error(`collective grouped my-shares fixture ${where} ${reason}`);
}

export function loadCollectiveMyShareFixtures(): CollectiveMyShareFixtures {
  const root = parse(
    readFileSync(resolve(process.cwd(), "src/testdata/collective-grouped-my-shares.yaml"), "utf8"),
    { strict: true },
  );
  assertExactKeys(
    root,
    ["groupId", "groups", "members", "rows", "cases", "continuationCases"],
    "root",
  );

  const groups = {} as Record<MyShareGroupKey, HelperGroupSummary>;
  for (const key of GROUP_KEYS) {
    groups[key] = zHelperGroupSummary.parse(root.groups[key]);
  }

  const members = {} as Record<MyShareGroupKey, MyShareMemberSpec[]>;
  for (const key of GROUP_KEYS) {
    const declared: MyShareMemberSpec[] = root.members[key];
    if (!Array.isArray(declared) || declared.length === 0) {
      fail(`members.${key}`, "must declare at least one member");
    }
    for (const member of declared) {
      assertExactKeys(member, ["name", "title"], `members.${key}`);
    }
    members[key] = declared;
  }

  const rows = root.rows as MyShareRowSpec[];
  const rowByName = new Map(rows.map((row) => [row.name, row]));
  for (const row of rows) {
    assertExactKeys(row, ["name", "id", "local_id", "title", "status"], `row ${row.name}`);
    if (row.status !== "approved" && row.status !== "pending") {
      fail(`row ${row.name}`, `states an unknown status "${String(row.status)}"`);
    }
  }

  const rawCases = (root.cases ?? []) as MyShareCase[];
  assertNamesMatch(
    rawCases.map((value) => value.name),
    REQUIRED_CASES,
    "collective grouped my-shares",
  );
  const cases = rawCases.map((value): MyShareCase => {
    const name = value.name;
    for (const field of Object.keys(value)) {
      if (!CASE_FIELDS.has(field)) fail(`case "${name}"`, `states an unknown field "${field}"`);
    }
    if (!value.why?.trim()) fail(`case "${name}"`, "states no reason it exists");
    if (!rowByName.has(value.row)) fail(`case "${name}"`, `names an undeclared row "${value.row}"`);
    if (value.flat !== "rendered" && value.flat !== "empty") {
      fail(`case "${name}"`, `states an unknown flat mode "${String(value.flat)}"`);
    }
    if (value.grouped !== "owner" && value.grouped !== "context" && value.grouped !== "empty") {
      fail(`case "${name}"`, `states an unknown grouped item "${String(value.grouped)}"`);
    }
    if (value.grouped === "empty" && value.flat === "rendered") {
      fail(
        `case "${name}"`,
        "renders a flat contribution but serves no grouped item to hang its disclosure under",
      );
    }
    return value;
  });

  const rawContinuation = (root.continuationCases ?? []) as MyShareContinuationCase[];
  assertNamesMatch(
    rawContinuation.map((value) => value.name),
    REQUIRED_CONTINUATION_CASES,
    "collective grouped my-shares continuation",
  );
  const continuationCases = rawContinuation.map((value): MyShareContinuationCase => {
    const name = value.name;
    for (const field of Object.keys(value)) {
      if (!CONTINUATION_FIELDS.has(field)) {
        fail(`continuation case "${name}"`, `states an unknown field "${field}"`);
      }
    }
    if (!value.why?.trim()) fail(`continuation case "${name}"`, "states no reason it exists");
    if (value.pageSize !== GROUPED_TOP_LEVEL_PAGE_SIZE) {
      fail(
        `continuation case "${name}"`,
        `states page size ${value.pageSize}; the panel requests ${GROUPED_TOP_LEVEL_PAGE_SIZE}`,
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
    return value;
  });

  return { groupId: root.groupId, groups, members, rows, cases, continuationCases };
}

/** One declared row, or a loud failure for an undeclared one. */
export function myShareRow(fixtures: CollectiveMyShareFixtures, name: string): MyShareRowSpec {
  const row = fixtures.rows.find((candidate) => candidate.name === name);
  if (row == null) throw new Error(`collective grouped my-shares fixture: no declared row "${name}"`);
  return row;
}

/**
 * The `myShare` arm every grouped my-shares row carries, built from the same
 * contribution the flat read serves. It is the flat contribution's own shape, so
 * a row's state -- its title, provider, workflow status and shared time -- is
 * stated the same way on both reads.
 */
function myShareArm(row: {
  id: string;
  local_id: string;
  title: string;
  status: MyShareStatus;
}): UserGroupShare {
  return {
    id: row.id,
    owner_id: MY_SHARES_VIEWER_ID,
    local_id: row.local_id,
    parent_session_id: null,
    title: row.title,
    model_provider: MY_SHARES_PROVIDER,
    model_name: MY_SHARES_MODEL,
    visibility: "shared",
    published_at: MY_SHARES_PUBLISHED_AT,
    turn_count: 12,
    tokens_in: null,
    tokens_out: null,
    status: row.status,
    shared_at: MY_SHARES_SHARED_AT,
  };
}

/** The flat contribution `GET /groups/{id}/my-shares` serves for one row. */
export function flatMyShare(fixtures: CollectiveMyShareFixtures, name: string): UserGroupShare {
  return myShareArm(myShareRow(fixtures, name));
}

/**
 * One grouped owner item as the grouped my-shares route serves it: the same
 * contribution under `myShare`, with the saved helper groups the server grouped
 * under it.
 */
export function groupedMyShareOwner(
  fixtures: CollectiveMyShareFixtures,
  name: string,
  groups: readonly HelperGroupSummary[] = [],
): VillageSessionListItem {
  const row = myShareRow(fixtures, name);
  return {
    kind: "transcript",
    transcript: {
      session: makeTranscriptFixture({
        id: row.id,
        local_id: row.local_id,
        owner_id: MY_SHARES_VIEWER_ID,
        title: row.title,
        model_provider: MY_SHARES_PROVIDER,
        model_name: MY_SHARES_MODEL,
        visibility: "shared",
        published_at: MY_SHARES_PUBLISHED_AT,
        project_name: "peasant",
        project_display_name: "peasant",
      }),
      myShare: myShareArm(row),
    },
    ...(groups.length > 0 ? { helperGroups: [...groups] } : {}),
  } as VillageSessionListItem;
}

/**
 * The helper-only context container: a saved helper whose starter was not
 * shared with this collective. It has no owner row, so the server serves it as
 * read context carrying the owner's status.
 */
export function myShareContextContainer(
  fixtures: CollectiveMyShareFixtures,
): VillageSessionListItem {
  return {
    kind: "context_container",
    context: { groupId: fixtures.groups.context.groupId, ownerStatus: "known_unavailable" },
    helperGroups: [fixtures.groups.context],
  } as VillageSessionListItem;
}

/** One grouped page, in the shared shape the grouped my-shares route validates. */
export function groupedMySharesPage(
  items: VillageSessionListItem[],
  page: number,
  totalItems: number,
): VillageSessionListPayload {
  return {
    items,
    page,
    limit: GROUPED_TOP_LEVEL_PAGE_SIZE,
    totalItems,
    ordinarySessionTotal: totalItems,
    helperThreadTotal: items.reduce(
      (sum, item) =>
        sum + (item.helperGroups ?? []).reduce((total, group) => total + group.helperThreadCount, 0),
      0,
    ),
  };
}

/**
 * A full first page of ordinary owners that carry no saved helpers, so a
 * declared later-page group is genuinely absent until the continuation reads
 * its page. Each carries its own `myShare` arm, as the route serves it.
 */
export function groupedMySharesFirstPage(count: number): VillageSessionListItem[] {
  return Array.from({ length: count }, (_, index) => {
    const id = ordinaryOwnerUUID(index);
    const localID = `ses_ordinarymyshare${index}`;
    const title = `ordinary grouped contribution ${index}`;
    return {
      kind: "transcript",
      transcript: {
        session: makeTranscriptFixture({
          id,
          local_id: localID,
          owner_id: MY_SHARES_VIEWER_ID,
          title,
          model_provider: MY_SHARES_PROVIDER,
          visibility: "shared",
          published_at: MY_SHARES_PUBLISHED_AT,
        }),
        myShare: myShareArm({ id, local_id: localID, title, status: "approved" }),
      },
    } as VillageSessionListItem;
  });
}

/** One display member item as the grouped member endpoint serves it. */
function memberItem(spec: MyShareMemberSpec): VillageSessionListItem {
  const id = memberUUID(spec.name);
  const localID = memberLocalID(spec.name);
  return {
    kind: "transcript",
    transcript: {
      session: makeTranscriptFixture({
        id,
        local_id: localID,
        owner_id: MY_SHARES_VIEWER_ID,
        title: spec.title,
        model_provider: MY_SHARES_PROVIDER,
        visibility: "shared",
        published_at: MY_SHARES_PUBLISHED_AT,
      }),
      myShare: myShareArm({ id, local_id: localID, title: spec.title, status: "approved" }),
    },
  } as VillageSessionListItem;
}

/** The member page one of the case's groups answers with under its own scope. */
export function myShareMemberPage(
  fixtures: CollectiveMyShareFixtures,
  key: MyShareGroupKey,
  limit: number,
): HelperMemberPageFixture {
  const members = fixtures.members[key].map(memberItem);
  return { members, limit, total: members.length };
}

/** The wire identity a labeled member is served under. */
export function memberUUIDFor(label: string): string {
  return memberUUID(label);
}

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { zHelperGroupSummary, type HelperGroupSummary } from "@peasant-labs/schema";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { assertNamesMatch } from "@/test/fixtureAssertions";

/**
 * Typed loader for `src/testdata/grouped-helper-mounts.yaml`.
 *
 * The corpus is the single place a mounted helper-group case is stated: the
 * group it renders, the member page it reaches, the member it selects and the
 * exact expectations it declares. This loader refuses a corpus that loses a
 * required NAME, repeats one, states an unknown field, or states an expectation
 * the declared groups and member pages cannot produce — so a case cannot
 * silently disappear and an inert expectation cannot survive.
 */

/** The groups a case may render. `context` is the helper-only container. */
export const GROUP_KEYS = ["owner", "other", "context"] as const;
export type GroupKey = (typeof GROUP_KEYS)[number];

export interface MemberSpec {
  id: string;
  title: string;
  turnCount: number;
  inputSubmissionCount: number | null;
}

export interface MemberPageSpec {
  limit: number;
  total: number;
  members: MemberSpec[];
}

/** One member request a case expects, in the order the host must issue it. */
export interface MemberRequestExpectation {
  scope: string;
  page: string;
  limit: string;
}

/** The notice a case requires the open group to state, if any. */
export type ExpectedNotice = "scope-expired" | "member-load-failed";

export interface GroupedHelperMountCase {
  name: string;
  group: GroupKey;
  expand: boolean;
  secondGroup?: GroupKey;
  /** Close `secondGroup` again and assert the first group's page stays mounted. */
  collapseSecond?: boolean;
  /** The member page the case navigates to (default 1). */
  page?: number;
  /** HTTP status the case's group scope answers with, if not 200. */
  scopeStatus?: number;
  /** Member label the case toggles through the selection host. */
  select?: string;
  /** Press the failure notice's retry control. */
  retry?: boolean;
  /** Assert this state is mounted while the first page is in flight. */
  loadingNotice?: "helper-group-loading";
  expectedRequests: MemberRequestExpectation[];
  expectedMembers: string[];
  expectedSelected?: string[];
  expectedNotice?: ExpectedNotice;
  /** Originating-list refreshes the case expects (409 recovery only). */
  expectedOriginRefreshes?: number;
}

export interface GroupedHelperMountFixtures {
  groups: Record<GroupKey, HelperGroupSummary>;
  context: { groupId: string; ownerStatus: string };
  /** Member pages, keyed by group then by page number. */
  memberPages: Record<GroupKey, Record<number, MemberPageSpec>>;
  cases: GroupedHelperMountCase[];
}

const REQUIRED_CASES = [
  "collapsed-sends-no-request",
  "expand-loads-exact-scope",
  "page-two-keeps-original-scope",
  "expired-scope-hides-members",
  "denied-member-request-states-failure",
  "failing-member-request-states-failure",
  "selection-is-one-member",
  "independent-groups-are-independent",
  "context-container-has-no-row",
];

const REQUIRED_CASE_FIELDS = ["name", "group", "expand", "expectedRequests", "expectedMembers"];

const CASE_FIELDS = new Set([
  ...REQUIRED_CASE_FIELDS,
  "secondGroup",
  "collapseSecond",
  "page",
  "scopeStatus",
  "select",
  "retry",
  "loadingNotice",
  "expectedSelected",
  "expectedNotice",
  "expectedOriginRefreshes",
]);

/** Where this loader reports a violation, so the message names the case. */
function fail(name: string, reason: string): never {
  throw new Error(`grouped helper mount fixture case "${name}" ${reason}`);
}

function memberSpec(value: MemberSpec, caseName: string): MemberSpec {
  if (!value.id || typeof value.title !== "string" || !Number.isInteger(value.turnCount)) {
    fail(caseName, "stated member rows need an id, a title and an integer turn count");
  }
  if (value.inputSubmissionCount !== null && !Number.isInteger(value.inputSubmissionCount)) {
    fail(caseName, "stated a member input count that is neither an integer nor null (absent)");
  }
  return value;
}

function memberPage(value: MemberPageSpec, location: string): MemberPageSpec {
  if (!Number.isInteger(value.limit) || !Number.isInteger(value.total) || !Array.isArray(value.members)) {
    throw new Error(`${location}: a member page needs integer limit/total and a members array`);
  }
  const ids = new Set<string>();
  value.members.forEach((member) => {
    memberSpec(member, `${location}:${member.id}`);
    if (ids.has(member.id)) throw new Error(`${location}: duplicate member id ${member.id}`);
    ids.add(member.id);
  });
  return value;
}

function groupKey(value: unknown, name: string, field: string): GroupKey {
  if (typeof value !== "string" || !(GROUP_KEYS as readonly string[]).includes(value)) {
    fail(name, `stated an unknown ${field} "${String(value)}"; known groups are ${GROUP_KEYS.join(", ")}`);
  }
  return value as GroupKey;
}

/**
 * The facts line the published row must draw for this member. The wording is
 * stated here so a case can require it; the mounted test asserts the rendered
 * text contains each string.
 */
export function memberFactStrings(spec: MemberSpec): string[] {
  return [
    spec.inputSubmissionCount === null
      ? "unknown input submissions"
      : `${spec.inputSubmissionCount} input submission${spec.inputSubmissionCount === 1 ? "" : "s"}`,
    `${spec.turnCount} turn${spec.turnCount === 1 ? "" : "s"}`,
  ];
}

export function loadGroupedHelperMountFixtures(): GroupedHelperMountFixtures {
  const root = parse(
    readFileSync(resolve(process.cwd(), "src/testdata/grouped-helper-mounts.yaml"), "utf8"),
    { strict: true },
  );
  const groups = {} as Record<GroupKey, HelperGroupSummary>;
  for (const key of GROUP_KEYS) {
    groups[key] = zHelperGroupSummary.parse(root.groups[key]);
  }
  const context = {
    groupId: root.context.groupId as string,
    ownerStatus: root.context.ownerStatus as string,
  };
  const memberPages = {} as Record<GroupKey, Record<number, MemberPageSpec>>;
  for (const key of GROUP_KEYS) {
    const pages: Record<number, MemberPageSpec> = {};
    const declared = (root.memberPages?.[key] ?? {}) as Record<string, MemberPageSpec>;
    for (const [page, spec] of Object.entries(declared)) {
      if (!/^[1-9][0-9]*$/.test(page)) {
        throw new Error(`grouped helper mount fixture memberPages.${key} has a non-page key ${page}`);
      }
      pages[Number(page)] = memberPage(spec, `memberPages.${key}.${page}`);
    }
    memberPages[key] = pages;
  }
  const scopeOf = (key: GroupKey): string => groups[key].memberScope;
  const scopeOwner = new Map<string, GroupKey>(
    GROUP_KEYS.map((key) => [scopeOf(key), key]),
  );

  const rawCases = root.cases as GroupedHelperMountCase[];
  assertNamesMatch(
    rawCases.map((value) => value.name),
    REQUIRED_CASES,
    "grouped helper mount",
  );

  const cases = rawCases.map((value): GroupedHelperMountCase => {
    const name = value.name;
    for (const field of Object.keys(value)) {
      if (!CASE_FIELDS.has(field)) fail(name, `states an unknown field "${field}"`);
    }
    for (const field of REQUIRED_CASE_FIELDS) {
      if (!(field in value)) fail(name, `is missing the required field "${field}"`);
    }
    if (typeof value.expand !== "boolean") fail(name, "must state a boolean expand");
    if (!Array.isArray(value.expectedRequests)) fail(name, "must state expectedRequests as a list");
    if (!Array.isArray(value.expectedMembers)) fail(name, "must state expectedMembers as a list");
    if (value.page !== undefined && (!Number.isInteger(value.page) || value.page < 1)) {
      fail(name, "stated a member page that is not a positive integer");
    }
    if (
      value.scopeStatus !== undefined &&
      (!Number.isInteger(value.scopeStatus) || value.scopeStatus < 100)
    ) {
      fail(name, "stated a scope status that is not an HTTP status code");
    }
    if (
      value.expectedOriginRefreshes !== undefined &&
      (!Number.isInteger(value.expectedOriginRefreshes) || value.expectedOriginRefreshes < 0)
    ) {
      fail(name, "stated an origin-refresh count that is not a non-negative integer");
    }
    if (value.loadingNotice !== undefined && value.loadingNotice !== "helper-group-loading") {
      fail(name, `stated an unknown loading notice "${String(value.loadingNotice)}"`);
    }
    if (
      value.expectedNotice !== undefined &&
      value.expectedNotice !== "scope-expired" &&
      value.expectedNotice !== "member-load-failed"
    ) {
      fail(name, `stated an unknown expected notice "${String(value.expectedNotice)}"`);
    }

    // Every expected request must name a declared scope and a declared page, so
    // an expectation cannot outlive the member page it was written against.
    for (const request of value.expectedRequests) {
      const owner = scopeOwner.get(request.scope);
      if (owner == null) {
        fail(name, `expects a request to the undeclared scope "${request.scope}"`);
      }
      const spec = memberPages[owner]?.[Number(request.page)];
      if (spec == null) {
        fail(name, `expects page ${request.page} of group "${owner}", which declares no such member page`);
      }
      if (Number(request.limit) !== spec.limit) {
        fail(
          name,
          `expects limit ${request.limit} for group "${owner}" but its member page states ${spec.limit}`,
        );
      }
    }

    // The mounted member identities must be rows of the pages this case can
    // reach. A label nothing serves is a case asserting a row that cannot exist.
    const reachable = new Set<string>();
    for (const owner of GROUP_KEYS) {
      for (const spec of Object.values(memberPages[owner])) {
        for (const member of spec.members) reachable.add(member.id);
      }
    }
    for (const label of value.expectedMembers) {
      if (!reachable.has(label)) {
        fail(name, `expects member "${label}", which no declared member page serves`);
      }
    }
    for (const label of value.expectedSelected ?? []) {
      if (!value.expectedMembers.includes(label)) {
        fail(name, `expects "${label}" selected although it is not an expected mounted member`);
      }
    }

    if (value.group === "context" && value.expand) {
      fail(name, "renders a context container, which has no member disclosure to open");
    }

    // A group the case does not open states no member-disclosure expectation at
    // all: the collapsed arm and the context-container arm both live here.
    if (!value.expand) {
      if (value.expectedRequests.length > 0) {
        fail(name, "does not open its group but declares a member request");
      }
      if (value.expectedMembers.length > 0) {
        fail(name, "does not open its group but expects mounted members");
      }
      for (const field of [
        "secondGroup",
        "collapseSecond",
        "page",
        "scopeStatus",
        "select",
        "retry",
        "loadingNotice",
        "expectedSelected",
        "expectedNotice",
      ]) {
        if (field in value) fail(name, `does not open its group but states "${field}"`);
      }
    } else if (value.expectedRequests.length === 0) {
      fail(name, `opens group "${value.group}" but declares no expected member request`);
    }

    if (value.secondGroup !== undefined) {
      groupKey(value.secondGroup, name, "second group");
      if (value.secondGroup === "context") fail(name, "named a context container as its second group");
      if (value.secondGroup === value.group) fail(name, "named the same group twice");
    }
    if (value.collapseSecond === true && value.secondGroup === undefined) {
      fail(name, "collapses a second group it never names");
    }

    const collapsed = value.scopeStatus !== undefined && value.scopeStatus !== 200;
    if (value.scopeStatus === 409) {
      if (value.expectedNotice !== "scope-expired") {
        fail(name, "states a 409 scope but does not expect the scope-expired notice");
      }
      if (value.expectedMembers.length > 0) fail(name, "states a 409 scope, which mounts no members");
      if (value.expectedOriginRefreshes !== 1) {
        fail(name, "states a 409 scope but does not expect exactly one originating-list refresh");
      }
      if (value.expectedRequests.length !== 1) fail(name, "states a 409 scope but not its single failed request");
    } else if (collapsed) {
      if (value.expectedNotice !== "member-load-failed") {
        fail(name, `states status ${value.scopeStatus} but does not expect the member-load-failed notice`);
      }
      if (value.expectedMembers.length > 0) {
        fail(name, `states status ${value.scopeStatus}, which mounts no members`);
      }
      if ((value.expectedOriginRefreshes ?? 0) !== 0) {
        fail(name, `states status ${value.scopeStatus}, which must not refresh the originating list`);
      }
      if (value.retry && value.expectedRequests.length !== 2) {
        fail(name, "expects a retry but does not declare both the failed request and the retried request");
      }
      if (!value.retry && value.expectedRequests.length !== 1) {
        fail(name, "states a failed scope but declares neither a retry nor a single request");
      }
    } else if (value.expectedOriginRefreshes !== undefined) {
      fail(name, "expects an originating-list refresh without the 409 state that offers one");
    }

    if (value.select !== undefined && (value.expectedSelected ?? []).length === 0) {
      fail(name, `selects "${value.select}" but declares no expectedSelected`);
    }
    if (value.page !== undefined) {
      const last = value.expectedRequests[value.expectedRequests.length - 1];
      if (Number(last?.page) !== value.page) {
        fail(name, `navigates to page ${value.page} but its last expected request is page ${last?.page}`);
      }
    }
    return value;
  });
  return { groups, context, memberPages, cases };
}

/** A display member item as the grouped member endpoint serves it. */
const MEMBER_OWNER_ID = "12345678-1234-4234-8234-123456789013";

/**
 * The wire identity a labeled member is served under. The corpus states a
 * human label for readable assertions; the wire carries a UUID transcript id
 * and a harness session id, exactly as the canonical validator demands.
 */
export function memberUUID(label: string): string {
  let hash = 0;
  for (const char of label) hash = (hash * 31 + char.charCodeAt(0)) >>> 0;
  const tail = hash.toString(16).padStart(12, "0").slice(-12);
  return `aaaaaaaa-bbbb-4ccc-8ddd-${tail}`;
}

export function memberLocalID(label: string): string {
  return `ses_${label.replace(/[^a-zA-Z0-9]/g, "")}`;
}

export function memberItem(spec: MemberSpec) {
  return {
    kind: "transcript" as const,
    transcript: {
      session: makeTranscriptFixture({
        id: memberUUID(spec.id),
        local_id: memberLocalID(spec.id),
        owner_id: MEMBER_OWNER_ID,
        title: spec.title,
        turn_count: spec.turnCount,
        ...(spec.inputSubmissionCount === null
          ? {}
          : { input_submission_count: spec.inputSubmissionCount }),
      }),
    },
  };
}

/**
 * The declared member page a `scope`+`page` request is answered from, or
 * undefined when no page is declared. The mounted host's mock HTTP boundary and
 * a case's post-collapse assertion both read it, so neither restates fixture
 * data.
 */
export function memberPageFor(
  fixtures: GroupedHelperMountFixtures,
  scope: string,
  page: number,
): MemberPageSpec | undefined {
  for (const key of GROUP_KEYS) {
    if (fixtures.groups[key].memberScope === scope) return fixtures.memberPages[key][page];
  }
  return undefined;
}

/** The declared row behind a member label, across every group's member pages. */
export function memberSpecByLabel(
  fixtures: GroupedHelperMountFixtures,
  label: string,
): MemberSpec | undefined {
  for (const key of GROUP_KEYS) {
    for (const spec of Object.values(fixtures.memberPages[key])) {
      const found = spec.members.find((member) => member.id === label);
      if (found != null) return found;
    }
  }
  return undefined;
}

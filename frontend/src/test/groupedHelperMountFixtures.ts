import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { zHelperGroupSummary, type HelperGroupSummary } from "@peasant-labs/schema";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";

/**
 * Typed loader for `src/testdata/grouped-helper-mounts.yaml`.
 *
 * The corpus is the single place a mounted helper-group case is stated; the
 * loader refuses a corpus that loses a required NAME or repeats one, so a case
 * cannot silently disappear from the suite.
 */

export interface MemberSpec {
  id: string;
  title: string;
  turnCount: number | null;
  inputSubmissionCount: number | null;
}

export interface MemberPageSpec {
  page: number;
  limit: number;
  total: number;
  members: MemberSpec[];
}

export interface GroupedHelperMountCase {
  name: string;
  group: string;
  expand: boolean;
  page?: number;
  scopeStatus?: number;
  select?: string;
  secondGroup?: string;
  expectedMembers?: string[];
  expectedRequest?: Record<string, string>;
  expectedSelected?: string[];
}

export interface GroupedHelperMountFixtures {
  groups: Record<string, HelperGroupSummary>;
  context: { groupId: string; ownerStatus: string };
  pages: Record<string, MemberPageSpec>;
  cases: GroupedHelperMountCase[];
}

const REQUIRED_CASES = [
  "collapsed-sends-no-request",
  "expand-loads-exact-scope",
  "page-two-keeps-original-scope",
  "expired-scope-hides-members",
  "selection-is-one-member",
  "independent-groups-are-independent",
  "context-container-has-no-row",
];

function memberSpec(value: MemberSpec, caseName: string): MemberSpec {
  if (!value.id || typeof value.title !== "string" || !Number.isInteger(value.turnCount)) {
    throw new Error(`${caseName}: member rows need an id, a title and an integer turn count`);
  }
  if (value.inputSubmissionCount !== null && !Number.isInteger(value.inputSubmissionCount)) {
    throw new Error(`${caseName}: a member's input count is an integer or null (absent)`);
  }
  return value;
}

function memberPage(value: MemberPageSpec, name: string): MemberPageSpec {
  if (
    !Number.isInteger(value.page) ||
    !Number.isInteger(value.limit) ||
    !Number.isInteger(value.total) ||
    !Array.isArray(value.members)
  ) {
    throw new Error(`${name}: a member page needs integer page/limit/total and a members array`);
  }
  const ids = new Set<string>();
  value.members.forEach((member) => {
    memberSpec(member, `${name}:${member.id}`);
    if (ids.has(member.id)) throw new Error(`${name}: duplicate member id ${member.id}`);
    ids.add(member.id);
  });
  return value;
}

export function loadGroupedHelperMountFixtures(): GroupedHelperMountFixtures {
  const root = parse(
    readFileSync(resolve(process.cwd(), "src/testdata/grouped-helper-mounts.yaml"), "utf8"),
    { strict: true },
  );
  const groups: Record<string, HelperGroupSummary> = {
    owner: zHelperGroupSummary.parse(root.group),
    other: zHelperGroupSummary.parse(root.otherGroup),
    context: zHelperGroupSummary.parse(root.contextGroup),
  };
  const context = {
    groupId: root.context.groupId as string,
    ownerStatus: root.context.ownerStatus as string,
  };
  const pages: Record<string, MemberPageSpec> = {
    pageOne: memberPage(root.pageOne, "pageOne"),
    pageTwo: memberPage(root.pageTwo, "pageTwo"),
    otherMembers: memberPage(root.otherMembers, "otherMembers"),
  };
  const names = new Set<string>();
  const cases = (root.cases as GroupedHelperMountCase[]).map((value) => {
    if (!value.name || names.has(value.name)) {
      throw new Error(`grouped helper mount fixture has a duplicate or missing name: ${value.name}`);
    }
    names.add(value.name);
    if (value.group === "context") return { ...value, expand: false };
    if (typeof value.expand !== "boolean") {
      throw new Error(`${value.name}: expand must be stated`);
    }
    if (!["owner", "other"].includes(value.group)) {
      throw new Error(`${value.name}: unknown group ${value.group}`);
    }
    return value;
  });
  for (const name of REQUIRED_CASES) {
    if (!names.has(name)) throw new Error(`required grouped helper mount case missing: ${name}`);
  }
  return { groups, context, pages, cases };
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

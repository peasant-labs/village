import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import {
  zHelperGroupSummary,
  type HelperGroupSummary,
  type VillageSessionListItem,
} from "@peasant-labs/schema";
import { assertNamesMatch } from "@/test/fixtureAssertions";
import { memberUUID, type MemberSpec } from "@/test/groupedHelperMountFixtures";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";

/**
 * Typed loader for `src/testdata/grouped-shape-oracles.yaml`.
 *
 * The corpus states the two named grouping oracles a mounted grouped row must
 * draw: one owner session carrying 1 input submission, 5 main turns and 2 saved
 * helper threads, and a page of 3 ordinary owners carrying 3 saved helper
 * threads across 3 top-level rows.
 *
 * SERVED DATA and EXPECTED TEXT are declared apart, so an edit to the served
 * measures changes the rendered text without changing the pinned expectation and
 * the case fails instead of agreeing with itself. This loader refuses a corpus
 * that loses a required NAME, repeats one, states an unknown field, or declares
 * an expectation/total its owner rows and groups cannot produce -- so a case
 * cannot silently disappear and an inert expectation cannot survive.
 */

export interface ShapeOwnerSpec {
  key: string;
  title: string;
  turnCount: number;
  /** A measured count, or null when the server did not measure it. */
  inputSubmissionCount: number | null;
  groupKeys: string[];
}

interface RawShapeOwnerSpec {
  title: string;
  turnCount: number;
  inputSubmissionCount: number | null;
  groups: string[];
}

export interface ShapeMemberPage {
  limit: number;
  total: number;
  members: MemberSpec[];
}

interface RawShapeOracleCase {
  name: string;
  owners: string[];
  ordinarySessionTotal: number;
  helperThreadTotal: number;
  topLevelItems: number;
  expectedOwnerFacts: Record<string, string[]>;
  expectedGroupLabels: Record<string, string>;
  expand: string[];
  expectedMemberIds: Record<string, string[]>;
  expectedMemberFacts: Record<string, string[]>;
}

export interface ShapeOracleCase extends Omit<RawShapeOracleCase, "owners"> {
  /** The owner rows the case mounts, in declaration order. */
  ownerKeys: string[];
}

export interface GroupedShapeOracleFixtures {
  owners: Record<string, ShapeOwnerSpec>;
  groups: Record<string, HelperGroupSummary>;
  memberPages: Record<string, ShapeMemberPage>;
  cases: ShapeOracleCase[];
}

const REQUIRED_CASES = [
  "owner-input1-main5-with-two-helpers",
  "ordinary3-helper3-top3",
];

const OWNER_FIELDS = ["title", "turnCount", "inputSubmissionCount", "groups"];
const CASE_FIELDS = [
  "name",
  "owners",
  "ordinarySessionTotal",
  "helperThreadTotal",
  "topLevelItems",
  "expectedOwnerFacts",
  "expectedGroupLabels",
  "expand",
  "expectedMemberIds",
  "expectedMemberFacts",
];
const MEMBER_PAGE_FIELDS = ["limit", "total", "members"];
const MEMBER_FIELDS = ["id", "title", "turnCount", "inputSubmissionCount"];

function fail(location: string, reason: string): never {
  throw new Error(`grouped shape oracle fixture "${location}" ${reason}`);
}

function exactKeys(value: object, allowed: string[], location: string): void {
  const unknown = Object.keys(value).filter((field) => !allowed.includes(field));
  if (unknown.length > 0) fail(location, `states unknown fields: ${unknown.join(", ")}`);
  const missing = allowed.filter((field) => !(field in value));
  if (missing.length > 0) fail(location, `is missing required fields: ${missing.join(", ")}`);
}

function stringList(value: unknown, location: string): string[] {
  if (!Array.isArray(value) || value.length === 0 || value.some((entry) => typeof entry !== "string" || !entry)) {
    fail(location, "must be a non-empty list of literal strings");
  }
  return value as string[];
}

function memberSpec(value: MemberSpec, location: string): MemberSpec {
  exactKeys(value, MEMBER_FIELDS, location);
  if (!value.id || typeof value.title !== "string" || !Number.isInteger(value.turnCount)) {
    fail(location, "needs an id, a title and an integer turn count");
  }
  if (value.inputSubmissionCount !== null && !Number.isInteger(value.inputSubmissionCount)) {
    fail(location, "states an input count that is neither an integer nor null (absent)");
  }
  return value;
}

function expectedKeys(
  value: unknown,
  keys: readonly string[],
  location: string,
): Record<string, string[]> {
  if (value == null || typeof value !== "object" || Array.isArray(value)) {
    fail(location, "must be a mapping keyed by the rows it states expectations for");
  }
  const map = value as Record<string, string[]>;
  const got = Object.keys(map).sort();
  const want = [...keys].sort();
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    fail(location, `states expectations for ${got.join(", ")}; want ${want.join(", ")}`);
  }
  for (const key of got) stringList(map[key], `${location}.${key}`);
  return map;
}

export function loadGroupedShapeOracleFixtures(): GroupedShapeOracleFixtures {
  const root = parse(
    readFileSync(resolve(process.cwd(), "src/testdata/grouped-shape-oracles.yaml"), "utf8"),
    { strict: true },
  );

  const groups: Record<string, HelperGroupSummary> = {};
  for (const [key, value] of Object.entries((root.groups ?? {}) as Record<string, HelperGroupSummary>)) {
    try {
      groups[key] = zHelperGroupSummary.parse(value);
    } catch (error) {
      fail(`groups.${key}`, `declared a group the canonical summary rejects: ${String(error)}`);
    }
  }
  if (Object.keys(groups).length === 0) fail("groups", "declares no helper groups");

  const owners: Record<string, ShapeOwnerSpec> = {};
  for (const [key, value] of Object.entries((root.owners ?? {}) as Record<string, RawShapeOwnerSpec>)) {
    exactKeys(value, OWNER_FIELDS, `owners.${key}`);
    if (typeof value.title !== "string" || !value.title) fail(`owners.${key}`, "is missing its title");
    if (!Number.isInteger(value.turnCount) || value.turnCount < 0) {
      fail(`owners.${key}`, "states a non-integer turn count");
    }
    if (value.inputSubmissionCount !== null && !Number.isInteger(value.inputSubmissionCount)) {
      fail(`owners.${key}`, "states an input count that is neither an integer nor null (absent)");
    }
    if (!Array.isArray(value.groups)) fail(`owners.${key}`, "states groups that are not a list");
    for (const groupKey of value.groups) {
      if (groups[groupKey] == null) fail(`owners.${key}`, `names the undeclared group "${groupKey}"`);
    }
    owners[key] = {
      key,
      title: value.title,
      turnCount: value.turnCount,
      inputSubmissionCount: value.inputSubmissionCount,
      groupKeys: value.groups,
    };
  }
  if (Object.keys(owners).length === 0) fail("owners", "declares no owner rows");

  const memberPages: Record<string, ShapeMemberPage> = {};
  for (const [scope, pages] of Object.entries(
    (root.memberPages ?? {}) as Record<string, Record<string, unknown>>,
  )) {
    const groupKey = Object.entries(groups).find(([, group]) => group.memberScope === scope)?.[0];
    if (groupKey == null) fail(`memberPages.${scope}`, "names a scope no declared group serves");
    for (const [page, raw] of Object.entries(pages)) {
      if (!/^[1-9][0-9]*$/.test(page)) fail(`memberPages.${scope}`, `has a non-page key ${page}`);
      exactKeys(raw as object, MEMBER_PAGE_FIELDS, `memberPages.${scope}.${page}`);
      const value = raw as ShapeMemberPage;
      if (!Number.isInteger(value.limit) || !Number.isInteger(value.total) || !Array.isArray(value.members)) {
        fail(`memberPages.${scope}.${page}`, "needs integer limit/total and a members array");
      }
      const ids = new Set<string>();
      value.members.forEach((member) => {
        memberSpec(member, `memberPages.${scope}.${page}:${member.id}`);
        if (ids.has(member.id)) fail(`memberPages.${scope}.${page}`, `repeats member id ${member.id}`);
        ids.add(member.id);
      });
      // The page total is the group's saved-identity count, exactly as the
      // server states it, so a page cannot claim a total its group does not.
      if (value.total !== groups[groupKey].helperThreadCount) {
        fail(
          `memberPages.${scope}.${page}`,
          `states total ${value.total} while group "${groupKey}" declares ${groups[groupKey].helperThreadCount} helper threads`,
        );
      }
      memberPages[scope] = value;
    }
  }
  for (const [groupKey, group] of Object.entries(groups)) {
    if (memberPages[group.memberScope] == null) {
      fail(`groups.${groupKey}`, `declares scope "${group.memberScope}" with no member page`);
    }
  }

  const rawCases = (root.cases ?? []) as RawShapeOracleCase[];
  assertNamesMatch(
    rawCases.map((value) => value.name),
    REQUIRED_CASES,
    "grouped shape oracle",
  );

  const cases = rawCases.map((value): ShapeOracleCase => {
    const name = value.name;
    exactKeys(value, CASE_FIELDS, name);
    if (!Array.isArray(value.owners) || value.owners.length === 0) {
      fail(name, "declares no owner rows");
    }
    for (const ownerKey of value.owners) {
      if (owners[ownerKey] == null) fail(name, `names the undeclared owner "${ownerKey}"`);
    }
    const caseGroups = value.owners.flatMap((ownerKey) => owners[ownerKey].groupKeys);
    if (new Set(caseGroups).size !== caseGroups.length) {
      fail(name, "mounts the same helper group under two owner rows");
    }
    // A case's totals are the owner rows it mounts and the saved threads those
    // rows disclose; a case cannot declare a shape its rows cannot produce.
    if (value.topLevelItems !== value.owners.length) {
      fail(name, `states ${value.topLevelItems} top-level items for ${value.owners.length} owner rows`);
    }
    if (value.ordinarySessionTotal !== value.owners.length) {
      fail(name, `states ${value.ordinarySessionTotal} ordinary sessions for ${value.owners.length} owner rows`);
    }
    const declaredHelperThreads = caseGroups.reduce(
      (total, groupKey) => total + groups[groupKey].helperThreadCount,
      0,
    );
    if (value.helperThreadTotal !== declaredHelperThreads) {
      fail(
        name,
        `states ${value.helperThreadTotal} helper threads while its owners' groups declare ${declaredHelperThreads}`,
      );
    }

    // The literal facts each mounted row must draw, keyed by the rows of THIS
    // case: an expectation for an unmounted row, or a mounted row without one,
    // is a corpus error rather than a silent gap.
    expectedKeys(value.expectedOwnerFacts, value.owners, `${name}:expectedOwnerFacts`);
    stringList(value.expand, `${name}:expand`);
    for (const groupKey of value.expand) {
      if (groups[groupKey] == null) fail(name, `opens the undeclared group "${groupKey}"`);
      if (!caseGroups.includes(groupKey)) fail(name, `opens group "${groupKey}", which no mounted owner carries`);
    }
    if (value.expectedGroupLabels == null || typeof value.expectedGroupLabels !== "object") {
      fail(name, "must state expectedGroupLabels as a mapping");
    }
    if (JSON.stringify(Object.keys(value.expectedGroupLabels).sort()) !== JSON.stringify([...caseGroups].sort())) {
      fail(name, `states group labels for ${Object.keys(value.expectedGroupLabels).join(", ")}; want ${caseGroups.join(", ")}`);
    }
    for (const [groupKey, label] of Object.entries(value.expectedGroupLabels)) {
      if (typeof label !== "string" || !label) fail(`${name}:expectedGroupLabels.${groupKey}`, "must be a literal string");
    }
    expectedKeys(value.expectedMemberIds, value.expand, `${name}:expectedMemberIds`);
    const expandedMembers = value.expand.flatMap((groupKey) => {
      const page = memberPages[groups[groupKey].memberScope];
      const expectedLabels = value.expectedMemberIds[groupKey];
      const servedLabels = page.members.map((member) => member.id);
      if (JSON.stringify(expectedLabels) !== JSON.stringify(servedLabels)) {
        fail(
          `${name}:expectedMemberIds.${groupKey}`,
          `states ${expectedLabels.join(", ")} while the page serves ${servedLabels.join(", ")}`,
        );
      }
      return expectedLabels;
    });
    expectedKeys(value.expectedMemberFacts, expandedMembers, `${name}:expectedMemberFacts`);

    return {
      name,
      ownerKeys: value.owners,
      ordinarySessionTotal: value.ordinarySessionTotal,
      helperThreadTotal: value.helperThreadTotal,
      topLevelItems: value.topLevelItems,
      expectedOwnerFacts: value.expectedOwnerFacts,
      expectedGroupLabels: value.expectedGroupLabels,
      expand: value.expand,
      expectedMemberIds: value.expectedMemberIds,
      expectedMemberFacts: value.expectedMemberFacts,
    };
  });

  return { owners, groups, memberPages, cases };
}

/** The isolated identity every generated owner row is served under. */
const SHAPE_OWNER_ACCOUNT = "12345678-1234-4234-8234-1234567890ef";

/**
 * One grouped owner row as the grouped list serves it: a transcript item
 * carrying the group summaries that hang off it and the owner's own measures.
 */
export function ownerItem(
  fixtures: GroupedShapeOracleFixtures,
  ownerKey: string,
): VillageSessionListItem {
  const owner = fixtures.owners[ownerKey];
  if (owner == null) throw new Error(`grouped shape oracle has no owner "${ownerKey}"`);
  return {
    kind: "transcript",
    transcript: {
      session: makeTranscriptFixture({
        id: memberUUID(ownerKey),
        local_id: `ses_${ownerKey.replace(/[^a-zA-Z0-9]/g, "")}`,
        owner_id: SHAPE_OWNER_ACCOUNT,
        title: owner.title,
        turn_count: owner.turnCount,
        ...(owner.inputSubmissionCount === null
          ? {}
          : { input_submission_count: owner.inputSubmissionCount }),
      }),
    },
    helperGroups: owner.groupKeys.map((groupKey) => fixtures.groups[groupKey]),
  } as unknown as VillageSessionListItem;
}

/** The declared member page a `scope`+`page` request is answered from. */
export function shapeMemberPageFor(
  fixtures: GroupedShapeOracleFixtures,
  scope: string,
  page: number,
): ShapeMemberPage | undefined {
  return page === 1 ? fixtures.memberPages[scope] : undefined;
}

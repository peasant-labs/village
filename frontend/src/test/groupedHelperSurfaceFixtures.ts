import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import {
  zHelperGroupSummary,
  type HelperGroupSummary,
} from "@peasant-labs/schema";
import { assertNamesMatch } from "@/test/fixtureAssertions";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { memberItem, memberUUID, type MemberSpec } from "@/test/groupedHelperMountFixtures";

/**
 * Typed loader for `src/testdata/grouped-helper-surfaces.yaml`.
 *
 * The corpus states what each mounted surface must serve and must show: the
 * helper-only context discovery has to render exactly once, and the grouped
 * page composition that carries the profile/project read past its first server
 * page. The loader refuses a corpus that loses a required NAME, repeats one,
 * states an unknown field, or declares a page composition the grouped endpoint
 * could not serve — a case cannot silently disappear and an inert expectation
 * cannot survive.
 */

export interface SurfaceContext {
  groupId: string;
  ownerStatus: string;
}

export interface ExploreSurfaceCase {
  name: string;
  context: SurfaceContext;
  group: HelperGroupSummary;
  member: MemberSpec;
}

export type ContinuationRoute = "profile" | "project";

export interface ContinuationCase {
  name: string;
  route: ContinuationRoute;
  owner: string;
  /** The project hash for the project arm; null for the profile arm. */
  projectHash: string | null;
  /** Flat rows the route's own list serves, so its panel is not empty. */
  flatTranscripts: number;
  /** Grouped top-level units one server page may carry. */
  pageSize: number;
  /** Grouped top-level units the route serves on its first page. */
  firstPageOrdinary: number;
  /** Grouped top-level units the same filters select in total. */
  totalItems: number;
  laterContext: SurfaceContext;
  laterGroup: HelperGroupSummary;
  laterMember: MemberSpec;
}

export interface GroupedHelperSurfaceFixtures {
  explore: ExploreSurfaceCase;
  continuationCases: ContinuationCase[];
}

const REQUIRED_CONTINUATION_CASES = [
  "profile-continuation-reaches-later-context",
  "project-continuation-reaches-later-context",
];

const EXPLORE_FIELDS = ["name", "context", "group", "member"];
const CONTINUATION_FIELDS = [
  "name",
  "route",
  "owner",
  "projectHash",
  "flatTranscripts",
  "pageSize",
  "firstPageOrdinary",
  "totalItems",
  "laterContext",
  "laterGroup",
  "laterMember",
];
const MEMBER_FIELDS = ["id", "title", "turnCount", "inputSubmissionCount"];

function fail(name: string, reason: string): never {
  throw new Error(`grouped helper surface fixture case "${name}" ${reason}`);
}

function checkFields(value: object, allowed: string[], name: string): void {
  const unknown = Object.keys(value).filter((field) => !allowed.includes(field));
  if (unknown.length > 0) fail(name, `states unknown fields: ${unknown.join(", ")}`);
  const missing = allowed.filter((field) => !(field in value));
  if (missing.length > 0) fail(name, `is missing required fields: ${missing.join(", ")}`);
}

function surfaceContext(value: SurfaceContext, name: string, field: string): SurfaceContext {
  checkFields(value, ["groupId", "ownerStatus"], `${name}:${field}`);
  if (!value.groupId || !value.ownerStatus) {
    fail(name, `declared a ${field} without both a group id and an owner status`);
  }
  return value;
}

function surfaceGroup(value: HelperGroupSummary, name: string, field: string): HelperGroupSummary {
  try {
    return zHelperGroupSummary.parse(value);
  } catch (error) {
    fail(name, `declared a ${field} the canonical group summary rejects: ${String(error)}`);
  }
}

function surfaceMember(value: MemberSpec, name: string, field: string): MemberSpec {
  checkFields(value, MEMBER_FIELDS, `${name}:${field}`);
  if (
    !value.id ||
    typeof value.title !== "string" ||
    !Number.isInteger(value.turnCount) ||
    (value.inputSubmissionCount !== null && !Number.isInteger(value.inputSubmissionCount))
  ) {
    fail(name, `declared a ${field} without an id, a title, an integer turn count and an input count or null`);
  }
  return value;
}

export function loadGroupedHelperSurfaceFixtures(): GroupedHelperSurfaceFixtures {
  const root = parse(
    readFileSync(resolve(process.cwd(), "src/testdata/grouped-helper-surfaces.yaml"), "utf8"),
    { strict: true },
  );

  const rawExplore = root.explore as ExploreSurfaceCase;
  checkFields(rawExplore, EXPLORE_FIELDS, rawExplore.name ?? "explore");
  if (!rawExplore.name) fail("explore", "is missing its name");
  const explore: ExploreSurfaceCase = {
    name: rawExplore.name,
    context: surfaceContext(rawExplore.context, rawExplore.name, "context"),
    group: surfaceGroup(rawExplore.group, rawExplore.name, "group"),
    member: surfaceMember(rawExplore.member, rawExplore.name, "member"),
  };
  if (explore.context.groupId !== explore.group.groupId) {
    fail(explore.name, "declared a context whose group id differs from its group summary");
  }

  const rawCases = root.continuationCases as ContinuationCase[];
  assertNamesMatch(
    rawCases.map((value) => value.name),
    REQUIRED_CONTINUATION_CASES,
    "grouped helper surface continuation",
  );

  const continuationCases = rawCases.map((value): ContinuationCase => {
    const name = value.name;
    checkFields(value, CONTINUATION_FIELDS, name);
    if (value.route !== "profile" && value.route !== "project") {
      fail(name, `stated an unknown route "${String(value.route)}"`);
    }
    if (!value.owner) fail(name, "is missing its owner");
    if (value.route === "project") {
      if (typeof value.projectHash !== "string" || !/^[0-9a-f]{64}$/.test(value.projectHash)) {
        fail(name, "states a project route without a 64-character hexadecimal project hash");
      }
    } else if (value.projectHash !== null) {
      fail(name, "states the profile route with a project hash");
    }
    for (const field of ["flatTranscripts", "pageSize", "firstPageOrdinary", "totalItems"] as const) {
      if (!Number.isInteger(value[field]) || value[field] < 1) {
        fail(name, `stated a non-positive integer ${field}`);
      }
    }
    if (value.firstPageOrdinary !== value.pageSize) {
      fail(
        name,
        `states first page ${value.firstPageOrdinary} of page size ${value.pageSize}; the first grouped page must be full for a later page to exist`,
      );
    }
    if (value.totalItems <= value.firstPageOrdinary) {
      fail(
        name,
        `states ${value.totalItems} grouped results for a first page of ${value.firstPageOrdinary}, so no continuation exists to test`,
      );
    }
    const laterContext = surfaceContext(value.laterContext, name, "laterContext");
    const laterGroup = surfaceGroup(value.laterGroup, name, "laterGroup");
    if (laterContext.groupId !== laterGroup.groupId) {
      fail(name, "declared a later context whose group id differs from its group summary");
    }
    return {
      ...value,
      laterContext,
      laterGroup,
      laterMember: surfaceMember(value.laterMember, name, "laterMember"),
    };
  });

  return { explore, continuationCases };
}

/** A canonical owner identity for every generated grouped row. */
export const SURFACE_OWNER_UUID = "12345678-1234-4234-8234-1234567890ab";

/**
 * The grouped list payload a page of one continuation case is served from: a
 * full first page of ordinary owners with no saved helpers, then the helper-only
 * context container the continuation reaches.
 */
export function groupedSurfacePage(
  testCase: ContinuationCase,
  page: number,
): unknown {
  const limit = testCase.pageSize;
  const ordinary = () =>
    Array.from({ length: testCase.firstPageOrdinary }, (_, index) => ({
      kind: "transcript" as const,
      transcript: {
        session: makeTranscriptFixture({
          id: ordinaryOwnerUUID(index),
          local_id: `ses_ordinary${index}`,
          owner_id: SURFACE_OWNER_UUID,
          title: `ordinary ${index}`,
          project_hash: testCase.projectHash ?? "0".repeat(64),
          project_name: "village",
          project_display_name: "village",
        }),
      },
    }));
  const later = [
    {
      kind: "context_container" as const,
      context: {
        groupId: testCase.laterContext.groupId,
        ownerStatus: testCase.laterContext.ownerStatus,
      },
      helperGroups: [testCase.laterGroup],
    },
  ];
  return {
    items: page === 1 ? ordinary() : later,
    page,
    limit,
    totalItems: testCase.totalItems,
    ordinarySessionTotal: testCase.firstPageOrdinary,
    helperThreadTotal: testCase.laterGroup.helperThreadCount,
  };
}

/** The grouped list payload the discovery section is served. */
export function exploreGroupedPayload(testCase: ExploreSurfaceCase): unknown {
  return {
    items: [
      {
        kind: "context_container",
        context: { groupId: testCase.context.groupId, ownerStatus: testCase.context.ownerStatus },
        helperGroups: [testCase.group],
      },
    ],
    page: 1,
    limit: 24,
    totalItems: 1,
    ordinarySessionTotal: 0,
    helperThreadTotal: testCase.group.helperThreadCount,
  };
}

/** The member payload a surface's single member scope is served. */
export function surfaceMemberPayload(spec: MemberSpec, limit = 20): unknown {
  return { members: [memberItem(spec)], page: 1, limit, total: 1 };
}

/** A stable wire identity for the generated ordinary grouped owners. */
export function ordinaryOwnerUUID(index: number): string {
  return memberUUID(`ordinary-owner-${index}`);
}

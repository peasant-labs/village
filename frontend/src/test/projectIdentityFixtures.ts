import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys } from "@/test/fixtureAssertions";

export type ProjectIdentityGroupingItem = {
  projectHash: string;
  rawProjectName: string;
  projectDisplayName: string;
  publishedAt: string;
};

export type ProjectIdentityGroupingCase = {
  name: string;
  items: ProjectIdentityGroupingItem[];
  expectedGroupCount: number;
  expectedGroupDisplayNames: string[];
  expectedGroupItemCounts: number[];
};

export type ProjectIdentityBreadcrumbHrefCase = {
  name: string;
  ownerUsername: string | null;
  projectHash: string | null;
  expectedHref: string | null;
};

export type RepoGroupLabelCase = {
  name: string;
  gitRemote: string | null;
  /** "" (never null) — matches the wire, which never sends null here. */
  projectRemoteLabel: string;
  projectDisplayName: string;
  expectedName: string;
};

export type ProjectIdentityFixtures = {
  groupingCases: ProjectIdentityGroupingCase[];
  breadcrumbHrefCases: ProjectIdentityBreadcrumbHrefCase[];
  repoGroupLabelCases: RepoGroupLabelCase[];
};

// Required-NAME manifests, not counts (this epoch bans count guards): a
// deleted case fails the loader because its name goes missing from the set,
// not because a tally shrinks.
const requiredGroupingCaseNames = [
  "mixed-name-same-hash-collapses-to-one-group",
  "distinct-hashes-stay-separate-even-with-the-same-display-name",
  "groups-sorted-by-most-recent-transcript",
] as const;

const requiredBreadcrumbHrefCaseNames = [
  "username-and-hash-present-builds-href",
  "missing-project-hash-degrades-to-no-href",
  "missing-owner-username-degrades-to-no-href",
  "username-with-reserved-url-characters-is-encoded",
] as const;

const requiredRepoGroupLabelCaseNames = [
  "label-reads-project-remote-label-not-project-display-name",
  "missing-remote-label-falls-back-to-the-raw-remote",
  "no-remote-at-all-stays-unattributed-regardless-of-labels",
] as const;

const groupingItemKeys = ["projectHash", "rawProjectName", "projectDisplayName", "publishedAt"];
const groupingCaseKeys = [
  "name",
  "items",
  "expectedGroupCount",
  "expectedGroupDisplayNames",
  "expectedGroupItemCounts",
];
const breadcrumbHrefCaseKeys = ["name", "ownerUsername", "projectHash", "expectedHref"];
const repoGroupLabelCaseKeys = ["name", "gitRemote", "projectRemoteLabel", "projectDisplayName", "expectedName"];

function assertNamesMatch(actual: string[], required: readonly string[], label: string): void {
  const got = [...actual].sort();
  const want = [...required].sort();
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    throw new Error(`${label} case names differ: got ${got.join(", ")}; want ${want.join(", ")}`);
  }
  if (new Set(actual).size !== actual.length) {
    throw new Error(`${label} fixture case names must be unique`);
  }
}

export function loadProjectIdentityFixtures(): ProjectIdentityFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/project-identity.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("project-identity fixture root must be an object");
  }
  assertExactKeys(parsed, ["groupingCases", "breadcrumbHrefCases", "repoGroupLabelCases"], "fixture root");
  const fixtures = parsed as ProjectIdentityFixtures;

  assertNamesMatch(
    fixtures.groupingCases.map((c) => c.name),
    requiredGroupingCaseNames,
    "project-identity groupingCases",
  );
  for (const c of fixtures.groupingCases) {
    assertExactKeys(c, groupingCaseKeys, `grouping case ${c.name}`);
    for (const item of c.items) {
      assertExactKeys(item, groupingItemKeys, `grouping case ${c.name} item`);
      if (!/^[0-9a-f]{64}$/.test(item.projectHash)) {
        throw new Error(`grouping case ${c.name}: projectHash must be 64 lowercase hex chars, got ${item.projectHash}`);
      }
    }
    if (
      c.expectedGroupDisplayNames.length !== c.expectedGroupCount ||
      c.expectedGroupItemCounts.length !== c.expectedGroupCount
    ) {
      throw new Error(
        `grouping case ${c.name}: expectedGroupDisplayNames/expectedGroupItemCounts must each have exactly ` +
          `expectedGroupCount (${c.expectedGroupCount}) entries — one per expected group, in the same order ` +
          `groupByProject returns them (most-recent-transcript first)`,
      );
    }
    const totalExpectedItems = c.expectedGroupItemCounts.reduce((a, b) => a + b, 0);
    if (totalExpectedItems !== c.items.length) {
      throw new Error(
        `grouping case ${c.name}: expectedGroupItemCounts sums to ${totalExpectedItems} but the case supplies ` +
          `${c.items.length} items — every input item must land in exactly one expected group`,
      );
    }
  }

  assertNamesMatch(
    fixtures.breadcrumbHrefCases.map((c) => c.name),
    requiredBreadcrumbHrefCaseNames,
    "project-identity breadcrumbHrefCases",
  );
  for (const c of fixtures.breadcrumbHrefCases) {
    assertExactKeys(c, breadcrumbHrefCaseKeys, `breadcrumb-href case ${c.name}`);
    if ((c.ownerUsername == null || c.projectHash == null) !== (c.expectedHref == null)) {
      throw new Error(
        `breadcrumb-href case ${c.name}: expectedHref must be null exactly when either ownerUsername or ` +
          `projectHash is missing, and non-null when both are present`,
      );
    }
  }

  assertNamesMatch(
    fixtures.repoGroupLabelCases.map((c) => c.name),
    requiredRepoGroupLabelCaseNames,
    "project-identity repoGroupLabelCases",
  );
  for (const c of fixtures.repoGroupLabelCases) {
    assertExactKeys(c, repoGroupLabelCaseKeys, `repo-group-label case ${c.name}`);
    if (c.gitRemote == null && c.expectedName !== "Unattributed") {
      throw new Error(
        `repo-group-label case ${c.name}: a null gitRemote must always expect the "Unattributed" bucket name`,
      );
    }
    if (c.gitRemote != null && c.expectedName === c.projectDisplayName) {
      throw new Error(
        `repo-group-label case ${c.name}: expectedName equals projectDisplayName, which makes this case ` +
          `unable to distinguish groupByRepo reading project_remote_label from a regression back to ` +
          `project_display_name — give expectedName and projectDisplayName different values`,
      );
    }
  }

  return fixtures;
}

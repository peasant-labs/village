import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { parse } from "yaml";
import { testdataPath } from "@/test/testdataPath";
import { zVillageContributionPageItem, type VillageContributionPageItem } from "@peasant-labs/schema";
import { pagedRowMeta, sectionBranches } from "./pagedTree";
import { projectHashOf, transcriptIdOf, transcriptOf } from "./testTokens";

interface SessionToken {
  id: string;
  project: string;
  branch?: string;
  provider?: string;
  visibility?: string;
  start?: string;
  end?: string;
}

interface FamilyToken {
  root: SessionToken;
  helpers?: SessionToken[];
}

interface BranchExpectation {
  label: string;
  families: string[];
  helpers?: Record<string, string[]>;
  rows?: string[];
}

interface Fixtures {
  branches: { name: string; families: FamilyToken[]; expect: BranchExpectation[] }[];
  meta: { name: string; session: SessionToken; family: string; expect: string[] }[];
}

const fixtures = parse(readFileSync(testdataPath("paged-contribute-tree.yaml"), "utf8")) as Fixtures;

const REQUIRED_BRANCHES = [
  "families-group-under-their-roots-branch",
  "the-unknown-branch-comes-last",
  "a-helper-on-another-branch-stays-with-its-root",
];
const REQUIRED_META = [
  "a-root-shows-its-harness-last-activity-and-visibility",
  "a-session-without-an-end-shows-its-start",
  "a-session-without-times-shows-its-publish-date",
  "a-helper-in-another-project-names-that-project",
  "a-session-without-a-harness-says-so",
];

function requireNames(actual: string[], required: string[], label: string): void {
  const missing = required.filter((name) => !actual.includes(name));
  if (missing.length > 0) throw new Error(`missing ${label} fixture cases: ${missing.join(", ")}`);
}

requireNames(fixtures.branches.map((c) => c.name), REQUIRED_BRANCHES, "branch");
requireNames(fixtures.meta.map((c) => c.name), REQUIRED_META, "meta");

function sessionOf(token: SessionToken) {
  return {
    ...transcriptOf({ id: token.id, project: token.project, provider: token.provider, visibility: token.visibility }),
    git_branch: token.branch ?? null,
    published_at: "2026-11-01T12:00:00Z",
    ...(token.start ? { session_start: token.start } : {}),
    ...(token.end ? { session_end: token.end } : {}),
  };
}

function itemOf(family: FamilyToken): VillageContributionPageItem {
  return zVillageContributionPageItem.parse({
    root: { transcript: sessionOf(family.root) },
    helpers: (family.helpers ?? []).map((helper) => ({ transcript: sessionOf(helper) })),
  });
}

const named = <T extends { name: string }>(cases: T[]) => cases.map((c) => [c.name, c] as const);
const ids = (tokens: string[]) => tokens.map(transcriptIdOf);

describe("branches of a project section", () => {
  it.each(named(fixtures.branches))("%s", (_name, testCase) => {
    const branches = sectionBranches(testCase.families.map(itemOf));
    expect(branches.map((branch) => branch.label)).toEqual(testCase.expect.map((branch) => branch.label));
    branches.forEach((branch, index) => {
      const want = testCase.expect[index];
      expect(branch.families.map((family) => family.root.transcript.id)).toEqual(ids(want.families));
      for (const [root, helpers] of Object.entries(want.helpers ?? {})) {
        const family = branch.families.find((f) => f.root.transcript.id === transcriptIdOf(root));
        expect(family?.helpers.map((row) => row.transcript.id)).toEqual(ids(helpers));
      }
      if (want.rows) expect(branch.rows.map((row) => row.transcript.id)).toEqual(ids(want.rows));
    });
  });
});

describe("session meta line", () => {
  it.each(named(fixtures.meta))("%s", (_name, testCase) => {
    const transcript = itemOf({ root: testCase.session }).root.transcript;
    const row = {
      transcript,
      family_project_hash: projectHashOf(testCase.family),
      root_id: transcript.id,
      is_root: true,
    };
    expect(pagedRowMeta(row)).toEqual(testCase.expect);
  });
});

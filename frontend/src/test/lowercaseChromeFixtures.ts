import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/**
 * Loader for `src/testdata/lowercase-chrome.yaml` — the corpus behind
 * `src/lowercaseChrome.test.tsx`.
 *
 * Deletion protection is the required-NAME manifest below, never a count. The
 * closed set of surfaces and their states lives here too, and the loader
 * requires at least one case per surface, so a surface cannot quietly drop out
 * of the corpus while its name stays in the list.
 */

/** Each surface the lowercase pass covers, and the states it can be mounted in. */
export const SURFACE_STATES = {
  navbar: ["account-menu-open", "signed-out"],
  "linked-repositories": ["owner-with-repositories", "not-configured"],
  "group-page": ["owner", "visitor", "not-found", "confirm-remove", "invite-search"],
  "publish-page": ["with-transcripts", "import-dialog-open"],
  "contribute-picker": ["open", "no-collectives", "contributed"],
  "pending-approval-bar": ["pending"],
} as const;

export type LowercaseSurface = keyof typeof SURFACE_STATES;

export type LowercaseContentKey =
  | "collective"
  | "transcript"
  | "repository"
  | "viewer"
  | "githubUser";

export type LowercaseChromeCase = {
  name: string;
  surface: LowercaseSurface;
  state: string;
  expectContent: LowercaseContentKey[];
};

export type LowercaseChromeFixtures = {
  content: Record<LowercaseContentKey, string>;
  cases: LowercaseChromeCase[];
};

const requiredCaseNames = [
  "the-header-with-the-account-menu-open",
  "the-header-for-a-signed-out-visitor",
  "linked-repositories-for-an-owner",
  "linked-repositories-when-github-is-not-set-up",
  "a-collective-page-for-its-owner",
  "a-collective-page-for-a-signed-in-visitor",
  "a-collective-that-does-not-exist",
  "a-collective-page-confirming-a-removal",
  "a-collective-page-searching-for-someone-to-invite",
  "the-publish-dashboard-with-a-transcript",
  "the-publish-dashboard-with-the-import-dialog-open",
  "the-contribute-picker-open",
  "the-contribute-picker-with-no-collectives",
  "the-contribute-picker-after-contributing",
  "the-pending-approval-bar",
] as const;

const contentKeys: LowercaseContentKey[] = [
  "collective",
  "transcript",
  "repository",
  "viewer",
  "githubUser",
];

export function loadLowercaseChromeFixtures(): LowercaseChromeFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/lowercase-chrome.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("lowercase-chrome fixture root must be an object");
  }
  assertExactKeys(parsed, ["content", "cases"], "lowercase-chrome fixture root");
  const fixtures = parsed as LowercaseChromeFixtures;
  assertExactKeys(fixtures.content, contentKeys, "lowercase-chrome content");

  // User content that is already lowercase proves nothing about keeping its
  // case. The collective name, the one the acceptance names, must carry a
  // capital, and so must the transcript title and the repository.
  for (const key of ["collective", "transcript", "repository", "githubUser"] as const) {
    if (!/[A-Z]/.test(fixtures.content[key])) {
      throw new Error(`lowercase-chrome content.${key} must carry a capital letter to be a test of anything`);
    }
  }

  assertNamesMatch(
    fixtures.cases.map((c) => c.name),
    requiredCaseNames,
    "lowercase-chrome cases",
  );
  for (const c of fixtures.cases) {
    assertExactKeys(c, ["name", "surface", "state", "expectContent"], `lowercase-chrome case ${c.name}`);
    const states = SURFACE_STATES[c.surface] as readonly string[] | undefined;
    if (states == null) {
      throw new Error(`lowercase-chrome case ${c.name}: ${c.surface} is not a covered surface`);
    }
    if (!states.includes(c.state)) {
      throw new Error(
        `lowercase-chrome case ${c.name}: ${c.surface} has no state ${c.state} (${states.join(", ")})`,
      );
    }
    for (const key of c.expectContent) {
      if (!contentKeys.includes(key)) {
        throw new Error(`lowercase-chrome case ${c.name}: ${key} is not a content key`);
      }
    }
  }
  for (const surface of Object.keys(SURFACE_STATES)) {
    if (!fixtures.cases.some((c) => c.surface === surface)) {
      throw new Error(`lowercase-chrome corpus has no case for the ${surface} surface`);
    }
  }
  if (!fixtures.cases.some((c) => c.expectContent.includes("collective"))) {
    throw new Error("lowercase-chrome corpus must hold a case that shows a collective's name");
  }
  return fixtures;
}

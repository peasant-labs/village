import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import { makeGroup, type CollectiveWorld, type WorldMember } from "@/test/mountedCollectivePages";
import type { Group } from "@/lib/types";

/** Typed loader for `src/testdata/collective-settings.yaml`. */

export type SettingsAction =
  | { kind: "edit-text"; label: string; value: string }
  | { kind: "toggle"; label: string }
  | { kind: "choose"; group: string; label: string }
  | { kind: "select"; label: string; value: string }
  | { kind: "role"; member: string; role: string }
  | { kind: "remove"; member: string }
  | { kind: "invite"; value: string }
  | { kind: "delete" }
  | { kind: "none" };

export interface SettingsWrite {
  method: string;
  path: string;
  body: unknown;
}

export interface SettingsCase {
  name: string;
  why: string;
  search: string;
  fail: { method: string; path: string; status: number; error: string } | null;
  action: SettingsAction;
  expect: {
    writes: SettingsWrite[];
    shows: string[];
    alert: string | null;
    switchOn: boolean | null;
    /** The card pressed afterwards in the chosen group (a `choose` action). */
    pressed: string | null;
    notice: string | null;
    replaced: string | null;
    pushed: string | null;
  };
}

/** Which section an address opens. */
export interface SectionCase {
  name: string;
  why: string;
  search: string;
  expectSection: string;
  expectTitle: string;
}

export interface CollectiveSettingsFixtures {
  cases: SettingsCase[];
  sectionCases: SectionCase[];
  /** A fresh world for one case, and the ids `{id}` and `{member}` stand for. */
  worldFor(settingsCase: SettingsCase): CollectiveWorld;
  /** A path from the fixture with `{id}` and `{member}` filled in for this case. */
  pathFor(settingsCase: SettingsCase, path: string): string;
}

const REQUIRED_CASES = [
  "a-text-field-saves-only-after-edit",
  "the-description-saves-after-edit",
  "a-choice-card-saves-the-moment-it-is-pressed",
  "a-refused-choice-puts-the-held-card-back",
  "a-failed-switch-write-puts-the-value-back",
  "the-toggle-off-sends-exactly-that-field",
  "when-a-member-leaves-saves-the-moment-it-changes",
  "a-role-change-uses-the-member-role-route",
  "a-member-removal-asks-first-then-uses-the-member-route",
  "an-invite-by-username-uses-the-members-route",
  "the-check-mode-radios-are-absent",
  "the-github-section-counts-the-linked-repositories",
  "github-installed-says-so-and-clears-the-parameter",
  "delete-asks-for-consent-then-deletes-the-collective",
];

const REQUIRED_SECTION_CASES = [
  "no-section-opens-general",
  "the-access-section",
  "the-members-section",
  "the-github-section",
  "the-pull-requests-section",
  "the-danger-zone",
  "an-unknown-section-opens-general",
  "the-install-return-opens-github",
];

const SECTIONS = new Set(["general", "access", "members", "github", "pull-requests", "danger"]);

/** The section each action's control lives in: a case must open that section to reach it. */
const ACTION_SECTION: Partial<Record<SettingsAction["kind"], string>> = {
  toggle: "pull-requests",
  choose: "access",
  select: "members",
  role: "members",
  remove: "members",
  invite: "members",
  delete: "danger",
};

const ACTION_FIELDS: Record<SettingsAction["kind"], string[]> = {
  "edit-text": ["kind", "label", "value"],
  toggle: ["kind", "label"],
  choose: ["kind", "group", "label"],
  select: ["kind", "label", "value"],
  role: ["kind", "member", "role"],
  remove: ["kind", "member"],
  invite: ["kind", "value"],
  delete: ["kind"],
  none: ["kind"],
};

/** The section an address opens, as the page reads it. */
function sectionOf(search: string): string {
  const params = new URLSearchParams(search);
  if (params.get("github_installed") === "1") return "github";
  const asked = params.get("section") ?? "";
  return SECTIONS.has(asked) ? asked : "general";
}

function fail(where: string, reason: string): never {
  throw new Error(`collective-settings fixture ${where} ${reason}`);
}

export function loadCollectiveSettingsFixtures(): CollectiveSettingsFixtures {
  const root = parse(
    readFileSync(resolve(process.cwd(), "src/testdata/collective-settings.yaml"), "utf8"),
    { strict: true },
  ) as Record<string, never>;
  assertExactKeys(root, ["collective", "viewer", "members", "cases", "sectionCases"], "collective-settings root");
  const collective = root.collective as Partial<Group> & { id: string; name: string };
  const viewer = root.viewer as { id: string; username: string };
  const members = root.members as WorldMember[];
  for (const member of members) assertExactKeys(member, ["id", "username", "name", "role"], `member ${member.username}`);

  const cases = root.cases as SettingsCase[];
  assertNamesMatch(cases.map((c) => c.name), REQUIRED_CASES, "collective settings");
  for (const c of cases) {
    assertExactKeys(c, ["name", "why", "search", "fail", "action", "expect"], `case ${c.name}`);
    assertExactKeys(c.expect, ["writes", "shows", "alert", "switchOn", "pressed", "notice", "replaced", "pushed"], `case ${c.name} expect`);
    if (!c.why?.trim()) fail(`case "${c.name}"`, "states no reason it exists");
    const fields = ACTION_FIELDS[c.action.kind];
    if (!fields) fail(`case "${c.name}"`, `names an unknown action "${c.action.kind}"`);
    assertExactKeys(c.action, fields, `case ${c.name} action`);
    if ("member" in c.action && !members.some((m) => m.username === (c.action as { member: string }).member)) {
      fail(`case "${c.name}"`, "names a member the collective does not have");
    }
    if (c.fail && c.expect.alert !== c.fail.error) fail(`case "${c.name}"`, "fails a write but expects a different alert");
    for (const write of c.expect.writes) assertExactKeys(write, ["method", "path", "body"], `case ${c.name} write`);
    const needs = ACTION_SECTION[c.action.kind];
    if (needs && sectionOf(c.search) !== needs) fail(`case "${c.name}"`, `acts in the ${needs} section but opens ${sectionOf(c.search)}`);
    if ((c.expect.pressed !== null) !== (c.action.kind === "choose")) fail(`case "${c.name}"`, "expects a pressed card without choosing one, or chooses one without saying which stays pressed");
    if (c.expect.switchOn !== null && sectionOf(c.search) !== "pull-requests") fail(`case "${c.name}"`, "expects the switch outside the pull requests section");
  }

  const sectionCases = root.sectionCases as SectionCase[];
  assertNamesMatch(sectionCases.map((c) => c.name), REQUIRED_SECTION_CASES, "collective settings sections");
  for (const c of sectionCases) {
    assertExactKeys(c, ["name", "why", "search", "expectSection", "expectTitle"], `section case ${c.name}`);
    if (!c.why?.trim()) fail(`section case "${c.name}"`, "states no reason it exists");
    if (!SECTIONS.has(c.expectSection)) fail(`section case "${c.name}"`, `expects an unknown section "${c.expectSection}"`);
  }

  const memberId = (c: SettingsCase) =>
    "member" in c.action ? members.find((m) => m.username === (c.action as { member: string }).member)?.id ?? "" : "";

  return {
    cases,
    sectionCases,
    worldFor: (c) => ({
      viewer,
      group: makeGroup(collective),
      role: "owner",
      canRead: true,
      members: members.map((m) => ({ ...m })),
      transcripts: [],
      linked: ["acme/ingest-api"],
      available: [{ repo: "acme/ingest-api", private: true, publishers: 1 }],
      failures: c.fail ? [{ method: c.fail.method, path: c.fail.path.replace("{id}", collective.id), status: c.fail.status, error: c.fail.error }] : [],
    }),
    pathFor: (c, path) => path.replace("{id}", collective.id).replace("{member}", memberId(c)),
  };
}

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
  | { kind: "role"; member: string; role: string }
  | { kind: "remove"; member: string }
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
    notice: string | null;
    replaced: string | null;
  };
}

export interface CollectiveSettingsFixtures {
  cases: SettingsCase[];
  /** A fresh world for one case, and the ids `{id}` and `{member}` stand for. */
  worldFor(settingsCase: SettingsCase): CollectiveWorld;
  /** A path from the fixture with `{id}` and `{member}` filled in for this case. */
  pathFor(settingsCase: SettingsCase, path: string): string;
}

const REQUIRED_CASES = [
  "a-text-field-saves-only-after-edit",
  "a-failed-switch-write-puts-the-value-back",
  "the-toggle-off-sends-exactly-that-field",
  "a-role-change-uses-the-member-role-route",
  "a-member-removal-asks-first-then-uses-the-member-route",
  "the-check-mode-radios-are-absent",
  "github-installed-says-so-and-clears-the-parameter",
];

const ACTION_FIELDS: Record<SettingsAction["kind"], string[]> = {
  "edit-text": ["kind", "label", "value"],
  toggle: ["kind", "label"],
  role: ["kind", "member", "role"],
  remove: ["kind", "member"],
  none: ["kind"],
};

function fail(where: string, reason: string): never {
  throw new Error(`collective-settings fixture ${where} ${reason}`);
}

export function loadCollectiveSettingsFixtures(): CollectiveSettingsFixtures {
  const root = parse(
    readFileSync(resolve(process.cwd(), "src/testdata/collective-settings.yaml"), "utf8"),
    { strict: true },
  ) as Record<string, never>;
  assertExactKeys(root, ["collective", "viewer", "members", "cases"], "collective-settings root");
  const collective = root.collective as Partial<Group> & { id: string; name: string };
  const viewer = root.viewer as { id: string; username: string };
  const members = root.members as WorldMember[];
  for (const member of members) assertExactKeys(member, ["id", "username", "name", "role"], `member ${member.username}`);

  const cases = root.cases as SettingsCase[];
  assertNamesMatch(cases.map((c) => c.name), REQUIRED_CASES, "collective settings");
  for (const c of cases) {
    assertExactKeys(c, ["name", "why", "search", "fail", "action", "expect"], `case ${c.name}`);
    assertExactKeys(c.expect, ["writes", "shows", "alert", "switchOn", "notice", "replaced"], `case ${c.name} expect`);
    if (!c.why?.trim()) fail(`case "${c.name}"`, "states no reason it exists");
    const fields = ACTION_FIELDS[c.action.kind];
    if (!fields) fail(`case "${c.name}"`, `names an unknown action "${c.action.kind}"`);
    assertExactKeys(c.action, fields, `case ${c.name} action`);
    if ("member" in c.action && !members.some((m) => m.username === (c.action as { member: string }).member)) {
      fail(`case "${c.name}"`, "names a member the collective does not have");
    }
    if (c.fail && c.expect.alert !== c.fail.error) fail(`case "${c.name}"`, "fails a write but expects a different alert");
    for (const write of c.expect.writes) assertExactKeys(write, ["method", "path", "body"], `case ${c.name} write`);
  }

  const memberId = (c: SettingsCase) =>
    "member" in c.action ? members.find((m) => m.username === (c.action as { member: string }).member)?.id ?? "" : "";

  return {
    cases,
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

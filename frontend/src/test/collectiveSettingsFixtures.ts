import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import { makeGroup, type CollectiveWorld, type WorldMember } from "@/test/mountedCollectivePages";
import type { VillageCollectiveInvitationSent } from "@peasant-labs/schema";
import type { Group } from "@/lib/types";
import { profileOwner } from "@/test/publicOwnerFixture";

/** Typed loader for `src/testdata/collective-settings.yaml`. */

export type SettingsAction =
  | { kind: "edit-text"; label: string; value: string }
  | { kind: "toggle"; label: string }
  | { kind: "choose"; group: string; label: string }
  | { kind: "select"; label: string; value: string }
  | { kind: "role"; member: string; role: string }
  | { kind: "remove"; member: string }
  | { kind: "invite"; value: string }
  | { kind: "revoke"; invitation: string }
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
  "an-invite-by-username-sends-an-invitation",
  "the-invited-list-shows-the-typed-handle-and-who-sent-it",
  "revoking-an-invitation-uses-its-own-route",
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
  revoke: "members",
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
  revoke: ["kind", "invitation"],
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

/** One open invitation as the fixture states it. */
interface WorldInvitation {
  id: string;
  username: string;
  /** The sending owner's username, or `anonymous` for one hidden from the viewer. */
  by: string;
}

function sentInvitation(invitation: WorldInvitation, members: WorldMember[]): VillageCollectiveInvitationSent {
  const sender = members.find((m) => m.username === invitation.by);
  return {
    id: invitation.id,
    invited_username: invitation.username,
    invited_by: sender
      ? profileOwner({ id: sender.id, github_username: sender.username, display_name: sender.name })
      : { kind: "anonymous" },
    created_at: "2026-10-20T12:00:00Z",
    expires_at: "2026-11-19T12:00:00Z",
  };
}

function fail(where: string, reason: string): never {
  throw new Error(`collective-settings fixture ${where} ${reason}`);
}

export function loadCollectiveSettingsFixtures(): CollectiveSettingsFixtures {
  const root = parse(
    readFileSync(resolve(process.cwd(), "src/testdata/collective-settings.yaml"), "utf8"),
    { strict: true },
  ) as Record<string, never>;
  assertExactKeys(root, ["collective", "viewer", "invitations", "members", "cases", "sectionCases"], "collective-settings root");
  const collective = root.collective as Partial<Group> & { id: string; name: string };
  const viewer = root.viewer as { id: string; username: string };
  const members = root.members as WorldMember[];
  for (const member of members) assertExactKeys(member, ["id", "username", "name", "role"], `member ${member.username}`);
  const invitations = root.invitations as WorldInvitation[];
  for (const invitation of invitations) {
    assertExactKeys(invitation, ["id", "username", "by"], `invitation ${invitation.username}`);
    if (invitation.by !== "anonymous" && !members.some((m) => m.username === invitation.by && m.role === "owner")) {
      fail(`invitation "${invitation.username}"`, "names a sender who is not an owner of the collective");
    }
  }

  const cases = root.cases as SettingsCase[];
  assertNamesMatch(cases.map((c) => c.name), REQUIRED_CASES, "collective settings");
  for (const c of cases) {
    assertExactKeys(c, ["name", "why", "search", "fail", "action", "expect"], `case ${c.name}`);
    assertExactKeys(c.expect, ["writes", "shows", "alert", "switchOn", "pressed", "notice", "replaced", "pushed"], `case ${c.name} expect`);
    if (!c.why?.trim()) fail(`case "${c.name}"`, "states no reason it exists");
    const fields = ACTION_FIELDS[c.action.kind];
    if (!fields) fail(`case "${c.name}"`, `names an unknown action "${c.action.kind}"`);
    assertExactKeys(c.action, fields, `case ${c.name} action`);
    if (c.action.kind === "revoke" && !invitations.some((i) => i.username === (c.action as { invitation: string }).invitation)) {
      fail(`case "${c.name}"`, "names an invitation the collective does not have");
    }
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

  const invitationId = (c: SettingsCase) =>
    c.action.kind === "revoke" ? invitations.find((i) => i.username === (c.action as { invitation: string }).invitation)?.id ?? "" : "";
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
      invitations: invitations.map((invitation) => sentInvitation(invitation, members)),
      transcripts: [],
      linked: ["acme/ingest-api"],
      available: [{ repo: "acme/ingest-api", private: true, publishers: 1 }],
      failures: c.fail ? [{ method: c.fail.method, path: c.fail.path.replace("{id}", collective.id), status: c.fail.status, error: c.fail.error }] : [],
    }),
    pathFor: (c, path) =>
      path
        .replace("{id}", collective.id)
        .replace("{member}", memberId(c))
        .replace("{invitation}", invitationId(c)),
  };
}

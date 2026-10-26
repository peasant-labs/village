import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import type { VillageCollectiveSearchResult } from "@peasant-labs/schema";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import {
  makeGroup,
  type CollectiveWorld,
  type WorldMember,
  type WorldRepo,
  type WorldTranscript,
} from "@/test/mountedCollectivePages";
import type { VisibleGroup } from "@/lib/types";
import type { PendingShare } from "@/lib/review/types";
import { profileOwner } from "@/test/publicOwnerFixture";

/**
 * Typed loaders for `src/testdata/collective-page.yaml` (the collective page,
 * the pull request cells and the world the collectives list recovers in) and
 * `src/testdata/repo-picker.yaml` (the repo picker's saves).
 */

// ── the collective page ──────────────────────────────────────────────────────

export interface PageCaseExpect {
  whoCanPublish: string;
  whoCanRead: string;
  yourRole: string;
  /** The head's action row, in order, by accessible name. */
  actions: string[];
  overflow: { label: string; route: string }[];
  /** The tabs offered, in order, by label. */
  tabs: string[];
  transcriptRows: number;
  /** The stats tab's github org line. */
  orgLine: string;
  manage: boolean;
  /** The members tab's summary, or null when the owner keeps the list private. */
  memberBreakdown: string | null;
  /** The members the stats count: a pending request is not one. */
  memberCount: number;
}

export interface PageCase {
  name: string;
  why: string;
  signedIn: boolean;
  role: string;
  canRead: boolean;
  group: { acceptance_mode: string; data_access: string; linked_github_org: string | null; display_members: boolean };
  linked: string[];
  expect: PageCaseExpect;
}

/** One address and key sequence on the collective page's tablist. */
export interface TabCase {
  name: string;
  why: string;
  role: string;
  acceptance_mode: string;
  withPending: boolean;
  search: string;
  keys: string[];
  expectSelected: string;
  expectSearch: string;
}

export interface PullRequestCase {
  name: string;
  why: string;
  rows: { id: string; pullRequests: string[]; count: number }[];
  expect: { id: string; labels: string[]; repo: string | null }[];
  hrefs: Record<string, string>;
}

export interface CollectivePageFixtures {
  cases: PageCase[];
  tabCases: TabCase[];
  /** What waits for the owner's approval, as the queue route answers it. */
  pending: PendingShare[];
  pullRequestCases: PullRequestCase[];
  /** A fresh world for one page case: every case gets its own copy to change. */
  worldFor(pageCase: PageCase): CollectiveWorld;
  /** A fresh world for one tab case. */
  tabWorld(tabCase: TabCase): CollectiveWorld;
  /** A fresh world for the collectives list. */
  listWorld(): CollectiveWorld;
}

const REQUIRED_PAGE_CASES = [
  "the-owner-sees-the-chips-the-org-total-and-both-overflow-items",
  "an-owner-without-an-org-is-offered-connect-github",
  "the-org-line-counts-only-the-orgs-repositories",
  "a-member-sees-the-org-without-the-total-and-can-leave",
  "a-contributor-who-cannot-read-is-told-so",
  "a-non-member-of-an-open-collective-can-join",
  "a-non-member-of-a-curated-collective-is-offered-no-join",
  "a-signed-out-reader-of-a-public-collective-reads-and-is-asked-to-sign-in-to-join",
  "a-member-of-a-collective-with-a-private-member-list-sees-no-members",
];

const REQUIRED_TAB_CASES = [
  "no-tab-in-the-address-opens-transcripts",
  "the-address-opens-the-stats-tab",
  "an-unknown-tab-falls-back-to-transcripts",
  "waiting-is-not-offered-to-a-member-of-a-curated-collective",
  "the-curated-owner-opens-waiting-from-the-address",
  "arrow-keys-move-between-tabs-and-keep-the-address",
  "arrow-left-wraps-to-the-last-tab",
  "home-and-end-jump-and-the-first-tab-clears-the-address",
];

const TAB_IDS = new Set(["transcripts", "waiting", "members", "stats"]);
const TAB_KEYS = new Set(["ArrowRight", "ArrowLeft", "Home", "End"]);

const REQUIRED_PULL_REQUEST_CASES = [
  "one-repository-reads-as-numbers-under-the-repository",
  "two-repositories-in-one-row-carry-their-repository",
  "one-number-in-two-repositories-on-a-page-is-never-short",
  "more-pull-requests-than-the-row-names-are-counted",
  "no-pull-requests-reads-as-not-linked",
];

const ROLES = new Set(["", "pending", "contributor", "member", "owner"]);

function fail(where: string, reason: string): never {
  throw new Error(`collective-page fixture ${where} ${reason}`);
}

function readYAML(file: string): Record<string, unknown> {
  return parse(readFileSync(resolve(process.cwd(), "src/testdata", file), "utf8"), { strict: true });
}

export function loadCollectivePageFixtures(): CollectivePageFixtures {
  const root = readYAML("collective-page.yaml") as Record<string, never>;
  assertExactKeys(
    root,
    ["collective", "viewer", "members", "transcripts", "available", "pending", "cases", "tabCases", "visible", "searchable", "pullRequestCases"],
    "collective-page root",
  );
  const collective = root.collective as { id: string; name: string; description: string };
  const viewer = root.viewer as { id: string; username: string };
  const members = root.members as WorldMember[];
  const transcripts = root.transcripts as WorldTranscript[];
  const available = root.available as WorldRepo[];
  for (const member of members) assertExactKeys(member, ["id", "username", "name", "role"], `member ${member.username}`);
  for (const t of transcripts) assertExactKeys(t, ["id", "title", "author", "pullRequests", "pullRequestCount"], `transcript ${t.id}`);
  for (const repo of available) assertExactKeys(repo, ["repo", "private", "publishers"], `available ${repo.repo}`);

  const cases = root.cases as PageCase[];
  assertNamesMatch(cases.map((c) => c.name), REQUIRED_PAGE_CASES, "collective page");
  for (const c of cases) {
    assertExactKeys(c, ["name", "why", "signedIn", "role", "canRead", "group", "linked", "expect"], `case ${c.name}`);
    assertExactKeys(c.group, ["acceptance_mode", "data_access", "linked_github_org", "display_members"], `case ${c.name} group`);
    assertExactKeys(
      c.expect,
      ["whoCanPublish", "whoCanRead", "yourRole", "actions", "overflow", "tabs", "transcriptRows", "orgLine", "manage", "memberBreakdown", "memberCount"],
      `case ${c.name} expect`,
    );
    if (!c.why?.trim()) fail(`case "${c.name}"`, "states no reason it exists");
    if (!ROLES.has(c.role)) fail(`case "${c.name}"`, `names an unknown role "${c.role}"`);
    // An expectation must follow from the case's own facts, so a case cannot
    // declare an outcome its inputs could never produce and then pass.
    const owner = c.role === "owner";
    const curated = c.group.acceptance_mode === "curated";
    if (!c.signedIn && c.role !== "") fail(`case "${c.name}"`, "gives a role to somebody who is not signed in");
    if (c.expect.actions.includes("settings") !== owner) fail(`case "${c.name}"`, "expects settings for a viewer who is not the owner, or none for the owner");
    if (c.expect.actions.includes("sign in to join") !== (!c.signedIn && !curated)) {
      fail(`case "${c.name}"`, "offers a sign-in to join where joining needs no account, or withholds it where it does");
    }
    if (c.expect.manage && !(owner && c.group.linked_github_org)) {
      fail(`case "${c.name}"`, "expects manage without an owner and a linked org");
    }
    if (c.expect.tabs.includes("waiting for approval") !== (owner && curated)) {
      fail(`case "${c.name}"`, "offers the waiting tab to somebody other than a curated collective's owner");
    }
    if ((c.expect.memberBreakdown === null) !== (!c.group.display_members && !owner)) {
      fail(`case "${c.name}"`, "hides the members where the collective shows them, or shows them where it does not");
    }
    if (!c.canRead && c.expect.transcriptRows !== 0) fail(`case "${c.name}"`, "expects rows the server does not let the viewer read");
  }

  const tabCases = root.tabCases as TabCase[];
  assertNamesMatch(tabCases.map((c) => c.name), REQUIRED_TAB_CASES, "collective tabs");
  for (const c of tabCases) {
    assertExactKeys(
      c,
      ["name", "why", "role", "acceptance_mode", "withPending", "search", "keys", "expectSelected", "expectSearch"],
      `tab case ${c.name}`,
    );
    if (!c.why?.trim()) fail(`tab case "${c.name}"`, "states no reason it exists");
    if (!ROLES.has(c.role)) fail(`tab case "${c.name}"`, `names an unknown role "${c.role}"`);
    if (!TAB_IDS.has(c.expectSelected)) fail(`tab case "${c.name}"`, `expects an unknown tab "${c.expectSelected}"`);
    for (const key of c.keys) if (!TAB_KEYS.has(key)) fail(`tab case "${c.name}"`, `presses ${key}, which the tablist does not answer`);
    if (c.expectSelected === "waiting" && !(c.role === "owner" && c.acceptance_mode === "curated")) {
      fail(`tab case "${c.name}"`, "selects the waiting tab for somebody it is not offered to");
    }
    if (c.withPending && c.role !== "owner") fail(`tab case "${c.name}"`, "serves the queue to somebody who cannot read it");
  }
  const pending = (root.pending as { transcript_id: string; title: string; owner: string; provider: string; project: string; branch: string }[]).map(
    (row): PendingShare => {
      assertExactKeys(row, ["transcript_id", "title", "owner", "provider", "project", "branch"], `pending ${row.title}`);
      const owner = members.find((m) => m.username === row.owner);
      if (!owner) fail(`pending "${row.title}"`, `names ${row.owner}, who is not a member`);
      return {
        transcript_id: row.transcript_id,
        title: row.title,
        model_provider: row.provider,
        owner: profileOwner({ id: owner.id, github_username: owner.username }),
        local_id: `local-${row.transcript_id}`,
        parent_session_id: null,
        project_hash: "a".repeat(64),
        project_name: row.project,
        branch: row.branch,
        shared_at: "2026-01-04T00:00:00Z",
      } as PendingShare;
    },
  );

  const visible = (root.visible as { id: string; name: string; role: string | null; org: string | null; mode: string }[]).map(
    (row): VisibleGroup =>
      ({
        ...makeGroup({ id: row.id, name: row.name, linked_github_org: row.org, acceptance_mode: row.mode as VisibleGroup["acceptance_mode"] }),
        role: row.role as VisibleGroup["role"],
        member_since: null,
        member_count: 4,
        transcript_count: 7,
      }) as VisibleGroup,
  );
  const searchable = (root.searchable as { id: string; name: string; org: string | null }[]).map(
    (row): VillageCollectiveSearchResult => ({
      id: row.id,
      name: row.name,
      description: null,
      linked_github_org: row.org,
      member_count: 4,
      transcript_count: 7,
    }),
  );

  const pullRequestCases = root.pullRequestCases as PullRequestCase[];
  assertNamesMatch(pullRequestCases.map((c) => c.name), REQUIRED_PULL_REQUEST_CASES, "pull request cells");
  for (const c of pullRequestCases) {
    assertExactKeys(c, ["name", "why", "rows", "expect", "hrefs"], `pull request case ${c.name}`);
    if (!c.why?.trim()) fail(`pull request case "${c.name}"`, "states no reason it exists");
  }

  const baseWorld = (): CollectiveWorld => ({
    viewer,
    group: makeGroup({ id: collective.id, name: collective.name, description: collective.description }),
    role: "",
    canRead: false,
    members: members.map((m) => ({ ...m })),
    transcripts,
    linked: [],
    available,
  });

  return {
    cases,
    tabCases,
    pending,
    pullRequestCases,
    worldFor: (c) => ({
      ...baseWorld(),
      viewer: c.signedIn ? viewer : null,
      group: makeGroup({ id: collective.id, name: collective.name, description: collective.description, ...c.group } as never),
      role: c.role,
      canRead: c.canRead,
      linked: [...c.linked],
    }),
    tabWorld: (c) => ({
      ...baseWorld(),
      group: makeGroup({ id: collective.id, name: collective.name, description: collective.description, acceptance_mode: c.acceptance_mode } as never),
      role: c.role,
      canRead: true,
      pending: c.withPending ? pending.map((row) => ({ ...row })) : [],
    }),
    listWorld: () => ({ ...baseWorld(), visible, searchable }),
  };
}

// ── the repo picker ──────────────────────────────────────────────────────────

export type PickerStep =
  | { tick: string }
  | { untick: string }
  | { selectAll: true }
  | { clear: true }
  | { search: string };

export interface PickerCase {
  name: string;
  why: string;
  steps: PickerStep[];
  failOn: { call: number; status: number; error: string } | null;
  expect: {
    calls: { method: "POST" | "DELETE"; repo: string }[];
    title: string;
    message: string;
    orgLine: string;
  };
}

export interface RepoPickerFixtures {
  linked: string[];
  available: string[];
  cases: PickerCase[];
  /** A fresh world: the viewer owns a collective linked to acme, with the fixture's repositories. */
  worldFor(pickerCase: PickerCase): CollectiveWorld;
}

const REQUIRED_PICKER_CASES = [
  "add-two",
  "remove-one",
  "select-all",
  "clear",
  "search",
  "an-error-on-the-second-call",
];

const STEP_KINDS = new Set(["tick", "untick", "selectAll", "clear", "search"]);

export function loadRepoPickerFixtures(): RepoPickerFixtures {
  const root = readYAML("repo-picker.yaml") as Record<string, never>;
  assertExactKeys(root, ["linked", "available", "cases"], "repo-picker root");
  const cases = root.cases as PickerCase[];
  assertNamesMatch(cases.map((c) => c.name), REQUIRED_PICKER_CASES, "repo picker");
  const available = root.available as string[];
  for (const c of cases) {
    assertExactKeys(c, ["name", "why", "steps", "failOn", "expect"], `picker case ${c.name}`);
    assertExactKeys(c.expect, ["calls", "title", "message", "orgLine"], `picker case ${c.name} expect`);
    if (!c.why?.trim()) fail(`picker case "${c.name}"`, "states no reason it exists");
    for (const step of c.steps) {
      const keys = Object.keys(step);
      if (keys.length !== 1 || !STEP_KINDS.has(keys[0])) fail(`picker case "${c.name}"`, `has an unknown step ${JSON.stringify(step)}`);
      const repo = (step as { tick?: string; untick?: string }).tick ?? (step as { untick?: string }).untick;
      if (repo && !available.includes(repo)) fail(`picker case "${c.name}"`, `names ${repo}, which the App does not offer`);
    }
    if (c.failOn) {
      assertExactKeys(c.failOn, ["call", "status", "error"], `picker case ${c.name} failOn`);
      if (c.expect.calls.length !== c.failOn.call) {
        fail(`picker case "${c.name}"`, "fails a call but expects calls after it; the save stops at the first failure");
      }
    }
  }
  const linked = root.linked as string[];
  const page = readYAML("collective-page.yaml") as Record<string, never>;
  const collective = page.collective as { id: string; name: string };
  const viewer = page.viewer as { id: string; username: string };
  return {
    linked,
    available,
    cases,
    worldFor: (pickerCase) => ({
      viewer,
      group: makeGroup({ id: collective.id, name: collective.name, linked_github_org: "acme" }),
      role: "owner",
      canRead: true,
      members: [{ id: viewer.id, username: viewer.username, name: null, role: "owner" }],
      transcripts: [],
      linked: [...linked],
      available: available.map((repo) => ({ repo, private: false, publishers: 0 })),
      failures: pickerCase.failOn
        ? [{ write: pickerCase.failOn.call, status: pickerCase.failOn.status, error: pickerCase.failOn.error }]
        : [],
    }),
  };
}

export interface CollectiveRecoveryCase {
  name: string;
  why: string;
  surface: "list" | "detail" | "settings";
  operation: "read" | "create" | "create-retry";
  error: string | null;
}

export function loadCollectiveRecoveryFixtures(): CollectiveRecoveryCase[] {
  const root = readYAML("collective-recovery.yaml");
  assertExactKeys(root, ["cases"], "collective recovery root");
  const cases = root.cases as CollectiveRecoveryCase[];
  assertNamesMatch(cases.map((c) => c.name), [
    "a-list-read-can-be-retried",
    "a-detail-read-can-be-retried",
    "a-settings-read-can-be-retried",
    "a-failed-create-preserves-the-form-for-retry",
    "a-successful-create-opens-the-returned-collective",
  ], "collective recovery");
  for (const c of cases) {
    assertExactKeys(c, ["name", "why", "surface", "operation", "error"], `recovery ${c.name}`);
    if (!c.why?.trim() || !["list", "detail", "settings"].includes(c.surface) ||
        !["read", "create", "create-retry"].includes(c.operation)) {
      fail(c.name, "has an unknown recovery operation or surface");
    }
    if (c.operation !== "read" && c.surface !== "list") fail(c.name, "creates outside the list");
    if ((c.operation === "create") !== (c.error === null) || (c.error !== null && !c.error.trim())) {
      fail(c.name, "has no diagnostic for a refusal or an error on success");
    }
  }
  return cases;
}

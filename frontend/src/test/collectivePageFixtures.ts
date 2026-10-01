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

/**
 * Typed loaders for `src/testdata/collective-page.yaml` (the collective page,
 * the collectives list and the pull request cells) and
 * `src/testdata/repo-picker.yaml` (the repo picker's saves).
 */

// ── the collective page ──────────────────────────────────────────────────────

export interface PageCaseExpect {
  whoCanRead: string;
  whoCanPublish: string;
  yourRole: string;
  orgLine: string;
  manage: boolean;
  settings: boolean;
  overflow: { label: string; route: string }[];
  headerActions: string[];
  transcriptRows: number;
  memberBreakdown: string;
  /** The members the rail and the stats line count: a pending request is not one. */
  memberCount: number;
}

export interface PageCase {
  name: string;
  why: string;
  role: string;
  canRead: boolean;
  group: { acceptance_mode: string; data_access: string; linked_github_org: string | null };
  linked: string[];
  expect: PageCaseExpect;
}

export interface ListRowExpect {
  name: string;
  role: string;
  org: string;
  joining: string;
}

export interface ListCase {
  name: string;
  why: string;
  query: string;
  expectCaption: string;
  expectRows: ListRowExpect[];
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
  listCases: ListCase[];
  pullRequestCases: PullRequestCase[];
  /** A fresh world for one page case: every case gets its own copy to change. */
  worldFor(pageCase: PageCase): CollectiveWorld;
  /** A fresh world for the collectives list. */
  listWorld(): CollectiveWorld;
}

const REQUIRED_PAGE_CASES = [
  "the-owner-sees-the-boxes-the-org-total-and-both-overflow-items",
  "an-owner-without-an-org-is-offered-connect-github",
  "a-member-sees-the-org-without-the-total-and-can-leave",
  "a-contributor-who-cannot-read-is-told-so",
  "a-non-member-of-an-open-collective-can-join",
  "a-non-member-of-a-curated-collective-is-offered-no-join",
];

const REQUIRED_LIST_CASES = [
  "the-table-lists-every-visible-collective-with-its-org-and-joining",
  "a-search-shows-only-the-server-matches",
  "a-match-the-list-does-not-hold-claims-no-role",
];

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
    ["collective", "viewer", "members", "transcripts", "available", "cases", "listCases", "visible", "searchable", "pullRequestCases"],
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
    assertExactKeys(c, ["name", "why", "role", "canRead", "group", "linked", "expect"], `case ${c.name}`);
    assertExactKeys(c.group, ["acceptance_mode", "data_access", "linked_github_org"], `case ${c.name} group`);
    assertExactKeys(
      c.expect,
      ["whoCanRead", "whoCanPublish", "yourRole", "orgLine", "manage", "settings", "overflow", "headerActions", "transcriptRows", "memberBreakdown", "memberCount"],
      `case ${c.name} expect`,
    );
    if (!c.why?.trim()) fail(`case "${c.name}"`, "states no reason it exists");
    if (!ROLES.has(c.role)) fail(`case "${c.name}"`, `names an unknown role "${c.role}"`);
    // An expectation must follow from the case's own facts, so a case cannot
    // declare an outcome its inputs could never produce and then pass.
    if (c.expect.settings !== (c.role === "owner")) fail(`case "${c.name}"`, "expects settings for a viewer who is not the owner, or none for the owner");
    if (c.expect.manage && !(c.role === "owner" && c.group.linked_github_org)) {
      fail(`case "${c.name}"`, "expects manage without an owner and a linked org");
    }
    if (!c.canRead && c.expect.transcriptRows !== 0) fail(`case "${c.name}"`, "expects rows the server does not let the viewer read");
  }

  const listCases = root.listCases as ListCase[];
  assertNamesMatch(listCases.map((c) => c.name), REQUIRED_LIST_CASES, "collectives list");
  for (const c of listCases) {
    assertExactKeys(c, ["name", "why", "query", "expectCaption", "expectRows"], `list case ${c.name}`);
    if (!c.why?.trim()) fail(`list case "${c.name}"`, "states no reason it exists");
    for (const row of c.expectRows) assertExactKeys(row, ["name", "role", "org", "joining"], `list case ${c.name} row`);
  }
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
    listCases,
    pullRequestCases,
    worldFor: (c) => ({
      ...baseWorld(),
      group: makeGroup({ id: collective.id, name: collective.name, description: collective.description, ...c.group } as never),
      role: c.role,
      canRead: c.canRead,
      linked: [...c.linked],
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

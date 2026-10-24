import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { buildProjectHref } from "@/components/session-detail/v2/transcriptChrome";
import TranscriptList from "@/components/transcript/TranscriptList";
import { groupChildSessions } from "@/lib/childSessions";
import { ownerHandle, resolveAttribution } from "@/lib/format";
import { isOwnedBy, ownerKey, type PublicOwner } from "@/lib/publicOwner";
import { toReviewRows } from "@/lib/review/tree";
import type { PendingShare } from "@/lib/review/types";
import type { TranscriptRow } from "@/lib/types";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";

vi.mock("@/providers/AuthProvider", () => ({ useAuth: () => ({ user: null }) }));

type OwnerCase = {
  name: string;
  owner: PublicOwner | null;
  viewerId: string | null;
  expect: {
    label: string;
    anonymous: boolean;
    handle: string;
    ownedBy: boolean;
    key: string;
    projectHref: string | null;
  };
};

type FoldRow = {
  name: string;
  owner: string;
  sessionId: string;
  parentSessionId: string | null;
  parentTranscriptId: string | null;
};
type FoldCase = {
  name: string;
  rows: FoldRow[];
  expect: { rootRows: string[]; groups: Record<string, string[]> };
};

type Fixture = {
  projectHash: string;
  cases: OwnerCase[];
  owners: Record<string, PublicOwner>;
  foldCases: FoldCase[];
};

const requiredCaseNames = [
  "profile-seen-by-someone-else",
  "profile-seen-by-its-owner",
  "profile-seen-signed-out",
  "anonymous-seen-signed-in",
  "anonymous-seen-signed-out",
  "unknown-kind-reads-as-anonymous-and-keeps-nothing",
  "profile-without-an-id-names-nobody",
  "absent-owner-names-nobody",
  "viewer-with-the-same-username-is-not-the-owner",
] as const;

const requiredFoldCaseNames = [
  "a-profile-child-folds-under-its-resolved-parent",
  "an-anonymous-child-folds-under-its-resolved-parent",
  "a-shared-session-id-without-a-resolved-parent-never-folds",
  "a-resolved-parent-outside-the-response-keeps-the-row",
] as const;

function loadFixture(): Fixture {
  const parsed = parse(readFileSync(resolve(process.cwd(), "src/testdata/public-owner.yaml"), "utf8"), {
    strict: true,
  }) as Fixture;
  assertExactKeys(parsed, ["projectHash", "cases", "owners", "foldCases"], "public-owner fixture root");
  assertNamesMatch(parsed.cases.map((c) => c.name), requiredCaseNames, "public-owner cases");
  assertNamesMatch(parsed.foldCases.map((c) => c.name), requiredFoldCaseNames, "public-owner fold cases");
  for (const c of parsed.cases) {
    assertExactKeys(c, ["name", "owner", "viewerId", "expect"], `case ${c.name}`);
    assertExactKeys(c.expect, ["label", "anonymous", "handle", "ownedBy", "key", "projectHref"], `case ${c.name} expect`);
  }
  for (const c of parsed.foldCases) {
    assertExactKeys(c, ["name", "rows", "expect"], `fold case ${c.name}`);
    for (const row of c.rows) {
      assertExactKeys(row, ["name", "owner", "sessionId", "parentSessionId", "parentTranscriptId"], `fold case ${c.name} row`);
      if (!(row.owner in parsed.owners)) throw new Error(`fold case ${c.name}: unknown owner ${row.owner}`);
    }
    const expected = [...c.expect.rootRows, ...Object.values(c.expect.groups).flat()].sort();
    if (JSON.stringify(expected) !== JSON.stringify(c.rows.map((row) => row.name).sort())) {
      throw new Error(`fold case ${c.name}: the expectations must place every row exactly once`);
    }
  }
  return parsed;
}

const fixture = loadFixture();

afterEach(() => cleanup());

describe("a person as the server resolved them", () => {
  for (const c of fixture.cases) {
    it(c.name, () => {
      const attribution = resolveAttribution(c.owner);
      expect(attribution.label).toBe(c.expect.label);
      expect(attribution.anonymous).toBe(c.expect.anonymous);
      // Nothing is kept from an owner that names nobody.
      expect(attribution.profile === null).toBe(c.expect.anonymous);
      expect(ownerHandle(c.owner)).toBe(c.expect.handle);
      expect(isOwnedBy(c.owner, c.viewerId)).toBe(c.expect.ownedBy);
      expect(ownerKey(c.owner)).toBe(c.expect.key);
      expect(buildProjectHref(c.owner, fixture.projectHash)).toBe(c.expect.projectHref);
    });
  }

  it("names a review queue's publisher the same way", () => {
    const shares = fixture.cases.map(
      (c, index): PendingShare => ({
        transcript_id: `t-${index}`,
        title: c.name,
        model_provider: "claude-code",
        owner: c.owner as PublicOwner,
        local_id: `s-${index}`,
        parent_session_id: null,
        parent_transcript_id: null,
        project_hash: fixture.projectHash,
        project_name: "village",
        branch: "main",
        shared_at: "2026-08-20T10:00:00Z",
      }),
    );
    expect(toReviewRows(shares, new Set()).map((row) => row.owner_label)).toEqual(
      fixture.cases.map((c) => c.expect.label),
    );
  });
});

describe("folding started sessions reads only the parent the server resolved", () => {
  for (const c of fixture.foldCases) {
    it(c.name, () => {
      // The rows as a list response carries them; the fold reads its facts
      // out of the transcript alone, never the owner.
      const rows = c.rows.map((row) => ({
        name: row.name,
        owner: fixture.owners[row.owner],
        transcript: makeTranscriptFixture({
          id: row.name,
          local_id: row.sessionId,
          parent_session_id: row.parentSessionId,
          parent_transcript_id: row.parentTranscriptId,
        }),
      }));
      const grouping = groupChildSessions(rows);
      expect(grouping.rootItems.map((row) => row.name)).toEqual(c.expect.rootRows);
      expect(
        Object.fromEntries(grouping.groups.map((group) => [group.parent.name, group.children.map((row) => row.name)])),
      ).toEqual(c.expect.groups);
    });
  }
});

describe("a transcript list row draws only what the owner carries", () => {
  it("links a profile and never an anonymous owner, even where the list links owners", () => {
    const rows: TranscriptRow[] = fixture.cases.map((c, index) => ({
      transcript: makeTranscriptFixture({ id: `row-${index}`, local_id: `local-${index}`, title: c.name }),
      owner: c.owner as PublicOwner,
    }));
    const client = new QueryClient();
    const view = render(
      <QueryClientProvider client={client}>
        <TranscriptList items={rows} linkOwner bare />
      </QueryClientProvider>,
    );
    for (const [index, c] of fixture.cases.entries()) {
      const link = view.container.querySelector(`a[href="/transcripts/row-${index}"]`);
      if (link === null) throw new Error(`${c.name}: the row is not on screen`);
      // The row-wide link sits directly inside the row it opens.
      const rowRoot = link.parentElement as HTMLElement;
      expect(within(rowRoot).getByTitle(c.expect.label).textContent, c.name).toBe(c.expect.label);
      const profileLinks = [...rowRoot.querySelectorAll("a[href^='/users/']")].map((a) => a.getAttribute("href"));
      expect(profileLinks, c.name).toEqual(
        c.expect.anonymous ? [] : [`/users/${encodeURIComponent(c.expect.label)}`],
      );
    }
  });
});

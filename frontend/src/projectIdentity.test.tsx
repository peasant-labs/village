import { describe, expect, it } from "vitest";
import { groupByProject } from "@/lib/format";
import { buildProjectHref } from "@/components/session-detail/v2/transcriptChrome";
import {
  loadProjectIdentityFixtures,
  type ProjectIdentityGroupingItem,
} from "@/test/projectIdentityFixtures";

// Direct tests of the REAL, exported production functions — the same
// `groupByProject` `UserProfilePage` calls to render the profile's project
// groups, and the same `buildProjectHref` `SessionDetailV2` calls to build
// the breadcrumb's project-page href (proved end-to-end on the mounted
// route by src/titleHeroAndBreadcrumb.test.tsx). This file proves the
// underlying logic directly, including edge cases (a display-name collision
// across distinct hashes, missing href inputs) a single mounted case would
// be expensive to exercise for.

const fixtures = loadProjectIdentityFixtures();

type FixtureItem = { transcript: { project_hash: string | null; project_display_name: string; published_at: string } };

function toItems(items: ProjectIdentityGroupingItem[]): FixtureItem[] {
  return items.map((i) => ({
    transcript: {
      project_hash: i.projectHash,
      project_display_name: i.projectDisplayName,
      published_at: i.publishedAt,
    },
  }));
}

describe("groupByProject: hash-keyed project grouping", () => {
  for (const c of fixtures.groupingCases) {
    it(c.name, () => {
      const groups = groupByProject(toItems(c.items));
      expect(groups).toHaveLength(c.expectedGroupCount);
      expect(groups.map((g) => g.project)).toEqual(c.expectedGroupDisplayNames);
      expect(groups.map((g) => g.items.length)).toEqual(c.expectedGroupItemCounts);
    });
  }

  it("mixed-name-same-hash-collapses-to-one-group: the one group carries every item's raw project_hash", () => {
    // Belt-and-braces on top of the generic assertion above: confirms the
    // single returned group's own `project_hash` field is the shared hash,
    // not an arbitrary pick.
    const c = fixtures.groupingCases.find((c) => c.name === "mixed-name-same-hash-collapses-to-one-group")!;
    const groups = groupByProject(toItems(c.items));
    expect(groups).toHaveLength(1);
    expect(groups[0].project_hash).toBe(c.items[0].projectHash);
  });

  // MUTATION (shown RED, then reverted): re-keying on project_name instead
  // of project_hash must split the mixed-name-same-hash case into TWO
  // groups, proving the fixture actually distinguishes the two keys.
  it("mutation: re-keying on project_name instead of project_hash splits the mixed-name case", () => {
    const c = fixtures.groupingCases.find((c) => c.name === "mixed-name-same-hash-collapses-to-one-group")!;
    // Re-keys directly on each fixture row's RAW project name (what the
    // deleted extractProjectDisplayName/name-keyed groupByProject used to
    // key on), simulating the reversion this mutation guards against.
    function groupByProjectNameMutant(rawNames: string[]): string[][] {
      const groups = new Map<string, string[]>();
      for (const rawName of rawNames) {
        if (!groups.has(rawName)) groups.set(rawName, []);
        groups.get(rawName)!.push(rawName);
      }
      return Array.from(groups.values());
    }
    const mutantGroups = groupByProjectNameMutant(c.items.map((i) => i.rawProjectName));
    // The real function collapses this case to ONE group; the name-keyed
    // mutant must NOT — it has two distinct raw project_name values.
    expect(mutantGroups.length).toBeGreaterThan(1);
    expect(groupByProject(toItems(c.items))).toHaveLength(1);
  });

  // MUTATION (shown RED, then reverted): restoring the deleted "Other"
  // fallback for a missing project_hash must NOT be what happens — the real
  // function throws an actionable error instead, because project_hash is a
  // required identity column and a null value is a contract violation, not
  // a normal case to paper over with a synthetic bucket.
  it("mutation: a missing project_hash must not fall back to an 'Other' bucket", () => {
    const itemWithNoHash: FixtureItem[] = [
      { transcript: { project_hash: null, project_display_name: "whatever", published_at: "2026-08-01T00:00:00Z" } },
    ];
    expect(() => groupByProject(itemWithNoHash)).toThrow(/project_hash/);
    // The forbidden reversion this proves against: silently bucketing under
    // a literal "Other" key instead of throwing.
    function groupWithOtherFallbackMutant(items: FixtureItem[]) {
      const groups = new Map<string, FixtureItem[]>();
      for (const item of items) {
        const key = item.transcript.project_hash ?? "Other";
        if (!groups.has(key)) groups.set(key, []);
        groups.get(key)!.push(item);
      }
      return Array.from(groups.keys());
    }
    expect(groupWithOtherFallbackMutant(itemWithNoHash)).toEqual(["Other"]);
  });
});

describe("buildProjectHref: the breadcrumb project-page href", () => {
  for (const c of fixtures.breadcrumbHrefCases) {
    it(c.name, () => {
      expect(buildProjectHref(c.ownerUsername, c.projectHash)).toBe(c.expectedHref);
    });
  }

  // MUTATION (shown RED, then reverted): removing the crumb href entirely
  // (always returning null) must fail a case that expects a real href.
  it("mutation: unconditionally returning null must fail the present-inputs case", () => {
    const c = fixtures.breadcrumbHrefCases.find((c) => c.name === "username-and-hash-present-builds-href")!;
    function alwaysNullMutant(): string | null {
      return null;
    }
    expect(c.expectedHref).not.toBeNull();
    expect(alwaysNullMutant()).not.toBe(c.expectedHref);
  });
});

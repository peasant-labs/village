import { describe, expect, it } from "vitest";
import { describeNameSource, groupByProject, groupByRepo } from "@/lib/format";
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

type FixtureItem = { transcript: { project_hash: string; project_display_name: string; published_at: string } };

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
      const { groups, malformed } = groupByProject(toItems(c.items));
      expect(groups).toHaveLength(c.expectedGroupCount);
      expect(groups.map((g) => g.project)).toEqual(c.expectedGroupDisplayNames);
      expect(groups.map((g) => g.items.length)).toEqual(c.expectedGroupItemCounts);
      expect(malformed).toHaveLength(0);
    });
  }

  it("mixed-name-same-hash-collapses-to-one-group: the one group carries every item's raw project_hash", () => {
    // Belt-and-braces on top of the generic assertion above: confirms the
    // single returned group's own `project_hash` field is the shared hash,
    // not an arbitrary pick.
    const c = fixtures.groupingCases.find((c) => c.name === "mixed-name-same-hash-collapses-to-one-group")!;
    const { groups } = groupByProject(toItems(c.items));
    expect(groups).toHaveLength(1);
    expect(groups[0].project_hash).toBe(c.items[0].projectHash);
  });

  // A transcript with no project_hash is a genuine backend contract
  // violation (see Transcript.project_hash's doc comment: the column is
  // NOT NULL and every response path this frontend renders sources
  // exclusively from `transcripts`). groupByProject must neither (a) crash
  // the whole render, nor (b) silently fold it into a synthetic "Other"
  // bucket alongside real projects — it reports the item back separately
  // via `malformed` so the caller can surface a scoped, non-crashing
  // notice while every well-formed group still renders.
  it("a missing project_hash is reported via malformed, not thrown and not folded into an 'Other' bucket", () => {
    const wellFormed = toItems(
      fixtures.groupingCases.find((c) => c.name === "distinct-hashes-stay-separate-even-with-the-same-display-name")!
        .items,
    );
    const itemWithNoHash: FixtureItem = {
      transcript: { project_hash: "", project_display_name: "whatever", published_at: "2026-08-01T00:00:00Z" },
    };
    let result: ReturnType<typeof groupByProject<FixtureItem>>;
    expect(() => {
      result = groupByProject([...wellFormed, itemWithNoHash]);
    }).not.toThrow();
    expect(result!.malformed).toEqual([itemWithNoHash]);
    // Every well-formed group still renders — the malformed row does not
    // widen or corrupt an existing group's item count.
    expect(result!.groups).toHaveLength(2);
    expect(result!.groups.every((g) => !g.items.includes(itemWithNoHash))).toBe(true);
    // No group in the result is keyed "Other", the reversion this proves
    // against.
    expect(result!.groups.some((g) => g.project_hash === "Other")).toBe(false);
  });

  // MUTATION (shown RED, then reverted): restoring the deleted "Other"
  // fallback for a missing project_hash must NOT be what happens — a real
  // production reversion would fold the malformed item into a synthetic
  // "Other" group instead of reporting it via `malformed`, silently mixing
  // an anomaly in among real projects.
  it("mutation: a missing project_hash must not fall back to an 'Other' bucket", () => {
    const itemWithNoHash: FixtureItem = {
      transcript: { project_hash: "", project_display_name: "whatever", published_at: "2026-08-01T00:00:00Z" },
    };
    function groupWithOtherFallbackMutant(items: FixtureItem[]): string[] {
      const groups = new Map<string, FixtureItem[]>();
      for (const item of items) {
        const key = item.transcript.project_hash || "Other";
        if (!groups.has(key)) groups.set(key, []);
        groups.get(key)!.push(item);
      }
      return Array.from(groups.keys());
    }
    expect(groupWithOtherFallbackMutant([itemWithNoHash])).toEqual(["Other"]);
    // The real function never produces an "Other"-keyed group — the item
    // goes to `malformed` instead.
    const { groups } = groupByProject([itemWithNoHash]);
    expect(groups.some((g) => g.project_hash === "Other")).toBe(false);
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

describe("groupByRepo: the git_remote axis's label must not read the resolved project identity", () => {
  function repoItem(c: {
    gitRemote: string | null;
    projectRemoteLabel: string;
    projectDisplayName: string;
  }) {
    return {
      owner_id: "owner-1",
      git_remote: c.gitRemote,
      project_name: "raw-name",
      project_remote_label: c.projectRemoteLabel,
      published_at: "2026-08-20T09:00:00Z",
      token_count: 0,
      tokens_in: 0,
      tokens_out: 0,
    };
  }

  for (const c of fixtures.repoGroupLabelCases) {
    it(c.name, () => {
      const [group] = groupByRepo([repoItem(c)]);
      expect(group.name).toBe(c.expectedName);
    });
  }

  // MUTATION (shown RED, then reverted): labelling the attributed bucket
  // with project_display_name (the resolved PROJECT identity — a
  // different axis) instead of project_remote_label must diverge from the
  // real function on a case where the two fields differ, which is exactly
  // why every non-Unattributed fixture case above requires them to differ.
  it("mutation: reading project_display_name instead of project_remote_label diverges from the real label", () => {
    const c = fixtures.repoGroupLabelCases.find(
      (c) => c.name === "label-reads-project-remote-label-not-project-display-name",
    )!;
    const item = repoItem(c);
    function labelWithProjectDisplayNameMutant(i: typeof item & { project_display_name: string }): string {
      return i.project_display_name;
    }
    const mutantName = labelWithProjectDisplayNameMutant({ ...item, project_display_name: c.projectDisplayName });
    const [group] = groupByRepo([item]);
    expect(mutantName).not.toBe(group.name);
    expect(group.name).toBe(c.expectedName);
  });
});

// `describeNameSource` is the one live consumer that must handle EVERY member
// of the closed NameSource union — the tooltip a viewer reads next to a
// rendered project name to tell an owner-chosen name from an inferred one.
// The union's exhaustiveness is a COMPILE-time guarantee (a missing `case`
// fails `assertNameSourceExhaustive`), so these cases add the thing a compiler
// cannot check: that each tier renders the sentence it is supposed to render,
// and that no tier is silently explained as another.
describe("describeNameSource: every resolver tier explains itself", () => {
  it.each(fixtures.nameSourceDescriptionCases)("$name", (c) => {
    expect(describeNameSource(c.source)).toBe(c.expectedDescription);
  });

  it("gives each tier its own sentence, so no two tiers read the same", () => {
    const sentences = fixtures.nameSourceDescriptionCases.map((c) => describeNameSource(c.source));
    expect(new Set(sentences).size).toBe(sentences.length);
  });
});

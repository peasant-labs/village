import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { describe, expect, it, vi } from "vitest";
import {
  ALL_GROUP_BYS,
  ALL_VILLAGE_COLUMNS,
  DEFAULT_HOME_VIEW,
  HOME_VIEW_STORAGE_KEY,
  decodeLibraryView,
  loadHomeView,
  encodeLibraryView,
  groupKeysOf,
  groupLabelOf,
  migrateStoredView,
} from "@/lib/homeView";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import type { TranscriptListItem } from "@/lib/types";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { profileOwner } from "@/test/publicOwnerFixture";

/** The URL codec's cases. The codec owns the page, the size, and the grouping
 *  and nothing else, so the round-trip is the whole contract. */
type CodecCase = {
  name: string;
  query: string;
  expect: { page: number; perPage: number; groupBy: string };
};

const requiredCodecCases = [
  "omitted-values-take-the-library-defaults",
  "page-size-and-grouping-round-trip",
  "an-out-of-set-size-falls-back-to-twenty-five",
  "an-unknown-grouping-falls-back-to-project",
  "a-zero-page-falls-back-to-one",
  "a-non-number-page-falls-back-to-one",
  "the-nothing-grouping-round-trips",
] as const;

function codecCases(): CodecCase[] {
  const parsed = parse(readFileSync(resolve(process.cwd(), "src/testdata/library-view-codec.yaml"), "utf8"), {
    strict: true,
  }) as { cases: CodecCase[] };
  assertExactKeys(parsed, ["cases"], "library view codec root");
  assertNamesMatch(parsed.cases.map((c) => c.name), requiredCodecCases, "library view codec");
  for (const c of parsed.cases) {
    assertExactKeys(c, ["name", "query", "expect"], `library view codec ${c.name}`);
    assertExactKeys(c.expect, ["page", "perPage", "groupBy"], `library view codec ${c.name} expect`);
  }
  return parsed.cases;
}

describe("the Library URL codec", () => {
  for (const c of codecCases()) {
    it(c.name, () => {
      const decoded = decodeLibraryView(new URLSearchParams(c.query));
      expect(decoded.page).toBe(c.expect.page);
      expect(decoded.perPage).toBe(c.expect.perPage);
      expect(decoded.groupBy.wire).toBe(c.expect.groupBy);
      // The codec round-trips its own output, and the output carries exactly the
      // three values the URL owns.
      const encoded = encodeLibraryView(decoded);
      expect([...encoded.keys()].sort()).toEqual(["groupBy", "page", "perPage"]);
      expect([...encodeLibraryView(decodeLibraryView(encoded))]).toEqual([...encoded]);
    });
  }
});

/** A row with the facts the host's group helpers read. */
function row(overrides: {
  projectHash?: string;
  projectDisplayName?: string;
  modelProvider?: string;
  visibility?: "private" | "shared" | "public";
  sharedWith?: string[];
} = {}): TranscriptListItem {
  return {
    transcript: makeTranscriptFixture({
      id: `row-${Math.random().toString(16).slice(2)}`,
      title: "a row",
      project_hash: overrides.projectHash ?? "a".repeat(64),
      project_name: "ingest-api",
      project_display_name: overrides.projectDisplayName ?? "ingest-api",
      model_provider: overrides.modelProvider ?? "claude-code",
      visibility:
        overrides.visibility ?? ((overrides.sharedWith ?? []).length > 0 ? "shared" : "private"),
    }),
    tags: [],
    owner: profileOwner({ id: "owner-1", github_username: "alice-dev", display_name: "Alice" }),
    shares: (overrides.sharedWith ?? []).map((name, index) => ({
      transcript_id: "t",
      group_id: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
      group_name: name,
      acceptance_mode: "open",
      status: "approved",
      shared_at: "2026-01-01T00:00:00Z",
    })),
  } as unknown as TranscriptListItem;
}

describe("the host's group helpers", () => {
  it("return bare keys and qualify them only for the table", () => {
    const item = row({ projectHash: "b".repeat(64), projectDisplayName: "ingest-api" });
    const project = ALL_GROUP_BYS.find((member) => member.wire === "project")!;
    const keys = groupKeysOf(item, project);
    expect(keys).toEqual(["b".repeat(64)]);
    const labels = new Map([[`project:${"b".repeat(64)}`, "ingest-api"]]);
    expect(groupLabelOf(`project:${keys[0]}`, project, labels)).toBe("ingest-api");
    expect(groupLabelOf(`project:${keys[0]}`, project)).toBe("b".repeat(64));
  });

  it("files an ungrouped row in no group and a reader-less row under its own words", () => {
    const item = row();
    const none = ALL_GROUP_BYS.find((member) => member.wire === "none")!;
    const collective = ALL_GROUP_BYS.find((member) => member.wire === "collective")!;
    expect(groupKeysOf(item, none)).toEqual([]);
    expect(groupKeysOf(item, collective)).toEqual(["not shared with collective members"]);
    expect(groupKeysOf(row({ visibility: "public" }), collective)).toEqual(["anyone with the link"]);
    expect(groupKeysOf(row({ sharedWith: ["Acme Platform", "Acme Company"] }), collective)).toEqual([
      "Acme Platform",
      "Acme Company",
    ]);
  });
});

describe("the stored view migration", () => {
  it("keeps a readable columns record in the closed set's order and drops an unknown key", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const columns = migrateStoredView({
      columns: { when: true, project: true, readers: false, agent: true, pullRequests: false, turns: false, nope: true },
    });
    expect(columns).toEqual([
      ALL_VILLAGE_COLUMNS[0],
      ALL_VILLAGE_COLUMNS[1],
      ALL_VILLAGE_COLUMNS[3],
      ALL_VILLAGE_COLUMNS[6],
    ]);
    expect(warn).toHaveBeenCalledTimes(1);
    warn.mockRestore();
  });

  it("validates a visibleColumns array and falls back for an unreadable value", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const columns = migrateStoredView({ visibleColumns: ["when", "project", "nope"] });
    expect(columns).toEqual([ALL_VILLAGE_COLUMNS[0], ALL_VILLAGE_COLUMNS[1], ALL_VILLAGE_COLUMNS[6]]);
    expect(warn).toHaveBeenCalledTimes(1);
    warn.mockRestore();
    expect(migrateStoredView("not a view")).toEqual(DEFAULT_HOME_VIEW.columns);
    expect(migrateStoredView(null)).toEqual(DEFAULT_HOME_VIEW.columns);
  });

  it("gives both stored shapes the identity column even when it was not named", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const array = migrateStoredView({ visibleColumns: ["when"] });
    expect(array).toEqual([ALL_VILLAGE_COLUMNS[0], ALL_VILLAGE_COLUMNS[6]]);
    const record = migrateStoredView({ columns: { when: true } });
    expect(record).toEqual([ALL_VILLAGE_COLUMNS[0], ALL_VILLAGE_COLUMNS[6]]);
    warn.mockRestore();
  });

  it("keeps the shipped default when this browser has no stored view", () => {
    globalThis.localStorage?.clear();
    const loaded = loadHomeView();
    expect(loaded.columns).toEqual(DEFAULT_HOME_VIEW.columns);
    expect(loaded.collapsed).toEqual([]);
    // The default is the shipped one: the title plus the four columns the old
    // home drew, so a reader with no stored view is not reset to fewer.
    expect(DEFAULT_HOME_VIEW.columns.map((column) => column.wire)).toEqual([
      "transcript",
      "project",
      "readers",
      "agent",
      "when",
    ]);
    expect(globalThis.localStorage?.getItem(HOME_VIEW_STORAGE_KEY)).toBeNull();
  });
});

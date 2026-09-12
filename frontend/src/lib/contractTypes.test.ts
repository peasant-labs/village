import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import * as contract from "@peasant-labs/schema";

/**
 * The frontend's collectives, share, contribution, and repository wire types
 * are aliases of the published contract package's generated types, kept
 * under their historical names for one release. This guard fails when a
 * name on this list is re-declared by hand, or when an alias points at a
 * type the package does not export.
 *
 * `generated` is the package type the alias derives from. `declaration`, when
 * present, is the exact right-hand side the file must carry: a documented
 * composition or widening of the generated type rather than a bare alias.
 */
interface AliasExpectation {
  generated: string;
  declaration?: string;
}

const aliases: Record<string, Record<string, AliasExpectation>> = {
  "./types.ts": {
    Group: {
      generated: "VillageGroup",
      declaration:
        'Omit<VillageGroup, "post_prompts_check" | "prompts_check_mode"> & Partial<Pick<VillageGroup, "post_prompts_check" | "prompts_check_mode">> & Partial<Pick<VillageUserGroup, "member_count" | "transcript_count">> & { role: VillageGroupViewerRole; member_since: string | null; }',
    },
    VisibleGroup: { generated: "VillageVisibleGroup" },
    UserGroupShare: { generated: "VillageUserGroupShare" },
    GroupTranscriptStats: {
      generated: "VillageGroupTranscriptStats",
      declaration: 'AsJSONNumber<VillageGroupTranscriptStats, "total_duration_ms" | "total_tokens" | "total_turns">',
    },
    GroupModelBreakdown: { generated: "VillageGroupModelBreakdown" },
    GroupContributor: { generated: "VillageGroupContributor" },
    GroupMember: { generated: "VillageGroupMember" },
    GroupTranscript: {
      generated: "VillageGroupTranscript",
      declaration: 'AsPlainString<VillageGroupTranscript, "project_hash">',
    },
    CollectiveSearchResult: { generated: "VillageCollectiveSearchResult" },
    CollectiveSearchResponse: { generated: "VillageCollectiveSearchResponse" },
    LinkedRepository: { generated: "VillageLinkedRepository" },
    LinkedRepositoriesResponse: { generated: "VillageLinkedRepositoriesResponse" },
    RepositoryCommit: { generated: "VillageRepositoryCommit" },
    RepositoryCommitsResponse: { generated: "VillageRepositoryCommitsResponse" },
    ContributedCollective: { generated: "VillageContributedCollective" },
    TranscriptCollective: { generated: "VillageTranscriptCollective" },
    ShareEventStatus: { generated: "VillageShareStatus" },
    ShareEventActor: { generated: "VillageShareEventActor" },
    ShareEvent: { generated: "VillageShareEvent" },
    CollectiveSubmissionPair: { generated: "VillageCollectiveSubmission" },
  },
  "./review/types.ts": {
    PendingShare: {
      generated: "VillagePendingShare",
      declaration: 'AsPlainString<VillagePendingShare, "project_hash">',
    },
    ReviewDecision: { generated: "VillageReviewDecision" },
    BatchReviewRequest: { generated: "VillageBatchReviewRequest" },
    BatchReviewResponse: { generated: "VillageBatchReviewResponse" },
  },
  "./contribute/types.ts": {
    ContributableTranscript: {
      generated: "VillageContributableTranscript",
      declaration: 'AsPlainString<VillageContributableTranscript, "project_hash">',
    },
    ContributableResponse: {
      generated: "VillageContributableResponse",
      declaration: 'Omit<VillageContributableResponse, "transcripts"> & { transcripts: ContributableTranscript[] }',
    },
    BatchShareRequest: {
      generated: "VillageBatchShareRequest",
      declaration: 'AsPlainString<VillageBatchShareRequest, "project_hash">',
    },
    BatchShareStatus: { generated: "VillageContributionStatus" },
    BatchShareResponse: { generated: "VillageBatchShareResponse" },
  },
};

/**
 * Hand-written interfaces that share a name with a contract type but belong
 * to routes outside the collectives surface (transcript and profile reads).
 * Deriving them is separate work. Each entry must still be declared by hand
 * and still exported by the package, so this list can only shrink.
 */
const knownHandWrittenOutsideScope: Record<string, string[]> = {
  "./types.ts": ["User", "Transcript", "TranscriptListResponse"],
  "./review/types.ts": [],
  "./contribute/types.ts": [],
};

/** Reads a sibling source file with runs of whitespace folded to one space. */
function normalizedSource(relative: string): string {
  return readFileSync(fileURLToPath(new URL(relative, import.meta.url)), "utf8").replace(/\s+/g, " ");
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

describe("collectives wire types come from the contract package", () => {
  for (const [file, mapping] of Object.entries(aliases)) {
    describe(file, () => {
      const text = normalizedSource(file);
      for (const [local, expectation] of Object.entries(mapping)) {
        const declaration = expectation.declaration ?? expectation.generated;
        it(`${local} is declared as ${declaration}`, () => {
          const alias = `export type ${local} = ${declaration};`;
          expect(text, `${file} must declare "${alias}"`).toContain(alias);
          const redeclared = new RegExp(`export (interface|type) ${local} (?!= ${escapeRegExp(declaration)};)`);
          expect(text, `${file} re-declares ${local} by hand`).not.toMatch(redeclared);
        });
        it(`${expectation.generated} exists in the package`, () => {
          const runtimeName = `z${expectation.generated}`;
          expect(
            (contract as Record<string, unknown>)[runtimeName],
            `${runtimeName} is not exported by @peasant-labs/schema`,
          ).toBeDefined();
        });
      }
      it("declares no interface whose name the package already exports as a Village type", () => {
        const exported = new Set(
          Object.keys(contract)
            .filter((name) => /^zVillage[A-Z]/.test(name))
            .map((name) => name.slice("zVillage".length)),
        );
        // An interface, or an object type written out by hand, both count as a declaration.
        const declared = [...text.matchAll(/export (?:interface ([A-Za-z]+)\b|type ([A-Za-z]+) = \{)/g)].map(
          (m) => m[1] ?? m[2],
        );
        const allowed = new Set(knownHandWrittenOutsideScope[file] ?? []);
        const duplicates = declared.filter((name) => exported.has(name) && !allowed.has(name));
        expect(duplicates, `hand-written duplicates of contract types: ${duplicates.join(", ")}`).toEqual([]);
        for (const name of allowed) {
          expect(
            declared,
            `${name} is listed as a known hand-written duplicate but is no longer declared in ${file}; delete it from the list`,
          ).toContain(name);
          expect(
            exported.has(name),
            `${name} is listed as a known hand-written duplicate but the package no longer exports Village${name}; delete it from the list`,
          ).toBe(true);
        }
      });
    });
  }
});

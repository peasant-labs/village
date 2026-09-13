import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { describe, expect, it } from "vitest";
import * as contract from "@peasant-labs/schema";
import { assertExactKeys } from "@/test/fixtureAssertions";

/**
 * Guards the alias layer between the frontend's historical type names and the
 * contract package's generated types. The expectations live in
 * src/testdata/contract-type-aliases.yaml; the names the fixture must keep
 * live here, so deleting fixture rows cannot delete the manifest that
 * protects them.
 */
interface AliasRow {
  local: string;
  generated: string;
  declaration?: string;
}

interface AliasFile {
  path: string;
  known_hand_written_outside_scope: string[];
  aliases: AliasRow[];
}

const requiredAliases: Record<string, string[]> = {
  "./types.ts": [
    "Group",
    "VisibleGroup",
    "UserGroupShare",
    "GroupTranscriptStats",
    "GroupModelBreakdown",
    "GroupContributor",
    "GroupMember",
    "GroupTranscript",
    "CollectiveSearchResult",
    "CollectiveSearchResponse",
    "LinkedRepository",
    "LinkedRepositoriesResponse",
    "RepositoryCommit",
    "RepositoryCommitsResponse",
    "ContributedCollective",
    "TranscriptCollective",
    "ShareEventStatus",
    "ShareEventActor",
    "ShareEvent",
    "CollectiveSubmissionPair",
  ],
  "./review/types.ts": ["PendingShare", "ReviewDecision", "BatchReviewRequest", "BatchReviewResponse"],
  "./contribute/types.ts": [
    "ContributableTranscript",
    "ContributableResponse",
    "BatchShareRequest",
    "BatchShareStatus",
    "BatchShareResponse",
  ],
};

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function assertString(value: unknown, location: string): string {
  if (typeof value !== "string" || value.length === 0) throw new Error(`${location} must be a non-empty string`);
  return value;
}

function assertStringList(value: unknown, location: string): string[] {
  if (!Array.isArray(value) || value.some((item) => typeof item !== "string")) {
    throw new Error(`${location} must be a list of strings`);
  }
  return value as string[];
}

/** Loads the fixture strictly: unknown keys, missing keys, and repeated names all throw. */
function loadAliasFixture(): AliasFile[] {
  const fixturePath = resolve(process.cwd(), "src/testdata/contract-type-aliases.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (!isPlainObject(parsed)) throw new Error("contract-type-aliases fixture must be a mapping");
  assertExactKeys(parsed, ["files"], "contract-type-aliases fixture");
  if (!Array.isArray(parsed.files) || parsed.files.length === 0) throw new Error("files must be a non-empty list");
  const files = parsed.files.map((rawFile, fileIndex): AliasFile => {
    const at = `files[${fileIndex}]`;
    if (!isPlainObject(rawFile)) throw new Error(`${at} must be a mapping`);
    assertExactKeys(rawFile, ["path", "known_hand_written_outside_scope", "aliases"], at);
    const path = assertString(rawFile.path, `${at}.path`);
    const outsideScope = assertStringList(rawFile.known_hand_written_outside_scope, `${at}.known_hand_written_outside_scope`);
    if (!Array.isArray(rawFile.aliases) || rawFile.aliases.length === 0) throw new Error(`${at}.aliases must be a non-empty list`);
    const seen = new Set<string>();
    const aliases = rawFile.aliases.map((rawAlias, aliasIndex): AliasRow => {
      const where = `${at}.aliases[${aliasIndex}]`;
      if (!isPlainObject(rawAlias)) throw new Error(`${where} must be a mapping`);
      const keys = Object.keys(rawAlias).sort();
      const allowed = ["local", "generated", "declaration"];
      for (const key of keys) {
        if (!allowed.includes(key)) throw new Error(`${where} has unknown field ${key}`);
      }
      const local = assertString(rawAlias.local, `${where}.local`);
      const generated = assertString(rawAlias.generated, `${where}.generated`);
      if (seen.has(local)) throw new Error(`${path} lists ${local} twice`);
      seen.add(local);
      const row: AliasRow = { local, generated };
      if ("declaration" in rawAlias) row.declaration = assertString(rawAlias.declaration, `${where}.declaration`);
      return row;
    });
    return { path, known_hand_written_outside_scope: outsideScope, aliases };
  });
  for (const [path, required] of Object.entries(requiredAliases)) {
    const file = files.find((f) => f.path === path);
    if (!file) throw new Error(`the fixture omits required file ${path}`);
    const present = new Set(file.aliases.map((a) => a.local));
    const missing = required.filter((name) => !present.has(name));
    if (missing.length > 0) {
      throw new Error(`${path} omits required aliases ${missing.join(", ")}; restore them rather than removing them from the manifest`);
    }
  }
  return files;
}

/** Reads a source file under src/lib with runs of whitespace folded to one space. */
function normalizedSource(relative: string): string {
  return readFileSync(resolve(process.cwd(), "src/lib", relative), "utf8").replace(/\s+/g, " ");
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

describe("collectives wire types come from the contract package", () => {
  for (const file of loadAliasFixture()) {
    describe(file.path, () => {
      const text = normalizedSource(file.path);
      for (const row of file.aliases) {
        const declaration = row.declaration ?? row.generated;
        it(`${row.local} is declared as ${declaration}`, () => {
          const alias = `export type ${row.local} = ${declaration};`;
          expect(text, `${file.path} must declare "${alias}"`).toContain(alias);
          const redeclared = new RegExp(`export (interface|type) ${row.local} (?!= ${escapeRegExp(declaration)};)`);
          expect(text, `${file.path} re-declares ${row.local} by hand`).not.toMatch(redeclared);
        });
        it(`${row.generated} exists in the package`, () => {
          const runtimeName = `z${row.generated}`;
          expect(
            (contract as Record<string, unknown>)[runtimeName],
            `${runtimeName} is not exported by @peasant-labs/schema`,
          ).toBeDefined();
        });
      }
      it("declares no interface or object type whose name the package already exports as a Village type", () => {
        const exported = new Set(
          Object.keys(contract)
            .filter((name) => /^zVillage[A-Z]/.test(name))
            .map((name) => name.slice("zVillage".length)),
        );
        // An interface, or an object type written out by hand, both count as a declaration.
        const declared = [...text.matchAll(/export (?:interface ([A-Za-z]+)\b|type ([A-Za-z]+) = \{)/g)].map(
          (m) => m[1] ?? m[2],
        );
        const allowed = new Set(file.known_hand_written_outside_scope);
        const duplicates = declared.filter((name) => exported.has(name) && !allowed.has(name));
        expect(duplicates, `hand-written duplicates of contract types: ${duplicates.join(", ")}`).toEqual([]);
        for (const name of allowed) {
          expect(
            declared,
            `${name} is listed as a known hand-written duplicate but is no longer declared in ${file.path}; delete it from the fixture`,
          ).toContain(name);
          expect(
            exported.has(name),
            `${name} is listed as a known hand-written duplicate but the package no longer exports Village${name}; delete it from the fixture`,
          ).toBe(true);
        }
      });
    });
  }
});

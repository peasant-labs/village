import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { describe, expect, it } from "vitest";
import type {
  VillagePullRequestAttachmentResponse,
  VillagePromptRequestsResponse,
  VillageUserSettings,
  VillageUpdateUserSettingsRequest,
  VillageStatusResponse,
} from "@peasant-labs/schema";
import {
  zVillagePullRequestAttachmentResponse,
  zVillagePromptRequestsResponse,
  zVillageUserSettings,
  zVillageUpdateUserSettingsRequest,
  zVillageStatusResponse,
} from "@peasant-labs/schema";
import * as contract from "@peasant-labs/schema";
import { assertExactKeys } from "@/test/fixtureAssertions";

/**
 * Proves that the contract package's generated types for the pull request
 * prompt attachment surface parse and round-trip as shipped, before any page
 * renders them. Each fixture example is parsed with the package's own named
 * parser export; the required-name manifest below stops a fixture edit from
 * silently dropping coverage for one of the seven operations' wire types.
 */
const requiredExamples: string[] = [
  "pull request attachment response",
  "prompt requests response",
  "user settings",
  "update user settings request",
  "webhook acknowledgement",
];

/** A named package export that parses like a zod schema. Narrow and local: only
 *  the `parse` call this test needs, not the rest of the zod schema surface. */
interface ParserLike {
  parse: (value: unknown) => unknown;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isParserLike(value: unknown): value is ParserLike {
  return isPlainObject(value) && typeof value.parse === "function";
}

interface Example {
  name: string;
  parser: ParserLike;
  value: unknown;
}

/** Loads the fixture strictly: unknown keys, missing keys, repeated names, an
 *  unresolvable parser name, or a missing required example all throw. */
function loadExamples(): Example[] {
  const fixturePath = resolve(process.cwd(), "src/testdata/pull-request-contract.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (!isPlainObject(parsed)) throw new Error("pull-request-contract fixture must be a mapping");
  assertExactKeys(parsed, ["examples"], "pull-request-contract fixture");
  if (!Array.isArray(parsed.examples) || parsed.examples.length === 0) {
    throw new Error("pull-request-contract fixture examples must be a non-empty list");
  }

  const seen = new Set<string>();
  const examples = parsed.examples.map((raw, index): Example => {
    const at = `examples[${index}]`;
    if (!isPlainObject(raw)) throw new Error(`${at} must be a mapping`);
    assertExactKeys(raw, ["name", "parser", "value"], at);
    const { name, parser: parserName, value } = raw;
    if (typeof name !== "string" || name.length === 0) throw new Error(`${at}.name must be a non-empty string`);
    if (typeof parserName !== "string" || parserName.length === 0) {
      throw new Error(`${at}.parser must be a non-empty string`);
    }
    if (seen.has(name)) throw new Error(`the fixture lists example ${name} twice`);
    seen.add(name);

    const parserExport = (contract as Record<string, unknown>)[parserName];
    if (!isParserLike(parserExport)) {
      throw new Error(`${at}.parser names ${parserName}, which is not a package export with a parse function`);
    }

    return { name, parser: parserExport, value };
  });

  for (const name of requiredExamples) {
    if (!seen.has(name)) {
      throw new Error(`the fixture omits required example ${name}; restore it rather than removing it from the manifest`);
    }
  }

  return examples;
}

const examples = loadExamples();

function example(name: string): unknown {
  const found = examples.find((candidate) => candidate.name === name);
  if (!found) throw new Error(`no fixture example named ${name}`);
  return found.value;
}

describe("pull request attachment wire types parse as the package ships them", () => {
  for (const { name, parser, value } of examples) {
    it(`${name} parses and round-trips`, () => {
      const parsed = parser.parse(value);
      expect(parsed).toEqual(value);
    });
  }

  it("the parsed values are the exported types", () => {
    const typed: {
      attachment: VillagePullRequestAttachmentResponse;
      requests: VillagePromptRequestsResponse;
      settings: VillageUserSettings;
      update: VillageUpdateUserSettingsRequest;
      acknowledgement: VillageStatusResponse;
    } = {
      attachment: zVillagePullRequestAttachmentResponse.parse(example("pull request attachment response")),
      requests: zVillagePromptRequestsResponse.parse(example("prompt requests response")),
      settings: zVillageUserSettings.parse(example("user settings")),
      update: zVillageUpdateUserSettingsRequest.parse(example("update user settings request")),
      acknowledgement: zVillageStatusResponse.parse(example("webhook acknowledgement")),
    };

    expect(typed.attachment.attachment.state).toBe("waiting");
    expect(typed.requests.requests).toHaveLength(1);
  });
});

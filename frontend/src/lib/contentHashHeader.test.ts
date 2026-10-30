import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { describe, expect, it } from "vitest";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import { readContentHashHeader } from "./contentHashHeader";

interface Case {
  name: string;
  why: string;
  header: string | null;
  want: string | null;
}

const required = [
  "bare_hash",
  "surrounding_space_trimmed",
  "missing_header",
  "quoted_refused",
  "uppercase_refused",
  "short_refused",
];

const { cases } = parse(readFileSync(resolve(process.cwd(), "src/testdata/content-hash-header.yaml"), "utf8"), {
  strict: true,
}) as { cases: Case[] };
assertNamesMatch(cases.map((c) => c.name), required, "content-hash-header cases");
for (const c of cases) assertExactKeys(c, ["name", "why", "header", "want"], `content-hash-header case ${c.name}`);

describe("readContentHashHeader", () => {
  for (const c of cases) {
    it(c.name, () => {
      expect(readContentHashHeader(c.header), c.why).toBe(c.want);
    });
  }
});

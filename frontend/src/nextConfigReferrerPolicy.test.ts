// @vitest-environment node
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import YAML from "yaml";
import { expect, it } from "vitest";
import { getPathMatch } from "next/dist/shared/lib/router/utils/path-match";

import nextConfig from "../next.config";

// unstring-allow: data-path-literal — named once at module scope and read only through this constant
const FIXTURE = resolve(process.cwd(), "src/testdata/referrer-policy.yaml");

type Case = { name: string; path: string; noReferrer: boolean };
const { cases } = YAML.parse(readFileSync(FIXTURE, "utf8")) as { cases: Case[] };
const REQUIRED = ["transcript-page", "transcript-share-image", "home-page-unchanged", "review-page-unchanged"];

it("retains every referrer-policy case", () => {
  expect(cases.map((c) => c.name).sort()).toEqual([...REQUIRED].sort());
});

for (const c of cases) {
  it(c.name, async () => {
    const rules = (await nextConfig.headers?.()) ?? [];
    const policies = rules
      .filter((rule) => getPathMatch(rule.source)(c.path) !== false)
      .flatMap((rule) => rule.headers)
      .filter((header) => header.key.toLowerCase() === "referrer-policy")
      .map((header) => header.value);
    expect(policies).toEqual(c.noReferrer ? ["no-referrer"] : []);
  });
}

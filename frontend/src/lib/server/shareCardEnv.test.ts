import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import YAML from "yaml";
import { afterEach, describe, expect, it, vi } from "vitest";

import { publicOrigin, shareCardEnv } from "./shareCardEnv";

interface EnvCase {
  name: string;
  apiUrl?: string;
  origin?: string;
  expect: "null" | { apiBase: string; origin: string };
  publicOrigin: string;
}

const cases = (
  YAML.parse(readFileSync(resolve(process.cwd(), "testdata/share-card/env-cases.yaml"), "utf8")) as {
    cases: EnvCase[];
  }
).cases;

const REQUIRED = [
  "both-set",
  "trailing-slashes-on-api-trimmed",
  "origin-path-and-query-dropped",
  "api-unset",
  "origin-unset",
  "api-non-http-protocol",
  "origin-non-http-protocol",
  "origin-schemeless",
  "origin-only-set",
];

describe("shareCardEnv", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it("has every required case", () => {
    expect(cases.map((c) => c.name)).toEqual(expect.arrayContaining(REQUIRED));
  });

  it.each(cases.map((c) => [c.name, c] as const))("%s", (_name, c) => {
    vi.stubEnv("VILLAGE_INTERNAL_API_URL", c.apiUrl ?? "");
    vi.stubEnv("VILLAGE_PUBLIC_ORIGIN", c.origin ?? "");
    const env = shareCardEnv();
    if (c.expect === "null") {
      expect(env).toBeNull();
    } else {
      expect(env?.apiBase).toBe(c.expect.apiBase);
      expect(env?.origin.href).toBe(`${c.expect.origin}/`);
    }
    const origin = publicOrigin();
    if (c.publicOrigin === "null") expect(origin).toBeNull();
    else expect(origin?.href).toBe(`${c.publicOrigin}/`);
  });
});

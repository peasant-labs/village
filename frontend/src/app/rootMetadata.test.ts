import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import YAML from "yaml";
import { afterEach, describe, expect, it, vi } from "vitest";

import { rootMetadata } from "./rootMetadata";
import { SITE_METADATA } from "./siteMetadata";

interface EnvCase {
  name: string;
  apiUrl?: string;
  origin?: string;
  publicOrigin: string;
}

const cases = (
  YAML.parse(readFileSync(resolve(process.cwd(), "testdata/share-card/env-cases.yaml"), "utf8")) as {
    cases: EnvCase[];
  }
).cases;

// The public origin alone makes preview URLs absolute; the API URL is not needed for it.
const REQUIRED = ["origin-only-set", "api-unset", "both-set", "origin-unset", "origin-non-http-protocol"];

describe("rootMetadata", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it("has every required case", () => {
    expect(cases.map((c) => c.name)).toEqual(expect.arrayContaining(REQUIRED));
  });

  it.each(cases.map((c) => [c.name, c] as const))("%s", (_name, c) => {
    vi.stubEnv("VILLAGE_INTERNAL_API_URL", c.apiUrl ?? "");
    vi.stubEnv("VILLAGE_PUBLIC_ORIGIN", c.origin ?? "");
    const { metadataBase, ...rest } = rootMetadata();
    expect(rest).toEqual(SITE_METADATA);
    if (c.publicOrigin === "null") expect(metadataBase).toBeUndefined();
    else expect((metadataBase as URL).href).toBe(`${c.publicOrigin}/`);
  });
});

// @vitest-environment node
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import YAML from "yaml";
import { describe, expect, it } from "vitest";
import { HTML_LIMITED_BOT_UA_RE } from "next/dist/shared/lib/router/utils/is-bot";
import { shouldServeStreamingMetadata } from "next/dist/server/lib/streaming-metadata";

import nextConfig from "../next.config";

interface BotCase {
  name: string;
  userAgent: string;
  blocking: boolean;
}

const cases = (
  YAML.parse(readFileSync(resolve(process.cwd(), "testdata/share-card/bot-cases.yaml"), "utf8")) as {
    cases: BotCase[];
  }
).cases;

const REQUIRED = ["telegram", "mastodon", "bluesky-cardyb", "next-default-twitterbot", "browser"];

describe("htmlLimitedBots", () => {
  it("has every required case", () => {
    expect(cases.map((c) => c.name)).toEqual(expect.arrayContaining(REQUIRED));
  });

  it("keeps Next's own list whole", () => {
    expect(nextConfig.htmlLimitedBots?.source).toContain(HTML_LIMITED_BOT_UA_RE.source);
  });

  // Next itself turns the configured RegExp into its source string before this check.
  it.each(cases.map((c) => [c.name, c] as const))("%s", (_name, c) => {
    expect(shouldServeStreamingMetadata(c.userAgent, nextConfig.htmlLimitedBots?.source)).toBe(!c.blocking);
  });
});

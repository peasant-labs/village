import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import YAML from "yaml";
import { describe, expect, it } from "vitest";

import { adaptExplore, exploreProfilePath } from "./explore";
import type { TranscriptListResponse } from "@/lib/types";

interface ProfilePathCase {
  name: string;
  owner: "profile" | "anonymous";
  username?: string;
  payload: "made" | "copy";
  want: string | null;
}

const cases = (
  YAML.parse(readFileSync(resolve(process.cwd(), "src/testdata/explore-profile-path.yaml"), "utf8")) as {
    cases: ProfilePathCase[];
  }
).cases;

const REQUIRED = ["profile_owner", "anonymous_owner", "anonymous_owner_copy"];

function ownerPayloadFor(c: ProfilePathCase) {
  const owner =
    c.owner === "profile"
      ? { kind: "profile" as const, id: "11111111-1111-4111-8111-111111111111", username: c.username ?? "", display_name: null, avatar_url: null }
      : { kind: "anonymous" as const };
  const transcripts = {
    transcripts: [
      {
        transcript: { id: "22222222-2222-4222-8222-222222222222", model_provider: "claude-code" },
        tags: [],
        owner,
      },
    ],
    total: 1,
    page: 1,
    limit: 20,
  } as unknown as TranscriptListResponse;
  const made = adaptExplore(transcripts, { collectives: [] } as never, []).transcripts.transcripts[0].owner;
  return c.payload === "copy" ? { ...made } : made;
}

describe("exploreProfilePath", () => {
  it("has every required case", () => {
    const names = cases.map((c) => c.name);
    for (const name of REQUIRED) expect(names).toContain(name);
  });

  for (const c of cases) {
    it(c.name, () => {
      expect(exploreProfilePath(ownerPayloadFor(c))).toBe(c.want);
    });
  }
});

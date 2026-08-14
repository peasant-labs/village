import { describe, expect, it } from "vitest";
import {
  TRANSCRIPT_PAGE_SIZE,
  buildTranscriptListParams,
} from "@/lib/transcriptPageRequest";
import { loadTranscriptPageRequestFixtures } from "@/test/transcriptPageRequestFixtures";

const fixtures = loadTranscriptPageRequestFixtures();

describe("buildTranscriptListParams", () => {
  it("always requests the explicit Village page size", () => {
    expect(TRANSCRIPT_PAGE_SIZE).toBe(24);
  });

  for (const fixture of fixtures.paramBuilds) {
    it(fixture.name, () => {
      expect(buildTranscriptListParams(fixture.filters)).toEqual(fixture.expectedParams);
    });
  }
});

import { describe, expect, it } from "vitest";
import {
  TRANSCRIPT_PAGE_SIZE,
  buildTranscriptListParams,
  transcriptListFailureCause,
} from "@/lib/transcriptPageRequest";
import { TranscriptListResponseMismatchError } from "@/lib/queries/transcripts";
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

describe("transcriptListFailureCause", () => {
  for (const fixture of fixtures.failureCauses) {
    it(fixture.name, () => {
      const error =
        fixture.error.kind === "mismatch"
          ? new TranscriptListResponseMismatchError("2", "24", 5, 24)
          : fixture.error.kind === "message"
            ? new Error(fixture.error.message)
            : "not an error";
      expect(transcriptListFailureCause(error)).toBe(fixture.expect);
    });
  }
});

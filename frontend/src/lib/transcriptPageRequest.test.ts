import { describe, expect, it } from "vitest";
import {
  TRANSCRIPT_PAGE_SIZE,
  buildTranscriptListParams,
  transcriptListFailureNote,
} from "@/lib/transcriptPageRequest";
import { TranscriptListResponseMismatchError } from "@/lib/queries/transcripts";
import { ApiError } from "@/lib/api";
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

describe("transcriptListFailureNote", () => {
  for (const fixture of fixtures.failureNotes) {
    it(fixture.name, () => {
      const thrown = fixture.error;
      const error =
        thrown.kind === "mismatch"
          ? new TranscriptListResponseMismatchError("2", "24", 5, 24)
          : thrown.kind === "status"
            ? new ApiError(thrown.status, thrown.message)
            : thrown.kind === "network"
              ? new TypeError(thrown.message)
              : thrown.kind === "message"
                ? new Error(thrown.message)
                : "not an error";
      expect(transcriptListFailureNote(error)).toBe(fixture.expect);
    });
  }
});

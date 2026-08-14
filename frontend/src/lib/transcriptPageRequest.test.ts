import { describe, expect, it } from "vitest";
import {
  TRANSCRIPT_LIST_ENDPOINT,
  TRANSCRIPT_PAGE_SIZE,
  buildTranscriptListParams,
  validateSettledTranscriptPage,
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

describe("validateSettledTranscriptPage", () => {
  for (const fixture of fixtures.settledValidation) {
    it(fixture.name, () => {
      const result = validateSettledTranscriptPage({
        requestedPage: fixture.requestedPage,
        requestedLimit: fixture.requestedLimit,
        responsePage: fixture.responsePage,
        responseLimit: fixture.responseLimit,
      });
      expect(result.ok).toBe(fixture.expectedOk);
      if (!result.ok) {
        // The mismatch descriptor is actionable: it names what was requested,
        // what was received, where it failed, and that prior rows are retained.
        expect(result.requestedPage).toBe(fixture.requestedPage);
        expect(result.receivedPage).toBe(fixture.responsePage);
        expect(result.requestedLimit).toBe(fixture.requestedLimit);
        expect(result.receivedLimit).toBe(fixture.responseLimit);
        expect(result.message).toContain(`page ${fixture.requestedPage}`);
        expect(result.message).toContain(`page ${fixture.responsePage}`);
        expect(result.message).toContain(TRANSCRIPT_LIST_ENDPOINT);
        expect(result.message.toLowerCase()).toContain("retry");
        expect(result.message.toLowerCase()).toContain("not replaced");
      }
    });
  }
});

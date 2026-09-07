import { waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { installMountedRouteTeardown, installRESTFixture, renderProductionRoute } from "@/test/mountedProductionRoute";
import { loadLegacyRenderedFixtures } from "@/test/legacyRenderedFixtures";

installMountedRouteTeardown();

describe("backend-rendered legacy responses on the real transcript route", () => {
  for (const fixture of loadLegacyRenderedFixtures()) {
    it(fixture.name, async () => {
      const transcriptID = `legacy-${fixture.name}`;
      const detail = JSON.parse(fixture.rendered);
      const fetch = installRESTFixture(transcriptID, {
        transcript: {id: transcriptID, local_id: detail.id, model_provider: "claude-code", visibility: "public", title: "Legacy response", description: null, project_name: "fixture"},
        owner: {id: "fixture-owner"}, enriched_shares: [],
      }, detail, "legacy rendered response");
      await renderProductionRoute(transcriptID);
      await waitFor(() => expect(document.querySelector(".txn-turnwrap")?.textContent).toContain(detail.turns[0].content));
      expect(document.querySelectorAll(".txn-turnwrap")).toHaveLength(detail.turns.length);
      const urls = fetch.mock.calls.map(([url]) => String(url));
      expect(urls.findIndex((url) => url.endsWith(`/transcripts/${transcriptID}`))).toBeLessThan(urls.findIndex((url) => url.endsWith(`/transcripts/${transcriptID}/content`)));
    });
  }
});

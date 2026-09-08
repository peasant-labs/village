import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parseDocument } from "yaml";
import { fireEvent, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  installMountedRouteTeardown,
  installRESTFixture,
  renderProductionRoute,
  type MountedRouteTranscriptMetadata,
} from "@/test/mountedProductionRoute";

interface PiRouteFixture {
  name: string;
  content: string;
  capabilities: string[];
}

function loadPiRouteFixture(): PiRouteFixture {
  const path = resolve(process.cwd(), "../backend/internal/handler/testdata/observed_model_preservation/pi.yaml");
  const document = parseDocument(readFileSync(path, "utf8"), { uniqueKeys: true });
  if (document.errors.length > 0) throw document.errors[0];
  const cases = document.toJS()?.cases;
  if (!Array.isArray(cases)) throw new Error("Mounted Pi route fixture requires a cases array");
  const matches = cases.filter((entry) => entry?.name === "pi_all_metadata_and_usage_owners");
  if (matches.length !== 1 || typeof matches[0].content !== "string" || !Array.isArray(matches[0].capabilities)) {
    throw new Error("Mounted Pi route fixture requires exactly one complete named public-content case");
  }
  return matches[0] as PiRouteFixture;
}

installMountedRouteTeardown();

describe("mounted production transcript route with published Pi presentation", () => {
  it("renders separate namespace, name, error, usage, context and image evidence", async () => {
    const fixture = loadPiRouteFixture();
    const detail = JSON.parse(fixture.content).sessionDetail;
    const transcriptID = "pi-published-fairtrade";
    const metadata: MountedRouteTranscriptMetadata = {
      transcript: {
        id: transcriptID,
        local_id: detail.id,
        model_provider: "pi",
        visibility: "public",
        title: "Pi contract fixture",
        description: "Synthetic mounted production-route fixture.",
        project_name: "village",
      },
      owner: { id: "fixture-owner" },
      enriched_shares: [],
    };
    const fetchMock = installRESTFixture(transcriptID, metadata, detail, "mounted Pi namespace route");

    await renderProductionRoute(transcriptID);
    await waitFor(() => expect(document.querySelectorAll(".txn-turnwrap")).toHaveLength(detail.turns.length));

    const tool = document.querySelector<HTMLElement>(".txn-tc-head");
    expect(tool?.textContent).toContain("Extension.Tools");
    expect(tool?.textContent).toContain("inspect");
    expect(tool?.textContent).not.toContain("Extension.Tools.inspect");
    if (tool == null) throw new Error("Mounted Pi route did not render its canonical tool disclosure");
    fireEvent.click(tool);
    await waitFor(() => expect(document.body.textContent).toContain("error [image omitted]"));
    expect(document.body.textContent).toContain("error [image omitted]");
    expect(document.body.textContent).toContain("visible context [image omitted]");
    expect(document.body.textContent).toContain("recorded harness estimate");
    expect(fixture.capabilities).toContain("tool_namespace_v1");
    const requested = fetchMock.mock.calls.map(([input]) => String(input));
    expect(requested.some((url) => url.endsWith(`/transcripts/${transcriptID}`))).toBe(true);
    expect(requested.some((url) => url.endsWith(`/transcripts/${transcriptID}/content`))).toBe(true);
  });
});

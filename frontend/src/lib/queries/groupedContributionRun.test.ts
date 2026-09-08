import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ApiError } from "../api";
import { useContributeRun } from "./groupShares";
import { makeQueryClientWrapper } from "@/test/queryHookHelpers";
import type { BatchShareRequest } from "../contribute/types";

interface ExplicitRunCase {
  name: string;
  project: string;
  selected_ids: string[];
  visibility_confirmed: boolean;
  expected_requests: BatchShareRequest[];
  error_contains: string;
}

function loadExplicitRunCases(): ExplicitRunCase[] {
  const corpus: { cases: ExplicitRunCase[] } = parse(readFileSync(resolve(process.cwd(), "src/testdata/grouped-contribution-run.yaml"), "utf8"));
  const names = new Set<string>();
  for (const row of corpus.cases) {
    if (!row.name || names.has(row.name) || !Array.isArray(row.selected_ids) || !Array.isArray(row.expected_requests)) {
      throw new Error("grouped contribution run fixture has duplicate names or omitted explicit ID/request lists");
    }
    names.add(row.name);
  }
  for (const name of ["second-helper-only", "empty-selection-never-means-whole-project"]) {
    if (!names.has(name)) throw new Error(`missing required contribution run fixture ${name}`);
  }
  return corpus.cases;
}

afterEach(() => vi.unstubAllGlobals());

for (const c of loadExplicitRunCases()) {
  it(c.name, async () => {
    const requests: BatchShareRequest[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      expect(String(input)).toContain("/groups/collective/shares");
      expect(init?.method).toBe("POST");
      const body: BatchShareRequest = JSON.parse(String(init?.body));
      requests.push(body);
      return new Response(JSON.stringify({ project_hash: body.project_hash, shared: body.transcript_ids.map((id) => ({ transcript_id: id, status: "pending" })), already_shared: [] }), { status: 200, headers: { "content-type": "application/json" } });
    }));
    const { result } = renderHook(() => useContributeRun("collective"), { wrapper: makeQueryClientWrapper() });
    let outcome: unknown;
    await act(async () => {
      const results = await result.current.run(new Map([[c.project, c.selected_ids]]), c.visibility_confirmed);
      outcome = results.get(c.project);
    });
    expect(requests).toEqual(c.expected_requests);
    if (c.error_contains) {
      expect(outcome).toBeInstanceOf(ApiError);
      expect((outcome as ApiError).message).toContain(c.error_contains);
    } else {
      expect(outcome).not.toBeInstanceOf(ApiError);
    }
  });
}

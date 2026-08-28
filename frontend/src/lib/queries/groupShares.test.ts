import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { buildContributeTree } from "@/lib/contribute/tree";
import { groupByProject, selectAll } from "@/lib/contribute/selection";
import { ApiError } from "@/lib/api";
import { partitionRunOutcome, useContributeRun } from "@/lib/queries/groupShares";
import type { BatchShareResponse } from "@/lib/contribute/types";
import { makeQueryClientWrapper } from "@/test/queryHookHelpers";
import {
  caseByName,
  loadGroupsContributeTreeFixtures,
  toContributableTranscript,
} from "@/test/groupsContributeTreeFixtures";

const cases = loadGroupsContributeTreeFixtures();

interface RecordedRequest {
  method: string;
  url: string;
  body: unknown;
}

/** Stubs `fetch` for `/groups/{id}/shares`, answering per-project per the
 *  case's `failing` list; records every request so a test can assert the
 *  parsed BODY, never a call count alone. */
function installBatchShareREST(failing: string[], failureMessage: string): RecordedRequest[] {
  const requests: RecordedRequest[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? "GET").toUpperCase();
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : null;
    requests.push({ method, url, body });
    if (url.includes("/shares")) {
      if (failing.includes(body.project_hash)) {
        return new Response(JSON.stringify({ error: failureMessage }), {
          status: 409,
          headers: { "content-type": "application/json" },
        });
      }
      const response: BatchShareResponse = {
        project_hash: body.project_hash,
        shared: (body.transcript_ids as string[]).map((id) => ({ transcript_id: id, status: "approved" })),
        already_shared: [],
      };
      return new Response(JSON.stringify(response), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }
    throw new Error(`batch-share fixture received an unexpected ${method} request to ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return requests;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("useContributeRun (sequential one POST per project)", () => {
  it("fires exactly one POST per project, each body carrying only that project's ids", async () => {
    const c = caseByName(cases, "post", "one_post_per_project");
    const rows = c.rows.map(toContributableTranscript);
    const tree = buildContributeTree(rows);
    const selection = new Set(c.selectionIds);
    const batches = groupByProject(selection, tree);
    const requests = installBatchShareREST(c.failing, "");

    const { result } = renderHook(() => useContributeRun("group-1"), { wrapper: makeQueryClientWrapper() });
    let results!: Awaited<ReturnType<typeof result.current.run>>;
    await act(async () => {
      results = await result.current.run(batches, false);
    });

    const shareRequests = requests.filter((r) => r.url.includes("/shares"));
    expect(shareRequests).toHaveLength(2);
    const expected = c.expect.requestsByProject as Record<string, string[]>;
    for (const [projectHash, ids] of Object.entries(expected)) {
      const req = shareRequests.find((r) => (r.body as { project_hash: string }).project_hash === projectHash);
      expect(req).toBeDefined();
      expect((req!.body as { transcript_ids: string[] }).transcript_ids).toEqual(ids);
      expect((req!.body as { visibility_confirmed: boolean }).visibility_confirmed).toBe(false);
    }
    expect([...results.keys()].sort()).toEqual((c.expect.finalResultProjects as string[]).slice().sort());
  });

  it("continues past a failed project's POST and reports every project's outcome", async () => {
    const c = caseByName(cases, "post", "failure_continues_and_reports");
    const rows = c.rows.map(toContributableTranscript);
    const tree = buildContributeTree(rows);
    const selection = new Set(c.selectionIds);
    const batches = groupByProject(selection, tree);
    const requests = installBatchShareREST(c.failing, c.failureMessage ?? "");

    const { result } = renderHook(() => useContributeRun("group-1"), { wrapper: makeQueryClientWrapper() });
    let results!: Awaited<ReturnType<typeof result.current.run>>;
    await act(async () => {
      results = await result.current.run(batches, false);
    });

    const shareRequests = requests.filter((r) => r.url.includes("/shares"));
    expect(shareRequests).toHaveLength(2);
    expect([...results.keys()].sort()).toEqual((c.expect.finalResultProjects as string[]).slice().sort());

    const failedOutcome = results.get(c.failing[0]);
    expect(failedOutcome).toBeInstanceOf(ApiError);
    expect((failedOutcome as ApiError).message).toBe(c.expect.failedProjectMessage);

    await waitFor(() => expect(result.current.state.running).toBe(false));
    expect(result.current.state.done).toBe(2);
  });
});

describe("partitionRunOutcome (failed/unfinished projects stay selected)", () => {
  it("clears a succeeded project's ids and keeps a failed project's ids selected", async () => {
    const c = caseByName(cases, "post", "failed_projects_stay_selected");
    const rows = c.rows.map(toContributableTranscript);
    const tree = buildContributeTree(rows);
    const selection = new Set(c.selectionIds);
    const batches = groupByProject(selection, tree);
    installBatchShareREST(c.failing, c.failureMessage ?? "");

    const { result } = renderHook(() => useContributeRun("group-1"), { wrapper: makeQueryClientWrapper() });
    let results!: Awaited<ReturnType<typeof result.current.run>>;
    await act(async () => {
      results = await result.current.run(batches, false);
    });

    const { clearedIds, staySelectedIds } = partitionRunOutcome(batches, results);
    expect(staySelectedIds).toEqual(c.expect.selectionAfterRun);
    expect(clearedIds).toEqual(c.expect.clearedFromSelection);
  });
});

describe("the orphans synthetic node id is never sent in a POST body", () => {
  it("sends only real transcript ids, never the synthetic '<project_hash>::orphans' id", async () => {
    const c = caseByName(cases, "post", "orphans_node_never_sent");
    const rows = c.rows.map(toContributableTranscript);
    const tree = buildContributeTree(rows);
    const selection = selectAll(tree);
    const batches = groupByProject(selection, tree);
    const requests = installBatchShareREST(c.failing, "");

    const { result } = renderHook(() => useContributeRun("group-1"), { wrapper: makeQueryClientWrapper() });
    await act(async () => {
      await result.current.run(batches, false);
    });

    const shareRequests = requests.filter((r) => r.url.includes("/shares"));
    expect(shareRequests).toHaveLength(1);
    const sentIds = (shareRequests[0].body as { transcript_ids: string[] }).transcript_ids;
    const expected = (c.expect.requestsByProject as Record<string, string[]>)["proj-orphan-post"];
    expect(sentIds).toEqual(expected);
    expect(sentIds).not.toContain(c.expect.forbiddenBodyId);
  });
});

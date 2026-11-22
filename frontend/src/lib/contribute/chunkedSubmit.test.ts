import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { parse } from "yaml";
import {
  zVillageBatchShareResponse,
  type VillageBatchShareSelectionRequest,
  type VillageContributionStatus,
  type VillageTranscriptVisibility,
} from "@peasant-labs/schema";
import { ApiError } from "../api";
import {
  CONTRIBUTION_CHUNK_SIZE,
  planChunks,
  privateRecords,
  runContribution,
  selectionAfterRun,
  type ContributionReceipt,
  type PrivateApproval,
} from "./chunkedSubmit";
import type { FrozenSelection, SelectedRecord } from "./frozenSelection";
import { projectHashOf, transcriptIdOf } from "./testTokens";

type SelectedToken =
  | { id: string; project: string; family?: string; visibility?: VillageTranscriptVisibility; title?: string | null }
  | { range: [number, number]; project: string; visibility?: VillageTranscriptVisibility };

interface RequestToken {
  project: string;
  ids: string[];
  approvals: string[];
}

interface CallToken {
  expect: RequestToken;
  answer?: { shared?: Record<string, VillageContributionStatus>; already?: string[] };
  refuse?: { status: number; error?: string; code?: string; details?: { code: string; id?: string; current?: string }[] };
  network?: string;
  abort?: boolean;
}

type FailedToken = string | { reason: string; current?: string };

interface RunFixture {
  name: string;
  size?: number;
  selection: SelectedToken[];
  approved?: string[];
  approve_private?: PrivateApproval[];
  calls: CallToken[];
  expect_asked?: string[][];
  expect: {
    contributed: Record<string, VillageContributionStatus>;
    already: string[];
    failed: Record<string, FailedToken>;
    unattempted: string[];
    stopped?: string;
    remaining: string[];
  };
}

interface Fixtures {
  plan: { name: string; size: number; selection: SelectedToken[]; expect: [string, string[]][] }[];
  plan_sizes: { name: string; selection: SelectedToken[]; expect_sizes: number[] }[];
  private_records: { name: string; selection: SelectedToken[]; expect: string[] }[];
  runs: RunFixture[];
}

const fixtures = parse(readFileSync(resolve(process.cwd(), "src/testdata/chunked-submit.yaml"), "utf8")) as Fixtures;

const ID_TOKEN = new Map<string, string>();
function id(token: string): string {
  const value = transcriptIdOf(token);
  ID_TOKEN.set(value, token);
  return value;
}
function tokenOf(value: string): string {
  return ID_TOKEN.get(value) ?? value;
}

function selectionOf(tokens: SelectedToken[]): FrozenSelection {
  const selection = new Map<string, SelectedRecord>();
  const add = (token: string, project: string, family: string, visibility: VillageTranscriptVisibility, title: string | null) => {
    const value = id(token);
    selection.set(value, { id: value, project_hash: projectHashOf(project), family_project_hash: projectHashOf(family), visibility, title });
  };
  for (const token of tokens) {
    if ("range" in token) {
      for (let n = token.range[0]; n <= token.range[1]; n += 1) add(`t${n}`, token.project, token.project, token.visibility ?? "public", `session ${n}`);
    } else {
      const title = token.title === undefined ? `session ${token.id.replace(/^t/, "")}` : token.title;
      add(token.id, token.project, token.family ?? token.project, token.visibility ?? "public", title);
    }
  }
  return selection;
}

function requestToken(request: VillageBatchShareSelectionRequest): RequestToken {
  const project = String.fromCharCode(parseInt(request.project_hash.slice(0, 2), 16));
  return { project: `p${project}`, ids: request.transcript_ids.map(tokenOf), approvals: request.private_approvals.map(tokenOf) };
}

function receiptTokens(receipt: ContributionReceipt) {
  return {
    contributed: Object.fromEntries(receipt.contributed.map(({ record, status }) => [tokenOf(record.id), status])),
    already: receipt.already.map((record) => tokenOf(record.id)),
    failed: Object.fromEntries(
      receipt.failed.map((failure) => [
        tokenOf(failure.record.id),
        failure.current_project_hash
          ? { reason: failure.reason, current: `p${String.fromCharCode(parseInt(failure.current_project_hash.slice(0, 2), 16))}` }
          : failure.reason,
      ]),
    ),
    unattempted: receipt.unattempted.map((record) => tokenOf(record.id)),
    ...(receipt.stopped ? { stopped: receipt.stopped.reason } : {}),
  };
}

describe("planChunks", () => {
  for (const fixture of fixtures.plan) {
    it(fixture.name, () => {
      const chunks = planChunks(selectionOf(fixture.selection), fixture.size);
      expect(chunks.map((chunk) => [chunk.project_hash, chunk.records.map((record) => tokenOf(record.id))])).toEqual(
        fixture.expect.map(([project, ids]) => [projectHashOf(project), ids]),
      );
    });
  }
  for (const fixture of fixtures.plan_sizes) {
    it(fixture.name, () => {
      expect(CONTRIBUTION_CHUNK_SIZE).toBe(250);
      expect(planChunks(selectionOf(fixture.selection)).map((chunk) => chunk.records.length)).toEqual(fixture.expect_sizes);
    });
  }
  it("refuses a chunk size that is not a positive integer", () => {
    expect(() => planChunks(new Map(), 0)).toThrow(/positive integer/);
  });
});

describe("privateRecords", () => {
  for (const fixture of fixtures.private_records) {
    it(fixture.name, () => {
      expect(privateRecords(selectionOf(fixture.selection)).map((record) => tokenOf(record.id))).toEqual(fixture.expect);
    });
  }
});

describe("runContribution", () => {
  for (const fixture of fixtures.runs) {
    it(fixture.name, async () => {
      const selection = selectionOf(fixture.selection);
      const controller = new AbortController();
      const requests: RequestToken[] = [];
      const asked: string[][] = [];
      const answers = [...(fixture.approve_private ?? [])];
      const calls = [...fixture.calls];

      const receipt = await runContribution({
        selection,
        approved: new Set((fixture.approved ?? []).map(id)),
        chunkSize: fixture.size,
        signal: controller.signal,
        approvePrivate: async (records) => {
          asked.push(records.map((record) => tokenOf(record.id)));
          const answer = answers.shift();
          if (!answer) throw new Error("approvePrivate asked more often than the fixture answers");
          return answer;
        },
        submit: async (request) => {
          requests.push(requestToken(request));
          const call = calls.shift();
          if (!call) throw new Error(`unscripted request: ${JSON.stringify(requestToken(request))}`);
          if (call.abort) {
            controller.abort();
            throw new DOMException("aborted", "AbortError");
          }
          if (call.network) throw new TypeError(call.network);
          if (call.refuse) {
            const { status, error, code, details } = call.refuse;
            const body =
              error === undefined
                ? undefined
                : {
                    error,
                    ...(code ? { code } : {}),
                    ...(details
                      ? {
                          details: details.map((detail) => ({
                            code: detail.code,
                            ...(detail.id ? { transcript_id: id(detail.id) } : {}),
                            ...(detail.current ? { current_project_hash: projectHashOf(detail.current) } : {}),
                          })),
                        }
                      : {}),
                  };
            throw new ApiError(status, error ?? `API error: ${status}`, body);
          }
          return zVillageBatchShareResponse.parse({
            project_hash: request.project_hash,
            shared: Object.entries(call.answer?.shared ?? {}).map(([token, status]) => ({ transcript_id: id(token), status })),
            already_shared: (call.answer?.already ?? []).map(id),
          });
        },
      });

      expect(requests).toEqual(fixture.calls.map((call) => call.expect));
      expect(calls).toEqual([]);
      expect(asked).toEqual(fixture.expect_asked ?? []);
      const { remaining, ...buckets } = fixture.expect;
      expect(receiptTokens(receipt)).toEqual(buckets);

      // Every record of the frozen selection lands in exactly one bucket.
      const counted = receipt.contributed.length + receipt.already.length + receipt.failed.length + receipt.unattempted.length;
      expect(counted).toBe(selection.size);
      expect(receipt.total).toBe(selection.size);

      expect([...selectionAfterRun(selection, receipt).keys()].map(tokenOf).sort()).toEqual([...remaining].sort());
    });
  }
});

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parseDocument } from "yaml";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useTranscriptContent } from "./transcripts";

function corpus(path: string) {
  const doc = parseDocument(readFileSync(resolve(process.cwd(), path), "utf8"), { uniqueKeys: true });
  if (doc.errors.length) throw doc.errors[0];
  return doc.toJS();
}

const backendCorpus = "../backend/internal/handler/testdata/observed_model_preservation/";
const publicCase = corpus(`${backendCorpus}pi.yaml`).cases.find((c: { name: string }) => c.name === "pi_all_metadata_and_usage_owners");
if (!publicCase) throw new Error("Required Pi preservation fixture missing");
const boundaries = corpus(`${backendCorpus}pi_boundaries.yaml`).cases as { name: string; surface: string; find: string; replace: string; repeat?: number; status: number }[];
const legacy = corpus("src/testdata/transcript-content-raw.yaml").cases as { name: string; content: string; expected?: unknown; error?: boolean }[];
for (const name of ["legacy_single_object", "legacy_array", "legacy_jsonl", "legacy_duplicate_escaped_key", "invalid_envelope_no_jsonl_fallback"]) {
  if (legacy.filter((c) => c.name === name).length !== 1) throw new Error(`Required unique fixture missing: ${name}`);
}
for (const name of ["duplicate_content_key", "duplicate_opaque_key", "duplicate_owner", "wrong_usage_role", "wrong_metadata_target", "wrong_tool_result_ref", "token_overflow", "token_null", "cost_not_string", "opaque_integer_overflow", "opaque_underflow", "opaque_string_budget", "ordinary_text_outside_metadata_budget"]) {
  if (boundaries.filter((c) => c.name === name).length !== 1) throw new Error(`Required unique fixture missing: ${name}`);
}

afterEach(() => vi.unstubAllGlobals());

async function mountedRead(text: string, fails = false) {
  // The response exposes text only: a regression to response.json() fails here.
  const fetch = vi.fn().mockResolvedValue({ ok: true, text: async () => text });
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const hook = renderHook(() => useTranscriptContent("public-fixture"), { wrapper });
  await waitFor(() => expect(hook.result.current.status).toBe(fails ? "error" : "success"));
  expect(fetch.mock.calls[0][0]).toContain("/transcripts/public-fixture/content");
  const value = hook.result.current.data;
  if (fails) expect(value).toBeUndefined();
  hook.unmount(); client.clear();
  return value;
}

describe("raw content on the mounted fetch path", () => {
  it("preserves every public owner, metadata attachment, numeric value and recorded cost", async () => {
    expect(await mountedRead(publicCase.content)).toEqual(JSON.parse(publicCase.content));
  });
  it("validates the bare detail returned by the display endpoint", async () => {
    const detail = JSON.parse(publicCase.content).sessionDetail;
    expect(await mountedRead(JSON.stringify(detail))).toEqual(detail);
  });
  for (const c of boundaries.filter((c) => c.surface === "content")) {
    it(c.name, async () => {
      expect(publicCase.content).toContain(c.find);
      const replacement = c.repeat ? JSON.stringify("x".repeat(c.repeat)) : c.replace;
      await mountedRead(publicCase.content.replace(c.find, replacement), c.status !== 201);
    });
  }
  for (const c of legacy) {
    it(c.name, async () => {
      const result = await mountedRead(c.content, c.error);
      if (!c.error) expect(result).toEqual(c.expected);
    });
  }
});

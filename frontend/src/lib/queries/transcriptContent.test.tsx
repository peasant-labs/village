import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parseDocument } from "yaml";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useTranscriptContent } from "./transcripts";
import { loadLegacyRenderedFixtures } from "@/test/legacyRenderedFixtures";

function corpus(path: string) {
  const doc = parseDocument(readFileSync(resolve(process.cwd(), path), "utf8"), { uniqueKeys: true });
  if (doc.errors.length) throw doc.errors[0];
  return doc.toJS();
}

const backendCorpus = "../backend/internal/handler/testdata/observed_model_preservation/";
const publicCase = corpus(`${backendCorpus}pi.yaml`).cases.find((c: { name: string }) => c.name === "pi_all_metadata_and_usage_owners");
if (!publicCase) throw new Error("Required Pi preservation fixture missing");
const boundaries = corpus(`${backendCorpus}pi_boundaries.yaml`).cases as { name: string; surface: string; find: string; replace: string; repeat?: number; status: number }[];
const legacy = corpus("src/testdata/transcript-content-raw.yaml").cases as { name: string; content: string; expected?: unknown; error?: boolean; stringBytes?: number }[];
const renderedLegacy = loadLegacyRenderedFixtures();
const dispatch = corpus(`${backendCorpus}legacy_dispatch.yaml`).cases as {name: string; context?: string; content: string; readStatus: number}[];
for (const name of ["legacy_single_object", "legacy_array", "legacy_jsonl", "legacy_duplicate_escaped_key", "invalid_envelope_no_jsonl_fallback", "jsonl_total_byte_limit_survives_dispatch"]) {
  if (legacy.filter((c) => c.name === name).length !== 1) throw new Error(`Required unique fixture missing: ${name}`);
}
for (const name of ["duplicate_content_key", "duplicate_opaque_key", "duplicate_owner", "wrong_usage_role", "wrong_metadata_target", "wrong_tool_result_ref", "token_overflow", "token_null", "cost_not_string", "opaque_integer_overflow", "opaque_underflow", "opaque_string_budget", "ordinary_text_outside_metadata_budget", "complete_tool_namespace_requires_release"]) {
  if (boundaries.filter((c) => c.name === name).length !== 1) throw new Error(`Required unique fixture missing: ${name}`);
}

afterEach(() => vi.unstubAllGlobals());

async function mountedRead(text: string, fails = false, knownHarness?: string, secondRead?: string) {
  // The response exposes text only: a regression to response.json() fails here.
  const fetch = vi.fn().mockResolvedValueOnce({ ok: true, text: async () => text }).mockResolvedValue({ ok: true, text: async () => secondRead ?? text });
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const hook = renderHook(() => useTranscriptContent("public-fixture", {knownHarness}), { wrapper });
  await waitFor(() => expect(hook.result.current.status).toBe(fails ? "error" : "success"));
  expect(fetch.mock.calls[0][0]).toContain("/transcripts/public-fixture/content");
  const value = hook.result.current.data;
  if (fails) expect(value).toBeUndefined();
  if (secondRead !== undefined) {
    const next = await hook.result.current.refetch();
    expect(next.isSuccess).toBe(true);
    expect(next.data).toEqual(value);
    expect(fetch).toHaveBeenCalledTimes(2);
  }
  hook.unmount(); client.clear();
  return value;
}

describe("raw content on the mounted fetch path", () => {
  it("waits for metadata and keys cached validation by trusted harness", async () => {
    const text = renderedLegacy.find((c) => c.name === "array_to_empty_harness_detail")!.rendered;
    const fetch = vi.fn().mockResolvedValue({ok: true, text: async () => text});
    vi.stubGlobal("fetch", fetch);
    const client = new QueryClient({defaultOptions: {queries: {retry: false}}});
    const wrapper = ({children}: {children: ReactNode}) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const initialProps: {knownHarness?: string; enabled: boolean} = {enabled: false};
    const hook = renderHook(({knownHarness, enabled}: {knownHarness?: string; enabled: boolean}) => useTranscriptContent("public-fixture", {knownHarness, enabled}), {wrapper, initialProps});
    expect(fetch).not.toHaveBeenCalled();
    hook.rerender({knownHarness: "claude-code", enabled: true});
    await waitFor(() => expect(hook.result.current.isSuccess).toBe(true));
    hook.rerender({knownHarness: "pi", enabled: true});
    await waitFor(() => expect(hook.result.current.isError).toBe(true));
    expect(hook.result.current.data).toBeUndefined();
    expect(fetch).toHaveBeenCalledTimes(2);
    hook.unmount(); client.clear();
  });
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
      const text = c.stringBytes ? c.content.replaceAll("__LONG__", "x".repeat(c.stringBytes)) : c.content;
      const result = await mountedRead(text, c.error);
      if (!c.error) expect(result).toEqual(c.expected);
    });
  }
  for (const c of renderedLegacy) {
    it(`backend-rendered-${c.name}-and-second-read`, async () => {
      expect(await mountedRead(c.rendered, false, undefined, c.rendered)).toEqual(JSON.parse(c.rendered));
    });
    it(`prior-wire-${c.name}`, async () => {
      expect(await mountedRead(c.content)).toEqual(JSON.parse(c.content));
    });
  }
  for (const c of dispatch.filter((c) => c.content.trim().startsWith("{") || c.content.trim().startsWith("["))) {
    it(`shared-dispatch-${c.name}`, async () => {
      await mountedRead(c.content, c.readStatus >= 400, c.context);
    });
  }
});
